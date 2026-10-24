import AppKit
import SwiftUI

/// The words of a message box: Return sends, Shift-Return starts a new line,
/// and it grows to `maxLines` before it scrolls. Images pasted or dropped on
/// it become attachments rather than text.
///
/// AppKit's own text view rather than a SwiftUI text field, because the text
/// field keeps paste and drop to itself: a dropped screenshot became its
/// path, and Edit ▸ Paste never reached the app at all.
struct MessageField: NSViewRepresentable {
    @Binding var text: String
    var placeholder: String
    var fontSize: CGFloat = 14
    var enabled = true
    var maxLines = 10
    @Binding var focused: Bool
    var onSubmit: () -> Void
    /// Where pasted or dropped images go; nil leaves them to the text view,
    /// which takes only their text.
    var onImages: (([Attachment]) -> Void)?
    /// Whether images are being dragged over the box, to show where they
    /// will land.
    var onDropTargeted: ((Bool) -> Void)?

    static let lineSpacing: CGFloat = 3

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> NSScrollView {
        let textView = MessageTextView()
        textView.delegate = context.coordinator
        textView.isRichText = false
        textView.importsGraphics = false
        textView.allowsUndo = true
        textView.drawsBackground = false
        textView.isAutomaticQuoteSubstitutionEnabled = false
        textView.isAutomaticDashSubstitutionEnabled = false
        textView.isAutomaticTextReplacementEnabled = false
        textView.textContainerInset = .zero
        textView.textContainer?.lineFragmentPadding = 0
        textView.textContainer?.widthTracksTextView = true
        textView.isVerticallyResizable = true
        textView.isHorizontallyResizable = false
        textView.autoresizingMask = [.width]
        textView.focusChanged = { [weak coordinator = context.coordinator] now in coordinator?.focusChanged(now) }
        context.coordinator.textView = textView

        let scroll = NSScrollView()
        scroll.drawsBackground = false
        scroll.borderType = .noBorder
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.scrollerStyle = .overlay
        scroll.documentView = textView
        update(textView)
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        context.coordinator.parent = self
        guard let textView = context.coordinator.textView else { return }
        update(textView)
        if textView.string != text {
            textView.string = text
            textView.needsDisplay = true
        }
        // Focus follows the binding when it is set from outside, never
        // fighting a click elsewhere.
        if focused != context.coordinator.lastFocused {
            context.coordinator.lastFocused = focused
            if focused {
                textView.takeFocus()
            }
        }
    }

    private func update(_ textView: MessageTextView) {
        let font = Self.font(fontSize)
        let paragraph = NSMutableParagraphStyle()
        paragraph.lineSpacing = Self.lineSpacing
        textView.font = font
        textView.textColor = NSColor(Palette.text)
        textView.insertionPointColor = NSColor(Palette.text)
        textView.defaultParagraphStyle = paragraph
        textView.typingAttributes = [.font: font, .foregroundColor: NSColor(Palette.text), .paragraphStyle: paragraph]
        textView.isEditable = enabled
        textView.placeholder = placeholder
        textView.setAccessibilityPlaceholderValue(placeholder)
        textView.onSubmit = onSubmit
        textView.onImages = onImages
        textView.onDropTargeted = onDropTargeted
    }

    /// As tall as its lines, from one to `maxLines`.
    func sizeThatFits(_ proposal: ProposedViewSize, nsView: NSScrollView, context: Context) -> CGSize? {
        guard let textView = context.coordinator.textView,
              let layout = textView.layoutManager, let container = textView.textContainer else { return nil }
        let width = max(proposal.width ?? 200, 40)
        let line = ceil(layout.defaultLineHeight(for: Self.font(fontSize)) + Self.lineSpacing)
        container.containerSize = NSSize(width: width, height: .greatestFiniteMagnitude)
        layout.ensureLayout(for: container)
        let used = ceil(layout.usedRect(for: container).height)
        return CGSize(width: width, height: min(max(used, line), line * CGFloat(maxLines)))
    }

    static func font(_ size: CGFloat) -> NSFont {
        let descriptor = NSFontDescriptor(fontAttributes: [.family: "Google Sans"])
        return NSFont(descriptor: descriptor, size: size) ?? .systemFont(ofSize: size)
    }

    @MainActor
    final class Coordinator: NSObject, NSTextViewDelegate {
        var parent: MessageField
        weak var textView: MessageTextView?
        var lastFocused = false

        init(_ parent: MessageField) { self.parent = parent }

        func textDidChange(_ notification: Notification) {
            guard let textView else { return }
            parent.text = textView.string
        }

        func focusChanged(_ now: Bool) {
            lastFocused = now
            // Not while SwiftUI is updating the view that reported it.
            DispatchQueue.main.async { [parent] in
                if parent.focused != now { parent.focused = now }
            }
        }
    }
}

/// The text view inside a message box. Paste and drop are its own, so
/// images reach the box however they arrive: ⌘V, Edit ▸ Paste, the context
/// menu, or a drag from Finder, a screenshot or a browser.
final class MessageTextView: NSTextView {
    var placeholder = ""
    var onSubmit: () -> Void = {}
    var onImages: (([Attachment]) -> Void)?
    var onDropTargeted: ((Bool) -> Void)?
    var focusChanged: (Bool) -> Void = { _ in }
    /// Focus asked for before the box was in a window, given when it is.
    private var wantsFocus = false

    func takeFocus() {
        guard let window else {
            wantsFocus = true
            return
        }
        if window.firstResponder !== self { window.makeFirstResponder(self) }
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        if wantsFocus, window != nil {
            wantsFocus = false
            takeFocus()
        }
    }

    // MARK: Keys

    override func keyDown(with event: NSEvent) {
        let isReturn = event.keyCode == 36 || event.keyCode == 76
        guard isReturn, !hasMarkedText() else { return super.keyDown(with: event) }
        let flags = event.modifierFlags.intersection(.deviceIndependentFlagsMask)
        if flags.contains(.shift) || flags.contains(.option) {
            insertNewlineIgnoringFieldEditor(nil)
        } else if flags.isDisjoint(with: [.command, .control]) {
            onSubmit()
        } else {
            super.keyDown(with: event)
        }
    }

    // MARK: Paste

    override func paste(_ sender: Any?) {
        if takeImages(from: .general) { return }
        super.paste(sender)
    }

    override func pasteAsPlainText(_ sender: Any?) {
        if takeImages(from: .general) { return }
        super.pasteAsPlainText(sender)
    }

    override func pasteAsRichText(_ sender: Any?) {
        if takeImages(from: .general) { return }
        super.pasteAsPlainText(sender)
    }

    /// Paste stays available with only an image on the pasteboard: a plain
    /// text view would grey it out, and ⌘V with a screenshot would do
    /// nothing at all.
    override func validateUserInterfaceItem(_ item: any NSValidatedUserInterfaceItem) -> Bool {
        let pastes: [Selector] = [#selector(paste(_:)), #selector(pasteAsPlainText(_:)), #selector(pasteAsRichText(_:))]
        if let action = item.action, pastes.contains(action), onImages != nil, isEditable,
           Attachments.holdsImages(.general) {
            return true
        }
        return super.validateUserInterfaceItem(item)
    }

    /// Hands the pasteboard's images to the box, if it takes images and
    /// there are any. Copied files and screenshots alike.
    func takeImages(from pasteboard: NSPasteboard) -> Bool {
        guard let onImages, isEditable else { return false }
        let found = Attachments.from(pasteboard: pasteboard)
        guard !found.isEmpty else { return false }
        onImages(found)
        return true
    }

    // MARK: Drop

    private var draggingImages = false

    private func holdsImages(_ info: NSDraggingInfo) -> Bool {
        guard onImages != nil, isEditable else { return false }
        return Attachments.holdsImages(info.draggingPasteboard)
    }

    override func draggingEntered(_ sender: NSDraggingInfo) -> NSDragOperation {
        draggingImages = holdsImages(sender)
        guard draggingImages else { return super.draggingEntered(sender) }
        onDropTargeted?(true)
        return .copy
    }

    override func draggingUpdated(_ sender: NSDraggingInfo) -> NSDragOperation {
        draggingImages ? .copy : super.draggingUpdated(sender)
    }

    override func draggingExited(_ sender: NSDraggingInfo?) {
        if draggingImages {
            draggingImages = false
            onDropTargeted?(false)
            return
        }
        super.draggingExited(sender)
    }

    override func performDragOperation(_ sender: NSDraggingInfo) -> Bool {
        guard draggingImages else { return super.performDragOperation(sender) }
        draggingImages = false
        onDropTargeted?(false)
        return takeImages(from: sender.draggingPasteboard)
    }

    override func concludeDragOperation(_ sender: NSDraggingInfo?) {
        if draggingImages { return }
        super.concludeDragOperation(sender)
    }

    // MARK: Focus and placeholder

    override func becomeFirstResponder() -> Bool {
        let became = super.becomeFirstResponder()
        if became { focusChanged(true) }
        return became
    }

    override func resignFirstResponder() -> Bool {
        let resigned = super.resignFirstResponder()
        if resigned { focusChanged(false) }
        return resigned
    }

    override func didChangeText() {
        super.didChangeText()
        needsDisplay = true
    }

    override func draw(_ dirtyRect: NSRect) {
        super.draw(dirtyRect)
        guard string.isEmpty, !placeholder.isEmpty, let font else { return }
        let attributes: [NSAttributedString.Key: Any] = [
            .font: font,
            .foregroundColor: NSColor(Palette.muted),
        ]
        let origin = textContainerOrigin
        let width = bounds.width - origin.x * 2
        NSAttributedString(string: placeholder, attributes: attributes).draw(
            with: NSRect(x: origin.x, y: origin.y, width: max(width, 0), height: bounds.height),
            options: [.usesLineFragmentOrigin, .truncatesLastVisibleLine],
            context: nil
        )
    }
}
