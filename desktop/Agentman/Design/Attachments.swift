import AppKit
import UniformTypeIdentifiers

/// An image waiting in the message box to go to Agentman.
struct Attachment: Identifiable {
    let id = UUID()
    let image: NSImage
    let data: Data
    let mime: String
}

/// Turning what the user attaches, pastes or drops into images Agentman's
/// model can read: at most four, each no larger than the model uses, and
/// all of them within what one message to the daemon can carry. On the
/// main actor, where pastes and drops arrive.
@MainActor
enum Attachments {
    static let maxCount = 4
    /// The longest edge Claude reads an image at; anything larger is
    /// shrunk to it anyway.
    static let maxEdge: CGFloat = 1568
    /// What one message's images may come to, in base64: within the local
    /// socket's 16 MB frame with room to spare, so four screenshots go at the
    /// size the model reads without being squeezed.
    static let budget = 12_000_000

    /// An attachment from an image: shrunk to fit, as PNG when that stays
    /// small (screenshots keep their text sharp), as JPEG otherwise.
    static func make(_ image: NSImage, edge: CGFloat = maxEdge, quality: CGFloat = 0.85) -> Attachment? {
        guard let source = image.cgImage(forProposedRect: nil, context: nil, hints: nil) else { return nil }
        let scale = min(1, edge / CGFloat(max(source.width, source.height)))
        let width = max(1, Int(CGFloat(source.width) * scale))
        let height = max(1, Int(CGFloat(source.height) * scale))
        guard let context = CGContext(
            data: nil, width: width, height: height, bitsPerComponent: 8, bytesPerRow: 0,
            space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue
        ) else { return nil }
        context.interpolationQuality = .high
        context.draw(source, in: CGRect(x: 0, y: 0, width: width, height: height))
        guard let scaled = context.makeImage() else { return nil }
        let rep = NSBitmapImageRep(cgImage: scaled)
        let shown = NSImage(cgImage: scaled, size: NSSize(width: width, height: height))
        if quality >= 0.85, let png = rep.representation(using: .png, properties: [:]), png.count <= 700_000 {
            return Attachment(image: shown, data: png, mime: "image/png")
        }
        guard let jpeg = rep.representation(using: .jpeg, properties: [.compressionFactor: quality]) else { return nil }
        return Attachment(image: shown, data: jpeg, mime: "image/jpeg")
    }

    static func make(file url: URL) -> Attachment? {
        guard let type = UTType(filenameExtension: url.pathExtension), type.conforms(to: .image),
              let image = NSImage(contentsOf: url) else { return nil }
        return make(image)
    }

    /// The images on a pasteboard: image data, or image files copied in
    /// Finder. Empty when it holds only text.
    static func from(pasteboard: NSPasteboard) -> [Attachment] {
        if let urls = pasteboard.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL] {
            let files = urls.compactMap(make(file:))
            if !files.isEmpty { return files }
        }
        if pasteboard.canReadItem(withDataConformingToTypes: [UTType.png.identifier, UTType.tiff.identifier, UTType.jpeg.identifier]),
           let images = pasteboard.readObjects(forClasses: [NSImage.self]) as? [NSImage] {
            return images.compactMap { make($0) }
        }
        return []
    }

    /// The images in a drop: image files, or image data.
    static func from(providers: [NSItemProvider]) async -> [Attachment] {
        var found: [Attachment] = []
        for provider in providers {
            if provider.hasItemConformingToTypeIdentifier(UTType.fileURL.identifier),
               let url = await fileURL(from: provider), let attachment = make(file: url) {
                found.append(attachment)
            } else if provider.canLoadObject(ofClass: NSImage.self), let image = await image(from: provider),
                      let attachment = make(image) {
                found.append(attachment)
            }
        }
        return found
    }

    /// The images as the daemon takes them, squeezed further if together
    /// they would be too large for one message.
    static func encoded(_ attachments: [Attachment]) -> [InlineImage] {
        var images = attachments.map { InlineImage(mime: $0.mime, data: $0.data.base64EncodedString()) }
        // Smaller and softer, a step at a time, until the message fits.
        for (edge, quality) in [(1200.0, 0.6), (1000.0, 0.5), (800.0, 0.45), (640.0, 0.4), (480.0, 0.35)] {
            guard images.reduce(0, { $0 + $1.data.count }) > budget else { break }
            images = attachments.compactMap { make($0.image, edge: edge, quality: quality) }
                .map { InlineImage(mime: $0.mime, data: $0.data.base64EncodedString()) }
        }
        return images
    }

    private static func fileURL(from provider: NSItemProvider) async -> URL? {
        await withCheckedContinuation { continuation in
            _ = provider.loadObject(ofClass: URL.self) { url, _ in continuation.resume(returning: url) }
        }
    }

    private static func image(from provider: NSItemProvider) async -> NSImage? {
        await withCheckedContinuation { continuation in
            _ = provider.loadObject(ofClass: NSImage.self) { image, _ in continuation.resume(returning: image as? NSImage) }
        }
    }
}
