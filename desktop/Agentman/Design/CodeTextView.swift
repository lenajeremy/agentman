import AppKit
import SwiftUI

/// Code in a text view: line numbers down the side, syntax colouring, find
/// (⌘F), and long lines that scroll rather than wrap. Read-only in the
/// files panel; in the editor it edits, with each file's own undo, the
/// cursor's line lit, indenting that follows the code, and ⌘/ to comment.
struct CodeTextView: NSViewRepresentable {
    var text: String
    /// The file's path, for its language.
    var path: String
    var editable = false
    /// Changes when the text is replaced from outside the view, by a reload
    /// or a revert; while editing, the view's own text is the one that counts.
    var revision = 0
    var fontSize: CGFloat = 12
    /// Hidden, it keeps its text, scroll and selection for when its tab
    /// comes back to the front.
    var visible = true
    var indentUnit = "    "
    var undoManager: UndoManager? = nil
    var onChange: ((String) -> Void)? = nil
    /// The cursor's line and column, from 1.
    var onCursor: ((Int, Int) -> Void)? = nil

    /// Past this much text, colouring would cost more than it gives.
    static let colourLimit = 400_000

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> NSScrollView {
        let scroll = NSScrollView()
        scroll.hasVerticalScroller = true
        scroll.hasHorizontalScroller = true
        scroll.autohidesScrollers = true
        scroll.borderType = .noBorder
        let textView = EditorTextView()
        textView.delegate = context.coordinator
        textView.textStorage?.delegate = context.coordinator
        textView.isRichText = false
        textView.importsGraphics = false
        textView.allowsUndo = true
        textView.usesFindBar = true
        textView.isIncrementalSearchingEnabled = true
        textView.isAutomaticQuoteSubstitutionEnabled = false
        textView.isAutomaticDashSubstitutionEnabled = false
        textView.isAutomaticTextReplacementEnabled = false
        textView.isAutomaticSpellingCorrectionEnabled = false
        textView.isContinuousSpellCheckingEnabled = false
        textView.smartInsertDeleteEnabled = false
        textView.textContainerInset = NSSize(width: 8, height: 10)
        // Long lines scroll sideways, as they do in an editor.
        textView.isVerticallyResizable = true
        textView.isHorizontallyResizable = true
        textView.autoresizingMask = [.width]
        textView.minSize = NSSize(width: 0, height: 0)
        textView.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        textView.textContainer?.widthTracksTextView = false
        textView.textContainer?.containerSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        textView.font = Self.font(fontSize)
        textView.textColor = NSColor(Palette.text)
        textView.insertionPointColor = NSColor(Palette.text)
        textView.selectedTextAttributes = [.backgroundColor: NSColor(Palette.working).withAlphaComponent(0.25)]
        if editable {
            // The editor's page is the surface, apart from the paper around it.
            textView.drawsBackground = true
            textView.backgroundColor = NSColor(Palette.surface)
            scroll.drawsBackground = true
            scroll.backgroundColor = NSColor(Palette.surface)
            textView.highlightsLine = true
        } else {
            textView.drawsBackground = false
            scroll.drawsBackground = false
        }
        scroll.documentView = textView

        let ruler = LineNumberRuler(textView: textView, background: NSColor(editable ? Palette.surface : Palette.paper))
        ruler.showsCurrentLine = editable
        scroll.verticalRulerView = ruler
        scroll.hasVerticalRuler = true
        scroll.rulersVisible = true
        context.coordinator.ruler = ruler
        context.coordinator.textView = textView

        configure(textView)
        textView.string = text
        // A file opens at its top, as in any editor.
        textView.setSelectedRange(NSRange(location: 0, length: 0))
        context.coordinator.lastRevision = revision
        context.coordinator.lastVisible = visible
        scroll.isHidden = !visible
        context.coordinator.colourAll()
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        let coordinator = context.coordinator
        coordinator.parent = self
        guard let textView = scroll.documentView as? EditorTextView else { return }
        let resized = textView.font?.pointSize != fontSize
        configure(textView)
        if resized { coordinator.colourAll() }
        if editable {
            if revision != coordinator.lastRevision {
                coordinator.lastRevision = revision
                coordinator.replaceAll(with: text)
            }
        } else if textView.string != text {
            textView.string = text
            coordinator.colourAll()
        }
        scroll.isHidden = !visible
        if visible && !coordinator.lastVisible, editable {
            // A tab brought to the front takes the keyboard.
            DispatchQueue.main.async { textView.window?.makeFirstResponder(textView) }
        }
        coordinator.lastVisible = visible
    }

    private func configure(_ textView: EditorTextView) {
        textView.isEditable = editable
        textView.indentUnit = indentUnit
        let language = CodeHighlighter.language(for: path)
        textView.lineComment = language.flatMap { $0.lineComment.first ?? ($0.hashComment ? "#" : nil) }
        textView.indentsAfterColon = ["py", "yaml", "yml"].contains((path as NSString).pathExtension.lowercased())
        if textView.font?.pointSize != fontSize {
            textView.font = Self.font(fontSize)
        }
    }

    static func font(_ size: CGFloat) -> NSFont {
        NSFont(descriptor: NSFontDescriptor(fontAttributes: [.family: "Google Sans Code"]), size: size)
            ?? .monospacedSystemFont(ofSize: size, weight: .regular)
    }

    static var font: NSFont { font(12) }

    @MainActor
    final class Coordinator: NSObject, NSTextViewDelegate, NSTextStorageDelegate {
        var parent: CodeTextView
        weak var ruler: LineNumberRuler?
        weak var textView: EditorTextView?
        var lastRevision = 0
        var lastVisible = true
        /// What the last edit touched, to colour just those lines at once.
        private var edited: NSRange?
        private var fullColour: DispatchWorkItem?
        private var replacing = false

        init(_ parent: CodeTextView) { self.parent = parent }

        func undoManager(for view: NSTextView) -> UndoManager? {
            parent.undoManager ?? view.window?.undoManager
        }

        nonisolated func textStorage(_ textStorage: NSTextStorage, didProcessEditing editedMask: NSTextStorageEditActions,
                                     range editedRange: NSRange, changeInLength delta: Int) {
            guard editedMask.contains(.editedCharacters) else { return }
            MainActor.assumeIsolated { edited = editedRange }
        }

        func textDidChange(_ notification: Notification) {
            guard let textView else { return }
            ruler?.textChanged()
            if !replacing { parent.onChange?(textView.string) }
            colourEdited()
            scheduleFullColour()
            reportCursor()
        }

        func textViewDidChangeSelection(_ notification: Notification) {
            reportCursor()
            ruler?.needsDisplay = true
        }

        /// Sent on the next turn of the run loop: it can be asked for in the
        /// middle of a SwiftUI update, which mustn't change what it shows.
        private func reportCursor() {
            guard let textView, let ruler, let onCursor = parent.onCursor else { return }
            let location = textView.selectedRange().location
            let (line, start) = ruler.line(at: location)
            DispatchQueue.main.async { onCursor(line + 1, location - start + 1) }
        }

        /// The whole text, from outside: undoable, and the selection kept
        /// where it was as far as the new text allows.
        func replaceAll(with text: String) {
            guard let textView, let storage = textView.textStorage, textView.string != text else { return }
            let selection = textView.selectedRange()
            let whole = NSRange(location: 0, length: storage.length)
            replacing = true
            if textView.shouldChangeText(in: whole, replacementString: text) {
                storage.replaceCharacters(in: whole, with: text)
                textView.didChangeText()
            }
            replacing = false
            let length = (text as NSString).length
            textView.setSelectedRange(NSRange(location: min(selection.location, length), length: 0))
            colourAll()
        }

        /// Colours the whole text again: simple, and quick enough for the
        /// files an agent works on.
        func colourAll() {
            guard let textView, let storage = textView.textStorage else { return }
            colour(storage, in: NSRange(location: 0, length: storage.length))
            ruler?.textChanged()
            reportCursor()
        }

        /// While typing, only the lines the edit touched; the whole text
        /// follows once typing pauses, for comments and strings that span
        /// lines.
        private func colourEdited() {
            guard let textView, let storage = textView.textStorage, let edited else { return }
            self.edited = nil
            let text = storage.string as NSString
            let clamped = NSRange(location: min(edited.location, text.length), length: min(edited.length, text.length - min(edited.location, text.length)))
            colour(storage, in: text.lineRange(for: clamped))
        }

        private func scheduleFullColour() {
            fullColour?.cancel()
            let work = DispatchWorkItem { [weak self] in
                MainActor.assumeIsolated { self?.colourAll() }
            }
            fullColour = work
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.35, execute: work)
        }

        private func colour(_ storage: NSTextStorage, in range: NSRange) {
            guard range.length > 0 || range.location == 0 else { return }
            let font = CodeTextView.font(parent.fontSize)
            storage.beginEditing()
            storage.setAttributes([.font: font, .foregroundColor: NSColor(Palette.text)], range: range)
            if storage.length <= CodeTextView.colourLimit, let language = CodeHighlighter.language(for: parent.path) {
                let part = (storage.string as NSString).substring(with: range)
                for (found, token) in CodeHighlighter.tokens(in: part, language: language) {
                    storage.addAttribute(.foregroundColor, value: CodeHighlighter.color(token),
                                         range: NSRange(location: found.location + range.location, length: found.length))
                }
            }
            storage.endEditing()
        }
    }
}

/// The text view behind CodeTextView: the cursor's line lit, and the small
/// things an editor does with indenting and comments.
final class EditorTextView: NSTextView {
    var highlightsLine = false
    var indentUnit = "    "
    /// "//" or "#", for ⌘/; nil where the language has no line comment.
    var lineComment: String?
    /// Python and YAML open a block after a colon.
    var indentsAfterColon = false

    override func drawBackground(in rect: NSRect) {
        super.drawBackground(in: rect)
        guard highlightsLine, isEditable, selectedRange().length == 0, let layout = layoutManager else { return }
        let text = string as NSString
        let location = selectedRange().location
        var line: NSRect
        if location < text.length {
            line = layout.lineFragmentRect(forGlyphAt: layout.glyphIndexForCharacter(at: location), effectiveRange: nil)
        } else {
            line = layout.extraLineFragmentRect
            if line.isEmpty, location > 0 {
                line = layout.lineFragmentRect(forGlyphAt: layout.glyphIndexForCharacter(at: location - 1), effectiveRange: nil)
            }
        }
        guard !line.isEmpty else { return }
        line.origin.x = 0
        line.size.width = bounds.width
        line.origin.y += textContainerOrigin.y
        NSColor(Palette.text).withAlphaComponent(0.045).setFill()
        line.intersection(rect).fill()
    }

    override func setSelectedRanges(_ ranges: [NSValue], affinity: NSSelectionAffinity, stillSelecting: Bool) {
        super.setSelectedRanges(ranges, affinity: affinity, stillSelecting: stillSelecting)
        if highlightsLine { needsDisplay = true }
    }

    // MARK: Indenting

    /// A new line starts where the last one did, a step further in after
    /// an opening bracket; between a pair of brackets, the closing one gets
    /// its own line.
    override func insertNewline(_ sender: Any?) {
        guard isEditable else {
            super.insertNewline(sender)
            return
        }
        let text = string as NSString
        let selection = selectedRange()
        let caret = selection.location
        let lineStart = text.lineRange(for: NSRange(location: caret, length: 0)).location
        let before = text.substring(with: NSRange(location: lineStart, length: caret - lineStart))
        let leading = String(before.prefix { $0 == " " || $0 == "\t" })
        let trimmed = before.trimmingCharacters(in: .whitespaces)
        let opens = trimmed.last.map { "{([".contains($0) || ($0 == ":" && indentsAfterColon) } ?? false
        let after = NSMaxRange(selection) < text.length ? text.character(at: NSMaxRange(selection)) : 0
        let closes = opens && [0x7D, 0x29, 0x5D].contains(after)
        if closes {
            let inner = "\n" + leading + indentUnit
            insertText(inner + "\n" + leading, replacementRange: selection)
            setSelectedRange(NSRange(location: caret + (inner as NSString).length, length: 0))
        } else {
            insertText("\n" + leading + (opens ? indentUnit : ""), replacementRange: selection)
        }
    }

    /// Tab indents the lines of a selection that spans lines; otherwise it
    /// types one step of the file's indent.
    override func insertTab(_ sender: Any?) {
        guard isEditable else {
            super.insertTab(sender)
            return
        }
        let selection = selectedRange()
        if selection.length > 0, (string as NSString).substring(with: selection).contains("\n") {
            shiftLines(by: 1)
        } else {
            insertText(indentUnit, replacementRange: selection)
        }
    }

    override func insertBacktab(_ sender: Any?) {
        guard isEditable else { return }
        shiftLines(by: -1)
    }

    /// The selected lines a step in, or out.
    func shiftLines(by direction: Int) {
        editLines { lines in
            lines.map { line in
                if direction > 0 { return line.isEmpty ? line : indentUnit + line }
                if line.hasPrefix(indentUnit) { return String(line.dropFirst(indentUnit.count)) }
                if line.hasPrefix("\t") { return String(line.dropFirst()) }
                let spaces = min(line.prefix { $0 == " " }.count, max(indentUnit.count, 1))
                return String(line.dropFirst(spaces))
            }
        }
    }

    /// Comments the selected lines out, or back in when all of them are.
    func toggleComment() {
        guard let marker = lineComment else { return }
        editLines { lines in
            let filled = lines.filter { !$0.trimmingCharacters(in: .whitespaces).isEmpty }
            guard !filled.isEmpty else { return lines }
            let commented = filled.allSatisfy { $0.trimmingCharacters(in: .whitespaces).hasPrefix(marker) }
            let indent = filled.map { $0.prefix { $0 == " " || $0 == "\t" }.count }.min() ?? 0
            return lines.map { line in
                guard !line.trimmingCharacters(in: .whitespaces).isEmpty else { return line }
                if commented {
                    guard let found = line.range(of: marker) else { return line }
                    var rest = line[found.upperBound...]
                    if rest.hasPrefix(" ") { rest = rest.dropFirst() }
                    return String(line[..<found.lowerBound]) + rest
                }
                let split = line.index(line.startIndex, offsetBy: indent)
                return String(line[..<split]) + marker + " " + line[split...]
            }
        }
    }

    /// Rewrites the whole lines the selection touches, as one undoable step,
    /// and selects them.
    private func editLines(_ change: ([String]) -> [String]) {
        let text = string as NSString
        let range = text.lineRange(for: selectedRange())
        var lines = text.substring(with: range).components(separatedBy: "\n")
        let endsWithNewline = lines.last == ""
        if endsWithNewline { lines.removeLast() }
        var replacement = change(lines).joined(separator: "\n")
        if endsWithNewline { replacement += "\n" }
        guard replacement != text.substring(with: range), shouldChangeText(in: range, replacementString: replacement) else { return }
        textStorage?.replaceCharacters(in: range, with: replacement)
        didChangeText()
        let length = (replacement as NSString).length
        setSelectedRange(NSRange(location: range.location, length: endsWithNewline ? length - 1 : length))
    }

    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        guard isEditable, window?.firstResponder === self,
              event.modifierFlags.intersection(.deviceIndependentFlagsMask) == .command else {
            return super.performKeyEquivalent(with: event)
        }
        switch event.charactersIgnoringModifiers {
        case "/": toggleComment()
        case "]": shiftLines(by: 1)
        case "[": shiftLines(by: -1)
        default: return super.performKeyEquivalent(with: event)
        }
        return true
    }
}

/// Line numbers beside a text view, for the lines on screen; in the editor,
/// the cursor's line stands out.
final class LineNumberRuler: NSRulerView {
    private weak var textView: NSTextView?
    private let background: NSColor
    var showsCurrentLine = false
    /// Where each line starts, in UTF-16 offsets.
    private var lineStarts: [Int] = [0]

    init(textView: NSTextView, background: NSColor) {
        self.textView = textView
        self.background = background
        super.init(scrollView: textView.enclosingScrollView, orientation: .verticalRuler)
        clientView = textView
        ruleThickness = 44
        NotificationCenter.default.addObserver(
            self, selector: #selector(redraw), name: NSView.boundsDidChangeNotification,
            object: textView.enclosingScrollView?.contentView
        )
        textChanged()
    }

    required init(coder: NSCoder) { fatalError("not used") }

    @objc private func redraw() { needsDisplay = true }

    func textChanged() {
        guard let text = textView?.string as NSString? else { return }
        var starts = [0]
        var index = 0
        while index < text.length {
            let range = text.lineRange(for: NSRange(location: index, length: 0))
            index = NSMaxRange(range)
            if index < text.length || (index == text.length && text.length > 0 && text.character(at: text.length - 1) == 10) {
                starts.append(index)
            }
        }
        lineStarts = starts
        let digits = max(3, String(starts.count).count)
        ruleThickness = CGFloat(digits) * 8 + 18
        needsDisplay = true
    }

    /// The line a character is on, from 0, and where that line starts.
    func line(at location: Int) -> (Int, Int) {
        var low = 0, high = lineStarts.count - 1
        while low < high {
            let mid = (low + high + 1) / 2
            if lineStarts[mid] <= location { low = mid } else { high = mid - 1 }
        }
        return (low, lineStarts[low])
    }

    override func drawHashMarksAndLabels(in rect: NSRect) {
        guard let textView, let layout = textView.layoutManager, let container = textView.textContainer,
              let visible = scrollView?.contentView.bounds else { return }
        background.setFill()
        bounds.fill()
        let glyphs = layout.glyphRange(forBoundingRect: visible, in: container)
        let chars = layout.characterRange(forGlyphRange: glyphs, actualGlyphRange: nil)
        let font = NSFont(descriptor: CodeTextView.font.fontDescriptor, size: max(10.5, (textView.font?.pointSize ?? 12) - 1.5))
            ?? CodeTextView.font
        let plain: [NSAttributedString.Key: Any] = [.font: font, .foregroundColor: NSColor(Palette.faint)]
        let current: [NSAttributedString.Key: Any] = [.font: font, .foregroundColor: NSColor(Palette.textSecondary)]
        let cursorLine = showsCurrentLine ? line(at: textView.selectedRange().location).0 : -1
        // The first line on screen, then each one after it.
        var line = self.line(at: chars.location).0
        let length = (textView.string as NSString).length
        while line < lineStarts.count, lineStarts[line] <= NSMaxRange(chars) {
            let start = lineStarts[line]
            var fragment: NSRect
            if start < length {
                fragment = layout.lineFragmentRect(forGlyphAt: layout.glyphIndexForCharacter(at: start), effectiveRange: nil)
            } else {
                fragment = layout.extraLineFragmentRect
            }
            let y = fragment.minY + textView.textContainerInset.height - visible.minY
            let label = "\(line + 1)" as NSString
            let attributes = line == cursorLine ? current : plain
            let size = label.size(withAttributes: attributes)
            label.draw(at: NSPoint(x: ruleThickness - size.width - 8, y: y + (fragment.height - size.height) / 2), withAttributes: attributes)
            line += 1
        }
    }
}
