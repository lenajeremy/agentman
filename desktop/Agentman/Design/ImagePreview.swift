import AppKit
import SwiftUI

/// An image opened full window: what an agent or Agentman looked at.
struct Lightbox: Identifiable {
    let id = UUID()
    let image: NSImage
    let title: String
    /// The window it opens over: an editor's folder for its agent panel,
    /// nil for Agentman's own window.
    var place: String?
}

extension EnvironmentValues {
    /// Which window an image opened here should show over: an editor's
    /// folder in its agent panel, nil in Agentman's window.
    @Entry var lightboxPlace: String?
}

/// An image an agent or Agentman read, as a short label and a thumbnail.
/// Clicking the thumbnail opens it full window.
struct ImageThumbnail: View {
    @Environment(AgentsStore.self) private var store
    @Environment(\.lightboxPlace) private var lightboxPlace
    let sessionId: String
    let path: String
    let label: String
    /// In an agent's thread: the tool call that read it in a tool card, and
    /// the picture under the card, outside it.
    var carded = false
    /// The picture alone, for a row of them under one run of reads.
    var pictureOnly = false
    @State private var image: NSImage?
    @State private var failed = false

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
            // A daemon that has just started may not know the agent's files
            // yet; it is asked a few times before the thumbnail gives up.
            for attempt in 0..<4 {
                if attempt > 0 { try? await Task.sleep(for: .seconds(1.5)) }
                image = await store.manager.image(sessionId: sessionId, path: path)
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
                store.lightbox = Lightbox(image: image, title: label, place: lightboxPlace)
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
        guard let pattern = try? Regex(#"/[^\s"'`]+?\.(?i:png|jpe?g|gif|webp|heic|bmp|tiff?)"#),
              let match = text.firstMatch(of: pattern) else { return nil }
        return String(text[match.range])
    }
}

/// The full-window view of an image, over everything; Escape, the button or
/// a click outside the image closes it.
struct LightboxView: View {
    @Environment(AgentsStore.self) private var store
    let box: Lightbox

    var body: some View {
        ZStack {
            Color.black.opacity(0.82)
                .ignoresSafeArea()
                .onTapGesture { store.lightbox = nil }
            VStack(spacing: 14) {
                Image(nsImage: box.image)
                    .resizable()
                    .scaledToFit()
                    .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
                    .shadow(color: .black.opacity(0.4), radius: 30, y: 10)
                Text(box.title)
                    .font(TextStyle.caption)
                    .foregroundStyle(.white.opacity(0.75))
            }
            .padding(48)
        }
        .overlay(alignment: .topTrailing) {
            Button {
                store.lightbox = nil
            } label: {
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
        .transition(.opacity)
    }
}

/// The images the user sent with a message, as thumbnails that open full
/// window.
struct SentImages: View {
    let names: [String]

    var body: some View {
        HStack(spacing: 8) {
            ForEach(names, id: \.self) { name in
                SentImage(name: name)
            }
        }
    }
}

private struct SentImage: View {
    @Environment(AgentsStore.self) private var store
    @Environment(\.lightboxPlace) private var lightboxPlace
    let name: String
    @State private var image: NSImage?

    var body: some View {
        Group {
            if let image {
                Button {
                    store.lightbox = Lightbox(image: image, title: "Sent to Agentman", place: lightboxPlace)
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
                SentFile(path: path)
            }
        }
    }
}

private struct SentFile: View {
    @Environment(AgentsStore.self) private var store
    @Environment(\.lightboxPlace) private var lightboxPlace
    let path: String
    @State private var image: NSImage?
    @State private var missing = false

    var body: some View {
        Group {
            if let image {
                Button {
                    store.lightbox = Lightbox(image: image, title: (path as NSString).lastPathComponent, place: lightboxPlace)
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
            let loaded = await Task.detached { NSImage(contentsOfFile: path) }.value
            image = loaded
            missing = loaded == nil
        }
    }
}
