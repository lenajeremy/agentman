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

/// The prose of a reply as one selectable text view.
struct MarkdownProse: NSViewRepresentable {
    let blocks: [MarkdownBlock]
    /// What the blocks were made from: the text is built again only when
    /// this changes, so a selection survives the view being drawn again.
    let key: String

    func makeCoordinator() -> MarkdownTextCoordinator { MarkdownTextCoordinator() }

    func makeNSView(context: Context) -> MarkdownTextView {
        let view = MarkdownTextView(wraps: true)
        view.delegate = context.coordinator
        return view
    }

    func updateNSView(_ view: MarkdownTextView, context: Context) {
        let coordinator = context.coordinator
        coordinator.open = context.environment.openURL
        let highlight = context.environment.findHighlight
        guard coordinator.key != key || coordinator.highlight != highlight else { return }
        coordinator.key = key
        coordinator.highlight = highlight
        view.textStorage?.setAttributedString(MarkdownProseText.attributed(blocks, highlight: highlight))
    }

    func sizeThatFits(_ proposal: ProposedViewSize, nsView: MarkdownTextView, context: Context) -> CGSize? {
        let width = proposal.width.flatMap { $0.isFinite && $0 > 0 ? $0 : nil } ?? 600
        return nsView.size(fittingWidth: width)
    }
}

/// A code block's text, one line a line: its box scrolls sideways for a
/// long one, as before.
struct MarkdownCode: NSViewRepresentable {
    let text: NSAttributedString
    let key: String

    func makeCoordinator() -> MarkdownTextCoordinator { MarkdownTextCoordinator() }

    func makeNSView(context: Context) -> MarkdownTextView {
        let view = MarkdownTextView(wraps: false)
        view.delegate = context.coordinator
        return view
    }

    func updateNSView(_ view: MarkdownTextView, context: Context) {
        guard context.coordinator.key != key else { return }
        context.coordinator.key = key
        view.textStorage?.setAttributedString(text)
    }

    func sizeThatFits(_ proposal: ProposedViewSize, nsView: MarkdownTextView, context: Context) -> CGSize? {
        nsView.size(fittingWidth: nil)
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

/// A read-only TextKit 1 text view, as tall as its text at the width it is
/// given, or as wide as its longest line when it doesn't wrap.
final class MarkdownTextView: NSTextView, @preconcurrency NSLayoutManagerDelegate {
    private let wraps: Bool

    init(wraps: Bool) {
        self.wraps = wraps
        let storage = NSTextStorage()
        let layout = NSLayoutManager()
        storage.addLayoutManager(layout)
        let container = NSTextContainer(size: NSSize(width: wraps ? 600 : CGFloat.greatestFiniteMagnitude,
                                                     height: CGFloat.greatestFiniteMagnitude))
        container.lineFragmentPadding = 0
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
        linkTextAttributes = [.foregroundColor: NSColor(Palette.workingText), .cursor: NSCursor.pointingHand]
    }

    override init(frame frameRect: NSRect, textContainer container: NSTextContainer?) {
        wraps = true
        super.init(frame: frameRect, textContainer: container)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("not from a nib") }

    /// A line break within a paragraph is a line separator here; what is
    /// copied or quoted has a plain newline.
    override func writeSelection(to pboard: NSPasteboard, type: NSPasteboard.PasteboardType) -> Bool {
        guard type == .string else { return super.writeSelection(to: pboard, type: type) }
        let selected = selectedRanges.compactMap { ($0 as? NSValue)?.rangeValue }
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
        guard let storage = textStorage, storage.length > 0 else { return 0 }
        let index = layoutManager.characterIndexForGlyph(at: glyphIndex)
        guard index < storage.length else { return 0 }
        if index == storage.length - 1 { return 0 }
        // In prose a newline ends a paragraph; in code every line is one,
        // and the space goes between them all. A line ending a paragraph is
        // asked about by its last glyph before the newline.
        let characters = storage.string as NSString
        if wraps, characters.character(at: index) == 0x0A || characters.character(at: index + 1) == 0x0A { return 0 }
        let style = storage.attribute(.paragraphStyle, at: index, effectiveRange: nil) as? NSParagraphStyle
        return style?.lineSpacing ?? 0
    }

    /// While text is measured at a width, the frame keeps out of it: a text
    /// view touches its own frame as its container changes, and following the
    /// frame then laid the text out at the old width, or none.
    private var measuring = false

    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(newSize)
        if wraps, !measuring, newSize.width > 0, let container = textContainer, container.size.width != newSize.width {
            container.size = NSSize(width: newSize.width, height: CGFloat.greatestFiniteMagnitude)
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
enum MarkdownProseText {
    /// A size above the interface's own text, so what the agent says reads
    /// first, before its steps.
    static var body: NSFont { .systemFont(ofSize: 15 * Fonts.scale) }
    static var mono: NSFont {
        NSFont(name: "Google Sans Code", size: 13 * Fonts.scale) ?? .monospacedSystemFont(ofSize: 13 * Fonts.scale, weight: .regular)
    }
    static let blockGap: CGFloat = 14

    /// Whether a block is prose all the way down, and can join the text.
    static func isProse(_ block: MarkdownBlock) -> Bool {
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

    static func attributed(_ blocks: [MarkdownBlock], highlight: FindHighlight?) -> NSAttributedString {
        var paragraphs: [Paragraph] = []
        emit(blocks, gap: blockGap, context: Context(), into: &paragraphs)
        let out = NSMutableAttributedString()
        for (index, paragraph) in paragraphs.enumerated() {
            let last = index == paragraphs.count - 1
            if last { paragraph.style.paragraphSpacing = 0 }
            if !last {
                paragraph.text.append(NSAttributedString(string: "\n", attributes: [.font: body]))
            }
            paragraph.text.addAttribute(.paragraphStyle, value: paragraph.style,
                                        range: NSRange(location: 0, length: paragraph.text.length))
            out.append(paragraph.text)
        }
        if let highlight, !highlight.words.isEmpty {
            let plain = out.string
            let color = NSColor(highlight.current ? Palette.findCurrent : Palette.findMatch)
            for range in SearchWords.ranges(of: highlight.words, in: plain) {
                out.addAttribute(.backgroundColor, value: color, range: NSRange(range, in: plain))
            }
        }
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
        let color = NSColor(Palette.text).withAlphaComponent(context.alpha)
        switch block.kind {
        case .paragraph(let text):
            let paragraph = Paragraph(inline(text, font: body, color: color))
            paragraph.style.lineSpacing = 6
            place(paragraph, block: block, context: context)
            out.append(paragraph)
        case .heading(let level, let text):
            let size: CGFloat = level <= 1 ? 20 : level == 2 ? 17.5 : 15.5
            let paragraph = Paragraph(inline(text, font: .systemFont(ofSize: size * Fonts.scale, weight: .medium), color: color))
            paragraph.style.lineSpacing = 3
            paragraph.style.paragraphSpacingBefore = level <= 2 ? 10 : 4
            place(paragraph, block: block, context: context)
            out.append(paragraph)
        case .rule:
            // A hairline across the text, 8 clear above and below.
            let line = NSTextBlock()
            line.setWidth(1, type: .absoluteValueType, for: .border, edge: .minY)
            line.setBorderColor(NSColor(Palette.line), for: .minY)
            // A block's border is drawn only once the block has a width.
            line.setValue(100, type: .percentageValueType, for: .width)
            if context.indent > 0 { line.setWidth(context.indent, type: .absoluteValueType, for: .margin, edge: .minX) }
            // The room above goes in the block's margin: spacing before a
            // paragraph falls inside its block, under the line.
            line.setWidth(8, type: .absoluteValueType, for: .margin, edge: .minY)
            let paragraph = Paragraph(NSMutableAttributedString(string: "\u{00A0}", attributes: [.font: NSFont.systemFont(ofSize: 1)]))
            paragraph.style.textBlocks = context.blocks + [line]
            paragraph.extraAfter = 8
            out.append(paragraph)
        case .quote(let blocks):
            // The quote's bar, its text 10 to the right and a little faded.
            let bar = NSTextBlock()
            bar.setWidth(3, type: .absoluteValueType, for: .border, edge: .minX)
            bar.setBorderColor(NSColor(Palette.line), for: .minX)
            bar.setWidth(10, type: .absoluteValueType, for: .padding, edge: .minX)
            bar.setValue(100, type: .percentageValueType, for: .width)
            if context.indent > 0 { bar.setWidth(context.indent, type: .absoluteValueType, for: .margin, edge: .minX) }
            let inner = Context(indent: 0, blocks: context.blocks + [bar], alpha: context.alpha * 0.85)
            emit(blocks, gap: 10, context: inner, into: &out)
        case .list(let ordered, let start, let items):
            let markers = items.indices.map { ordered ? "\(start + $0)." : "•" }
            let widest = markers.map { ($0 as NSString).size(withAttributes: [.font: body]).width }.max() ?? 0
            let column = max(ordered ? 18 : 10, ceil(widest))
            let content = context.indent + column + 10
            for (index, item) in items.enumerated() {
                var inner = context
                inner.indent = content
                let first = out.count
                emit(item, gap: 8, context: inner, into: &out)
                // The marker goes before the item's first line, right-aligned
                // to its column, as the SwiftUI list had it.
                let marker = NSMutableAttributedString(string: "\t" + markers[index] + "\t",
                                                       attributes: [.font: body, .foregroundColor: NSColor(Palette.muted)])
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
                out[first].style.tabStops = [NSTextTab(textAlignment: .right, location: context.indent + column),
                                             NSTextTab(textAlignment: .left, location: content)]
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
        for run in text.runs {
            let intent = run.inlinePresentationIntent ?? []
            var runFont = font
            var attributes: [NSAttributedString.Key: Any] = [.foregroundColor: color]
            if intent.contains(.code) {
                runFont = mono
                attributes[.backgroundColor] = NSColor(Palette.fill)
            }
            if intent.contains(.stronglyEmphasized) {
                runFont = NSFontManager.shared.convert(runFont, toHaveTrait: .boldFontMask)
            }
            if intent.contains(.emphasized) {
                runFont = NSFontManager.shared.convert(runFont, toHaveTrait: .italicFontMask)
            }
            if intent.contains(.strikethrough) {
                attributes[.strikethroughStyle] = NSUnderlineStyle.single.rawValue
            }
            if let link = run.link {
                attributes[.link] = link
            }
            attributes[.font] = runFont
            // A line break within a paragraph stays within it: a newline
            // would start another, without its line spacing.
            let string = String(text[run.range].characters).replacingOccurrences(of: "\n", with: "\u{2028}")
            out.append(NSAttributedString(string: string, attributes: attributes))
        }
        return out
    }

    /// A code block's text: the code font, 4 between lines.
    static func code(_ text: NSAttributedString) -> NSAttributedString {
        let out = NSMutableAttributedString(attributedString: text)
        let style = NSMutableParagraphStyle()
        style.lineSpacing = 4
        let whole = NSRange(location: 0, length: out.length)
        out.addAttribute(.font, value: mono, range: whole)
        out.addAttribute(.paragraphStyle, value: style, range: whole)
        out.enumerateAttribute(.foregroundColor, in: whole) { value, range, _ in
            if value == nil { out.addAttribute(.foregroundColor, value: NSColor(Palette.text), range: range) }
        }
        return out
    }
}
