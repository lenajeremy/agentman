import AppKit
import SwiftUI

/// A picture fitted to where it shows, never drawn larger than its own size,
/// that zooms: the scroll wheel or a pinch about the pointer, ⌘+ and ⌘−, ⌘0
/// to fit it again, a double-click for its actual size and back. Zoomed in,
/// it scrolls. The image viewer and the
/// editor's image files both show pictures this way.
struct ZoomableImage: View {
    let image: NSImage
    /// How far it is zoomed past fitting: 1 fits.
    @Binding var zoom: CGFloat
    /// A click beside the picture, which closes the image viewer.
    var onTapBeside: (() -> Void)?

    /// The furthest in, in times its actual size.
    static let deepest: CGFloat = 8
    /// One step of ⌘+ or ⌘−.
    static let step: CGFloat = 1.25

    /// The scale that fits a picture this size in this space, never above
    /// its own size: a small picture isn't blown up to fill the window.
    static func fitScale(_ picture: CGSize, in space: CGSize) -> CGFloat {
        guard picture.width > 0, picture.height > 0, space.width > 0, space.height > 0 else { return 1 }
        return min(1, space.width / picture.width, space.height / picture.height)
    }

    /// The zoom that shows the picture at its own size, or twice that when
    /// fitting already does.
    static func actualSize(fit: CGFloat) -> CGFloat {
        fit < 1 ? 1 / fit : 2
    }

    /// A zoom held between fitting and the deepest.
    static func clamp(_ zoom: CGFloat, fit: CGFloat) -> CGFloat {
        min(max(zoom, 1), max(deepest / max(fit, 0.0001), 1))
    }

    var body: some View {
        PictureScroller(image: image, zoom: $zoom, onTapBeside: onTapBeside)
            .background {
                // ⌘+ (and ⌘=, the same key), ⌘− and ⌘0. Here rather than in a
                // menu, so they reach the picture before the editor's text
                // size commands; the scroll view holds them to its bounds.
                Group {
                    Button("Zoom In") { zoom *= Self.step }
                        .keyboardShortcut("+", modifiers: .command)
                    Button("Zoom In") { zoom *= Self.step }
                        .keyboardShortcut("=", modifiers: .command)
                    Button("Zoom Out") { zoom = max(1, zoom / Self.step) }
                        .keyboardShortcut("-", modifiers: .command)
                    Button("Fit") { zoom = 1 }
                        .keyboardShortcut("0", modifiers: .command)
                }
                .opacity(0)
                .allowsHitTesting(false)
                .accessibilityHidden(true)
            }
    }
}

/// AppKit's own magnifying scroll view, so a pinch zooms about the pointer
/// and a zoomed picture scrolls as any Mac document does.
private struct PictureScroller: NSViewRepresentable {
    let image: NSImage
    @Binding var zoom: CGFloat
    var onTapBeside: (() -> Void)?

    func makeNSView(context: Context) -> PictureScrollView {
        let view = PictureScrollView()
        view.show(image)
        return view
    }

    func updateNSView(_ view: PictureScrollView, context: Context) {
        view.onTapBeside = onTapBeside
        // The user's own zooming, told back without updating while SwiftUI
        // updates.
        view.onZoom = { zoom in
            DispatchQueue.main.async {
                if abs(self.zoom - zoom) > 0.001 { self.zoom = zoom }
            }
        }
        view.show(image)
        if abs(view.zoom - zoom) > 0.001 {
            view.setZoom(zoom)
        }
    }
}

/// The picture at its own size as the document, magnified to fit, and a
/// clip view that keeps it centred while it is smaller than the view.
final class PictureScrollView: NSScrollView {
    let picture = PictureView()
    var onTapBeside: (() -> Void)?
    var onZoom: ((CGFloat) -> Void)?
    /// The magnification that fits the picture.
    private(set) var fit: CGFloat = 1
    /// How far past fitting it is zoomed: 1 fits.
    private(set) var zoom: CGFloat = 1
    /// Whether the scroll under way zooms rather than pans, decided as it
    /// begins, so one gesture doesn't turn from one to the other.
    private var wheelZooms = false

    init() {
        super.init(frame: .zero)
        contentView = CentringClipView()
        documentView = picture
        drawsBackground = false
        borderType = .noBorder
        hasVerticalScroller = true
        hasHorizontalScroller = true
        autohidesScrollers = true
        scrollerStyle = .overlay
        allowsMagnification = true
        picture.onDoubleClick = { [weak self] point in self?.toggleZoom(at: point) }
        NotificationCenter.default.addObserver(self, selector: #selector(pinched), name: NSScrollView.didEndLiveMagnifyNotification, object: self)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("init(coder:) is not used") }

    func show(_ image: NSImage) {
        guard picture.image !== image else { return }
        picture.image = image
        picture.frame = NSRect(origin: .zero, size: image.size)
        zoom = 1
        refit()
    }

    /// Zooms to this far past fitting, about a point in the picture or the
    /// middle of what shows.
    func setZoom(_ wanted: CGFloat, at point: NSPoint? = nil) {
        zoom = ZoomableImage.clamp(wanted, fit: fit)
        let visible = documentVisibleRect
        setMagnification(fit * zoom, centeredAt: point ?? NSPoint(x: visible.midX, y: visible.midY))
        if abs(zoom - wanted) > 0.001 { onZoom?(zoom) }
    }

    override func setFrameSize(_ size: NSSize) {
        super.setFrameSize(size)
        refit()
    }

    /// Fits again after the picture or the view changes size: a fitted
    /// picture stays fitted; a zoomed one keeps its scale where it can.
    private func refit() {
        let fitted = ZoomableImage.fitScale(picture.frame.size, in: bounds.size)
        let kept = zoom == 1 ? fitted : fitted * ZoomableImage.clamp(magnification / fitted, fit: fitted)
        fit = fitted
        minMagnification = fitted
        maxMagnification = max(ZoomableImage.deepest, fitted)
        if abs(magnification - kept) > 0.0001 { magnification = kept }
        let now = kept / fitted
        if abs(now - zoom) > 0.001 {
            zoom = now
            onZoom?(zoom)
        }
    }

    private func toggleZoom(at point: NSPoint) {
        setZoom(zoom == 1 ? ZoomableImage.actualSize(fit: fit) : 1, at: point)
        onZoom?(zoom)
    }

    /// A mouse's wheel zooms about the pointer. A trackpad's scroll zooms a
    /// picture that fits, which has nowhere to go, and pans one zoomed in;
    /// with ⌘ held, any scroll zooms.
    override func scrollWheel(with event: NSEvent) {
        let starting = event.phase.contains(.began) || event.phase.contains(.mayBegin)
            || (event.phase.isEmpty && event.momentumPhase.isEmpty)
        if starting {
            wheelZooms = !event.hasPreciseScrollingDeltas || event.modifierFlags.contains(.command) || zoom == 1
        }
        guard wheelZooms else { return super.scrollWheel(with: event) }
        // A flick's coast isn't zoom.
        guard event.momentumPhase.isEmpty else { return }
        // Away from the person, the wheel rolled up or two fingers pushed up,
        // zooms in, whichever way scrolling is set to go.
        let away = event.scrollingDeltaY * (event.isDirectionInvertedFromDevice ? -1 : 1)
        wheelZoom(by: away, precise: event.hasPreciseScrollingDeltas, at: picture.convert(event.locationInWindow, from: nil))
    }

    /// Zooms by a scroll of this much, about a point in the picture: a notch
    /// of a wheel a step of ⌘+, a trackpad's points smoothly.
    func wheelZoom(by away: CGFloat, precise: Bool, at point: NSPoint) {
        guard away != 0 else { return }
        let factor = precise ? exp(away * 0.01) : pow(ZoomableImage.step, max(min(away, 3), -3))
        setZoom(zoom * factor, at: point)
        onZoom?(zoom)
    }

    override func smartMagnify(with event: NSEvent) {
        toggleZoom(at: picture.convert(event.locationInWindow, from: nil))
    }

    @objc private func pinched() {
        zoom = magnification / fit
        if abs(zoom - 1) < 0.01 { zoom = 1 }
        onZoom?(zoom)
    }
}

/// Keeps a picture smaller than the view in its middle, and takes a click
/// beside it.
private final class CentringClipView: NSClipView {
    override func constrainBoundsRect(_ proposed: NSRect) -> NSRect {
        var rect = super.constrainBoundsRect(proposed)
        guard let document = documentView?.frame else { return rect }
        if rect.width > document.width { rect.origin.x = document.minX - (rect.width - document.width) / 2 }
        if rect.height > document.height { rect.origin.y = document.minY - (rect.height - document.height) / 2 }
        return rect
    }

    override func mouseDown(with event: NSEvent) {
        if event.clickCount == 1, let scroller = superview as? PictureScrollView {
            scroller.onTapBeside?()
        }
    }
}

/// The picture itself: a click on it isn't one beside it, a double-click
/// zooms where it lands, and a right-click offers to copy it.
final class PictureView: NSImageView {
    var onDoubleClick: ((NSPoint) -> Void)?

    init() {
        super.init(frame: .zero)
        imageScaling = .scaleAxesIndependently
        isEditable = false
        animates = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("init(coder:) is not used") }

    override func mouseDown(with event: NSEvent) {
        if event.clickCount == 2 {
            onDoubleClick?(convert(event.locationInWindow, from: nil))
        }
    }

    override func menu(for event: NSEvent) -> NSMenu? {
        guard image != nil else { return nil }
        let menu = NSMenu()
        menu.addItem(withTitle: "Copy Image", action: #selector(copyImage), keyEquivalent: "").target = self
        return menu
    }

    @objc func copyImage() {
        if let image { ImageClipboard.copy(image) }
    }
}
