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
        // TextKit 1, whose attachment cells draw the file chips.
        let textView = MessageTextView(usingTextLayoutManager: false)
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
        if textView.plainText != text {
            textView.string = text
            textView.makeChips(all: true, notify: false)
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
        let paragraph = NSMutableParagraphStyle()
        paragraph.lineSpacing = Self.lineSpacing
        // The text with its file chips, each as wide as it draws.
        let measured = NSMutableAttributedString(attributedString: context.coordinator.textView?.textStorage ?? NSAttributedString(string: text))
        // A last empty line counts, as the text view shows it.
        if measured.length == 0 || measured.string.hasSuffix("\n") { measured.append(NSAttributedString(string: " ")) }
        measured.addAttributes([.font: font, .paragraphStyle: paragraph], range: NSRange(location: 0, length: measured.length))
        let used = measured.boundingRect(
            with: NSSize(width: width, height: .greatestFiniteMagnitude),
            options: [.usesLineFragmentOrigin, .usesFontLeading]
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
            parent.text = textView.plainText
            // Once AppKit is done with the change: in the middle of it, the
            // step typing leaves to undo would still count the letters a
            // chip replaced.
            DispatchQueue.main.async { [weak textView] in textView?.makeChips() }
            followMention()
            followSlash()
        }

        func textViewDidChangeSelection(_ notification: Notification) {
            followMention()
            followSlash()
        }

        /// Opens or closes the commands for `/` as the caret moves.
        private func followSlash() {
            guard let textView, let slash = parent.slash else { return }
            let selection = textView.selectedRange()
            if selection.length == 0, !textView.hasMarkedText() {
                slash.update(text: textView.string, caret: selection.location)
            } else {
                slash.dismiss()
            }
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
    /// A file chip shows its file while the pointer rests on it.
    lazy var hover = ChipHover(self) { [weak self] in self?.mentions?.root.path }

    func takeFocus() {
        guard let window else {
            wantsFocus = true
            return
        }
        if window.firstResponder !== self { window.makeFirstResponder(self) }
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        if window != nil { hover.track() } else { hover.close() }
        if wantsFocus, window != nil {
            wantsFocus = false
            takeFocus()
        }
    }

    // MARK: Keys

    override func mouseMoved(with event: NSEvent) {
        super.mouseMoved(with: event)
        hover.moved(event)
    }

    override func mouseExited(with event: NSEvent) {
        super.mouseExited(with: event)
        hover.close()
    }

    override func keyDown(with event: NSEvent) {
        hover.close()
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
        makeChips(all: true)
    }

    override func pasteAsPlainText(_ sender: Any?) {
        if takeImages(from: .general) { return }
        super.pasteAsPlainText(sender)
        makeChips(all: true)
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

    /// The text as it is sent: each chip as `@` and its path.
    var plainText: String { textStorage.map(FileChip.plain) ?? string }

    private var chipping = false

    /// Draws each `@path` naming a file in the folder as a chip: its icon
    /// and name. Not the word the caret ends, still being typed, unless
    /// `all`: text set or pasted in. `notify` keeps the change undoable and
    /// tells the box's delegate, as typing does.
    func makeChips(all: Bool = false, notify: Bool = true) {
        guard let storage = textStorage, !chipping, !hasMarkedText() else { return }
        let caret = selectedRange()
        let typing = { (range: NSRange) in
            !all && caret.length == 0 && caret.location >= range.location && caret.location <= NSMaxRange(range)
        }
        let attributes = typingAttributes.filter { $0.key != .attachment }
        var tags: [(range: NSRange, chip: NSAttributedString)] = []
        if let mentions, string.contains("@") {
            for chip in mentions.chips(in: string) where !typing(chip.range) {
                tags.append((chip.range, FileChip.text(chip.path, attributes: attributes)))
            }
        }
        // A skill's name typed in full: the agent's skills, as `/` lists them.
        if let skills = slash?.skills, !skills.isEmpty {
            for found in skillWords(skills) where !typing(found.range) {
                tags.append((found.range, SkillChip.text(found.command, attributes: attributes)))
            }
        }
        tags.sort { $0.range.location < $1.range.location }
        let found = tags.map(\.range)
        let chips = tags.map(\.chip)
        guard !found.isEmpty else { return }
        // Its own step to undo: typing before and after doesn't run into it.
        if notify { breakUndoCoalescing() }
        if notify, !shouldChangeText(inRanges: found.map { NSValue(range: $0) }, replacementStrings: chips.map(\.string)) { return }
        chipping = true
        storage.beginEditing()
        for (range, text) in zip(found, chips).reversed() {
            storage.replaceCharacters(in: range, with: text)
        }
        storage.endEditing()
        // The caret keeps its place among the letters around the chips.
        let before = found.filter { NSMaxRange($0) <= caret.location }.reduce(0) { $0 + $1.length - 1 }
        setSelectedRange(NSRange(location: max(caret.location - before, 0), length: 0))
        if notify {
            didChangeText()
            breakUndoCoalescing()
        }
        chipping = false
    }

    /// The skills named in the text by their prefix and name, where.
    private func skillWords(_ skills: [SlashCommand]) -> [(range: NSRange, command: SlashCommand)] {
        let known = Dictionary(skills.map { ($0.label, $0) }, uniquingKeysWith: { first, _ in first })
        let ns = string as NSString
        var found: [(NSRange, SlashCommand)] = []
        for match in Self.skillWord.matches(in: string, range: NSRange(location: 0, length: ns.length)) {
            var range = match.range
            var word = ns.substring(with: range)
            while known[word] == nil, let last = word.last, ".,;:!?)]}'\"".contains(last) {
                word.removeLast()
                range.length -= 1
            }
            if let command = known[word] { found.append((range, command)) }
        }
        return found
    }

    nonisolated(unsafe) private static let skillWord = try! NSRegularExpression(pattern: #"(?<![^\s])([/$][A-Za-z0-9][A-Za-z0-9._:\-]*)"#)

    /// Puts a skill picked from the list in place of the `/` and the letters
    /// typed, as a chip with a space after it.
    func insertSkill(_ command: SlashCommand, replacing range: NSRange) {
        guard let storage = textStorage, NSMaxRange(range) <= storage.length else { return }
        let attributes = typingAttributes.filter { $0.key != .attachment }
        let chip = NSMutableAttributedString(attributedString: SkillChip.text(command, attributes: attributes))
        chip.append(NSAttributedString(string: " ", attributes: attributes))
        breakUndoCoalescing()
        guard shouldChangeText(in: range, replacementString: chip.string) else { return }
        storage.replaceCharacters(in: range, with: chip)
        setSelectedRange(NSRange(location: range.location + chip.length, length: 0))
        didChangeText()
        breakUndoCoalescing()
    }

    /// A copy holds the paths the chips stand for.
    override func writeSelection(to pboard: NSPasteboard, type: NSPasteboard.PasteboardType) -> Bool {
        guard type == .string, let storage = textStorage, storage.containsAttachments else {
            return super.writeSelection(to: pboard, type: type)
        }
        let text = selectedRanges.map { FileChip.plain(storage.attributedSubstring(from: $0.rangeValue)) }.joined(separator: "\n")
        return pboard.setString(text, forType: .string)
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
