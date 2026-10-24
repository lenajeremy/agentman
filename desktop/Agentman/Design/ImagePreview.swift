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
