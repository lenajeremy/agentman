import AppKit
import SwiftUI
import XCTest
@testable import Agentman

@MainActor
final class MarkdownTextTests: XCTestCase {
    private static let reply = """
    The fix is in two parts, and both are **small**.

    First, the parser drops the bar Claude draws in front of a long question, \
    and `cleanTitle` keeps the tab strip out of the prompt.

    ## What changed

    - The bar is gone from a wrapped question.
    - A tick marks the earlier answer, not part of its label.

    > Tested against real panes from Claude 2.1.322.

    ```swift
    if let rest, barred := strings.CutPrefix(trimmed, "│"); barred && !strings.HasSuffix(rest, "│") {
        trimmed = strings.TrimSpace(rest)
    }
    ```

    Then the daemon was rebuilt, and [the PR](https://github.com/lenajeremy/agentman/pull/185) merged.
    """

    /// Prose runs together; a code block, a table or a picture stands alone
    /// and splits the run.
    func testARunOfProseIsOneTextAndCodeSplitsIt() {
        let segments = MarkdownView.segments(MarkdownView(Self.reply).blocks)
        let kinds = segments.map { segment -> String in
            switch segment.content {
            case .prose(let run): "prose \(run.count)"
            case .block: "block"
            }
        }
        XCTAssertEqual(kinds, ["prose 5", "block", "prose 1"])
    }

    func testTheProseReadsAsTheBlocksDid() throws {
        let blocks = MarkdownView(Self.reply).blocks
        guard case .prose(let run) = MarkdownView.segments(blocks)[0].content else { return XCTFail("no prose first") }
        let text = MarkdownProseText.attributed(run, highlight: FindHighlight(words: ["parser"], current: true))
        let plain = text.string
        XCTAssertTrue(plain.hasPrefix("The fix is in two parts"))
        XCTAssertEqual(plain.components(separatedBy: "\n").count, 6, "one line a paragraph, the list's items each one")

        let code = (plain as NSString).range(of: "cleanTitle")
        let codeFont = try XCTUnwrap(text.attribute(.font, at: code.location, effectiveRange: nil) as? NSFont)
        XCTAssertTrue(codeFont.fontName.contains("GoogleSansCode") || codeFont.isFixedPitch, "inline code in the code font: \(codeFont.fontName)")
        XCTAssertNotNil(text.attribute(.backgroundColor, at: code.location, effectiveRange: nil), "inline code on its grey")

        let bold = (plain as NSString).range(of: "small")
        let boldFont = try XCTUnwrap(text.attribute(.font, at: bold.location, effectiveRange: nil) as? NSFont)
        XCTAssertTrue(NSFontManager.shared.traits(of: boldFont).contains(.boldFontMask))

        let heading = (plain as NSString).range(of: "What changed")
        let headingFont = try XCTUnwrap(text.attribute(.font, at: heading.location, effectiveRange: nil) as? NSFont)
        XCTAssertEqual(headingFont.pointSize, 17.5)

        let item = (plain as NSString).range(of: "\t•\tThe bar is gone")
        XCTAssertNotEqual(item.location, NSNotFound, "a list item opens with its marker")
        let itemStyle = try XCTUnwrap(text.attribute(.paragraphStyle, at: item.location, effectiveRange: nil) as? NSParagraphStyle)
        XCTAssertGreaterThan(itemStyle.headIndent, 0, "wrapped lines hang under the item's text")

        let quote = (plain as NSString).range(of: "Tested against")
        let quoteStyle = try XCTUnwrap(text.attribute(.paragraphStyle, at: quote.location, effectiveRange: nil) as? NSParagraphStyle)
        XCTAssertEqual(quoteStyle.textBlocks.first?.width(for: .border, edge: .minX), 3, "the quote's bar")

        let found = (plain as NSString).range(of: "parser")
        XCTAssertNotNil(text.attribute(.backgroundColor, at: found.location, effectiveRange: nil), "Find marks a match")
        let last = try XCTUnwrap(text.attribute(.paragraphStyle, at: text.length - 1, effectiveRange: nil) as? NSParagraphStyle)
        XCTAssertEqual(last.paragraphSpacing, 0, "no room below the last line")
    }

    /// A hard line break stays within its paragraph, keeping the paragraph's
    /// spacing; copied, it is a plain newline.
    func testALineBreakStaysInItsParagraphAndCopiesAsANewline() throws {
        let view = MarkdownTextView(wraps: true)
        view.textStorage?.setAttributedString(MarkdownProseText.attributed(MarkdownView("one<br>two\n\nthree").blocks, highlight: nil))
        XCTAssertEqual(view.string, "one\u{2028}two\nthree")
        view.setSelectedRange(NSRange(location: 0, length: (view.string as NSString).length))
        let board = NSPasteboard(name: NSPasteboard.Name("markdown-text-test"))
        board.clearContents()
        XCTAssertTrue(view.writeSelection(to: board, type: .string))
        XCTAssertEqual(board.string(forType: .string), "one\ntwo\nthree")
        XCTAssertEqual(view.accessibilitySelectedText(), "one\ntwo\nthree")
    }

    /// Line spacing goes between a paragraph's lines, not after its last:
    /// two one-line paragraphs 10 apart are a line and 10 apart, as the
    /// SwiftUI text had them.
    func testLineSpacingIsBetweenAParagraphsLinesOnly() throws {
        let view = MarkdownTextView(wraps: true)
        let style = NSMutableParagraphStyle()
        style.lineSpacing = 6
        style.paragraphSpacing = 10
        let text = NSMutableAttributedString(string: "first item\nsecond item", attributes: [.font: NSFont.systemFont(ofSize: 14), .paragraphStyle: style])
        view.textStorage?.setAttributedString(text)
        _ = view.size(fittingWidth: 500)
        let layout = try XCTUnwrap(view.layoutManager)
        var tops: [CGFloat] = []
        layout.enumerateLineFragments(forGlyphRange: NSRange(location: 0, length: layout.numberOfGlyphs)) { rect, _, _, _, _ in tops.append(rect.minY) }
        XCTAssertEqual(tops.count, 2)
        XCTAssertEqual(tops.last ?? 0, 27, accuracy: 0.5, "line tops \(tops)")
    }

    func testALinkKeepsWhereItGoes() throws {
        let blocks = MarkdownView("See [the PR](https://example.com/185).").blocks
        let text = MarkdownProseText.attributed(blocks, highlight: nil)
        let at = (text.string as NSString).range(of: "the PR").location
        XCTAssertEqual(text.attribute(.link, at: at, effectiveRange: nil) as? URL, URL(string: "https://example.com/185"))
    }

    /// As tall as its text at the width it is given, taller when narrower;
    /// a code block's as wide as its longest line.
    func testATextIsAsTallAsItsLines() {
        let view = MarkdownTextView(wraps: true)
        view.textStorage?.setAttributedString(MarkdownProseText.attributed(MarkdownView(Self.reply).blocks, highlight: nil))
        let wide = view.size(fittingWidth: 700), narrow = view.size(fittingWidth: 300)
        XCTAssertEqual(wide.width, 700)
        XCTAssertGreaterThan(narrow.height, wide.height)

        let code = MarkdownTextView(wraps: false)
        code.textStorage?.setAttributedString(MarkdownProseText.code(NSAttributedString(string: "short\na much longer line of code than the first")))
        let size = code.size(fittingWidth: nil)
        XCTAssertGreaterThan(size.width, 200)
        XCTAssertLessThan(size.width, 600)
    }

    /// A paragraph drawn on its own (in a list or quote that also holds code)
    /// is redrawn when its link, its styles or a heading's level change, not
    /// only its letters.
    func testAParagraphOnItsOwnRedrawsWhenOnlyItsLinkOrStyleChanges() throws {
        func key(_ markdown: String) throws -> String {
            guard case .list(_, _, let items) = try XCTUnwrap(MarkdownView(markdown).blocks.first).kind,
                  let first = items.first?.first else { throw XCTSkip("not a list") }
            return MarkdownView.proseKey(first)
        }
        let code = "\n\n  ```sh\n  ls\n  ```"
        let plain = try key("- see [the guide](old.md)" + code)
        XCTAssertNotEqual(plain, try key("- see [the guide](new.md)" + code), "the link's address")
        XCTAssertNotEqual(plain, try key("- see **the guide**" + code), "a style")
        XCTAssertEqual(plain, try key("- see [the guide](old.md)" + code), "the same text, the same key")
        let h2 = MarkdownView("## Title").blocks[0], h3 = MarkdownView("### Title").blocks[0]
        XCTAssertNotEqual(MarkdownView.proseKey(h2), MarkdownView.proseKey(h3), "a heading's level")
    }

    /// Inline code has room either side for its box's padding: after the
    /// letter before it and after its own last one; none before it at the
    /// start of a line.
    func testInlineCodeHasRoomEitherSide() throws {
        func inline(_ markdown: String) throws -> NSAttributedString {
            MarkdownProseText.inline(try AttributedString(markdown: markdown), font: MarkdownProseText.body, color: .textColor)
        }
        func kern(_ text: NSAttributedString, at index: Int) -> CGFloat {
            text.attribute(.kern, at: index, effectiveRange: nil) as? CGFloat ?? 0
        }
        let pad = InlineCodeLayoutManager.padding
        let middle = try inline("run `ls -la` now")
        let code = (middle.string as NSString).range(of: "ls -la")
        XCTAssertNotNil(middle.attribute(.inlineCode, at: code.location, effectiveRange: nil))
        XCTAssertEqual(kern(middle, at: code.location - 1), pad, "room after the space before it")
        XCTAssertEqual(kern(middle, at: NSMaxRange(code) - 1), pad, "room after its last letter")
        XCTAssertEqual(kern(middle, at: 0), 0, "the words around it are as they were")
        XCTAssertEqual(kern(middle, at: code.location + 1), 0)
        XCTAssertNil(middle.attribute(.inlineCode, at: 0, effectiveRange: nil))

        let first = try inline("`ls` first")
        XCTAssertEqual(kern(first, at: 1), pad, "after its last letter")
        XCTAssertEqual(kern(first, at: 0), 0, "nothing before code that starts the line")
    }

    /// Its box: the code font's height round the baseline, out to the left
    /// into the room kept there.
    func testInlineCodeBoxSitsRoundTheCodesLetters() {
        let font = MarkdownProseText.mono
        let rect = NSRect(x: 40, y: 0, width: 60, height: 30)
        let box = InlineCodeLayoutManager.box(rect, font: font, baseline: 18).bounds
        XCTAssertEqual(box.minX, 40 - InlineCodeLayoutManager.padding, accuracy: 0.01)
        XCTAssertEqual(box.maxX, 100, accuracy: 0.01)
        XCTAssertEqual(box.minY, 18 - font.ascender - 2, accuracy: 0.01)
        XCTAssertEqual(box.maxY, 18 - font.descender + 2, accuracy: 0.01)
        XCTAssertLessThan(box.height, rect.height, "not the line's whole height")
    }

    // MARK: Code

    /// A reply's code is coloured as the editor colours it, without a
    /// document's folder.
    func testCodeInAReplyIsColouredByItsLanguage() throws {
        let code = "let name = \"Agentman\" // the app\nreturn 42"
        let text = MarkdownCodeText.coloured(code, language: "swift")
        let plain = text.string as NSString
        func colour(of word: String) throws -> [CGFloat] {
            try Self.rgb(XCTUnwrap(text.attribute(.foregroundColor, at: plain.range(of: word).location, effectiveRange: nil) as? NSColor))
        }
        XCTAssertEqual(try colour(of: "let"), Self.rgb(CodeHighlighter.color(.keyword)))
        XCTAssertEqual(try colour(of: "\"Agentman\""), Self.rgb(CodeHighlighter.color(.string)))
        XCTAssertEqual(try colour(of: "// the app"), Self.rgb(CodeHighlighter.color(.comment)))
        XCTAssertEqual(try colour(of: "42"), Self.rgb(CodeHighlighter.color(.number)))
        XCTAssertEqual(try colour(of: "name"), Self.rgb(NSColor(Palette.text)), "the rest in the text's colour")
        let font = try XCTUnwrap(text.attribute(.font, at: 0, effectiveRange: nil) as? NSFont)
        XCTAssertTrue(font.fontName.contains("GoogleSansCode") || font.isFixedPitch)
        // Kept and used again, though the cache may let it go under memory
        // pressure: the same either way.
        let again = MarkdownCodeText.coloured(code, language: "swift")
        XCTAssertEqual(again.string, text.string)
        XCTAssertEqual(try Self.rgb(XCTUnwrap(again.attribute(.foregroundColor, at: 0, effectiveRange: nil) as? NSColor)), try colour(of: "let"))
    }

    func testCodeInALanguageNotKnownIsPlain() throws {
        for language in [nil, "", "brainfuck"] {
            let text = MarkdownCodeText.coloured("let x = 1", language: language)
            var colours = Set<[CGFloat]>()
            text.enumerateAttribute(.foregroundColor, in: NSRange(location: 0, length: text.length)) { value, _, _ in
                if let color = value as? NSColor { colours.insert(Self.rgb(color)) }
            }
            XCTAssertEqual(colours, [Self.rgb(NSColor(Palette.text))], "\(language ?? "nil")")
        }
    }

    func testTheLanguageLabelIsTheFencesFirstWord() {
        XCTAssertEqual(MarkdownCodeText.label("swift"), "swift")
        XCTAssertEqual(MarkdownCodeText.label("TypeScript title=app.ts"), "typescript")
        XCTAssertNil(MarkdownCodeText.label(nil))
        XCTAssertNil(MarkdownCodeText.label("  "))
    }

    /// A colour as light mode draws it.
    private static func rgb(_ color: NSColor) -> [CGFloat] {
        var out: [CGFloat] = []
        NSAppearance(named: .aqua)?.performAsCurrentDrawingAppearance {
            let c = color.usingColorSpace(.sRGB) ?? color
            out = [c.redComponent, c.greenComponent, c.blueComponent].map { ($0 * 255).rounded() }
        }
        return out
    }

    // MARK: Pictures

    /// The reply drawn, then with all of it selected in each text, light and
    /// dark. Runs with TEST_RUNNER_AGENTMAN_SHOTS=<folder>.
    func testRenderASelectedReply() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        for (look, appearance) in [("light", NSAppearance.Name.aqua), ("dark", .darkAqua)] {
            let size = CGSize(width: 640, height: 900)
            let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless], backing: .buffered, defer: false)
            window.isReleasedWhenClosed = false
            window.alphaValue = 0
            window.appearance = NSAppearance(named: appearance)
            let short = "Run `! bash /private/tmp/claude-501/-Users-mac-Documents-job-automation/33e3853e-07aa-4157-8a0b/scratchpad/rej/pull.sh` so I can see the resume, then `npm test` again.\n\nA short block, as wide as its box:\n\n```sh\nam service restart\n```\n\n> A longer quote that runs on past the width of one line, so the bar is seen to follow it down.\n\n---\n\nAfter the rule."
            let host = NSHostingView(rootView: VStack(alignment: .leading, spacing: 24) {
                MarkdownView(Self.reply)
                MarkdownView(short)
            }
            .padding(24).frame(width: size.width, height: size.height, alignment: .top)
            .background(Palette.paper))
            window.contentView = host
            window.orderFrontRegardless()
            defer { window.orderOut(nil) }
            RunLoop.main.run(until: Date().addingTimeInterval(0.6))
            for (name, select) in [("", false), ("-selected", true)] {
                if select {
                    for text in Self.textViews(in: host) {
                        text.setSelectedRange(NSRange(location: 0, length: text.textStorage?.length ?? 0))
                    }
                }
                host.display()
                let bitmap = try XCTUnwrap(host.bitmapImageRepForCachingDisplay(in: host.bounds))
                host.cacheDisplay(in: host.bounds, to: bitmap)
                try XCTUnwrap(bitmap.representation(using: .png, properties: [:]))
                    .write(to: URL(fileURLWithPath: out).appendingPathComponent("reply\(name)-\(look).png"))
            }
        }
    }

    private static func textViews(in view: NSView) -> [MarkdownTextView] {
        (view as? MarkdownTextView).map { [$0] } ?? view.subviews.flatMap(textViews)
    }
}
