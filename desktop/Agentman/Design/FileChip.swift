import AppKit
import SwiftUI

/// A file named with `@` in a message box, drawn as its icon and its name
/// in place of `@` and its path: one character in the text, so the caret
/// steps over it and Backspace takes it whole. The path is what the message
/// sends, and what a copy of it holds.
final class FileChip: NSTextAttachmentCell {
    /// The file's path in the folder, as `@` named it.
    let path: String
    private let label: NSAttributedString
    private let icon: NSImage?
    private let textFont: NSFont

    static let iconSize: CGFloat = 14
    static let gap: CGFloat = 2

    init(path: String, font: NSFont) {
        self.path = path
        textFont = font
        let name = (path as NSString).lastPathComponent
        label = NSAttributedString(string: name, attributes: [.font: font, .foregroundColor: NSColor(Palette.mention)])
        icon = NSImage(named: FileIcon.asset(name))
        super.init(textCell: "")
    }

    @available(*, unavailable)
    required init(coder: NSCoder) { fatalError("init(coder:) is not used") }

    /// The chip for a path, with the box's own text attributes about it.
    static func text(_ path: String, attributes: [NSAttributedString.Key: Any]) -> NSAttributedString {
        let font = attributes[.font] as? NSFont ?? .systemFont(ofSize: NSFont.systemFontSize)
        let attachment = NSTextAttachment()
        attachment.attachmentCell = FileChip(path: path, font: font)
        let chip = NSMutableAttributedString(attachment: attachment)
        chip.addAttributes(attributes, range: NSRange(location: 0, length: chip.length))
        return chip
    }

    /// Text as it is sent: each chip back to `@` and its path.
    static func plain(_ text: NSAttributedString) -> String {
        guard text.containsAttachments else { return text.string }
        var plain = ""
        let string = text.string as NSString
        text.enumerateAttribute(.attachment, in: NSRange(location: 0, length: text.length)) { value, range, _ in
            if let chip = (value as? NSTextAttachment)?.attachmentCell as? FileChip {
                plain += String(repeating: "@" + chip.path, count: range.length)
            } else {
                plain += string.substring(with: range)
            }
        }
        return plain
    }

    // MARK: Drawing

    override func cellSize() -> NSSize {
        NSSize(width: ceil(Self.iconSize + Self.gap + label.size().width + 1), height: ceil(textFont.ascender - textFont.descender))
    }

    /// On the line's baseline, as the words beside it.
    override func cellBaselineOffset() -> NSPoint {
        NSPoint(x: 0, y: textFont.descender)
    }

    override func draw(withFrame cellFrame: NSRect, in controlView: NSView?) {
        // The icon level with the name's letters, the name on the line.
        let middle = cellFrame.minY + textFont.ascender - textFont.capHeight / 2
        icon?.draw(in: NSRect(x: cellFrame.minX, y: (middle - Self.iconSize / 2).rounded(), width: Self.iconSize, height: Self.iconSize),
                   from: .zero, operation: .sourceOver, fraction: 1, respectFlipped: true, hints: nil)
        label.draw(at: NSPoint(x: cellFrame.minX + Self.iconSize + Self.gap, y: cellFrame.minY))
    }

    override func draw(withFrame cellFrame: NSRect, in controlView: NSView?, characterIndex charIndex: Int,
                       layoutManager: NSLayoutManager) {
        draw(withFrame: cellFrame, in: controlView)
    }

    /// Clicks place the caret, as on any letter; a chip isn't dragged.
    override func wantsToTrackMouse() -> Bool { false }
}

/// Sent text with each `@path` naming a file drawn as the message box drew
/// it: the file's icon and name. Paths are as the reader got them, inside
/// the agent's folder or in full; a word that names no file stays as typed.
enum FileTags {
    enum Segment: Equatable {
        case text(String)
        case file(path: String, directory: Bool)
    }

    private static let word = try! NSRegularExpression(pattern: #"(?<![^\s])@([^\s@]+)"#)
    @MainActor private static var cache: [String: [Segment]] = [:]
    @MainActor private static var icons: [String: NSImage] = [:]

    @MainActor static func text(_ text: String, folder: String?) -> Text {
        let parts = segments(text, folder: folder)
        guard parts.contains(where: { if case .file = $0 { true } else { false } }) else { return Text(text) }
        return parts.reduce(Text("")) { line, part in
            switch part {
            case .text(let words):
                return Text("\(line)\(words)")
            case .file(let path, let directory):
                let name = (path as NSString).lastPathComponent
                // A narrow no-break space: close, and the icon never ends a
                // line its name begins.
                let tag = Text("\(Text(Image(nsImage: icon(name, directory: directory))).baselineOffset(-2))\u{202F}\(name)")
                    .foregroundStyle(Palette.mention)
                return Text("\(line)\(tag)")
            }
        }
    }

    /// The text cut into words and files, remembered: a conversation draws
    /// its messages again and again.
    @MainActor static func segments(_ text: String, folder: String?) -> [Segment] {
        guard text.contains("@") else { return [.text(text)] }
        let key = (folder ?? "") + "\u{0}" + text
        if let known = cache[key] { return known }
        let ns = text as NSString
        var parts: [Segment] = []
        var at = 0
        for match in word.matches(in: text, range: NSRange(location: 0, length: ns.length)) {
            var range = match.range
            var path = ns.substring(with: match.range(at: 1))
            var found = exists(path, in: folder)
            while found == nil, let last = path.last, ".,;:!?)]}'\"".contains(last) {
                path.removeLast()
                range.length -= 1
                found = exists(path, in: folder)
            }
            guard let directory = found else { continue }
            if range.location > at { parts.append(.text(ns.substring(with: NSRange(location: at, length: range.location - at)))) }
            parts.append(.file(path: path, directory: directory))
            at = NSMaxRange(range)
        }
        if at < ns.length { parts.append(.text(ns.substring(from: at))) }
        if cache.count > 500 { cache.removeAll() }
        cache[key] = parts
        return parts
    }

    /// Whether a path names something, and if so whether a folder.
    private static func exists(_ path: String, in folder: String?) -> Bool? {
        guard !path.isEmpty else { return nil }
        let full: String
        if path.hasPrefix("/") {
            full = path
        } else if let folder {
            full = (folder as NSString).appendingPathComponent(path)
        } else {
            return nil
        }
        var directory: ObjCBool = false
        return FileManager.default.fileExists(atPath: full, isDirectory: &directory) ? directory.boolValue : nil
    }

    /// The file's icon at the size of the words around it.
    @MainActor private static func icon(_ name: String, directory: Bool) -> NSImage {
        let asset = FileIcon.asset(name, directory: directory)
        if let icon = icons[asset] { return icon }
        let source = NSImage(named: asset)
        let icon = NSImage(size: NSSize(width: FileChip.iconSize, height: FileChip.iconSize), flipped: false) { rect in
            source?.draw(in: rect)
            return true
        }
        icons[asset] = icon
        return icon
    }
}
