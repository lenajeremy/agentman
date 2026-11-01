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
