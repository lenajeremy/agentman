import AppKit
import SwiftUI

/// Copies a picture to the clipboard, as PNG and TIFF so any app can take
/// it: right-click Copy Image wherever a picture shows.
enum ImageClipboard {
    /// The clipboard written to; tests use one of their own.
    @MainActor static var pasteboard = NSPasteboard.general

    @MainActor static func copy(_ image: NSImage) {
        guard let tiff = image.tiffRepresentation else { return }
        let item = NSPasteboardItem()
        if let png = NSBitmapImageRep(data: tiff)?.representation(using: .png, properties: [:]) {
            item.setData(png, forType: .png)
        }
        item.setData(tiff, forType: .tiff)
        pasteboard.clearContents()
        pasteboard.writeObjects([item])
    }
}

/// Images opened full window: what an agent or Agentman looked at, one
/// shown at a time, the rest a key or an arrow away.
struct Lightbox: Identifiable {
    /// One picture it can show: its name, and how to read it in full when
    /// it comes up.
    struct Picture {
        let title: String
        let load: @MainActor () async -> NSImage?
    }

    let id = UUID()
    let pictures: [Picture]
    /// The one shown first.
    let start: Int
    /// The window it opens over: an editor's folder for its agent panel,
    /// nil for Agentman's own window.
    var place: String?

    init(pictures: [Picture], start: Int = 0, place: String? = nil) {
        self.pictures = pictures
        self.start = min(max(start, 0), max(pictures.count - 1, 0))
        self.place = place
    }

    /// One image, on its own.
    init(image: NSImage, title: String, place: String? = nil) {
        self.init(pictures: [Picture(title: title) { image }], place: place)
    }

    /// Image files, from the one at path.
    static func files(_ paths: [String], from path: String, place: String? = nil) -> Lightbox {
        let paths = paths.contains(path) ? paths : [path]
        return Lightbox(pictures: paths.map { path in
            Picture(title: (path as NSString).lastPathComponent) { NSImage(contentsOfFile: path) }
        }, start: paths.firstIndex(of: path) ?? 0, place: place)
    }
}

extension EnvironmentValues {
    /// Which window an image opened here should show over: an editor's
    /// folder in its agent panel, nil in Agentman's window.
    @Entry var lightboxPlace: String?
    /// The pictures an agent looked at in the conversation, in order, so
    /// one opened full window leads on to the others.
    @Entry var imageGallery: [String] = []
    /// The conversation's folder, where the files its messages name are.
    @Entry var threadFolder: String?
}

/// Pictures made as small as where they show, decoded off the main thread
/// and kept. A screenshot three thousand pixels wide, shown 120 points high,
/// was decoded and scaled down on the main thread at every draw, and again
/// each time its row scrolled back into view.
enum Thumbnail {
    nonisolated(unsafe) private static let cache: NSCache<NSString, NSImage> = {
        let cache = NSCache<NSString, NSImage>()
        cache.totalCostLimit = 64 << 20
        return cache
    }()

    static func key(_ parts: String...) -> String { parts.joined(separator: "\u{0}") }

    /// One already made, for a row to start with rather than a placeholder
    /// that changes its size as it scrolls.
    static func cached(_ key: String) -> NSImage? { cache.object(forKey: key as NSString) }

    /// A picture file, `height` points high at most.
    static func file(_ path: String, height: CGFloat) async -> NSImage? {
        let key = key(path, "\(Int(height))")
        if let made = cached(key) { return made }
        let made = await Task.detached(priority: .userInitiated) { () -> NSImage? in
            guard let source = CGImageSourceCreateWithURL(URL(fileURLWithPath: path) as CFURL, nil) else { return nil }
            return small(source, height: height)
        }.value
        if let made { keep(made, key) }
        return made
    }

    /// A picture already in memory, `height` points high at most.
    static func image(_ image: NSImage, key: String, height: CGFloat) async -> NSImage? {
        if let made = cached(key) { return made }
        guard let cg = image.cgImage(forProposedRect: nil, context: nil, hints: nil) else { return image }
        let made = await Task.detached(priority: .userInitiated) { small(cg, height: height) }.value
        if let made { keep(made, key) }
        return made ?? image
    }

    private static func keep(_ image: NSImage, _ key: String) {
        let pixels = image.representations.first.map { $0.pixelsWide * $0.pixelsHigh } ?? 0
        cache.setObject(image, forKey: key as NSString, cost: pixels * 4)
    }

    /// Twice the points, for a Retina screen; never larger than it was.
    nonisolated private static func small(_ source: CGImageSource, height: CGFloat) -> NSImage? {
        let properties = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any]
        let width = (properties?[kCGImagePropertyPixelWidth] as? CGFloat) ?? 0
        let tall = (properties?[kCGImagePropertyPixelHeight] as? CGFloat) ?? 0
        guard width > 0, tall > 0 else { return nil }
        let scale = min(1, height * 2 / tall)
        let options: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceShouldCacheImmediately: true,
            kCGImageSourceThumbnailMaxPixelSize: max(1, Int((max(width, tall) * scale).rounded())),
        ]
        guard let made = CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary) else { return nil }
        return NSImage(cgImage: made, size: NSSize(width: CGFloat(made.width) / 2, height: CGFloat(made.height) / 2))
    }

    nonisolated private static func small(_ image: CGImage, height: CGFloat) -> NSImage? {
        let scale = min(1, height * 2 / CGFloat(max(image.height, 1)))
        let width = max(1, Int((CGFloat(image.width) * scale).rounded()))
        let tall = max(1, Int((CGFloat(image.height) * scale).rounded()))
        guard let context = CGContext(data: nil, width: width, height: tall, bitsPerComponent: 8, bytesPerRow: 0,
                                      space: CGColorSpace(name: CGColorSpace.sRGB) ?? CGColorSpaceCreateDeviceRGB(),
                                      bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return nil }
        context.interpolationQuality = .high
        context.draw(image, in: CGRect(x: 0, y: 0, width: width, height: tall))
        guard let made = context.makeImage() else { return nil }
        return NSImage(cgImage: made, size: NSSize(width: CGFloat(width) / 2, height: CGFloat(tall) / 2))
    }
}

/// An image an agent or Agentman read, as a short label and a thumbnail.
/// Clicking the thumbnail opens it full window.
struct ImageThumbnail: View {
    @Environment(AgentsStore.self) private var store
    @Environment(\.lightboxPlace) private var lightboxPlace
    @Environment(\.imageGallery) private var gallery
    let sessionId: String
    let path: String
    let label: String
    /// In an agent's thread: the tool call that read it in a tool card, and
    /// the picture under the card, outside it.
    var carded = false
    /// The picture alone, for a row of them under one run of reads.
    var pictureOnly = false
    /// The thumbnail; the picture itself is the store's, for the lightbox.
    @State private var image: NSImage?
    @State private var failed = false

    init(sessionId: String, path: String, label: String, carded: Bool = false, pictureOnly: Bool = false) {
        self.sessionId = sessionId
        self.path = path
        self.label = label
        self.carded = carded
        self.pictureOnly = pictureOnly
        _image = State(initialValue: Thumbnail.cached(Thumbnail.key(sessionId, path, "120")))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: carded ? 8 : 7) {
            if pictureOnly {
                picture
            } else if carded {
                header.toolCard()
                picture.padding(.leading, 12)
            } else {
                header
                picture.padding(.leading, 18)
            }
        }
        .task(id: path) {
            guard image == nil else { return }
            // A daemon that has just started may not know the agent's files
            // yet; it is asked a few times before the thumbnail gives up.
            for attempt in 0..<4 {
                if attempt > 0 { try? await Task.sleep(for: .seconds(1.5)) }
                if let full = await store.manager.image(sessionId: sessionId, path: path) {
                    image = await Thumbnail.image(full, key: Thumbnail.key(sessionId, path, "120"), height: 120)
                }
                if image != nil || Task.isCancelled { break }
            }
            failed = image == nil
        }
    }

    private var header: some View {
        HStack(spacing: 6) {
            Image(systemName: "photo")
                .font(.system(size: 10, weight: .medium))
                .frame(width: 12)
            Text(label)
                .lineLimit(1)
                .truncationMode(.middle)
        }
        .font(TextStyle.caption)
        .foregroundStyle(Palette.muted)
        .help(path)
    }

    @ViewBuilder private var picture: some View {
        if let image {
            Button {
                Task {
                    // Opened when this one is read, then the arrows go
                    // through the conversation's other pictures.
                    let full = await store.manager.image(sessionId: sessionId, path: path) ?? image
                    let paths = gallery.contains(path) ? gallery : [path]
                    let manager = store.manager, sessionId = sessionId
                    store.lightbox = Lightbox(pictures: paths.map { each in
                        Lightbox.Picture(title: (each as NSString).lastPathComponent) {
                            each == path ? full : await manager.image(sessionId: sessionId, path: each)
                        }
                    }, start: paths.firstIndex(of: path) ?? 0, place: lightboxPlace)
                }
            } label: {
                Image(nsImage: image)
                    .resizable()
                    .scaledToFill()
                    .frame(width: thumbnailWidth(image), height: 120)
                    .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Palette.line))
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("Open full window")
            .contextMenu {
                Button("Copy Image") {
                    Task { ImageClipboard.copy(await store.manager.image(sessionId: sessionId, path: path) ?? image) }
                }
            }
        } else if !failed {
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .fill(Palette.fill)
                .frame(width: 160, height: 120)
                .overlay(ProgressView().controlSize(.small))
        }
    }

    /// As wide as the image's shape at the thumbnail's height, within bounds.
    private func thumbnailWidth(_ image: NSImage) -> CGFloat {
        guard image.size.height > 0 else { return 160 }
        return min(280, max(90, 120 * image.size.width / image.size.height))
    }

    /// The image file a path names, if it is one.
    static func imagePath(in text: String) -> String? {
        guard let match = text.firstMatch(of: imagePattern) else { return nil }
        return String(text[match.range])
    }

    /// Made once: making it took longer than most searches with it.
    nonisolated(unsafe) private static let imagePattern = try! Regex(#"/[^\s"'`]+?\.(?i:png|jpe?g|gif|webp|heic|bmp|tiff?)"#)
}

/// The full-window view of images, over everything: ← and →, or the arrows
/// at its sides, go through them; the picture zooms (see ZoomableImage);
/// Escape, the button or a click beside the picture closes it.
struct LightboxView: View {
    @Environment(AgentsStore.self) private var store
    let box: Lightbox
    @State private var index: Int
    /// The picture showing, and which one it is: the last one stays up
    /// while the next is read.
    @State private var image: NSImage?
    @State private var shown: Int?
    @State private var zoom: CGFloat = 1

    init(box: Lightbox) {
        self.box = box
        _index = State(initialValue: box.start)
    }

    private var count: Int { box.pictures.count }

    var body: some View {
        ZStack {
            Color.black.opacity(0.85)
                .ignoresSafeArea()
                .onTapGesture { close() }
            VStack(spacing: 12) {
                Group {
                    if let image {
                        ZoomableImage(image: image, zoom: $zoom, onTapBeside: close)
                    } else if shown == index {
                        Text("This image can't be read.")
                            .font(TextStyle.body)
                            .foregroundStyle(.white.opacity(0.7))
                            .frame(maxWidth: .infinity, maxHeight: .infinity)
                            .contentShape(Rectangle())
                            .onTapGesture { close() }
                    } else {
                        Color.clear
                            .contentShape(Rectangle())
                            .onTapGesture { close() }
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                caption
            }
            .padding(.horizontal, count > 1 ? 80 : 40)
            .padding(.top, 60)
            .padding(.bottom, 22)
            if count > 1 {
                HStack {
                    arrow("chevron.left", help: "Previous Image (←)", enabled: index > 0) { move(-1) }
                    Spacer()
                    arrow("chevron.right", help: "Next Image (→)", enabled: index < count - 1) { move(1) }
                }
                .padding(.horizontal, 20)
            }
        }
        .overlay(alignment: .topTrailing) {
            Button(action: close) {
                Image(systemName: "xmark")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(.white)
                    .frame(width: 32, height: 32)
                    .background(.white.opacity(0.15), in: Circle())
            }
            .buttonStyle(.plain)
            .keyboardShortcut(.cancelAction)
            .help("Close (Esc)")
            .padding(18)
        }
        .background {
            // ← and →, wherever the keyboard is.
            Group {
                Button("Previous Image") { move(-1) }.keyboardShortcut(.leftArrow, modifiers: [])
                Button("Next Image") { move(1) }.keyboardShortcut(.rightArrow, modifiers: [])
            }
            .opacity(0)
            .allowsHitTesting(false)
            .accessibilityHidden(true)
        }
        .task(id: index) {
            guard index < count else { return }
            let loaded = await box.pictures[index].load()
            guard !Task.isCancelled else { return }
            zoom = 1
            image = loaded
            shown = index
        }
        .transition(.opacity)
    }

    /// Its name, where it is in the run, and a spinner while the next is read.
    private var caption: some View {
        HStack(spacing: 10) {
            if count > 0 {
                Text(box.pictures[index].title)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            if count > 1 {
                Text("\(index + 1) of \(count)")
                    .monospacedDigit()
                    .foregroundStyle(.white.opacity(0.5))
            }
            if shown != index {
                ProgressView()
                    .controlSize(.mini)
                    .tint(.white)
            }
        }
        .font(TextStyle.caption)
        .foregroundStyle(.white.opacity(0.75))
        .frame(height: 16)
    }

    private func close() { store.lightbox = nil }

    private func move(_ by: Int) {
        let next = index + by
        guard next >= 0, next < count else { return }
        index = next
    }

    private func arrow(_ symbol: String, help: String, enabled: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: 15, weight: .semibold))
                .foregroundStyle(.white)
                .frame(width: 40, height: 40)
                .background(.white.opacity(0.15), in: Circle())
                .contentShape(Circle())
        }
        .buttonStyle(.plain)
        .opacity(enabled ? 1 : 0.25)
        .disabled(!enabled)
        .help(help)
    }
}

/// The images the user sent with a message, as thumbnails that open full
/// window.
struct SentImages: View {
    let names: [String]

    var body: some View {
        HStack(spacing: 8) {
            ForEach(names, id: \.self) { name in
                SentImage(name: name, group: names)
            }
        }
    }
}

private struct SentImage: View {
    @Environment(AgentsStore.self) private var store
    @Environment(\.lightboxPlace) private var lightboxPlace
    let name: String
    /// The message's images, which the arrows go through.
    var group: [String] = []
    @State private var image: NSImage?

    var body: some View {
        Group {
            if let image {
                Button {
                    let names = group.contains(name) ? group : [name]
                    let manager = store.manager
                    store.lightbox = Lightbox(pictures: names.map { each in
                        Lightbox.Picture(title: "Sent to Agentman") { await manager.keptImage(each) }
                    }, start: names.firstIndex(of: name) ?? 0, place: lightboxPlace)
                } label: {
                    Image(nsImage: image)
                        .resizable()
                        .scaledToFill()
                        .frame(width: min(220, max(80, 110 * image.size.width / max(image.size.height, 1))), height: 110)
                        .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
                        .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(Palette.line))
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help("Open full window")
                .contextMenu {
                    Button("Copy Image") { ImageClipboard.copy(image) }
                }
            } else {
                RoundedRectangle(cornerRadius: 12, style: .continuous)
                    .fill(Palette.fill)
                    .frame(width: 110, height: 110)
                    .overlay(ProgressView().controlSize(.small))
            }
        }
        .task(id: name) { image = await store.manager.keptImage(name) }
    }
}

/// Images waiting in the message box, each with a way to take it out.
struct AttachmentStrip: View {
    @Binding var attachments: [Attachment]

    var body: some View {
        HStack(spacing: 8) {
            ForEach(attachments) { attachment in
                preview(attachment)
                    .overlay(alignment: .topTrailing) {
                        Button {
                            withAnimation(.easeOut(duration: 0.12)) {
                                attachments.removeAll { $0.id == attachment.id }
                            }
                        } label: {
                            Image(systemName: "xmark")
                                .font(.system(size: 8, weight: .bold))
                                .foregroundStyle(.white)
                                .frame(width: 18, height: 18)
                                .background(.black.opacity(0.65), in: Circle())
                        }
                        .buttonStyle(.plain)
                        .offset(x: 5, y: -5)
                        .help("Remove")
                    }
            }
        }
        .padding(.top, 6)
        .padding(.trailing, 6)
    }

    /// A picture as itself; any other file as its icon and name.
    @ViewBuilder private func preview(_ attachment: Attachment) -> some View {
        if let file = attachment.file {
            HStack(spacing: 8) {
                Image(nsImage: attachment.image)
                    .resizable()
                    .frame(width: 28, height: 28)
                Text(file.lastPathComponent)
                    .font(Fonts.sans(12.5, .medium))
                    .foregroundStyle(Palette.text)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .frame(maxWidth: 170, alignment: .leading)
            }
            .padding(.horizontal, 10)
            .frame(height: 56)
            .background(Palette.fill, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            .help(file.path)
        } else {
            Image(nsImage: attachment.image)
                .resizable()
                .scaledToFill()
                .frame(width: 56, height: 56)
                .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Palette.line))
        }
    }
}

/// Where dropped images will land, over the conversation while they are
/// dragged across it.
struct DropTarget: View {
    let label: String

    var body: some View {
        RoundedRectangle(cornerRadius: 18, style: .continuous)
            .strokeBorder(Palette.working, style: StrokeStyle(lineWidth: 2, dash: [6, 5]))
            .background(Palette.workingWash.opacity(0.6), in: RoundedRectangle(cornerRadius: 18, style: .continuous))
            .overlay {
                Label(label, systemImage: "photo.on.rectangle")
                    .font(TextStyle.bodyMedium)
                    .foregroundStyle(Palette.workingText)
            }
            .padding(18)
            .allowsHitTesting(false)
    }
}

/// A message the user sent an agent, split into the images that went with
/// it and its words. The daemon types each image's path ahead of the words,
/// from the folder where it keeps them. A picture pasted into the agent's
/// own terminal is kept wherever the agent keeps it, so only the agent's
/// record of the message (`attached`) can say it went with it.
struct SentMessage: Equatable {
    var images: [String] = []
    /// Files sent with it, named ahead of the words as @path.
    var files: [String] = []
    var text: String

    init(_ message: String, attached: [String] = []) {
        images = attached
        var rest = Substring(message)
        while let end = rest.firstIndex(where: \.isWhitespace) ?? (rest.isEmpty ? nil : rest.endIndex) {
            let token = rest[..<end]
            if SentMessage.isKeptImage(token) {
                if !attached.contains(String(token)) { images.append(String(token)) }
            } else if let file = SentMessage.sentFile(token) {
                files.append(file)
            } else {
                break
            }
            rest = rest[end...].drop(while: \.isWhitespace)
        }
        text = String(rest)
    }

    /// A file sent with a message: "@" and a path, to a file of the user's
    /// or one the daemon keeps. Only an absolute path to a file: "@docs/a.md"
    /// in a sentence stays words.
    static func sentFile(_ token: Substring) -> String? {
        guard token.hasPrefix("@/"), !token.contains("..") else { return nil }
        let path = String(token.dropFirst())
        var directory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: path, isDirectory: &directory), !directory.boolValue else { return nil }
        return path
    }

    /// An image the daemon filed for a message: under its own images folder,
    /// so nothing else a message mentions is read from disk.
    static func isKeptImage(_ token: Substring) -> Bool {
        token.hasPrefix("/") && token.contains("/.agentman/images/") && !token.contains("..") &&
            ["png", "jpg", "jpeg", "gif", "webp"].contains(token.split(separator: ".").last?.lowercased() ?? "")
    }
}

/// The files sent with a message, by name, over its bubble; a click opens
/// one as Finder would.
struct SentFileChips: View {
    let paths: [String]

    var body: some View {
        HStack(spacing: 8) {
            ForEach(paths, id: \.self) { path in
                Button {
                    NSWorkspace.shared.open(URL(fileURLWithPath: path))
                } label: {
                    HStack(spacing: 7) {
                        Image(nsImage: NSWorkspace.shared.icon(forFile: path))
                            .resizable()
                            .frame(width: 18, height: 18)
                        Text((path as NSString).lastPathComponent)
                            .font(Fonts.sans(12.5, .medium))
                            .foregroundStyle(Palette.text)
                            .lineLimit(1)
                            .truncationMode(.middle)
                    }
                    .padding(.horizontal, 12)
                    .padding(.vertical, 8)
                    .background(Palette.toolCard, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
                }
                .buttonStyle(.plain)
                .help(path)
            }
        }
    }
}

/// The images sent with a message, as thumbnails that open full window,
/// read from where the daemon filed them on this Mac.
struct SentFiles: View {
    let paths: [String]

    var body: some View {
        HStack(spacing: 8) {
            ForEach(paths, id: \.self) { path in
                SentFile(path: path, group: paths)
            }
        }
    }
}

private struct SentFile: View {
    @Environment(AgentsStore.self) private var store
    @Environment(\.lightboxPlace) private var lightboxPlace
    let path: String
    /// The message's images, which the arrows go through.
    var group: [String] = []
    /// The thumbnail; the picture itself is read when it is opened.
    @State private var image: NSImage?
    @State private var missing = false

    init(path: String, group: [String] = []) {
        self.path = path
        self.group = group
        _image = State(initialValue: Thumbnail.cached(Thumbnail.key(path, "110")))
    }

    var body: some View {
        Group {
            if let image {
                Button {
                    store.lightbox = .files(group, from: path, place: lightboxPlace)
                } label: {
                    Image(nsImage: image)
                        .resizable()
                        .scaledToFill()
                        .frame(width: min(220, max(80, 110 * image.size.width / max(image.size.height, 1))), height: 110)
                        .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
                        .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(Palette.line))
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help("Open full window")
                .contextMenu {
                    Button("Copy Image") { ImageClipboard.copy(NSImage(contentsOfFile: path) ?? image) }
                }
            } else if missing {
                Label("Image no longer kept", systemImage: "photo")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
            } else {
                RoundedRectangle(cornerRadius: 12, style: .continuous)
                    .fill(Palette.fill)
                    .frame(width: 110, height: 110)
            }
        }
        .task(id: path) {
            guard image == nil else { return }
            let loaded = await Thumbnail.file(path, height: 110)
            image = loaded
            missing = loaded == nil
        }
    }
}
