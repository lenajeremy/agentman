import Markdown
import SwiftUI

// Agents answer in markdown: paragraphs, lists, code, and often tables. This
// renders it as SwiftUI views, parsed by swift-markdown (CommonMark plus
// GitHub's tables), so a reply reads the way it does on the phone rather than
// as raw pipes and hashes.

/// A whole reply, block by block.
struct MarkdownView: View {
    let blocks: [MarkdownBlock]

    init(_ source: String) {
        blocks = MarkdownCache.blocks(for: source)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            ForEach(blocks) { block in
                BlockView(block: block)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// Inline markdown only, for one-line places such as a question's prompt.
func inlineMarkdown(_ text: String) -> AttributedString {
    let options = AttributedString.MarkdownParsingOptions(
        interpretedSyntax: .inlineOnlyPreservingWhitespace,
        failurePolicy: .returnPartiallyParsedIfPossible
    )
    return (try? AttributedString(markdown: text, options: options)) ?? AttributedString(text)
}

struct MarkdownBlock: Identifiable {
    enum Kind {
        case paragraph(AttributedString)
        case heading(level: Int, text: AttributedString)
        case code(language: String?, code: String)
        case quote([MarkdownBlock])
        case list(ordered: Bool, start: Int, items: [[MarkdownBlock]])
        case table(header: [AttributedString], rows: [[AttributedString]])
        case rule
    }

    let id: Int
    let kind: Kind
}

// MARK: - Parsing

/// Parsed replies, by their text. A conversation re-renders often and its
/// replies never change, so each is parsed once.
@MainActor
private enum MarkdownCache {
    private static var parsed: [String: [MarkdownBlock]] = [:]

    static func blocks(for source: String) -> [MarkdownBlock] {
        if let hit = parsed[source] { return hit }
        var builder = BlockBuilder()
        let blocks = builder.blocks(of: Document(parsing: source))
        if parsed.count > 2000 { parsed.removeAll() }
        parsed[source] = blocks
        return blocks
    }
}

private struct BlockBuilder {
    private var next = 0

    mutating func blocks(of parent: Markup) -> [MarkdownBlock] {
        parent.children.compactMap { block(from: $0) }
    }

    private mutating func make(_ kind: MarkdownBlock.Kind) -> MarkdownBlock {
        next += 1
        return MarkdownBlock(id: next, kind: kind)
    }

    private mutating func block(from node: Markup) -> MarkdownBlock? {
        switch node {
        case let paragraph as Paragraph:
            return make(.paragraph(Inline.render(paragraph)))
        case let heading as Heading:
            return make(.heading(level: heading.level, text: Inline.render(heading)))
        case let code as CodeBlock:
            let text = code.code.hasSuffix("\n") ? String(code.code.dropLast()) : code.code
            return make(.code(language: code.language, code: text))
        case let quote as BlockQuote:
            return make(.quote(blocks(of: quote)))
        case let list as OrderedList:
            return make(.list(ordered: true, start: Int(list.startIndex), items: items(of: list)))
        case let list as UnorderedList:
            return make(.list(ordered: false, start: 1, items: items(of: list)))
        case let table as Markdown.Table:
            let header = table.head.cells.map { Inline.render($0) }
            let rows = table.body.rows.map { row in Array(row.cells.map { Inline.render($0) }) }
            return make(.table(header: Array(header), rows: Array(rows)))
        case is ThematicBreak:
            return make(.rule)
        case let html as HTMLBlock:
            return make(.paragraph(AttributedString(html.rawHTML.trimmingCharacters(in: .newlines))))
        default:
            let text = node.format().trimmingCharacters(in: .whitespacesAndNewlines)
            return text.isEmpty ? nil : make(.paragraph(AttributedString(text)))
        }
    }

    private mutating func items(of list: Markup) -> [[MarkdownBlock]] {
        list.children.compactMap { child -> [MarkdownBlock]? in
            guard let item = child as? ListItem else { return nil }
            var content = blocks(of: item)
            if let box = item.checkbox, case .paragraph(let text)? = content.first?.kind {
                let mark = AttributedString(box == .checked ? "☑︎ " : "☐ ")
                content[0] = MarkdownBlock(id: content[0].id, kind: .paragraph(mark + text))
            }
            return content
        }
    }
}

/// Inline nodes to one attributed string: bold, italic, code, strikethrough
/// and links, with the code in Google Sans Code on a fill.
private enum Inline {
    static func render(_ node: Markup) -> AttributedString {
        var out = AttributedString()
        for child in node.children {
            out += render(inline: child)
        }
        return out
    }

    private static func render(inline node: Markup) -> AttributedString {
        switch node {
        case let text as Markdown.Text:
            return AttributedString(text.string)
        case is SoftBreak:
            return AttributedString(" ")
        case is LineBreak:
            return AttributedString("\n")
        case let code as InlineCode:
            var piece = AttributedString(code.code)
            piece.font = Fonts.mono(12.5)
            piece.backgroundColor = Palette.fill
            return piece
        case let strong as Strong:
            var piece = render(strong)
            piece.inlinePresentationIntent = .stronglyEmphasized
            return piece
        case let emphasis as Emphasis:
            var piece = render(emphasis)
            piece.inlinePresentationIntent = .emphasized
            return piece
        case let strike as Strikethrough:
            var piece = render(strike)
            piece.strikethroughStyle = .single
            return piece
        case let link as Markdown.Link:
            var piece = render(link)
            if piece.characters.isEmpty, let destination = link.destination {
                piece = AttributedString(destination)
            }
            if let destination = link.destination, let url = URL(string: destination) {
                piece.link = url
                piece.foregroundColor = Palette.workingText
            }
            return piece
        case let image as Markdown.Image:
            let alt = render(image)
            return alt.characters.isEmpty ? AttributedString(image.source ?? "") : alt
        case let html as InlineHTML:
            return AttributedString(html.rawHTML)
        default:
            return render(node)
        }
    }
}

// MARK: - Views

private struct BlockView: View {
    let block: MarkdownBlock

    var body: some View {
        switch block.kind {
        case .paragraph(let text):
            SwiftUI.Text(text)
                .font(TextStyle.body)
                .foregroundStyle(Palette.text)
                .lineSpacing(6)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
        case .heading(let level, let text):
            SwiftUI.Text(text)
                .font(Fonts.sans(level <= 1 ? 19 : level == 2 ? 16.5 : 14.5, .medium))
                .foregroundStyle(Palette.text)
                .lineSpacing(3)
                .padding(.top, level <= 2 ? 10 : 4)
                .textSelection(.enabled)
        case .code(_, let code):
            ScrollView(.horizontal, showsIndicators: false) {
                SwiftUI.Text(code)
                    .font(TextStyle.code)
                    .foregroundStyle(Palette.text)
                    .lineSpacing(4)
                    .textSelection(.enabled)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 12)
            }
            .background(Palette.fill, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
        case .quote(let blocks):
            HStack(alignment: .top, spacing: 10) {
                RoundedRectangle(cornerRadius: 1.5)
                    .fill(Palette.line)
                    .frame(width: 3)
                VStack(alignment: .leading, spacing: 10) {
                    ForEach(blocks) { BlockView(block: $0) }
                }
                .opacity(0.85)
            }
        case .list(let ordered, let start, let items):
            VStack(alignment: .leading, spacing: 10) {
                ForEach(Array(items.enumerated()), id: \.offset) { index, item in
                    HStack(alignment: .firstTextBaseline, spacing: 10) {
                        SwiftUI.Text(ordered ? "\(start + index)." : "•")
                            .font(TextStyle.body)
                            .foregroundStyle(Palette.muted)
                            .frame(minWidth: ordered ? 18 : 10, alignment: .trailing)
                        VStack(alignment: .leading, spacing: 8) {
                            ForEach(item) { BlockView(block: $0) }
                        }
                    }
                }
            }
        case .table(let header, let rows):
            TableBlock(header: header, rows: rows)
        case .rule:
            Rectangle()
                .fill(Palette.line)
                .frame(height: 1)
                .padding(.vertical, 8)
        }
    }
}

/// A GitHub table: a header row, hairlines between rows, and horizontal
/// scrolling when it is wider than the conversation.
private struct TableBlock: View {
    let header: [AttributedString]
    let rows: [[AttributedString]]

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            Grid(alignment: .leading, horizontalSpacing: 0, verticalSpacing: 0) {
                GridRow {
                    ForEach(Array(header.enumerated()), id: \.offset) { _, cell in
                        cellView(cell, header: true)
                    }
                }
                .background(Palette.fill)
                ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
                    Divider().overlay(Palette.line)
                    GridRow {
                        ForEach(Array(row.enumerated()), id: \.offset) { _, cell in
                            cellView(cell, header: false)
                        }
                    }
                }
            }
            .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Palette.line))
            .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
        }
    }

    /// Wide enough to read, narrow enough that a table of a few columns fits
    /// the conversation's width and wraps instead of running off its edge.
    private var cellWidth: CGFloat {
        min(360, max(140, 700 / CGFloat(max(header.count, 1))))
    }

    private func cellView(_ text: AttributedString, header: Bool) -> some View {
        SwiftUI.Text(text)
            .font(header ? TextStyle.bodyMedium : TextStyle.body)
            .foregroundStyle(Palette.text)
            .textSelection(.enabled)
            .frame(maxWidth: cellWidth, alignment: .leading)
            .fixedSize(horizontal: false, vertical: true)
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
    }
}
