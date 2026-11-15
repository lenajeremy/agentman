import SwiftUI
import XCTest
@testable import Agentman

@MainActor
final class MarkdownTests: XCTestCase {
    // The shapes agents actually write, each to its own kind of block.
    func testAReplyBecomesBlocks() {
        let reply = """
        ## Done

        | Hover on | Before |
        |---|---|
        | `put` | Signature only |

        - [x] Built the image
        - Pushed `6af39d5`

        ```go
        fmt.Println("hi")
        ```

        > Checked on a real sandbox.

        See [the docs](https://example.com) for **more**.
        """
        let kinds = MarkdownView(reply).blocks.map { block -> String in
            switch block.kind {
            case .heading(let level, _): "h\(level)"
            case .table(let header, let rows): "table \(header.count)x\(rows.count)"
            case .list(let ordered, _, let items): "\(ordered ? "ol" : "ul") \(items.count)"
            case .code(let language, let code): "code \(language ?? "") \(code)"
            case .quote: "quote"
            case .paragraph: "p"
            case .rule: "hr"
            case .images(let images): "img \(images.count)"
            }
        }
        XCTAssertEqual(kinds, ["h2", "table 2x1", "ul 2", "code go fmt.Println(\"hi\")", "quote", "p"])
    }

    func testInlineStylesSurvive() throws {
        guard case .paragraph(let text)? = MarkdownView("Use `am serve` and see [docs](https://agentman.dev).").blocks.first?.kind else {
            return XCTFail("not a paragraph")
        }
        XCTAssertEqual(String(text.characters), "Use am serve and see docs.")
        XCTAssertTrue(text.runs.contains { $0.link == URL(string: "https://agentman.dev") })
    }

    /// The links in a paragraph: each linked stretch of words and where it goes.
    private func links(in reply: String) -> [String] {
        guard case .paragraph(let text)? = MarkdownView(reply).blocks.first?.kind else {
            XCTFail("not a paragraph")
            return []
        }
        return text.runs.compactMap { run in
            run.link.map { "\(String(text[run.range].characters)) -> \($0.absoluteString)" }
        }
    }

    // Bare addresses link as GitHub, the phone and the web app link them:
    // the agent's "Keel at http://localhost:8812" was only words on the Mac.
    func testBareAddressesAreLinks() {
        XCTAssertEqual(links(in: "Keel at http://localhost:8812\nCobalt at https://localhost:8813/a_b_c."),
                       ["http://localhost:8812 -> http://localhost:8812",
                        "https://localhost:8813/a_b_c -> https://localhost:8813/a_b_c"])
        XCTAssertEqual(links(in: "Open www.agentman.dev, then (see https://en.wikipedia.org/wiki/Go_(game))."),
                       ["www.agentman.dev -> http://www.agentman.dev",
                        "https://en.wikipedia.org/wiki/Go_(game) -> https://en.wikipedia.org/wiki/Go_(game)"])
        XCTAssertEqual(links(in: "It's up (at https://agentman.dev/board)! Is **https://a.dev** bold?"),
                       ["https://agentman.dev/board -> https://agentman.dev/board",
                        "https://a.dev -> https://a.dev"])
    }

    // Code stays code, a written link keeps its own address, and an address
    // run into a word, or a scheme with nothing after it, isn't one.
    func testOnlyBareAddressesAreLinked() {
        XCTAssertEqual(links(in: "Run `curl http://localhost:8812` and [the board](https://agentman.dev/board) at http://x.dev"),
                       ["the board -> https://agentman.dev/board", "http://x.dev -> http://x.dev"])
        XCTAssertEqual(links(in: "nohttp://a.dev, http:// alone, and xwww.a.dev"), [])
        XCTAssertEqual(links(in: "<http://a.dev>"), ["http://a.dev -> http://a.dev"])
    }

    // In headings and table cells too.
    func testBareAddressesLinkInHeadingsAndTables() {
        let reply = "## Live at http://localhost:8812\n\n| Site | Where |\n|---|---|\n| Keel | http://localhost:8812 |\n"
        let blocks = MarkdownView(reply).blocks
        guard case .heading(_, let heading)? = blocks.first?.kind,
              case .table(_, let rows)? = blocks.last?.kind else {
            return XCTFail("not a heading and a table")
        }
        let address = URL(string: "http://localhost:8812")
        XCTAssertTrue(heading.runs.contains { $0.link == address })
        XCTAssertTrue(rows[0][1].runs.contains { $0.link == address })
    }

    // A cell that wraps makes its row taller, so no line is cut off.
    func testATableRowGrowsWithItsWrappedCell() {
        func height(_ cell: String) -> CGFloat {
            let table = "| Hover on | Before | Now |\n|---|---|---|\n| `put` | \(cell) | Signature only |\n"
            return NSHostingView(rootView: MarkdownView(table).frame(width: 900)).fittingSize.height
        }
        let oneLine = height("Signature only")
        let wrapped = height(String(repeating: "Same, plus the description ", count: 6))
        XCTAssertGreaterThan(wrapped, oneLine + 40, "one line \(oneLine), wrapped \(wrapped)")
    }

    // Parsed replies are kept while they are read, not every one ever shown:
    // a long day's worth stays within two generations, and a reply read
    // again parses the same.
    func testParsedRepliesAreKeptWhileRead() {
        let first = MarkdownView("**Kept** while read: `\(UUID())`").blocks
        for index in 0..<(MarkdownCache.generation * 5) {
            _ = MarkdownView("Reply \(index): **bold** and `code` \(UUID())")
        }
        XCTAssertLessThanOrEqual(MarkdownCache.count, MarkdownCache.generation * 2)
        let reply = "A reply read again: **bold**, `code`\n\n- one\n- two"
        let parsed = MarkdownView(reply).blocks
        for index in 0..<(MarkdownCache.generation + 10) {
            _ = MarkdownView("Another \(index) \(UUID())")
        }
        XCTAssertEqual(MarkdownView(reply).blocks.map(\.id), parsed.map(\.id))
        XCTAssertFalse(first.isEmpty)
    }
}
