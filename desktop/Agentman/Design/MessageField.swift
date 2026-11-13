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
    @Environment(\.chatAutofocus) private var chatAutofocus
    @Binding var text: String
    var placeholder: String
    var fontSize: CGFloat = 14
    var enabled = true
    /// Seven lines, then it scrolls: a long draft leaves the conversation
    /// above it in view. (Ten covered too much of it.)
    var maxLines = MessageField.defaultMaxLines
    nonisolated static let defaultMaxLines = 7
    @Binding var focused: Bool
    /// Give a newly opened composer the keyboard once it can take input.
    /// Subsequent updates and re-enabling must respect where the user went.
    var autofocus = false
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
    /// What was said to the agent before, for ↑ and ↓.
    var history: PromptHistory?

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
        if !context.coordinator.reportingFocus, focused != context.coordinator.lastFocused {
            context.coordinator.lastFocused = focused
            if focused {
                textView.takeFocus()
            }
        }
        textView.requestAutofocus()
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
        textView.chatAutofocus = chatAutofocus
        textView.autofocus = autofocus
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
        textView.history = history
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
        /// Native focus is reported on the next turn, outside SwiftUI's
        /// update. Until then its old binding is not a new focus request.
        var reportingFocus = false
        private var focusGeneration = 0

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
            if selection.length == 0, !textView.hasMarkedText(), !textView.showsHistory {
                slash.update(text: textView.string, caret: selection.location)
            } else {
                slash.dismiss()
            }
        }

        /// Opens or closes the files for `@` as the caret moves.
        private func followMention() {
            guard let textView, let mentions = parent.mentions else { return }
            let selection = textView.selectedRange()
            if selection.length == 0, !textView.hasMarkedText(), !textView.showsHistory {
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
            // Coalesce quick changes so an old gain cannot undo a loss.
            reportingFocus = true
            focusGeneration += 1
            let generation = focusGeneration
            DispatchQueue.main.async { [weak self] in
                guard let self, generation == focusGeneration else { return }
                if parent.focused != now { parent.focused = now }
                reportingFocus = false
            }
        }
    }
}

/// One chat's initial focus request. Agentman's welcome and conversation
/// use different boxes; replacing one is not opening another chat.
@MainActor
final class ChatAutofocus {
    var completed = false
    private weak var lastFocusedView: NSTextView?
    private var hasOwnedFocus = false
    private var hasOrigin = false
    private weak var origin: NSResponder?

    func remember(_ window: NSWindow) {
        guard !hasOrigin else { return }
        origin = window.firstResponder
        hasOrigin = true
    }

    /// True once this box's request is settled; false while unavailable.
    func request(_ text: NSTextView) -> Bool {
        guard let window = text.window else { return false }
        if completed {
            // A focused welcome box may have been replaced by the chat's
            // regular box. Only transfer from a removed box into an empty
            // responder slot; never take it from another control.
            guard hasOwnedFocus, lastFocusedView !== text, text.isEditable, window.attachedSheet == nil,
                  window.firstResponder == nil || window.firstResponder === window || window.firstResponder === window.contentView else { return true }
            return window.makeFirstResponder(text)
        }
        remember(window)
        guard text.isEditable else { return false }
        let responder = window.firstResponder
        let empty = responder == nil || responder === window || responder === window.contentView
        if (!empty && responder !== origin) || window.attachedSheet != nil {
            completed = true
            return true
        }
        let focused = window.makeFirstResponder(text)
        if focused { completed = true }
        return focused
    }

    func receivedFocus(_ text: NSTextView) {
        completed = true
        hasOwnedFocus = true
        lastFocusedView = text
    }
}

extension EnvironmentValues {
    @Entry var chatAutofocus: ChatAutofocus? = nil
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
    var history: PromptHistory?
    /// Focus asked for before the box was in a window, given when it is.
    private var wantsFocus = false
    var autofocus = false
    var chatAutofocus: ChatAutofocus?
    private let initialFocus = ChatAutofocus()
    private var focusRequest: ChatAutofocus { chatAutofocus ?? initialFocus }

    private var autofocusPending = false
    private var autofocusSettled = false

    func requestAutofocus() {
        guard autofocus, let window, !autofocusPending, !autofocusSettled else { return }
        focusRequest.remember(window)
        autofocusPending = true
        // Focus after SwiftUI has finished attaching/removing its boxes;
        // otherwise that layout can immediately undo makeFirstResponder.
        DispatchQueue.main.async { [weak self, weak window] in
            guard let self else { return }
            autofocusPending = false
            guard let window, self.window === window else { return }
            autofocusSettled = focusRequest.request(self)
        }
    }

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
        requestAutofocus()
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
        if stepsThroughHistory(event) { return }
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

    // MARK: History

    /// Whether the box holds a prompt from the history, as it was given: a
    /// `/` or `@` in it opens no menu over the next ↑.
    var showsHistory: Bool { history?.shows(plainText) ?? false }

    /// ↑ on the first line goes back through what was said to the agent,
    /// ↓ on the last line forward again, as in a terminal; on any other
    /// line they move the caret, and a menu for `/` or `@` takes them first.
    private func stepsThroughHistory(_ event: NSEvent) -> Bool {
        guard let history, isEditable, !hasMarkedText(), event.keyCode == 126 || event.keyCode == 125,
              event.modifierFlags.isDisjoint(with: [.shift, .command, .option, .control]) else { return false }
        if !showsHistory, slash?.isOpen == true || mentions?.isOpen == true { return false }
        if event.keyCode == 126 {
            return caretLine(.first) && history.older(than: plainText) { [weak self] in self?.show(fromHistory: $0, caretAtStart: true) }
        }
        return caretLine(.last) && history.newer(than: plainText) { [weak self] in self?.show(fromHistory: $0, caretAtStart: false) }
    }

    enum Line { case first, last }

    /// Whether the caret, with nothing selected, is on the box's first or
    /// last line as drawn: a long line wraps onto several.
    func caretLine(_ line: Line) -> Bool {
        let caret = selectedRange()
        guard caret.length == 0 else { return false }
        let length = (string as NSString).length
        guard length > 0, let layoutManager, let textContainer else { return true }
        layoutManager.ensureLayout(for: textContainer)
        let here = lineRect(at: caret.location, layoutManager)
        let edge = lineRect(at: line == .first ? 0 : length, layoutManager)
        return line == .first ? here.minY <= edge.minY + 0.5 : here.minY >= edge.minY - 0.5
    }

    /// The line a caret at location is on; at the very end after a line
    /// break, the empty line the text view draws there.
    private func lineRect(at location: Int, _ layoutManager: NSLayoutManager) -> NSRect {
        let length = (string as NSString).length
        if location >= length {
            let extra = layoutManager.extraLineFragmentRect
            if !extra.isEmpty { return extra }
            return layoutManager.lineFragmentRect(forGlyphAt: layoutManager.glyphIndexForCharacter(at: length - 1), effectiveRange: nil)
        }
        return layoutManager.lineFragmentRect(forGlyphAt: layoutManager.glyphIndexForCharacter(at: location), effectiveRange: nil)
    }

    /// Puts a prompt from the history, or the draft back, in the box, as one
    /// change undo takes back. Going back, the caret is at its start, so the
    /// next ↑ goes on back; coming forward, at its end.
    func show(fromHistory text: String, caretAtStart: Bool) {
        mentions?.dismiss()
        slash?.dismiss()
        let all = NSRange(location: 0, length: (string as NSString).length)
        if shouldChangeText(in: all, replacementString: text) {
            replaceCharacters(in: all, with: text)
            didChangeText()
        }
        makeChips(all: true)
        history?.showing(plainText)
        let end = (string as NSString).length
        setSelectedRange(NSRange(location: caretAtStart ? 0 : end, length: 0))
        scrollRangeToVisible(selectedRange())
        mentions?.dismiss()
        slash?.dismiss()
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
        if became {
            focusRequest.receivedFocus(self)
            focusChanged(true)
        }
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
