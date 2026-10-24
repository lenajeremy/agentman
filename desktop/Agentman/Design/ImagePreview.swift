import AppKit
import SwiftUI

/// An image opened full window: what an agent or Agentman looked at.
struct Lightbox: Identifiable {
    let id = UUID()
    let image: NSImage
    let title: String
}

/// An image an agent or Agentman read, as a short label and a thumbnail.
/// Clicking the thumbnail opens it full window.
struct ImageThumbnail: View {
    @Environment(AgentsStore.self) private var store
    let sessionId: String
    let path: String
    let label: String
    @State private var image: NSImage?
    @State private var failed = false

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
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

            if let image {
                Button {
                    store.lightbox = Lightbox(image: image, title: label)
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
                .padding(.leading, 18)
            } else if !failed {
                RoundedRectangle(cornerRadius: 10, style: .continuous)
                    .fill(Palette.fill)
                    .frame(width: 160, height: 120)
                    .overlay(ProgressView().controlSize(.small))
                    .padding(.leading, 18)
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
    let name: String
    @State private var image: NSImage?

    var body: some View {
        Group {
            if let image {
                Button {
                    store.lightbox = Lightbox(image: image, title: "Sent to Agentman")
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
                Image(nsImage: attachment.image)
                    .resizable()
                    .scaledToFill()
                    .frame(width: 56, height: 56)
                    .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Palette.line))
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
}

/// Pasting images into the message box: ⌘V with images on the pasteboard
/// attaches them; with text, it pastes as ever.
struct PasteImages: ViewModifier {
    var active: Bool
    var onImages: ([Attachment]) -> Void
    @State private var monitor = PasteMonitor()

    func body(content: Content) -> some View {
        // The monitor outlives this copy of the view, so it is handed what is
        // true now on every update rather than keeping what was true when it
        // was installed: then, the box had not taken focus yet.
        monitor.active = active
        monitor.onImages = onImages
        return content
            .onAppear { monitor.start() }
            .onDisappear { monitor.stop() }
    }
}

/// Watches for ⌘V on its way to the focused text field, and takes it when
/// the pasteboard holds images.
@MainActor
final class PasteMonitor {
    var active = false
    var onImages: ([Attachment]) -> Void = { _ in }
    private var token: Any?

    func start() {
        guard token == nil else { return }
        token = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            guard PasteMonitor.isPaste(event) else { return event }
            let taken = MainActor.assumeIsolated { self?.take() ?? false }
            return taken ? nil : event
        }
    }

    func stop() {
        if let token { NSEvent.removeMonitor(token) }
        token = nil
    }

    /// Attaches the pasteboard's images, if the box is focused and there
    /// are any.
    func take(from pasteboard: NSPasteboard = .general) -> Bool {
        guard active else { return false }
        let found = Attachments.from(pasteboard: pasteboard)
        guard !found.isEmpty else { return false }
        onImages(found)
        return true
    }

    /// ⌘V, with Caps Lock on or off. On a layout without Latin letters
    /// (Cyrillic, Greek…) it is the key where V is on a US keyboard, as it
    /// is for the Edit menu. ⌥ and ⌃ make it a different shortcut.
    nonisolated static func isPaste(_ event: NSEvent) -> Bool {
        let flags = event.modifierFlags.intersection(.deviceIndependentFlagsMask)
        guard flags.contains(.command), flags.isDisjoint(with: [.option, .control]) else { return false }
        return isPasteKey(characters: event.charactersIgnoringModifiers ?? "", keyCode: event.keyCode)
    }

    nonisolated static func isPasteKey(characters: String, keyCode: UInt16) -> Bool {
        let typed = characters.lowercased()
        if typed == "v" { return true }
        let latin = typed.unicodeScalars.allSatisfy { $0.isASCII }
        return !latin && keyCode == 9
    }
}
