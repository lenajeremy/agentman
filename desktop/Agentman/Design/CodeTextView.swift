import AppKit
import SwiftUI

/// Code in a text view: line numbers down the side, syntax colouring, find
/// (⌘F), and long lines that scroll rather than wrap. Read-only here; the
/// editor makes it editable.
struct CodeTextView: NSViewRepresentable {
    var text: String
    /// The file's path, for its language.
    var path: String
    var editable = false
    var onChange: ((String) -> Void)? = nil

    /// Past this much text, colouring would cost more than it gives.
    static let colourLimit = 400_000

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> NSScrollView {
        let scroll = NSTextView.scrollableTextView()
        scroll.drawsBackground = false
        scroll.hasHorizontalScroller = true
        scroll.autohidesScrollers = true
        guard let textView = scroll.documentView as? NSTextView else { return scroll }
        textView.delegate = context.coordinator
        textView.isRichText = false
        textView.allowsUndo = true
        textView.usesFindBar = true
        textView.isIncrementalSearchingEnabled = true
        textView.isAutomaticQuoteSubstitutionEnabled = false
        textView.isAutomaticDashSubstitutionEnabled = false
        textView.isAutomaticTextReplacementEnabled = false
        textView.isAutomaticSpellingCorrectionEnabled = false
        textView.isContinuousSpellCheckingEnabled = false
        textView.smartInsertDeleteEnabled = false
        textView.drawsBackground = false
        textView.textContainerInset = NSSize(width: 8, height: 10)
        // Long lines scroll sideways, as they do in an editor.
        textView.isHorizontallyResizable = true
        textView.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        textView.textContainer?.widthTracksTextView = false
        textView.textContainer?.containerSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        textView.font = Self.font
        textView.textColor = NSColor(Palette.text)
        textView.insertionPointColor = NSColor(Palette.text)
        textView.selectedTextAttributes = [.backgroundColor: NSColor(Palette.working).withAlphaComponent(0.25)]

        let ruler = LineNumberRuler(textView: textView)
        scroll.verticalRulerView = ruler
        scroll.hasVerticalRuler = true
        scroll.rulersVisible = true
        context.coordinator.ruler = ruler

        textView.isEditable = editable
        textView.string = text
        context.coordinator.colour(textView)
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        context.coordinator.parent = self
        guard let textView = scroll.documentView as? NSTextView else { return }
        textView.isEditable = editable
        if textView.string != text {
            textView.string = text
            context.coordinator.colour(textView)
        }
    }

    static var font: NSFont {
        NSFont(descriptor: NSFontDescriptor(fontAttributes: [.family: "Google Sans Code"]), size: 12)
            ?? .monospacedSystemFont(ofSize: 12, weight: .regular)
    }

    @MainActor
    final class Coordinator: NSObject, NSTextViewDelegate {
        var parent: CodeTextView
        weak var ruler: LineNumberRuler?

        init(_ parent: CodeTextView) { self.parent = parent }

        func textDidChange(_ notification: Notification) {
            guard let textView = notification.object as? NSTextView else { return }
            parent.onChange?(textView.string)
            colour(textView)
        }

        /// Colours the whole text again: simple, and quick enough for the
        /// files an agent works on.
        func colour(_ textView: NSTextView) {
            guard let storage = textView.textStorage else { return }
            let text = storage.string
            let whole = NSRange(location: 0, length: storage.length)
            storage.beginEditing()
            storage.setAttributes([.font: CodeTextView.font, .foregroundColor: NSColor(Palette.text)], range: whole)
            if storage.length <= CodeTextView.colourLimit, let language = CodeHighlighter.language(for: parent.path) {
                for (range, token) in CodeHighlighter.tokens(in: text, language: language) {
                    storage.addAttribute(.foregroundColor, value: CodeHighlighter.color(token), range: range)
                }
            }
            storage.endEditing()
            ruler?.textChanged()
        }
    }
}

/// Line numbers beside a text view, for the lines on screen.
final class LineNumberRuler: NSRulerView {
    private weak var textView: NSTextView?
    /// Where each line starts, in UTF-16 offsets.
    private var lineStarts: [Int] = [0]

    init(textView: NSTextView) {
        self.textView = textView
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

    override func drawHashMarksAndLabels(in rect: NSRect) {
        guard let textView, let layout = textView.layoutManager, let container = textView.textContainer,
              let visible = scrollView?.contentView.bounds else { return }
        NSColor(Palette.paper).setFill()
        bounds.fill()
        let glyphs = layout.glyphRange(forBoundingRect: visible, in: container)
        let chars = layout.characterRange(forGlyphRange: glyphs, actualGlyphRange: nil)
        let attributes: [NSAttributedString.Key: Any] = [
            .font: NSFont(descriptor: CodeTextView.font.fontDescriptor, size: 10.5) ?? CodeTextView.font,
            .foregroundColor: NSColor(Palette.faint),
        ]
        // The first line on screen, then each one after it.
        var low = 0, high = lineStarts.count - 1
        while low < high {
            let mid = (low + high + 1) / 2
            if lineStarts[mid] <= chars.location { low = mid } else { high = mid - 1 }
        }
        var line = low
        let length = (textView.string as NSString).length
        while line < lineStarts.count, lineStarts[line] <= NSMaxRange(chars) {
            let start = lineStarts[line]
            var fragment: NSRect
            if start < length {
                let glyph = layout.glyphIndexForCharacter(at: start)
                fragment = layout.lineFragmentRect(forGlyphAt: glyph, effectiveRange: nil)
            } else {
                fragment = layout.extraLineFragmentRect
            }
            let y = fragment.minY + textView.textContainerInset.height - visible.minY
            let label = "\(line + 1)" as NSString
            let size = label.size(withAttributes: attributes)
            label.draw(at: NSPoint(x: ruleThickness - size.width - 8, y: y + (fragment.height - size.height) / 2), withAttributes: attributes)
            line += 1
        }
    }
}
