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
    /// The editor's files, for `@`, in an editor's agent panel.
    var mentions: FileMentions?
    /// The agent's commands, for `/` at the start, where it takes them.
    var slash: SlashCommands?

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
        textView.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        textView.focusChanged = { [weak coordinator = context.coordinator] now in coordinator?.focusChanged(now) }
        context.coordinator.textView = textView

        let scroll = MessageScrollView()
        scroll.drawsBackground = false
        scroll.borderType = .noBorder
        scroll.hasVerticalScroller = false
        scroll.hasHorizontalScroller = false
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
            textView.styleMentions()
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
        textView.mentions = mentions
        mentions?.textView = textView
        textView.slash = slash
        slash?.textView = textView
    }

    /// As tall as its lines, from one to `maxLines`. Measured on the text
    /// alone, never the live text view: SwiftUI asks at several widths, and
    /// resizing the view's text container to each one left it at the last,
    /// a narrow guess, so lines wrapped there and clicks to its right went
    /// nowhere; and changing it in the middle of a layout pass started a
    /// loop of layouts that AppKit ends by crashing.
    func sizeThatFits(_ proposal: ProposedViewSize, nsView: NSScrollView, context: Context) -> CGSize? {
        let width = max(proposal.width ?? 200, 40)
        let font = Self.font(fontSize)
        let line = ceil(font.ascender - font.descender + font.leading + Self.lineSpacing)
        var string = context.coordinator.textView?.string ?? text
        // A last empty line counts, as the text view shows it.
        if string.isEmpty || string.hasSuffix("\n") { string += " " }
        let paragraph = NSMutableParagraphStyle()
        paragraph.lineSpacing = Self.lineSpacing
        let used = (string as NSString).boundingRect(
            with: NSSize(width: width, height: .greatestFiniteMagnitude),
            options: [.usesLineFragmentOrigin, .usesFontLeading],
            attributes: [.font: font, .paragraphStyle: paragraph]
        ).height
        return CGSize(width: width, height: min(max(ceil(used), line), line * CGFloat(maxLines)))
    }

    static func font(_ size: CGFloat) -> NSFont {
        .systemFont(ofSize: size * Fonts.scale)
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
            textView.styleMentions()
            followMention()
            parent.slash?.update(text: textView.string)
        }

        func textViewDidChangeSelection(_ notification: Notification) {
            followMention()
        }

        /// Opens or closes the files for `@` as the caret moves.
        private func followMention() {
            guard let textView, let mentions = parent.mentions else { return }
            let selection = textView.selectedRange()
            if selection.length == 0, !textView.hasMarkedText() {
                mentions.update(text: textView.string, caret: selection.location)
            } else {
                mentions.dismiss()
            }
        }

        /// Backspace or Delete at a file chip takes the whole chip.
        func textView(_ textView: NSTextView, shouldChangeTextIn range: NSRange, replacementString: String?) -> Bool {
            guard let mentions = parent.mentions, replacementString?.isEmpty == true, range.length <= 1 else { return true }
            for chip in mentions.chips(in: textView.string)
            where NSIntersectionRange(chip.range, range).length > 0 && chip.range != range {
                textView.insertText("", replacementRange: chip.range)
                return false
            }
            return true
        }

        func focusChanged(_ now: Bool) {
            lastFocused = now
            if !now {
                parent.mentions?.dismiss()
                parent.slash?.dismiss()
            }
            // Not while SwiftUI is updating the view that reported it.
            DispatchQueue.main.async { [parent] in
                if parent.focused != now { parent.focused = now }
            }
        }
    }
}

/// The scroll view around a message box's text. It keeps the text view
/// exactly as wide as the box, and its text with it, and at least as tall,
/// so the whole box takes clicks and lines wrap at its edge. A click here
/// that still misses the text goes to it.
final class MessageScrollView: NSScrollView {
    override func tile() {
        super.tile()
        guard let textView = documentView as? NSTextView else { return }
        let size = contentSize
        textView.minSize = NSSize(width: 0, height: size.height)
        if textView.frame.width != size.width || textView.frame.height < size.height {
            textView.setFrameSize(NSSize(width: size.width, height: max(textView.frame.height, size.height)))
        }
    }

    override func mouseDown(with event: NSEvent) {
        guard let textView = documentView as? MessageTextView, textView.isEditable else {
            super.mouseDown(with: event)
            return
        }
        textView.takeFocus()
        textView.setSelectedRange(NSRange(location: (textView.string as NSString).length, length: 0))
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
    var mentions: FileMentions?
    var slash: SlashCommands?
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
        // The commands for `/`, as the files for `@`.
        if let slash, slash.isOpen, !slash.results.isEmpty, !hasMarkedText() {
            switch event.keyCode {
            case 125: return slash.move(1)
            case 126: return slash.move(-1)
            case 36, 76, 48:
                // Return on a name already typed in full sends it; Tab, or
                // Return on part of one, completes it.
                if event.keyCode != 48, let command = slash.results.first, slash.query == command.name,
                   command.argumentHint?.isEmpty ?? true {
                    slash.dismiss()
                    break
                }
                return slash.choose()
            case 53: return slash.dismiss()
            default: break
            }
        }
        // The files for `@`: arrows move, Return or Tab picks, Escape closes.
        if let mentions, mentions.isOpen, !hasMarkedText() {
            switch event.keyCode {
            case 125: return mentions.move(1)
            case 126: return mentions.move(-1)
            case 36, 76, 48: return mentions.choose()
            case 53: return mentions.dismiss()
            default: break
            }
        }
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

    // MARK: File chips

    /// The `@path` of each file named in the text, in the brand colour;
    /// drawBackground puts the chip behind it. Layout attributes only: the
    /// text stays plain.
    func styleMentions() {
        guard let layoutManager else { return }
        let all = NSRange(location: 0, length: (string as NSString).length)
        layoutManager.removeTemporaryAttribute(.foregroundColor, forCharacterRange: all)
        guard let mentions else { return }
        for chip in mentions.chips(in: string) {
            layoutManager.addTemporaryAttribute(.foregroundColor, value: NSColor(Palette.workingText), forCharacterRange: chip.range)
        }
        needsDisplay = true
    }

    override func drawBackground(in rect: NSRect) {
        super.drawBackground(in: rect)
        guard let mentions, let layoutManager, let textContainer, string.contains("@") else { return }
        NSColor(Palette.working).withAlphaComponent(0.13).setFill()
        let origin = textContainerOrigin
        for chip in mentions.chips(in: string) {
            let glyphs = layoutManager.glyphRange(forCharacterRange: chip.range, actualCharacterRange: nil)
            layoutManager.enumerateEnclosingRects(forGlyphRange: glyphs, withinSelectedGlyphRange: NSRange(location: NSNotFound, length: 0),
                                                  in: textContainer) { box, _ in
                let chipBox = box.offsetBy(dx: origin.x, dy: origin.y).insetBy(dx: -3, dy: 0.5)
                NSBezierPath(roundedRect: chipBox, xRadius: 5, yRadius: 5).fill()
            }
        }
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
