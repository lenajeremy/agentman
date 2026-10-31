import AppKit
import SwiftUI

/// A glance at a file named in a message, shown while the pointer rests on
/// its tag: the start of its text, the picture, or what a folder holds.
enum FilePreview {
    enum Content: Equatable {
        case text([String], more: Bool)
        case image(NSImage)
        case folder([String], more: Int)
        case nothing(String)
    }

    static let lines = 14
    static let bytes = 8192

    /// What the preview shows of the file at url, read there and then: no
    /// more than its first few kilobytes.
    @MainActor static func read(_ url: URL) -> Content {
        var directory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: url.path, isDirectory: &directory) else {
            return .nothing("This file isn't there any more.")
        }
        if directory.boolValue {
            let names = ((try? FileManager.default.contentsOfDirectory(at: url, includingPropertiesForKeys: [.isDirectoryKey])) ?? [])
                .filter { !$0.lastPathComponent.hasPrefix(".") }
                .map { $0.lastPathComponent + ((try? $0.resourceValues(forKeys: [.isDirectoryKey]).isDirectory) == true ? "/" : "") }
                .sorted { $0.localizedStandardCompare($1) == .orderedAscending }
            return names.isEmpty ? .nothing("An empty folder.") : .folder(Array(names.prefix(lines)), more: max(names.count - lines, 0))
        }
        if EditorDocument.imageTypes.contains(url.pathExtension.lowercased()) {
            return NSImage(contentsOf: url).map { .image($0) } ?? .nothing("This picture can't be read.")
        }
        guard let handle = try? FileHandle(forReadingFrom: url) else { return .nothing("This file can't be read.") }
        defer { try? handle.close() }
        let data = (try? handle.read(upToCount: bytes)) ?? Data()
        if data.isEmpty { return .nothing("An empty file.") }
        if data.contains(0) { return .nothing("This file isn't text.") }
        let all = String(decoding: data, as: UTF8.self)
            .replacingOccurrences(of: "\r\n", with: "\n")
            .replacingOccurrences(of: "\t", with: "    ")
            .components(separatedBy: "\n")
        let cut = data.count == bytes
        // A last line cut short by the read isn't shown half.
        let whole = cut && all.count > 1 ? Array(all.dropLast()) : all
        let shown = Array(whole.prefix(lines))
        return .text(shown, more: cut || whole.count > lines)
    }
}

/// The preview itself: the file's name and folder, then what it holds.
struct FilePreviewCard: View {
    let url: URL
    /// The conversation's folder: a file inside it says where in it.
    var base: String?
    let content: FilePreview.Content

    init(url: URL, base: String? = nil) {
        self.url = url
        self.base = base
        content = FilePreview.read(url)
    }

    /// Where the file is: in the project, or in full from home.
    private var place: String {
        let folder = url.deletingLastPathComponent().standardizedFileURL.path
        if let base {
            let root = URL(fileURLWithPath: base).standardizedFileURL.path
            if folder == root { return (root as NSString).lastPathComponent }
            if folder.hasPrefix(root + "/") { return String(folder.dropFirst(root.count + 1)) }
        }
        return (folder as NSString).abbreviatingWithTildeInPath
    }

    private var isFolder: Bool { if case .folder = content { true } else { false } }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                FileTypeIcon(name: url.lastPathComponent, directory: isFolder, size: 14)
                Text(url.lastPathComponent)
                    .font(TextStyle.captionMedium)
                    .foregroundStyle(Palette.text)
                    .lineLimit(1)
                Text(place)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.faint)
                    .lineLimit(1)
                    .truncationMode(.head)
            }
            switch content {
            case .text(let lines, let more):
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(Array(lines.enumerated()), id: \.offset) { _, line in
                        Text(line.isEmpty ? " " : line)
                            .lineLimit(1)
                            .truncationMode(.tail)
                    }
                    if more {
                        Text("…").foregroundStyle(Palette.faint)
                    }
                }
                .font(TextStyle.code)
                .foregroundStyle(Palette.textSecondary)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(8)
                .background(Palette.fill.opacity(0.6), in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            case .image(let image):
                Image(nsImage: image)
                    .resizable()
                    .interpolation(.high)
                    .scaledToFit()
                    .frame(maxWidth: .infinity, maxHeight: 200)
                    .clipShape(RoundedRectangle(cornerRadius: 6, style: .continuous))
            case .folder(let names, let more):
                VStack(alignment: .leading, spacing: 3) {
                    ForEach(names, id: \.self) { name in
                        HStack(spacing: 6) {
                            FileTypeIcon(name: name, directory: name.hasSuffix("/"), size: 12)
                            Text(name.hasSuffix("/") ? String(name.dropLast()) : name).lineLimit(1)
                        }
                    }
                    if more > 0 {
                        Text("and \(more) more").foregroundStyle(Palette.faint)
                    }
                }
                .font(TextStyle.caption)
                .foregroundStyle(Palette.textSecondary)
            case .nothing(let why):
                Text(why)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
            }
        }
        .padding(12)
        .frame(width: 480, alignment: .leading)
    }
}

/// Shows a file's preview while the pointer rests on its tag in a text
/// view, and takes it away when the pointer moves off, a key is pressed or
/// the text scrolls. The text view passes on its mouse movement.
@MainActor
final class ChipHover {
    private weak var textView: NSTextView?
    /// The folder a tag's path is in, when it isn't given in full.
    var folder: () -> String?
    private var area: NSTrackingArea?
    private var waiting: DispatchWorkItem?
    private var pending: String?
    private(set) var popover: NSPopover?
    private var shown: String?

    static let delay: TimeInterval = 0.35

    init(_ textView: NSTextView, folder: @escaping () -> String?) {
        self.textView = textView
        self.folder = folder
    }

    /// Follows the pointer over the text view, once it is in a window.
    func track() {
        guard let textView, area == nil else { return }
        let area = NSTrackingArea(rect: .zero, options: [.mouseMoved, .mouseEnteredAndExited, .activeInActiveApp, .inVisibleRect],
                                  owner: textView, userInfo: nil)
        textView.addTrackingArea(area)
        self.area = area
    }

    func moved(_ event: NSEvent) {
        guard let textView else { return }
        let point = textView.convert(event.locationInWindow, from: nil)
        guard let (path, rect) = chip(at: point) else {
            close()
            return
        }
        if path == shown || path == pending { return }
        close()
        pending = path
        let work = DispatchWorkItem { [weak self] in self?.show(path, at: rect) }
        waiting = work
        DispatchQueue.main.asyncAfter(deadline: .now() + Self.delay, execute: work)
    }

    func close() {
        waiting?.cancel()
        waiting = nil
        pending = nil
        shown = nil
        popover?.close()
        popover = nil
    }

    /// The tag under a point in the text view, and where it is drawn.
    func chip(at point: NSPoint) -> (path: String, rect: NSRect)? {
        guard let textView, let layout = textView.layoutManager, let container = textView.textContainer,
              let storage = textView.textStorage, storage.containsAttachments else { return nil }
        let origin = textView.textContainerOrigin
        let inside = NSPoint(x: point.x - origin.x, y: point.y - origin.y)
        var fraction: CGFloat = 0
        let glyph = layout.glyphIndex(for: inside, in: container, fractionOfDistanceThroughGlyph: &fraction)
        guard glyph < layout.numberOfGlyphs else { return nil }
        let rect = layout.boundingRect(forGlyphRange: NSRange(location: glyph, length: 1), in: container)
        guard rect.contains(inside) else { return nil }
        let index = layout.characterIndexForGlyph(at: glyph)
        guard index < storage.length,
              let chip = (storage.attribute(.attachment, at: index, effectiveRange: nil) as? NSTextAttachment)?.attachmentCell as? FileChip
        else { return nil }
        return (chip.path, rect.offsetBy(dx: origin.x, dy: origin.y))
    }

    /// Where a tag's file is.
    func url(_ path: String) -> URL? {
        if path.hasPrefix("/") { return URL(fileURLWithPath: path) }
        guard let folder = folder() else { return nil }
        return URL(fileURLWithPath: folder).appendingPathComponent(path)
    }

    private func show(_ path: String, at rect: NSRect) {
        pending = nil
        guard let textView, textView.window != nil, let url = url(path) else { return }
        let host = NSHostingController(rootView: FilePreviewCard(url: url, base: folder()))
        host.sizingOptions = .preferredContentSize
        let popover = NSPopover()
        popover.contentViewController = host
        popover.behavior = .semitransient
        popover.animates = false
        popover.show(relativeTo: rect, of: textView, preferredEdge: .minY)
        self.popover = popover
        shown = path
    }
}
