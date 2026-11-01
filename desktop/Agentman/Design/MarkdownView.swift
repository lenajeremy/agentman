import Markdown
import SwiftUI
import UniformTypeIdentifiers

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
        /// Pictures on a line of their own: a README's banner, a row of badges.
        case images([MarkdownImage])
    }

    let id: Int
    let kind: Kind
    /// Inside HTML that centres, as a README's `<div align="center">` does.
    var centered = false
}

/// A picture in markdown, as `![alt](source)` or an HTML `<img>`.
struct MarkdownImage: Hashable {
    let source: String
    let alt: String
    /// What the picture links to, as a badge does.
    var link: URL?
    /// The width an `<img>` asks for.
    var width: CGFloat?
}

extension EnvironmentValues {
    /// The folder of a Markdown file shown as a document, as the editor's
    /// preview shows one: its pictures are read from there and its code is
    /// coloured. Replies can also show screenshots with absolute local paths.
    @Entry var markdownFolder: URL?
}

// MARK: - Parsing

/// Parsed replies, by their text. A conversation re-renders often and its
/// replies never change, so each is parsed once while it is in use.
///
/// Kept in two generations: the newer fills, a reply read again from the
/// older moves into it, and when the newer is full it becomes the older and
/// the older goes. A parsed reply holds some 35 times its text (18 KB for
/// a short one), and up to 2,000 were kept, whether shown or long gone, as
/// were the versions of a Markdown file in the editor's preview as it was
/// typed.
@MainActor
enum MarkdownCache {
    private static var recent: [String: [MarkdownBlock]] = [:]
    private static var older: [String: [MarkdownBlock]] = [:]
    /// Replies in each generation: more than a conversation shows at once.
    static let generation = 200

    static func blocks(for source: String) -> [MarkdownBlock] {
        if let hit = recent[source] { return hit }
        let blocks: [MarkdownBlock]
        if let kept = older.removeValue(forKey: source) {
            blocks = kept
        } else {
            var builder = BlockBuilder()
            blocks = builder.blocks(of: Document(parsing: source))
        }
        if recent.count >= generation {
            older = recent
            recent = [:]
        }
        recent[source] = blocks
        return blocks
    }

    /// How many parsed replies are kept, for tests.
    static var count: Int { recent.count + older.count }
}

private struct BlockBuilder {
    private var next = 0
    /// Inside a `<div align="center">` that an HTML block opened and a later
    /// one closes, with markdown between them.
    private var centering = false

    mutating func blocks(of parent: Markup) -> [MarkdownBlock] {
        parent.children.flatMap { blocks(from: $0) }
    }

    private mutating func make(_ kind: MarkdownBlock.Kind) -> MarkdownBlock {
        next += 1
        return MarkdownBlock(id: next, kind: kind, centered: centering)
    }

    private mutating func blocks(from node: Markup) -> [MarkdownBlock] {
        switch node {
        case let paragraph as Paragraph:
            if let images = Self.images(alone: paragraph) {
                return [make(.images(images))]
            }
            return [make(.paragraph(Inline.render(paragraph)))]
        case let html as HTMLBlock:
            return blocks(html: html.rawHTML)
        default:
            return block(from: node).map { [$0] } ?? []
        }
    }

    private mutating func block(from node: Markup) -> MarkdownBlock? {
        switch node {
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
        default:
            let text = node.format().trimmingCharacters(in: .whitespacesAndNewlines)
            return text.isEmpty ? nil : make(.paragraph(AttributedString(text)))
        }
    }

    /// The pictures of a paragraph that is nothing else: a picture, or a
    /// picture in a link, with spaces between them.
    private static func images(alone paragraph: Paragraph) -> [MarkdownImage]? {
        var found: [MarkdownImage] = []
        func image(_ node: Markdown.Image, link: URL?) -> MarkdownImage {
            MarkdownImage(source: node.source ?? "", alt: node.plainText, link: link)
        }
        for child in paragraph.children {
            switch child {
            case let picture as Markdown.Image:
                found.append(image(picture, link: nil))
            case let link as Markdown.Link:
                let pictures = Array(link.children)
                guard !pictures.isEmpty, pictures.allSatisfy({ $0 is Markdown.Image }) else { return nil }
                let target = link.destination.flatMap(URL.init(string:))
                found += pictures.compactMap { $0 as? Markdown.Image }.map { image($0, link: target) }
            case let text as Markdown.Text where text.string.trimmingCharacters(in: .whitespaces).isEmpty:
                continue
            case is SoftBreak, is LineBreak:
                continue
            default:
                return nil
            }
        }
        return found.isEmpty ? nil : found
    }

    /// A block of HTML, as READMEs open with one, read for what it shows:
    /// its pictures, its headings and its words, in order, with the tags
    /// gone. A picture in a centred block is centred.
    private mutating func blocks(html raw: String) -> [MarkdownBlock] {
        let source = raw.replacing(/<!--[\s\S]*?-->/, with: "")
        let opens = source.contains(/(?i)<div[^>]*align\s*=\s*["']?center/)
        let closes = source.contains(/(?i)<\/div\s*>/)
        let around = centering
        if source.contains(/(?i)align\s*=\s*["']?center/) { centering = true }
        defer {
            if opens && !closes {
                centering = true
            } else if closes && !opens {
                centering = false
            } else {
                centering = around
            }
        }
        var out: [MarkdownBlock] = []
        var text = "", heading: Int?
        var pictures: [MarkdownImage] = []
        var link: URL?
        func flushText() {
            let words = HTML.decode(text).split(whereSeparator: \.isWhitespace).joined(separator: " ")
            text = ""
            guard !words.isEmpty else { return }
            if let level = heading {
                out.append(make(.heading(level: level, text: AttributedString(words))))
            } else {
                out.append(make(.paragraph(AttributedString(words))))
            }
        }
        func flushPictures() {
            guard !pictures.isEmpty else { return }
            out.append(make(.images(pictures)))
            pictures = []
        }
        var rest = Substring(source)
        while let tag = rest.firstMatch(of: /<(\/?)([a-zA-Z][a-zA-Z0-9]*)([^>]*)>/) {
            text += rest[rest.startIndex..<tag.range.lowerBound]
            rest = rest[tag.range.upperBound...]
            let closing = !tag.1.isEmpty, name = tag.2.lowercased(), attributes = String(tag.3)
            switch name {
            case "img":
                flushText()
                if let source = HTML.attribute("src", in: attributes) {
                    pictures.append(MarkdownImage(source: source, alt: HTML.attribute("alt", in: attributes) ?? "", link: link,
                                                  width: HTML.attribute("width", in: attributes).flatMap(Double.init).map { CGFloat($0) }))
                }
            case "a":
                link = closing ? nil : HTML.attribute("href", in: attributes).flatMap(URL.init(string:))
            case "h1", "h2", "h3", "h4", "h5", "h6":
                flushText()
                flushPictures()
                heading = closing ? nil : Int(name.dropFirst())
            case "br", "p", "div", "li", "ul", "ol", "table", "tr", "td", "th", "section", "picture", "center", "blockquote", "hr":
                flushText()
                flushPictures()
            default:
                break
            }
        }
        text += rest
        flushText()
        flushPictures()
        return out
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

/// What a block of HTML needs reading: an attribute's value, and the
/// entities in its words.
private enum HTML {
    static func attribute(_ name: String, in attributes: String) -> String? {
        let pattern = try! Regex("(?i)\\b\(name)\\s*=\\s*(?:\"([^\"]*)\"|'([^']*)'|([^\\s>]+))")
        guard let match = attributes.firstMatch(of: pattern) else { return nil }
        for index in 1...3 {
            if let value = match.output[index].substring { return decode(String(value)) }
        }
        return nil
    }

    static func decode(_ text: String) -> String {
        var out = text
        for (entity, character) in [("&nbsp;", " "), ("&lt;", "<"), ("&gt;", ">"), ("&quot;", "\""), ("&#39;", "'"), ("&apos;", "'"), ("&amp;", "&")] {
            out = out.replacingOccurrences(of: entity, with: character)
        }
        return out
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
            // The tags themselves: a <br> breaks the line, the rest go.
            return html.rawHTML.contains(/(?i)^<br/) ? AttributedString("\n") : AttributedString()
        default:
            return render(node)
        }
    }
}

// MARK: - Views

private struct BlockView: View {
    let block: MarkdownBlock
    @Environment(\.markdownFolder) private var folder
    @Environment(\.findHighlight) private var highlight

    private func marked(_ text: AttributedString) -> AttributedString {
        highlight?.mark(text) ?? text
    }

    var body: some View {
        content
            .multilineTextAlignment(block.centered ? .center : .leading)
            .frame(maxWidth: .infinity, alignment: block.centered ? .center : .leading)
    }

    @ViewBuilder private var content: some View {
        switch block.kind {
        case .paragraph(let text):
            SwiftUI.Text(marked(text))
                .font(TextStyle.body)
                .foregroundStyle(Palette.text)
                .lineSpacing(6)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
        case .heading(let level, let text):
            SwiftUI.Text(marked(text))
                .font(Fonts.sans(level <= 1 ? 19 : level == 2 ? 16.5 : 14.5, .medium))
                .foregroundStyle(Palette.text)
                .lineSpacing(3)
                .padding(.top, level <= 2 ? 10 : 4)
                .textSelection(.enabled)
        case .code(let language, let code):
            ScrollView(.horizontal, showsIndicators: false) {
                SwiftUI.Text(folder == nil ? AttributedString(code) : Self.coloured(code, language: language))
                    .font(TextStyle.code)
                    .foregroundStyle(Palette.text)
                    .lineSpacing(4)
                    .textSelection(.enabled)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 12)
            }
            // A scroll view and a quote's bar both stretch to any height, so
            // without this a code block above a quote shared its height with
            // the quote: the code was cut off and the bar ran on below it.
            .fixedSize(horizontal: false, vertical: true)
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
            .fixedSize(horizontal: false, vertical: true)
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
        case .images(let images):
            FlowLayout(spacing: 6) {
                ForEach(Array(images.enumerated()), id: \.offset) { _, image in
                    MarkdownImageView(image: image)
                }
            }
        }
    }

    /// Code in a document, coloured as the editor colours its language.
    private static func coloured(_ code: String, language: String?) -> AttributedString {
        let named = ["typescript": "ts", "javascript": "js", "python": "py", "shell": "sh", "bash": "sh", "zsh": "sh",
                     "console": "sh", "rust": "rs", "ruby": "rb", "kotlin": "kt", "yml": "yaml", "golang": "go"]
        let fence = (language ?? "").lowercased()
        guard !fence.isEmpty, let grammar = CodeHighlighter.language(for: "code." + (named[fence] ?? fence)) else {
            return AttributedString(code)
        }
        let text = NSMutableAttributedString(string: code)
        for (range, token) in CodeHighlighter.tokens(in: code, language: grammar) {
            text.addAttribute(.foregroundColor, value: CodeHighlighter.color(token), range: range)
        }
        return (try? AttributedString(text, including: \.appKit)) ?? AttributedString(code)
    }
}

/// A picture in a document: one from the file's folder, or from the web
/// over HTTPS. Replies also show local screenshots; failures keep their caption.
private struct MarkdownImageView: View {
    let image: MarkdownImage
    @Environment(\.markdownFolder) private var folder
    @State private var loaded: NSImage?
    @State private var failed = false

    var body: some View {
        if let link = image.link {
            Link(destination: link) { picture }
                .buttonStyle(.plain)
                .help(link.absoluteString)
        } else {
            picture
        }
    }

    @ViewBuilder private var picture: some View {
        if let folder, let local = MarkdownImages.local(image.source, in: folder) {
            sized(local)
        } else if folder == nil,
                  image.source.hasPrefix("/") || URL(string: image.source)?.isFileURL == true,
                  let local = MarkdownImages.local(image.source, in: URL(fileURLWithPath: "/", isDirectory: true)) {
            sized(local)
        } else if folder != nil, let url = URL(string: image.source), url.scheme == "https" {
            Group {
                if let loaded {
                    sized(loaded)
                } else if failed {
                    words
                } else {
                    Color.clear.frame(width: image.width ?? 60, height: 20)
                }
            }
            .task(id: url) {
                if let picture = await MarkdownImages.remote(url) {
                    loaded = picture
                } else {
                    failed = true
                }
            }
        } else {
            words
        }
    }

    /// As wide as the `<img>` asks, or as the picture is, and never wider
    /// than the page; as tall as that width makes it.
    private func sized(_ picture: NSImage) -> some View {
        let natural = picture.size
        let aspect = natural.width > 0 && natural.height > 0 ? natural.width / natural.height : 1
        let width = image.width ?? natural.width
        return SwiftUI.Image(nsImage: picture)
            .resizable()
            .aspectRatio(aspect, contentMode: .fit)
            .frame(maxWidth: width, maxHeight: width / aspect)
            .clipShape(RoundedRectangle(cornerRadius: 4, style: .continuous))
            .accessibilityLabel(image.alt)
    }

    private var words: some View {
        SwiftUI.Text(image.alt.isEmpty ? image.source : image.alt)
            .font(TextStyle.body)
            .foregroundStyle(Palette.muted)
    }
}

/// Pictures read from disk for documents, kept while the text is edited
/// around them.
@MainActor
enum MarkdownImages {
    private static let cache = NSCache<NSString, NSImage>()
    /// A screenshot is a few megabytes; past this a path isn't a picture to
    /// show in a reply.
    static let maxLocalBytes = 32 << 20

    static func local(_ source: String, in folder: URL) -> NSImage? {
        guard !source.isEmpty else { return nil }
        let url: URL
        if let scheme = URL(string: source)?.scheme {
            guard scheme == "file", let file = URL(string: source) else { return nil }
            url = file
        } else {
            let path = source.removingPercentEncoding ?? source
            url = path.hasPrefix("/") ? URL(fileURLWithPath: path) : folder.appendingPathComponent(path)
        }
        let key = url.standardizedFileURL.path as NSString
        if let hit = cache.object(forKey: key) { return hit }
        // A reply can name any path, read as the page draws: only a regular
        // file of an image type, of a picture's size. /dev/zero, a pipe or a
        // disk image would hold the app reading it.
        guard isPicture(url.standardizedFileURL),
              let image = NSImage(contentsOf: url.standardizedFileURL) else { return nil }
        cache.setObject(image, forKey: key)
        return image
    }

    static func isPicture(_ url: URL) -> Bool {
        guard let values = try? url.resourceValues(forKeys: [.isRegularFileKey, .fileSizeKey, .contentTypeKey]),
              values.isRegularFile == true, let size = values.fileSize, size <= maxLocalBytes,
              let type = values.contentType else { return false }
        return type.conforms(to: .image)
    }

    /// A picture from the web, as a README's badges are: NSImage reads the
    /// SVG they come as, which AsyncImage does not.
    static func remote(_ url: URL) async -> NSImage? {
        let key = url.absoluteString as NSString
        if let hit = cache.object(forKey: key) { return hit }
        var request = URLRequest(url: url, timeoutInterval: 15)
        request.setValue("Agentman", forHTTPHeaderField: "User-Agent")
        guard let (data, response) = try? await URLSession.shared.data(for: request),
              (response as? HTTPURLResponse)?.statusCode == 200, data.count < 8 << 20,
              let image = NSImage(data: data) else { return nil }
        cache.setObject(image, forKey: key)
        return image
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
        WrappedCell(maxWidth: cellWidth) {
            SwiftUI.Text(text)
                .font(header ? TextStyle.bodyMedium : TextStyle.body)
                .foregroundStyle(Palette.text)
                .textSelection(.enabled)
        }
        // Fills its column, so the header's band runs the whole row.
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }
}

/// A cell as tall as its text wrapped at the width it is drawn at. The
/// horizontal scroll view proposes no width, so `.frame(maxWidth:)` measured
/// the row as one long line and a wrapped cell spilled over its neighbours.
private struct WrappedCell: Layout {
    let maxWidth: CGFloat

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        guard let text = subviews.first else { return .zero }
        let width = min(text.sizeThatFits(.unspecified).width, proposal.width ?? .infinity, maxWidth)
        return CGSize(width: width, height: text.sizeThatFits(ProposedViewSize(width: width, height: nil)).height)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        subviews.first?.place(at: bounds.origin, proposal: ProposedViewSize(width: bounds.width, height: nil))
    }
}
