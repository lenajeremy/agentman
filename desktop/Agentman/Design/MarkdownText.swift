import AppKit
import SwiftUI

// What an agent said, as Mac text views: the prose of a reply (paragraphs,
// headings, lists, quotes, rules) in one, so a selection runs through all of
// it as in Notes or Mail, and each code block in one of its own.
//
// SwiftUI's selectable Text selects within one Text only, so a reply could be
// selected a paragraph at a time, and it painted its highlight under inline
// code's and a code block's grey, where it couldn't be seen. TextKit 1 draws a
// selection over a run's background; TextKit 2, which a text view uses by
// default, draws the background over the selection, the same fault. Both
// views here are TextKit 1.
//
// Passive replies draw via a lightweight TextKit 1 NSView surface whose
// backing layer stays cached while scrolling (AppKit dirties NSTextView's
// backing layer on every scroll step, even offscreen). Clicking, dragging, or
// right-clicking promotes that surface in place to a live MarkdownTextView so
// selection, links, copying, context menus and Reply quotes work natively.

/// The prose of a reply as one selectable text view. The view reaches a
/// little past the text on each side, its text inset by as much, so inline
/// code that starts a line has room for its box's padding there too.
struct MarkdownProse: View {
    let blocks: [MarkdownBlock]
    let key: String

    var body: some View {
        MarkdownProseView(blocks: blocks, key: key)
            .padding(.horizontal, -InlineCodeLayoutManager.padding)
    }
}

struct MarkdownProseView: NSViewRepresentable {
    let blocks: [MarkdownBlock]
    /// What the blocks were made from: the text is built again only when
    /// this changes, so a selection survives the view being drawn again.
    let key: String

    func makeCoordinator() -> MarkdownTextCoordinator { MarkdownTextCoordinator() }

    func makeNSView(context: Context) -> MarkdownTextSurface {
        let view = MarkdownTextSurface(wraps: true)
        view.coordinator = context.coordinator
        return view
    }

    func updateNSView(_ view: MarkdownTextSurface, context: Context) {
        let coordinator = context.coordinator
        coordinator.open = context.environment.openURL
        let highlight = context.environment.findHighlight
        sync(view, coordinator: coordinator, highlight: highlight)
    }

    func sizeThatFits(_ proposal: ProposedViewSize, nsView: MarkdownTextSurface, context: Context) -> CGSize? {
        sync(nsView, coordinator: context.coordinator, highlight: context.environment.findHighlight)
        let width = proposal.width.flatMap { $0.isFinite && $0 > 0 ? $0 : nil } ?? 600
        return nsView.size(fittingWidth: width)
    }

    private func sync(_ view: MarkdownTextSurface, coordinator: MarkdownTextCoordinator, highlight: FindHighlight?) {
        guard coordinator.key != key || coordinator.highlight != highlight else { return }
        coordinator.key = key
        coordinator.highlight = highlight
        let attributed = MarkdownProseCache.attributed(key: key, blocks: blocks, highlight: highlight)
        view.configure(key: key, highlight: highlight, attributed: attributed)
    }
}

/// A code block's text, one line a line: its box scrolls sideways for a
/// long one, as before.
struct MarkdownCode: NSViewRepresentable {
    let text: NSAttributedString
    let key: String

    func makeCoordinator() -> MarkdownTextCoordinator { MarkdownTextCoordinator() }

    func makeNSView(context: Context) -> MarkdownTextSurface {
        let view = MarkdownTextSurface(wraps: false)
        view.coordinator = context.coordinator
        return view
    }

    func updateNSView(_ view: MarkdownTextSurface, context: Context) {
        sync(view, coordinator: context.coordinator)
    }

    func sizeThatFits(_ proposal: ProposedViewSize, nsView: MarkdownTextSurface, context: Context) -> CGSize? {
        sync(nsView, coordinator: context.coordinator)
        return nsView.size(fittingWidth: nil)
    }

    private func sync(_ view: MarkdownTextSurface, coordinator: MarkdownTextCoordinator) {
        guard coordinator.key != key else { return }
        coordinator.key = key
        view.configure(key: key, highlight: nil, attributed: text)
    }
}

@MainActor
final class MarkdownTextCoordinator: NSObject, NSTextViewDelegate {
    var key: String?
    var highlight: FindHighlight?
    var open: OpenURLAction?

    /// A link goes where SwiftUI's would: the editor's preview follows a link
    /// to a file in the project, a reply's opens in the browser.
    func textView(_ textView: NSTextView, clickedOnLink link: Any, at charIndex: Int) -> Bool {
        let url = (link as? URL) ?? (link as? String).flatMap(URL.init(string:))
        guard let url else { return false }
        if let open { open(url) } else { NSWorkspace.shared.open(url) }
        return true
    }
}

/// Two-generation cache for built prose NSAttributedStrings so scrolling a
/// reply back into view reuses its attributed text in O(1).
@MainActor
enum MarkdownProseCache {
    private struct CacheKey: Hashable {
        let key: String
        let words: [String]
        let current: Bool

        init(key: String, highlight: FindHighlight?) {
            self.key = key
            self.words = highlight?.words ?? []
            self.current = highlight?.current ?? false
        }
    }

    private static var recent: [CacheKey: NSAttributedString] = [:]
    private static var older: [CacheKey: NSAttributedString] = [:]
    static let generation = 250

    static func attributed(key: String, blocks: [MarkdownBlock], highlight: FindHighlight?) -> NSAttributedString {
        let cacheKey = CacheKey(key: key, highlight: highlight)
        if let hit = recent[cacheKey] { return hit }
        let built: NSAttributedString
        if let kept = older.removeValue(forKey: cacheKey) {
            built = kept
        } else {
            built = MarkdownProseText.attributed(blocks, highlight: highlight)
        }
        if recent.count >= generation {
            older = recent
            recent = [:]
        }
        recent[cacheKey] = built
        return built
    }

    static func clear() {
        recent.removeAll()
        older.removeAll()
    }
}

/// Two-generation cache for measured TextKit 1 sizes keyed by content identity
/// and quantized width, eliminating SwiftUI's repeated width-probe ping-pong
/// and making warm LazyVStack re-entry zero-layout.
@MainActor
enum MarkdownSizeCache {
    private struct CacheKey: Hashable {
        let key: String
        let words: [String]
        let current: Bool
        let widthHalfPt: Int
        let wraps: Bool

        init(key: String, highlight: FindHighlight?, widthHalfPt: Int, wraps: Bool) {
            self.key = key
            self.words = highlight?.words ?? []
            self.current = highlight?.current ?? false
            self.widthHalfPt = widthHalfPt
            self.wraps = wraps
        }
    }

    private static var recent: [CacheKey: CGSize] = [:]
    private static var older: [CacheKey: CGSize] = [:]
    static let generation = 800

    static func get(key: String, highlight: FindHighlight?, widthHalfPt: Int, wraps: Bool) -> CGSize? {
        let cacheKey = CacheKey(key: key, highlight: highlight, widthHalfPt: widthHalfPt, wraps: wraps)
        if let hit = recent[cacheKey] { return hit }
        if let kept = older.removeValue(forKey: cacheKey) {
            store(kept, for: cacheKey)
            return kept
        }
        return nil
    }

    static func set(_ size: CGSize, key: String, highlight: FindHighlight?, widthHalfPt: Int, wraps: Bool) {
        let cacheKey = CacheKey(key: key, highlight: highlight, widthHalfPt: widthHalfPt, wraps: wraps)
        store(size, for: cacheKey)
    }

    private static func store(_ size: CGSize, for cacheKey: CacheKey) {
        if recent.count >= generation {
            older = recent
            recent = [:]
        }
        recent[cacheKey] = size
    }

    static func clear() {
        recent.removeAll()
        older.removeAll()
    }
}

/// Shared TextKit 1 line-spacing rule: space between a paragraph's lines only,
/// never after its last line. Uses mutableString.character(at:) to avoid
/// bridging the whole storage string on every line fragment.
@MainActor
private func markdownLineSpacing(in layoutManager: NSLayoutManager, storage: NSTextStorage?,
                                 wraps: Bool, glyphIndex: Int) -> CGFloat {
    guard let storage, storage.length > 0 else { return 0 }
    let index = layoutManager.characterIndexForGlyph(at: glyphIndex)
    guard index < storage.length else { return 0 }
    if index == storage.length - 1 { return 0 }
    // In prose a newline ends a paragraph; in code every line is one,
    // and the space goes between them all. A line ending a paragraph is
    // asked about by its last glyph before the newline.
    if wraps {
        let chars = storage.mutableString
        if chars.character(at: index) == 0x0A || chars.character(at: index + 1) == 0x0A { return 0 }
    }
    let style = storage.attribute(.paragraphStyle, at: index, effectiveRange: nil) as? NSParagraphStyle
    return style?.lineSpacing ?? 0
}

/// Lightweight TextKit 1 drawing surface for read-only markdown prose and code.
/// Uses layer-backed `updateLayer()` with a cached Retina CGImage so AppKit
/// never re-rasterizes its backing store during scroll frames; activates a
/// live MarkdownTextView child on pointer interaction for native selection,
/// links, copying, context menus, and Reply quotes.
final class MarkdownTextSurface: NSView, @preconcurrency NSLayoutManagerDelegate {
    private let wraps: Bool
    private let side: CGFloat
    private let storage = NSTextStorage()
    private let layout = InlineCodeLayoutManager()
    private let container: NSTextContainer

    weak var coordinator: MarkdownTextCoordinator?
    private(set) var currentKey = ""
    private var currentHighlight: FindHighlight?
    private var cachedAttributed: NSAttributedString?
    private var linkRanges: [NSRange] = []
    /// Width currently laid out in `container`, or nil if invalidated.
    private var laidOutWidth: CGFloat?
    /// Per-instance size memo for SwiftUI's repeated width probes.
    private var localSizes: [Int: CGSize] = [:]
    private(set) var activeTextView: MarkdownTextView?
    /// Tracks what is already rasterized into `layer.contents` / `tileLayers`.
    private struct RenderStamp: Equatable {
        let key: String
        let highlightWords: [String]
        let highlightCurrent: Bool
        let widthHalfPt: Int
        let heightHalfPt: Int
        let scaleHalf: Int
        let appearance: NSAppearance.Name
    }
    private var renderedStamp: RenderStamp?
    private var tileLayers: [CALayer] = []
    private static let maxSingleBitmapHeight: CGFloat = 2048
    private static let tileHeight: CGFloat = 1024

    override var isFlipped: Bool { true }
    override var wantsUpdateLayer: Bool { true }

    init(wraps: Bool) {
        self.wraps = wraps
        self.side = wraps ? InlineCodeLayoutManager.padding : 0
        let container = NSTextContainer(size: NSSize(width: wraps ? 600 : CGFloat.greatestFiniteMagnitude,
                                                     height: CGFloat.greatestFiniteMagnitude))
        container.lineFragmentPadding = side
        container.widthTracksTextView = false
        self.container = container
        super.init(frame: .zero)
        storage.addLayoutManager(layout)
        layout.addTextContainer(container)
        layout.delegate = self
        wantsLayer = true
        layerContentsRedrawPolicy = .onSetNeedsDisplay
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("not from a nib") }

    func configure(key: String, highlight: FindHighlight?, attributed: NSAttributedString) {
        currentKey = key
        currentHighlight = highlight
        cachedAttributed = attributed
        localSizes.removeAll(keepingCapacity: true)
        laidOutWidth = nil
        renderedStamp = nil
        var links: [NSRange] = []
        attributed.enumerateAttribute(.link, in: NSRange(location: 0, length: attributed.length)) { value, range, _ in
            if value != nil { links.append(range) }
        }
        linkRanges = links
        if links.isEmpty {
            storage.setAttributedString(attributed)
        } else {
            // NSLayoutManager paints `.link` runs in AppKit's default underlined
            // system blue outside an NSTextView. Strip `.link` only from the
            // passive drawing storage so links match MarkdownTextView's
            // linkTextAttributes (Palette.workingText, no underline) identically.
            let passive = NSMutableAttributedString(attributedString: attributed)
            for range in links {
                passive.removeAttribute(.link, range: range)
            }
            storage.setAttributedString(passive)
        }
        if let active = activeTextView {
            // Keep an active selection only if the key didn't change; here
            // configure is only called when key or highlight changed.
            if active.window?.firstResponder === active, active.selectedRange().length > 0 {
                let selection = active.selectedRange()
                active.textStorage?.setAttributedString(attributed)
                let len = active.textStorage?.length ?? 0
                if NSMaxRange(selection) <= len { active.setSelectedRange(selection) }
                active.needsDisplay = true
            } else {
                deactivateTextView()
            }
        }
        needsDisplay = true
        window?.invalidateCursorRects(for: self)
    }

    func layoutManager(_ layoutManager: NSLayoutManager, lineSpacingAfterGlyphAt glyphIndex: Int,
                       withProposedLineFragmentRect rect: NSRect) -> CGFloat {
        markdownLineSpacing(in: layoutManager, storage: storage, wraps: wraps, glyphIndex: glyphIndex)
    }

    override func setFrameSize(_ newSize: NSSize) {
        let widthChanged = wraps && abs(frame.width - newSize.width) > 0.01
        let sizeChanged = abs(frame.width - newSize.width) > 0.01 || abs(frame.height - newSize.height) > 0.01
        super.setFrameSize(newSize)
        if let active = activeTextView, active.frame != bounds {
            active.frame = bounds
        }
        if widthChanged {
            laidOutWidth = nil
            renderedStamp = nil
            needsDisplay = true
            window?.invalidateCursorRects(for: self)
        } else if sizeChanged, activeTextView == nil {
            renderedStamp = nil
            needsDisplay = true
        }
    }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        renderedStamp = nil
        needsDisplay = true
    }

    override func viewDidChangeBackingProperties() {
        super.viewDidChangeBackingProperties()
        renderedStamp = nil
        needsDisplay = true
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        if window == nil {
            deactivateTextView()
        } else if renderedStamp == nil {
            needsDisplay = true
        }
    }

    private func ensureLayout(forWidth width: CGFloat) {
        let targetWidth = wraps ? width : CGFloat.greatestFiniteMagnitude
        if laidOutWidth != targetWidth {
            let newSize = NSSize(width: targetWidth, height: CGFloat.greatestFiniteMagnitude)
            if container.size != newSize {
                container.size = newSize
            }
            layout.invalidateLayout(forCharacterRange: NSRange(location: 0, length: storage.length), actualCharacterRange: nil)
            laidOutWidth = targetWidth
        }
        layout.ensureLayout(for: container)
    }

    /// The size the text takes: wrapped at width, or its longest line.
    func size(fittingWidth width: CGFloat?) -> CGSize {
        let widthKey = width.flatMap { $0.isFinite ? Int((min(max($0, 0), 100_000) * 2).rounded()) : nil } ?? -1
        if let local = localSizes[widthKey] { return local }
        if !currentKey.isEmpty,
           let cached = MarkdownSizeCache.get(key: currentKey, highlight: currentHighlight, widthHalfPt: widthKey, wraps: wraps) {
            if localSizes.count >= 4 { localSizes.removeAll(keepingCapacity: true) }
            localSizes[widthKey] = cached
            return cached
        }
        let targetWidth = width.flatMap { $0.isFinite && $0 > 0 ? $0 : nil } ?? CGFloat.greatestFiniteMagnitude
        ensureLayout(forWidth: targetWidth)
        let used = layout.usedRect(for: container)
        let result = CGSize(width: width ?? ceil(used.width), height: ceil(used.height))
        if localSizes.count >= 4 { localSizes.removeAll(keepingCapacity: true) }
        localSizes[widthKey] = result
        if !currentKey.isEmpty {
            MarkdownSizeCache.set(result, key: currentKey, highlight: currentHighlight, widthHalfPt: widthKey, wraps: wraps)
        }
        return result
    }

    private func clearRenderedContents() {
        layer?.contents = nil
        for tile in tileLayers { tile.removeFromSuperlayer() }
        tileLayers.removeAll(keepingCapacity: true)
        renderedStamp = nil
    }

    private func renderBitmap(rect: CGRect, scale: CGFloat) -> CGImage? {
        let pixelW = max(1, Int(ceil(rect.width * scale)))
        let pixelH = max(1, Int(ceil(rect.height * scale)))
        let colorSpace = CGColorSpace(name: CGColorSpace.sRGB) ?? CGColorSpaceCreateDeviceRGB()
        guard let ctx = CGContext(
            data: nil,
            width: pixelW,
            height: pixelH,
            bitsPerComponent: 8,
            bytesPerRow: 0,
            space: colorSpace,
            bitmapInfo: CGImageAlphaInfo.premultipliedFirst.rawValue | CGBitmapInfo.byteOrder32Little.rawValue
        ) else { return nil }

        // Flip to TextKit 1's top-left origin coordinate space.
        ctx.translateBy(x: 0, y: CGFloat(pixelH))
        ctx.scaleBy(x: scale, y: -scale)
        ctx.translateBy(x: -rect.minX, y: -rect.minY)
        ctx.setAllowsAntialiasing(true)
        ctx.setShouldAntialias(true)
        ctx.setAllowsFontSmoothing(true)
        ctx.setShouldSmoothFonts(true)
        ctx.setAllowsFontSubpixelPositioning(true)
        ctx.setShouldSubpixelPositionFonts(true)
        ctx.setAllowsFontSubpixelQuantization(true)
        ctx.setShouldSubpixelQuantizeFonts(true)

        effectiveAppearance.performAsCurrentDrawingAppearance {
            let nsCtx = NSGraphicsContext(cgContext: ctx, flipped: true)
            let previous = NSGraphicsContext.current
            NSGraphicsContext.current = nsCtx
            let glyphs = layout.glyphRange(forBoundingRect: rect, in: container)
            if glyphs.length > 0 {
                layout.drawBackground(forGlyphRange: glyphs, at: .zero)
                layout.drawGlyphs(forGlyphRange: glyphs, at: .zero)
            }
            NSGraphicsContext.current = previous
        }
        return ctx.makeImage()
    }

    override func updateLayer() {
        guard let layer else { return }
        guard activeTextView == nil, storage.length > 0, bounds.width > 0.5, bounds.height > 0.5 else {
            clearRenderedContents()
            return
        }
        let scale = max(1, window?.backingScaleFactor ?? NSScreen.main?.backingScaleFactor ?? 2)
        let stamp = RenderStamp(
            key: currentKey,
            highlightWords: currentHighlight?.words ?? [],
            highlightCurrent: currentHighlight?.current ?? false,
            widthHalfPt: Int((min(bounds.width, 100_000) * 2).rounded()),
            heightHalfPt: Int((min(bounds.height, 100_000) * 2).rounded()),
            scaleHalf: Int((scale * 2).rounded()),
            appearance: effectiveAppearance.bestMatch(from: [.darkAqua, .aqua]) ?? .aqua
        )
        if renderedStamp == stamp && (layer.contents != nil || !tileLayers.isEmpty) { return }

        let width = wraps ? bounds.width : CGFloat.greatestFiniteMagnitude
        ensureLayout(forWidth: width)

        CATransaction.begin()
        CATransaction.setDisableActions(true)
        for tile in tileLayers { tile.removeFromSuperlayer() }
        tileLayers.removeAll(keepingCapacity: true)

        if bounds.height <= Self.maxSingleBitmapHeight {
            layer.contentsScale = scale
            layer.contentsGravity = .topLeft
            layer.contents = renderBitmap(rect: bounds, scale: scale)
        } else {
            layer.contents = nil
            var y: CGFloat = 0
            while y < bounds.height {
                let h = min(Self.tileHeight, bounds.height - y)
                let rect = CGRect(x: 0, y: y, width: bounds.width, height: h)
                let tile = CALayer()
                tile.frame = rect
                tile.contentsScale = scale
                tile.contentsGravity = .topLeft
                tile.contents = renderBitmap(rect: rect, scale: scale)
                layer.addSublayer(tile)
                tileLayers.append(tile)
                y += h
            }
        }
        CATransaction.commit()
        renderedStamp = stamp
    }

    override func resetCursorRects() {
        guard activeTextView == nil else { return }
        addCursorRect(bounds, cursor: .iBeam)
        guard !linkRanges.isEmpty, storage.length > 0 else { return }
        let width = wraps ? (bounds.width > 0 ? bounds.width : 600) : CGFloat.greatestFiniteMagnitude
        ensureLayout(forWidth: width)
        for range in linkRanges where NSMaxRange(range) <= storage.length {
            let glyphs = layout.glyphRange(forCharacterRange: range, actualCharacterRange: nil)
            layout.enumerateEnclosingRects(forGlyphRange: glyphs, withinSelectedGlyphRange: NSRange(location: NSNotFound, length: 0), in: container) { rect, _ in
                self.addCursorRect(rect.intersection(self.bounds), cursor: .pointingHand)
            }
        }
    }

    /// Promotes this surface to a live MarkdownTextView child for interaction
    /// or test inspection, preserving layout and appearance identically.
    @discardableResult
    func activateTextView() -> MarkdownTextView {
        if let active = activeTextView { return active }
        let tv = MarkdownTextView(wraps: wraps)
        tv.delegate = coordinator
        tv.frame = bounds
        tv.autoresizingMask = [.width, .height]
        if let cachedAttributed {
            tv.textStorage?.setAttributedString(cachedAttributed)
        } else if storage.length > 0 {
            tv.textStorage?.setAttributedString(storage)
        }
        tv.onResign = { [weak self] in
            DispatchQueue.main.async { self?.deactivateIfIdle() }
        }
        clearRenderedContents()
        addSubview(tv)
        if wraps, bounds.width > 0 {
            _ = tv.size(fittingWidth: bounds.width)
        } else if !wraps {
            _ = tv.size(fittingWidth: nil)
        }
        tv.frame = bounds
        activeTextView = tv
        needsDisplay = true
        window?.invalidateCursorRects(for: self)
        return tv
    }

    private func deactivateIfIdle() {
        guard let active = activeTextView else { return }
        if active.window == nil || (active.window?.firstResponder !== active && active.selectedRange().length == 0) {
            deactivateTextView()
        }
    }

    func deactivateTextView() {
        guard let active = activeTextView else { return }
        active.onResign = nil
        active.removeFromSuperview()
        activeTextView = nil
        renderedStamp = nil
        needsDisplay = true
        window?.invalidateCursorRects(for: self)
    }

    override func mouseDown(with event: NSEvent) {
        let tv = activateTextView()
        window?.makeFirstResponder(tv)
        tv.mouseDown(with: event)
    }

    override func rightMouseDown(with event: NSEvent) {
        let tv = activateTextView()
        tv.rightMouseDown(with: event)
    }

    override func menu(for event: NSEvent) -> NSMenu? {
        activateTextView().menu(for: event)
    }

    // MARK: Accessibility

    override func isAccessibilityElement() -> Bool {
        activeTextView == nil
    }

    override func accessibilityRole() -> NSAccessibility.Role? {
        activeTextView == nil ? .staticText : nil
    }

    override func accessibilityValue() -> Any? {
        storage.string.replacingOccurrences(of: "\u{2028}", with: "\n")
    }
}

/// A read-only TextKit 1 text view, as tall as its text at the width it is
/// given, or as wide as its longest line when it doesn't wrap.
final class MarkdownTextView: NSTextView, @preconcurrency NSLayoutManagerDelegate {
    /// Posted when a mouse click/drag in this text view ends with a non-empty
    /// selection, so SelectionWatcherView still sees drags whose mouseUp was
    /// consumed inside NSTextView's modal tracking loop.
    static let selectionFinishedNotification = Notification.Name("agentmanMarkdownSelectionFinished")

    private let wraps: Bool
    /// The text's inset on each side: room for inline code's box.
    private let side: CGFloat
    var onResign: (() -> Void)?

    init(wraps: Bool) {
        self.wraps = wraps
        let storage = NSTextStorage()
        let layout = InlineCodeLayoutManager()
        storage.addLayoutManager(layout)
        // Room beside wrapped text for inline code's box (see MarkdownProse).
        side = wraps ? InlineCodeLayoutManager.padding : 0
        let container = NSTextContainer(size: NSSize(width: wraps ? 600 : CGFloat.greatestFiniteMagnitude,
                                                     height: CGFloat.greatestFiniteMagnitude))
        // Room inside the container for inline code's box beside text that
        // starts a line: a text view draws nothing outside its container.
        container.lineFragmentPadding = side
        container.widthTracksTextView = false
        layout.addTextContainer(container)
        super.init(frame: .zero, textContainer: container)
        layout.delegate = self
        isEditable = false
        isSelectable = true
        isRichText = true
        drawsBackground = false
        textContainerInset = .zero
        isVerticallyResizable = false
        isHorizontallyResizable = false
        focusRingType = .none
        linkTextAttributes = [.foregroundColor: MarkdownProseText.linkColor, .cursor: NSCursor.pointingHand]
    }

    override init(frame frameRect: NSRect, textContainer container: NSTextContainer?) {
        wraps = true
        side = 0
        super.init(frame: frameRect, textContainer: container)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("not from a nib") }

    override func mouseDown(with event: NSEvent) {
        super.mouseDown(with: event)
        if selectedRange().length > 0 {
            let point = NSApp.currentEvent?.locationInWindow ?? event.locationInWindow
            NotificationCenter.default.post(
                name: Self.selectionFinishedNotification,
                object: self,
                userInfo: ["point": NSValue(point: point)]
            )
        }
    }

    override func resignFirstResponder() -> Bool {
        let resigned = super.resignFirstResponder()
        if resigned { onResign?() }
        return resigned
    }

    /// A line break within a paragraph is a line separator here; what is
    /// copied or quoted has a plain newline.
    override func writeSelection(to pboard: NSPasteboard, type: NSPasteboard.PasteboardType) -> Bool {
        guard type == .string else { return super.writeSelection(to: pboard, type: type) }
        let selected = selectedRanges.map(\.rangeValue)
            .map { (string as NSString).substring(with: $0) }
            .joined(separator: "\n")
        return pboard.setString(selected.replacingOccurrences(of: "\u{2028}", with: "\n"), forType: .string)
    }

    override func accessibilitySelectedText() -> String? {
        super.accessibilitySelectedText()?.replacingOccurrences(of: "\u{2028}", with: "\n")
    }

    /// Line spacing between a paragraph's lines only, as SwiftUI's has it:
    /// TextKit adds a paragraph's line spacing after its last line too, and
    /// every paragraph, item and code block had 6 more below it than before.
    func layoutManager(_ layoutManager: NSLayoutManager, lineSpacingAfterGlyphAt glyphIndex: Int,
                       withProposedLineFragmentRect rect: NSRect) -> CGFloat {
        markdownLineSpacing(in: layoutManager, storage: textStorage, wraps: wraps, glyphIndex: glyphIndex)
    }

    /// While text is measured at a width, the frame keeps out of it: a text
    /// view touches its own frame as its container changes, and following the
    /// frame then laid the text out at the old width, or none.
    private var measuring = false

    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(newSize)
        if wraps, !measuring, newSize.width > 0, let container = textContainer, container.size.width != newSize.width {
            container.size = NSSize(width: newSize.width, height: CGFloat.greatestFiniteMagnitude)
            layoutManager?.invalidateLayout(forCharacterRange: NSRange(location: 0, length: textStorage?.length ?? 0), actualCharacterRange: nil)
        }
    }

    /// The size the text takes: wrapped at width, or its longest line.
    func size(fittingWidth width: CGFloat?) -> CGSize {
        guard let layout = layoutManager, let container = textContainer else { return .zero }
        measuring = true
        defer {
            measuring = false
            // Drawn at the width it has, which SwiftUI sets from this answer.
            if wraps, frame.width > 0, container.size.width != frame.width {
                container.size = NSSize(width: frame.width, height: CGFloat.greatestFiniteMagnitude)
                layout.invalidateLayout(forCharacterRange: NSRange(location: 0, length: textStorage?.length ?? 0), actualCharacterRange: nil)
            }
        }
        let size = NSSize(width: width ?? CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        if container.size != size {
            container.size = size
            // Laid out again at the new width: a container's new size alone
            // left the old line breaks in place.
            layout.invalidateLayout(forCharacterRange: NSRange(location: 0, length: textStorage?.length ?? 0), actualCharacterRange: nil)
        }
        layout.ensureLayout(for: container)
        let used = layout.usedRect(for: container)
        return CGSize(width: width ?? ceil(used.width), height: ceil(used.height))
    }
}

/// A reply's prose as one attributed string, in the sizes and spacing the
/// SwiftUI blocks had: 14 between blocks, 10 between a list's items and a
/// quote's blocks, 8 within an item, and a heading's room above it.
@MainActor
enum MarkdownProseText {
    /// A size above the interface's own text, so what the agent says reads
    /// first, before its steps.
    static let body: NSFont = .systemFont(ofSize: 15 * Fonts.scale)
    static let mono: NSFont = NSFont(name: "Google Sans Code", size: 13 * Fonts.scale)
        ?? .monospacedSystemFont(ofSize: 13 * Fonts.scale, weight: .regular)
    static let blockGap: CGFloat = 14

    // Pre-cached font variants avoid NSFontManager trait conversions on every run.
    private static let bodyBold: NSFont = NSFontManager.shared.convert(body, toHaveTrait: .boldFontMask)
    private static let bodyItalic: NSFont = NSFontManager.shared.convert(body, toHaveTrait: .italicFontMask)
    private static let bodyBoldItalic: NSFont = NSFontManager.shared.convert(bodyBold, toHaveTrait: .italicFontMask)

    private static let monoBold: NSFont = NSFontManager.shared.convert(mono, toHaveTrait: .boldFontMask)
    private static let monoItalic: NSFont = NSFontManager.shared.convert(mono, toHaveTrait: .italicFontMask)
    private static let monoBoldItalic: NSFont = NSFontManager.shared.convert(monoBold, toHaveTrait: .italicFontMask)

    private static let h1: NSFont = .systemFont(ofSize: 20 * Fonts.scale, weight: .medium)
    private static let h2: NSFont = .systemFont(ofSize: 17.5 * Fonts.scale, weight: .medium)
    private static let h3: NSFont = .systemFont(ofSize: 15.5 * Fonts.scale, weight: .medium)
    private static let tinyRuleFont: NSFont = .systemFont(ofSize: 1)

    // Pre-cached dynamic appearance-aware NSColors avoid SwiftUI.Color -> NSColor bridging per run.
    static let textColor = NSColor(Palette.text)
    static let quoteColor: NSColor = dynamicAlpha(textColor, alpha: 0.85)
    static let mutedColor = NSColor(Palette.muted)
    static let lineColor = NSColor(Palette.line)
    static let fillColor = NSColor(Palette.fill)
    static let linkColor = NSColor(Palette.workingText)
    static let findMatchColor = NSColor(Palette.findMatch)
    static let findCurrentColor = NSColor(Palette.findCurrent)

    private static func dynamicAlpha(_ base: NSColor, alpha: CGFloat) -> NSColor {
        if alpha >= 0.999 { return base }
        return NSColor(name: nil) { appearance in
            var resolved = base
            appearance.performAsCurrentDrawingAppearance {
                resolved = base.withAlphaComponent(alpha)
            }
            return resolved
        }
    }

    private static func fontVariant(base: NSFont, bold: Bool, italic: Bool) -> NSFont {
        if !bold && !italic { return base }
        if base === body {
            if bold && italic { return bodyBoldItalic }
            return bold ? bodyBold : bodyItalic
        }
        if base === mono {
            if bold && italic { return monoBoldItalic }
            return bold ? monoBold : monoItalic
        }
        var out = base
        if bold { out = NSFontManager.shared.convert(out, toHaveTrait: .boldFontMask) }
        if italic { out = NSFontManager.shared.convert(out, toHaveTrait: .italicFontMask) }
        return out
    }

    /// Whether a block is prose all the way down, and can join the text.
    nonisolated static func isProse(_ block: MarkdownBlock) -> Bool {
        switch block.kind {
        case .paragraph, .heading, .rule: true
        case .quote(let blocks): blocks.allSatisfy(isProse)
        case .list(_, _, let items): items.allSatisfy { $0.allSatisfy(isProse) }
        case .code, .table, .images: false
        }
    }

    final class Paragraph {
        let text: NSMutableAttributedString
        let style = NSMutableParagraphStyle()
        /// Room this one keeps below it beyond the gap after it: a rule's.
        var extraAfter: CGFloat = 0

        init(_ text: NSMutableAttributedString) { self.text = text }
    }

    struct Context {
        var indent: CGFloat = 0
        var blocks: [NSTextBlock] = []
        var alpha: CGFloat = 1
    }

    private static let newlineAttr = NSAttributedString(string: "\n", attributes: [.font: body])
    private static let bulletWidth: CGFloat = ("•" as NSString).size(withAttributes: [.font: body]).width

    static func attributed(_ blocks: [MarkdownBlock], highlight: FindHighlight?) -> NSAttributedString {
        var paragraphs: [Paragraph] = []
        paragraphs.reserveCapacity(blocks.count)
        emit(blocks, gap: blockGap, context: Context(), into: &paragraphs)
        let out = NSMutableAttributedString()
        out.beginEditing()
        for (index, paragraph) in paragraphs.enumerated() {
            let last = index == paragraphs.count - 1
            if last { paragraph.style.paragraphSpacing = 0 }
            if !last {
                paragraph.text.append(newlineAttr)
            }
            paragraph.text.addAttribute(.paragraphStyle, value: paragraph.style,
                                        range: NSRange(location: 0, length: paragraph.text.length))
            out.append(paragraph.text)
        }
        if let highlight, !highlight.words.isEmpty {
            let plain = out.string
            let color = highlight.current ? findCurrentColor : findMatchColor
            for range in SearchWords.ranges(of: highlight.words, in: plain) {
                out.addAttribute(.backgroundColor, value: color, range: NSRange(range, in: plain))
            }
        }
        out.endEditing()
        return out
    }

    /// Each block's paragraphs, gap apart; the last one's room below is the
    /// caller's to set.
    private static func emit(_ blocks: [MarkdownBlock], gap: CGFloat, context: Context, into out: inout [Paragraph]) {
        for (index, block) in blocks.enumerated() {
            emit(block, context: context, into: &out)
            if index < blocks.count - 1, let last = out.last {
                last.style.paragraphSpacing = gap + last.extraAfter
            }
        }
    }

    private static func emit(_ block: MarkdownBlock, context: Context, into out: inout [Paragraph]) {
        let color: NSColor
        if context.alpha >= 0.999 {
            color = textColor
        } else if abs(context.alpha - 0.85) < 0.001 {
            color = quoteColor
        } else {
            color = dynamicAlpha(textColor, alpha: context.alpha)
        }
        switch block.kind {
        case .paragraph(let text):
            let paragraph = Paragraph(inline(text, font: body, color: color))
            paragraph.style.lineSpacing = 6
            place(paragraph, block: block, context: context)
            out.append(paragraph)
        case .heading(let level, let text):
            let headingFont = level <= 1 ? h1 : level == 2 ? h2 : h3
            let paragraph = Paragraph(inline(text, font: headingFont, color: color))
            paragraph.style.lineSpacing = 3
            paragraph.style.paragraphSpacingBefore = level <= 2 ? 10 : 4
            place(paragraph, block: block, context: context)
            out.append(paragraph)
        case .rule:
            // A hairline across the text, 8 clear above and below.
            let line = NSTextBlock()
            line.setWidth(1, type: .absoluteValueType, for: .border, edge: .minY)
            line.setBorderColor(lineColor, for: .minY)
            // A block's border is drawn only once the block has a width.
            line.setValue(100, type: .percentageValueType, for: .width)
            if context.indent > 0 { line.setWidth(context.indent, type: .absoluteValueType, for: .margin, edge: .minX) }
            // The room above goes in the block's margin: spacing before a
            // paragraph falls inside its block, under the line.
            line.setWidth(8, type: .absoluteValueType, for: .margin, edge: .minY)
            let paragraph = Paragraph(NSMutableAttributedString(string: "\u{00A0}", attributes: [.font: tinyRuleFont]))
            paragraph.style.textBlocks = context.blocks + [line]
            paragraph.extraAfter = 8
            out.append(paragraph)
        case .quote(let blocks):
            // The quote's bar, its text 10 to the right and a little faded.
            let bar = NSTextBlock()
            bar.setWidth(3, type: .absoluteValueType, for: .border, edge: .minX)
            bar.setBorderColor(lineColor, for: .minX)
            bar.setWidth(10, type: .absoluteValueType, for: .padding, edge: .minX)
            bar.setValue(100, type: .percentageValueType, for: .width)
            if context.indent > 0 { bar.setWidth(context.indent, type: .absoluteValueType, for: .margin, edge: .minX) }
            let inner = Context(indent: 0, blocks: context.blocks + [bar], alpha: context.alpha * 0.85)
            emit(blocks, gap: 10, context: inner, into: &out)
        case .list(let ordered, let start, let items):
            let markers: [String]
            let widest: CGFloat
            if ordered {
                markers = items.indices.map { "\(start + $0)." }
                widest = markers.map { ($0 as NSString).size(withAttributes: [.font: body]).width }.max() ?? 0
            } else {
                markers = Array(repeating: "•", count: items.count)
                widest = bulletWidth
            }
            let column = max(ordered ? 18 : 10, ceil(widest))
            let content = context.indent + column + 10
            let markerAttrs: [NSAttributedString.Key: Any] = [.font: body, .foregroundColor: mutedColor]
            let tabs = [NSTextTab(textAlignment: .right, location: context.indent + column),
                        NSTextTab(textAlignment: .left, location: content)]
            for (index, item) in items.enumerated() {
                var inner = context
                inner.indent = content
                let first = out.count
                emit(item, gap: 8, context: inner, into: &out)
                // The marker goes before the item's first line, right-aligned
                // to its column, as the SwiftUI list had it.
                let marker = NSMutableAttributedString(string: "\t" + markers[index] + "\t", attributes: markerAttrs)
                if first < out.count, startsWithText(item) {
                    out[first].text.insert(marker, at: 0)
                    out[first].style.firstLineHeadIndent = context.indent
                } else {
                    let lone = Paragraph(marker)
                    lone.style.lineSpacing = 6
                    lone.style.firstLineHeadIndent = context.indent
                    lone.style.headIndent = content
                    lone.style.textBlocks = context.blocks
                    out.insert(lone, at: first)
                    lone.style.paragraphSpacing = 8
                }
                out[first].style.tabStops = tabs
                if index < items.count - 1, let last = out.last {
                    last.style.paragraphSpacing = 10 + last.extraAfter
                }
            }
        case .code, .table, .images:
            // Not prose: MarkdownView draws these on their own.
            break
        }
    }

    /// Whether an item opens with a line of text the marker can go before.
    private static func startsWithText(_ item: [MarkdownBlock]) -> Bool {
        switch item.first?.kind {
        case .paragraph?, .heading?: true
        default: false
        }
    }

    private static func place(_ paragraph: Paragraph, block: MarkdownBlock, context: Context) {
        paragraph.style.firstLineHeadIndent = context.indent
        paragraph.style.headIndent = context.indent
        paragraph.style.textBlocks = context.blocks
        paragraph.style.alignment = block.centered ? .center : .natural
    }

    /// A paragraph's runs, in Mac fonts: bold and italic as they were marked,
    /// code in the code font on its grey, links and struck-through text.
    static func inline(_ text: AttributedString, font: NSFont, color: NSColor) -> NSMutableAttributedString {
        let out = NSMutableAttributedString()
        var hasInlineCode = false
        for run in text.runs {
            let intent = run.inlinePresentationIntent ?? []
            let isCode = intent.contains(.code)
            let baseFont = isCode ? mono : font
            let runFont = fontVariant(base: baseFont,
                                      bold: intent.contains(.stronglyEmphasized),
                                      italic: intent.contains(.emphasized))
            var attributes: [NSAttributedString.Key: Any] = [
                .foregroundColor: run.link != nil ? linkColor : color,
                .font: runFont,
            ]
            if isCode {
                hasInlineCode = true
                attributes[.backgroundColor] = fillColor
                attributes[.inlineCode] = true
            }
            if intent.contains(.strikethrough) {
                attributes[.strikethroughStyle] = NSUnderlineStyle.single.rawValue
            }
            if let link = run.link {
                attributes[.link] = link
            }
            // A line break within a paragraph stays within it: a newline
            // would start another, without its line spacing.
            let raw = String(text[run.range].characters)
            let string = raw.contains("\n") ? raw.replacingOccurrences(of: "\n", with: "\u{2028}") : raw
            out.append(NSAttributedString(string: string, attributes: attributes))
        }
        if hasInlineCode { roomForCode(in: out) }
        return out
    }

    /// Room either side of inline code for its box's padding: space after
    /// the letter before it, unless the code starts the line, and after its
    /// own last letter.
    static func roomForCode(in text: NSMutableAttributedString) {
        let whole = NSRange(location: 0, length: text.length)
        let pad = InlineCodeLayoutManager.padding
        let chars = text.mutableString
        text.enumerateAttribute(.inlineCode, in: whole) { value, range, _ in
            guard value != nil, range.length > 0 else { return }
            text.addAttribute(.kern, value: pad, range: NSRange(location: NSMaxRange(range) - 1, length: 1))
            if range.location > 0 {
                let ch = chars.character(at: range.location - 1)
                if ch != 0x0A && ch != 0x2028 {
                    text.addAttribute(.kern, value: pad, range: NSRange(location: range.location - 1, length: 1))
                }
            }
        }
    }

    private static let codeParagraphStyle: NSParagraphStyle = {
        let style = NSMutableParagraphStyle()
        style.lineSpacing = 4
        return style
    }()

    /// A code block's text: the code font, 4 between lines.
    static func code(_ text: NSAttributedString) -> NSAttributedString {
        let out = NSMutableAttributedString(attributedString: text)
        let whole = NSRange(location: 0, length: out.length)
        out.beginEditing()
        out.addAttribute(.font, value: mono, range: whole)
        out.addAttribute(.paragraphStyle, value: codeParagraphStyle, range: whole)
        out.enumerateAttribute(.foregroundColor, in: whole) { value, range, _ in
            if value == nil { out.addAttribute(.foregroundColor, value: textColor, range: range) }
        }
        out.endEditing()
        return out
    }
}

extension NSAttributedString.Key {
    /// Inline code in a reply, which draws its grey as a box of its own.
    static let inlineCode = NSAttributedString.Key("agentmanInlineCode")
}

/// Inline code's grey as a rounded box round the code's own letters, with
/// room either side of them, rather than the plain rectangle a text view
/// fills: the line's whole height, edge to edge with the words beside it,
/// and on wrapped code one block across the lines. Every other background,
/// a Find mark among them, is drawn as before.
final class InlineCodeLayoutManager: NSLayoutManager {
    /// The room either side of the code, kept for it by kerning.
    static let padding: CGFloat = 3
    /// Where the glyphs being drawn start in the view, for finding a line's
    /// baseline from a rectangle there.
    private var drawingAt = CGPoint.zero

    override func drawBackground(forGlyphRange glyphsToShow: NSRange, at origin: NSPoint) {
        drawingAt = origin
        super.drawBackground(forGlyphRange: glyphsToShow, at: origin)
    }

    override func fillBackgroundRectArray(_ rectArray: UnsafePointer<NSRect>, count rectCount: Int, forCharacterRange charRange: NSRange, color: NSColor) {
        guard let storage = textStorage, charRange.location < storage.length,
              storage.attribute(.inlineCode, at: charRange.location, effectiveRange: nil) != nil,
              let container = textContainers.first,
              let font = storage.attribute(.font, at: charRange.location, effectiveRange: nil) as? NSFont else {
            super.fillBackgroundRectArray(rectArray, count: rectCount, forCharacterRange: charRange, color: color)
            return
        }
        color.setFill()
        for index in 0..<rectCount {
            let glyph: Int
            if rectCount == 1 {
                glyph = glyphIndexForCharacter(at: charRange.location)
            } else {
                let point = CGPoint(x: rectArray[index].midX - drawingAt.x, y: rectArray[index].midY - drawingAt.y)
                glyph = glyphIndex(for: point, in: container)
            }
            let line = lineFragmentRect(forGlyphAt: glyph, effectiveRange: nil)
            let baseline = drawingAt.y + line.minY + location(forGlyphAt: glyph).y
            Self.box(rectArray[index], font: font, baseline: baseline).fill()
        }
    }

    /// The box for one line's run of code: the code font's height and a
    /// little more, round its baseline; out to the left into the room kept
    /// before it (the room after is in the run already).
    static func box(_ rect: NSRect, font: NSFont, baseline: CGFloat) -> NSBezierPath {
        let top = baseline - font.ascender - 2
        let bottom = baseline - font.descender + 2
        let frame = CGRect(x: rect.minX - padding, y: top, width: rect.width + padding, height: bottom - top)
        return NSBezierPath(roundedRect: frame, xRadius: 4, yRadius: 4)
    }
}
