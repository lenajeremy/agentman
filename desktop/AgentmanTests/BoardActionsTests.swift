import AppKit
import SwiftUI
import XCTest
@testable import Agentman

@MainActor
final class BoardActionsTests: XCTestCase {
    private func git(branch: String? = "feat/x", defaultBranch: String? = "main", upstream: String? = nil, remote: String? = "origin",
                     changes: Int = 0, ahead: Int = 0, aheadOfDefault: Int = 0) throws -> GitState {
        var fields: [String: Any] = ["sessionId": "s", "ahead": ahead, "behind": 0, "aheadOfDefault": aheadOfDefault, "changes": changes]
        fields["branch"] = branch
        fields["defaultBranch"] = defaultBranch
        fields["upstream"] = upstream
        fields["remote"] = remote
        return try JSONDecoder().decode(GitState.self, from: JSONSerialization.data(withJSONObject: fields))
    }

    func testPushingToADefaultBranchAsksFirstAndAFeatureBranchDoesNot() throws {
        let main = try git(branch: "main", upstream: "origin/main", ahead: 1)
        XCTAssertTrue(GitAction.push.pushesToProtected(main))
        // A pull request isn't a push to it, and a commit has its own modal.
        XCTAssertFalse(GitAction.createPR.pushesToProtected(main))
        XCTAssertFalse(GitAction.commit(push: true).pushesToProtected(main))

        let feature = try git(upstream: "origin/feat/x", ahead: 1)
        XCTAssertFalse(GitAction.push.pushesToProtected(feature))
        // A branch tracking the default branch goes to the default branch.
        XCTAssertTrue(GitAction.push.pushesToProtected(try git(upstream: "origin/main", ahead: 1)))
    }

    func testAFailedRunStaysOnTheCardUntilDealtWith() async throws {
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-actions.sock"))
        store.perform(.push, on: "s")
        XCTAssertEqual(store.boardGitRuns["s"]?.isRunning, true)
        // A second click while it runs starts nothing.
        let first = store.boardGitRuns["s"]?.id
        store.perform(.push, on: "s")
        XCTAssertEqual(store.boardGitRuns["s"]?.id, first)
        for _ in 0..<200 where store.boardGitRuns["s"]?.isRunning == true {
            try await Task.sleep(for: .milliseconds(50))
        }
        guard case .failed(let title, let detail, let retry)? = store.boardGitRuns["s"]?.phase else {
            return XCTFail("\(String(describing: store.boardGitRuns["s"]))")
        }
        XCTAssertEqual(title, "Push failed")
        XCTAssertFalse(detail.isEmpty)
        XCTAssertEqual(retry, .push)
    }

    func testCommittingIsLeftToItsModal() {
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-actions.sock"))
        store.perform(.commit(push: true), on: "s")
        store.perform(.idle("Up to date", hint: ""), on: "s")
        XCTAssertNil(store.boardGitRuns["s"])
    }
}

@MainActor
final class ReplyQuotesTests: XCTestCase {
    private static var kept: [NSWindow] = []

    func testAQuoteGoesAheadOfTheWordsAndComesBack() {
        let message = ReplyQuotes.message(["use the second approach"], words: "why not the first?")
        XCTAssertEqual(message, "> use the second approach\n\nwhy not the first?")
        let back = ReplyQuotes.split(message)
        XCTAssertEqual(back.quotes, ["use the second approach"])
        XCTAssertEqual(back.words, "why not the first?")
    }

    func testSeveralQuotesAndLinesWithinThem() {
        let quotes = ["line one\nline two", "with\n\na gap"]
        let message = ReplyQuotes.message(quotes, words: "both of these\n\nsecond paragraph")
        let back = ReplyQuotes.split(message)
        XCTAssertEqual(back.quotes, quotes)
        XCTAssertEqual(back.words, "both of these\n\nsecond paragraph")
    }

    func testAQuoteWithNoWordsKeepsItsShape() {
        XCTAssertEqual(ReplyQuotes.message(["just this"], words: ""), "> just this")
        let back = ReplyQuotes.split("> just this")
        XCTAssertEqual(back.quotes, ["just this"])
        XCTAssertEqual(back.words, "")
    }

    func testOrdinaryMessagesAreLeftAlone() {
        for text in ["no quote here", "a > b is true", "> quote\nthen words with no blank line", "words first\n\n> then a quote", ""] {
            let back = ReplyQuotes.split(text)
            XCTAssertEqual(back.quotes, [], text)
            XCTAssertEqual(back.words, text)
        }
    }

    func testAFileNamedAheadOfAQuoteStaysAFile() throws {
        let file = FileManager.default.temporaryDirectory.appendingPathComponent("quote-\(UUID().uuidString).txt")
        try "x".write(to: file, atomically: true, encoding: .utf8)
        defer { try? FileManager.default.removeItem(at: file) }
        let sent = SentMessage("@\(file.path)\n\n> the part\n\nabout this")
        XCTAssertEqual(sent.files, [file.path])
        XCTAssertEqual(ReplyQuotes.split(sent.text).quotes, ["the part"])
        XCTAssertEqual(ReplyQuotes.split(sent.text).words, "about this")
    }

    // MARK: - Selecting

    private final class Offers {
        var added: [String] = []
    }

    private struct Page: View {
        let offers: Offers
        var body: some View {
            VStack(alignment: .leading, spacing: 12) {
                Text("What the agent said, which can be quoted.")
                    .textSelection(.enabled)
                    .quotable()
                Text("Words of the user's own, which can't.")
                    .textSelection(.enabled)
            }
            .frame(width: 420, height: 160, alignment: .topLeading)
            .offersQuotes(when: true) { offers.added.append($0) }
        }
    }

    private func views<T: NSView>(_ type: T.Type, in view: NSView) -> [T] {
        ((view as? T).map { [$0] } ?? []) + view.subviews.flatMap { views(type, in: $0) }
    }

    private func page(_ offers: Offers) throws -> NSWindow {
        let window = NSPanel(contentRect: NSRect(x: 0, y: 0, width: 420, height: 160), styleMask: [.titled, .nonactivatingPanel], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        Self.kept.append(window)
        window.alphaValue = 0
        window.contentView = NSHostingView(rootView: Page(offers: offers))
        window.orderFrontRegardless()
        window.makeKey()
        RunLoop.main.run(until: Date().addingTimeInterval(0.8))
        return window
    }

    /// The text views of the page, top to bottom.
    private func texts(_ window: NSWindow) throws -> [NSView] {
        let root = try XCTUnwrap(window.contentView)
        return root.subviews
            .filter { String(describing: type(of: $0)).contains("TextInteraction") }
            .sorted { $0.convert($0.bounds, to: nil).maxY > $1.convert($1.bounds, to: nil).maxY }
    }

    func testTextSelectedInAnAgentsMessageIsReadWithoutTheClipboard() throws {
        let window = try page(Offers())
        defer { window.orderOut(nil) }
        let before = NSPasteboard.general.changeCount
        let text = try XCTUnwrap(try texts(window).first)
        window.makeFirstResponder(text)
        text.perform(NSSelectorFromString("selectAll:"), with: nil)
        RunLoop.main.run(until: Date().addingTimeInterval(0.2))
        let selected = try XCTUnwrap(SelectionWatcherView.selection(in: window))
        XCTAssertEqual(selected.text, "What the agent said, which can be quoted.")
        XCTAssertEqual(NSPasteboard.general.changeCount, before, "the clipboard is the user's")
    }

    func testRenderTheQuoteChip() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let shots = URL(fileURLWithPath: out)
        try FileManager.default.createDirectory(at: shots, withIntermediateDirectories: true)
        for (name, dark) in [("reply-chip-light", false), ("reply-chip-dark", true)] {
            let window = NSPanel(contentRect: NSRect(x: 0, y: 0, width: 560, height: 190), styleMask: [.titled, .nonactivatingPanel], backing: .buffered, defer: false)
            window.isReleasedWhenClosed = false
            Self.kept.append(window)
            window.alphaValue = 0
            window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
            let page = VStack(alignment: .leading, spacing: 14) {
                Text("The second approach would cache the total between renders, which is faster but can show a stale number after a discount changes.")
                    .font(TextStyle.body)
                    .foregroundStyle(Palette.text)
                    .lineSpacing(6)
                    .textSelection(.enabled)
                    .quotable()
                Text("I'd keep the first approach for now.")
                    .font(TextStyle.body)
                    .foregroundStyle(Palette.text)
            }
            .padding(24)
            .frame(width: 560, height: 190, alignment: .topLeading)
            .background(Palette.paper)
            .offersQuotes(when: true) { _ in }
            window.contentView = NSHostingView(rootView: page)
            window.orderFrontRegardless()
            window.makeKey()
            RunLoop.main.run(until: Date().addingTimeInterval(0.8))
            let root = try XCTUnwrap(window.contentView)
            let text = try XCTUnwrap(try texts(window).first)
            window.makeFirstResponder(text)
            text.perform(NSSelectorFromString("selectAll:"), with: nil)
            RunLoop.main.run(until: Date().addingTimeInterval(0.2))
            let watcher = try XCTUnwrap(views(SelectionWatcherView.self, in: root).first)
            let frame = text.convert(text.bounds, to: nil)
            watcher.read(near: NSPoint(x: frame.midX, y: frame.minY + 8))
            RunLoop.main.run(until: Date().addingTimeInterval(0.4))
            let bitmap = try XCTUnwrap(root.bitmapImageRepForCachingDisplay(in: root.bounds))
            root.cacheDisplay(in: root.bounds, to: bitmap)
            try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: shots.appendingPathComponent(name + ".png"))
            window.orderOut(nil)
        }
    }

    func testAQuoteIsOnlyOfferedOverAnAgentsMessage() throws {
        let window = try page(Offers())
        defer { window.orderOut(nil) }
        let watcher = try XCTUnwrap(views(SelectionWatcherView.self, in: try XCTUnwrap(window.contentView)).first)
        var offered: [QuoteOffer?] = []
        watcher.offered = { offered.append($0) }

        let found = try texts(window)
        XCTAssertEqual(found.count, 2)
        window.makeFirstResponder(found[0])
        found[0].perform(NSSelectorFromString("selectAll:"), with: nil)
        RunLoop.main.run(until: Date().addingTimeInterval(0.2))
        watcher.read(near: NSPoint(x: 100, y: 100))
        XCTAssertEqual(offered.last??.text, "What the agent said, which can be quoted.")
        let point = watcher.convert(NSPoint(x: 100, y: 100), from: nil)
        XCTAssertGreaterThan(try XCTUnwrap(offered.last??.origin.y), point.y, "under the selection, clear of what's above it")

        watcher.read(near: NSPoint(x: 100, y: 6))
        XCTAssertLessThan(try XCTUnwrap(offered.last??.origin.y), watcher.convert(NSPoint(x: 100, y: 6), from: nil).y, "over it where there's no room below")

        window.makeFirstResponder(found[1])
        found[1].perform(NSSelectorFromString("selectAll:"), with: nil)
        RunLoop.main.run(until: Date().addingTimeInterval(0.2))
        watcher.read(near: NSPoint(x: 100, y: 60))
        XCTAssertNil(offered.last ?? nil, "not the user's own words")
    }
}
