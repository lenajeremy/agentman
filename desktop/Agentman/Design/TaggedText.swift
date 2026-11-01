import AppKit
import SwiftUI

/// Sent text: each file it names shows as a tag, its icon and name, and
/// shows the file when the pointer rests on it. Drawn by AppKit when it
/// names a file, since SwiftUI's Text can't say where in it the pointer is;
/// by SwiftUI's Text otherwise.
struct TaggedText: View {
    let text: String
    /// Where the paths it names are, when they aren't given in full.
    let folder: String?
    /// The skills it can name, the conversation's agent's.
    var skills: [SlashCommand] = []
    var color = Palette.text
    var lineSpacing: CGFloat = 5
    var selectable = true
    /// Lines shown before it is cut short, nil for all of them.
    var lineLimit: Int?
    @Environment(\.findHighlight) private var highlight

    var body: some View {
        if FileTags.namesTags(text, folder: folder, skills: skills) {
            TaggedTextField(text: text, folder: folder, skills: skills, color: color, lineSpacing: lineSpacing,
                            selectable: selectable, lineLimit: lineLimit, highlight: highlight)
        } else if selectable {
            plain.textSelection(.enabled)
        } else {
            plain
        }
    }

    private var plain: some View {
        Text(highlight?.mark(AttributedString(text)) ?? AttributedString(text))
            .font(TextStyle.body)
            .foregroundStyle(color)
            .lineSpacing(lineSpacing)
            .lineLimit(lineLimit)
    }
}

private struct TaggedTextField: NSViewRepresentable {
    let text: String
    let folder: String?
    let skills: [SlashCommand]
    let color: Color
    let lineSpacing: CGFloat
    let selectable: Bool
    let lineLimit: Int?
    let highlight: FindHighlight?

    func makeNSView(context: Context) -> TaggedTextView {
        // TextKit 1, whose attachment cells draw the tags.
        let view = TaggedTextView(usingTextLayoutManager: false)
        view.isEditable = false
        view.drawsBackground = false
        view.textContainerInset = .zero
        view.textContainer?.lineFragmentPadding = 0
        view.textContainer?.widthTracksTextView = true
        view.isVerticallyResizable = false
        view.isHorizontallyResizable = false
        return view
    }

    func updateNSView(_ view: TaggedTextView, context: Context) {
        view.folder = folder
        view.isSelectable = selectable
        view.textContainer?.maximumNumberOfLines = lineLimit ?? 0
        view.textContainer?.lineBreakMode = lineLimit == nil ? .byWordWrapping : .byTruncatingTail
        let made = attributed
        if view.shown != made {
            view.textStorage?.setAttributedString(made)
            view.shown = made
        }
    }

    /// As wide as its longest line, up to what is offered, and as tall as
    /// its lines.
    func sizeThatFits(_ proposal: ProposedViewSize, nsView: TaggedTextView, context: Context) -> CGSize? {
        Self.measure(attributed, width: proposal.width ?? 10_000, lines: lineLimit ?? 0)
    }

    private var attributed: NSAttributedString {
        let paragraph = NSMutableParagraphStyle()
        paragraph.lineSpacing = lineSpacing
        let attributes: [NSAttributedString.Key: Any] = [
            .font: NSFont.systemFont(ofSize: 14 * Fonts.scale),
            .foregroundColor: NSColor(color),
            .paragraphStyle: paragraph,
        ]
        let made = NSMutableAttributedString()
        for part in FileTags.segments(text, folder: folder, skills: skills) {
            switch part {
            case .text(let words):
                made.append(NSAttributedString(string: words, attributes: attributes))
            case .file(let path, let directory):
                made.append(FileChip.text(path, directory: directory, attributes: attributes))
            case .skill(let command):
                made.append(SkillChip.text(command, attributes: attributes))
            }
        }
        if let highlight {
            let plain = made.string
            for range in SearchWords.ranges(of: highlight.words, in: plain) {
                made.addAttribute(.backgroundColor, value: NSColor(highlight.current ? Palette.findCurrent : Palette.findMatch),
                                  range: NSRange(range, in: plain))
            }
        }
        return made
    }

    /// Lays the text out as the text view does, at a width.
    static func measure(_ text: NSAttributedString, width: CGFloat, lines: Int) -> CGSize {
        let storage = NSTextStorage(attributedString: text)
        let layout = NSLayoutManager()
        storage.addLayoutManager(layout)
        let container = NSTextContainer(size: NSSize(width: max(width, 1), height: .greatestFiniteMagnitude))
        container.lineFragmentPadding = 0
        container.maximumNumberOfLines = lines
        container.lineBreakMode = lines == 0 ? .byWordWrapping : .byTruncatingTail
        layout.addTextContainer(container)
        layout.ensureLayout(for: container)
        let used = layout.usedRect(for: container)
        return CGSize(width: min(ceil(used.width), max(width, 1)), height: ceil(used.height))
    }
}

/// Read-only text with file tags: a file shows on hover, a copy holds the
/// paths, and a right-click with nothing selected copies all of it.
final class TaggedTextView: NSTextView {
    var folder: String?
    /// The text it shows, to set again only when it changes.
    var shown: NSAttributedString?
    lazy var hover = ChipHover(self) { [weak self] in self?.folder }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        if window != nil { hover.track() } else { hover.close() }
    }

    override func mouseMoved(with event: NSEvent) {
        super.mouseMoved(with: event)
        hover.moved(event)
    }

    override func mouseExited(with event: NSEvent) {
        super.mouseExited(with: event)
        hover.close()
    }

    override func scrollWheel(with event: NSEvent) {
        hover.close()
        super.scrollWheel(with: event)
    }

    override func writeSelection(to pboard: NSPasteboard, type: NSPasteboard.PasteboardType) -> Bool {
        guard type == .string, let storage = textStorage else { return super.writeSelection(to: pboard, type: type) }
        let text = selectedRanges.map { FileChip.plain(storage.attributedSubstring(from: $0.rangeValue)) }.joined(separator: "\n")
        return pboard.setString(text, forType: .string)
    }

    override func menu(for event: NSEvent) -> NSMenu? {
        guard selectedRange().length == 0, let storage = textStorage else { return super.menu(for: event) }
        let menu = NSMenu()
        let plain = FileChip.plain(storage)
        let item = NSMenuItem(title: "Copy", action: #selector(copyAll(_:)), keyEquivalent: "")
        item.target = self
        item.representedObject = plain
        menu.addItem(item)
        return menu
    }

    @objc private func copyAll(_ item: NSMenuItem) {
        if let text = item.representedObject as? String { Clipboard.copy(text) }
    }
}
