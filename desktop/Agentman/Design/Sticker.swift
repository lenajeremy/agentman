import AppKit
import ImageIO
import SwiftUI

/// The website's stickers, the phone's own files: animated WebP exported from
/// brand/mascot with fun-isometric, played frame by frame by ImageIO, and a
/// still frame under Reduce Motion.
struct Sticker: View {
    enum Name: String {
        case phone, terminal, heart, notes, mascot
    }

    let name: Name
    var size: CGFloat
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        StickerPlayer(name: name.rawValue, moving: !reduceMotion)
            .frame(width: size, height: size)
            .accessibilityHidden(true)
    }
}

/// A sticker on a patch of the site's dots, as the empty screens show it.
/// On a dark page it sits on a cream disc: its black outline would vanish on
/// black.
struct StickerOnDots: View {
    let name: Sticker.Name
    var size: CGFloat
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        ZStack {
            DotPatch()
                .frame(width: size * 1.14, height: size * 1.14)
            if scheme == .dark {
                Circle()
                    .fill(Color(light: 0xF3E6D8, dark: 0xF3E6D8))
                    .frame(width: size * 0.84, height: size * 0.84)
            }
            Sticker(name: name, size: size)
        }
        .frame(width: size * 1.14, height: size * 1.14)
    }
}

/// The site's dots, fading out from the middle.
struct DotPatch: View {
    var spacing: CGFloat = 14

    var body: some View {
        Canvas { context, size in
            let centre = CGPoint(x: size.width / 2, y: size.height / 2)
            let reach = min(size.width, size.height) / 2
            var y = spacing / 2
            while y < size.height {
                var x = spacing / 2
                while x < size.width {
                    let distance = hypot(x - centre.x, y - centre.y) / reach
                    // Full inside the middle half, gone at the edge.
                    let strength = max(0, min(1, (1 - distance) / 0.55))
                    if strength > 0 {
                        context.fill(Path(ellipseIn: CGRect(x: x - 1.3, y: y - 1.3, width: 2.6, height: 2.6)),
                                     with: .color(Palette.creamDots.opacity(strength)))
                    }
                    x += spacing
                }
                y += spacing
            }
        }
        .allowsHitTesting(false)
    }
}

private struct StickerPlayer: NSViewRepresentable {
    let name: String
    let moving: Bool

    func makeNSView(context: Context) -> StickerView { StickerView() }

    func updateNSView(_ view: StickerView, context: Context) {
        view.play(name: name, moving: moving)
    }

    static func dismantleNSView(_ view: StickerView, coordinator: ()) {
        view.stop()
    }
}

/// Plays one sticker into its layer. Each run has its own token, so a
/// sticker swapped or taken away stops the frames meant for it.
final class StickerView: NSView {
    private final class Run: @unchecked Sendable {
        var stopped = false
    }

    private var current: Run?
    private var playing: (name: String, moving: Bool)?

    override init(frame: NSRect) {
        super.init(frame: frame)
        wantsLayer = true
        layer?.contentsGravity = .resizeAspect
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    func play(name: String, moving: Bool) {
        if let playing, playing.name == name, playing.moving == moving { return }
        stop()
        playing = (name, moving)
        // The still first, so a sticker is never blank while it starts or
        // where frames are not drawn (a picture of the window).
        if let url = Bundle.main.url(forResource: name, withExtension: "png"),
           let source = CGImageSourceCreateWithURL(url as CFURL, nil),
           let image = CGImageSourceCreateImageAtIndex(source, 0, nil) {
            layer?.contents = image
        }
        guard moving, let url = Bundle.main.url(forResource: name, withExtension: "webp") else { return }
        let run = Run()
        current = run
        CGAnimateImageAtURLWithBlock(url as CFURL, nil) { [weak self] _, image, stop in
            MainActor.assumeIsolated {
                guard let self, !run.stopped else {
                    stop.pointee = true
                    return
                }
                self.layer?.contents = image
            }
        }
    }

    func stop() {
        current?.stopped = true
        current = nil
        playing = nil
    }
}
