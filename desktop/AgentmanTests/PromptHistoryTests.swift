import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// ↑ and ↓ in an agent's message box: back through what was said to it, as
/// in a terminal, from the thread as loaded and then the daemon, a page at
/// a time, and forward again to the draft.
@MainActor
final class PromptHistoryTests: XCTestCase {
    /// A history over a thread holding loaded, newest first, with older
    /// pages the daemon reads from "page 1" on.
    private func history(_ loaded: [String], cursor: String? = "page 1", older: [String: PromptHistory.Prompts] = [:],
                         fetches: @escaping () -> Void = {}) -> PromptHistory {
        let history = PromptHistory()
        history.sessionId = "claude:s1"
        history.loaded = { _ in PromptHistory.Prompts(prompts: loaded, cursor: cursor, hasMore: cursor != nil) }
        history.fetch = { _, cursor in
            fetches()
            return older[cursor]
        }
        return history
    }

    /// What ↑ shows, waiting for the daemon when it is asked.
    private func up(_ history: PromptHistory, from text: String) async -> String? {
        var shown: String?
        let took = history.older(than: text) { shown = $0 }
        for _ in 0..<50 where took && shown == nil {
            try? await Task.sleep(for: .milliseconds(10))
        }
        if let shown { history.showing(shown) }
        return shown
    }

    private func down(_ history: PromptHistory, from text: String) -> String? {
        var shown: String?
        _ = history.newer(than: text) { shown = $0 }
        if let shown, history.isBrowsing { history.showing(shown) }
        return shown
    }

    func testUpGoesBackThroughTheThreadThenTheDaemon() async {
        var fetched = 0
        let thread = (1...8).reversed().map { "prompt \($0)" }
        let history = history(thread, older: [
            "page 1": .init(prompts: ["older two", "older one"], cursor: "page 2", hasMore: true),
            "page 2": .init(prompts: ["the very first"], cursor: nil, hasMore: false),
        ], fetches: { fetched += 1 })

        var text = "half a thought"
        var seen: [String] = []
        while let next = await up(history, from: text) {
            seen.append(next)
            text = next
            if seen.count == 2 {
                XCTAssertEqual(fetched, 0, "the thread's own messages need no daemon, far from their end")
            }
        }
        XCTAssertEqual(seen, thread + ["older two", "older one", "the very first"])
        XCTAssertEqual(fetched, 2, "a page at a time, each once")
        XCTAssertFalse(history.older(than: "the very first") { _ in XCTFail("nothing further back") })

        // ↓ comes forward again, and past the newest gives the draft back.
        var back: [String] = []
        while let next = down(history, from: text) {
            back.append(next)
            text = next
        }
        XCTAssertEqual(back, Array(seen.reversed().dropFirst()) + ["half a thought"])
        XCTAssertFalse(history.isBrowsing)
        XCTAssertTrue(history.prompts.isEmpty, "back on the draft, nothing is held")
        XCTAssertNil(down(history, from: "half a thought"), "↓ on the draft is the caret's")
    }

    // Near the end of what it holds, the next page is read before ↑ gets
    // there, so ↑ doesn't wait on it.
    func testTheNextPageIsReadBeforeItIsWanted() async throws {
        var fetched = 0
        let history = history(["three", "two", "one"], older: [
            "page 1": .init(prompts: ["older"], cursor: nil, hasMore: false),
        ], fetches: { fetched += 1 })
        var shown: String?
        XCTAssertTrue(history.older(than: "") { shown = $0 })
        XCTAssertEqual(shown, "three")
        try await Task.sleep(for: .milliseconds(50))
        XCTAssertEqual(fetched, 1, "read ahead")
        XCTAssertEqual(history.prompts, ["three", "two", "one", "older"])
        history.showing("three")
        for want in ["two", "one", "older"] {
            var now: String?
            XCTAssertTrue(history.older(than: shown ?? "") { now = $0 })
            XCTAssertEqual(now, want, "shown at once")
            shown = now
            history.showing(want)
        }
    }

    // Changing a prompt from the history makes it the message being
    // written: ↑ starts again from the newest, ↓ is the caret's.
    func testChangedWordsStartAgain() async {
        let history = history(["third", "second"], cursor: nil)
        _ = await up(history, from: "")
        let second = await up(history, from: "third")
        XCTAssertEqual(second, "second")
        let again = await up(history, from: "second, changed")
        XCTAssertEqual(again, "third", "started again, with the changed words as the draft")
        XCTAssertEqual(down(history, from: "third"), "second, changed")

        _ = await up(history, from: "")
        XCTAssertNil(down(history, from: "third, changed"))
        XCTAssertFalse(history.isBrowsing)
    }

    func testAPromptSaidTwiceInARowComesOnce() async {
        let history = history(["again", "again", "once", "again"], cursor: nil)
        var seen: [String] = []
        var text = ""
        while let next = await up(history, from: text) {
            seen.append(next)
            text = next
        }
        XCTAssertEqual(seen, ["again", "once", "again"])
    }

    // An older daemon can't say: ↑ stops at the thread.
    func testWithoutTheDaemonUpStopsAtTheThread() async {
        let history = history(["only"])
        _ = await up(history, from: "")
        let none = await up(history, from: "only")
        XCTAssertNil(none)
        XCTAssertFalse(history.older(than: "only") { _ in XCTFail() })
        XCTAssertEqual(down(history, from: "only"), "")
    }

    func testNoAgentOrNothingSaidLeavesTheKeysAlone() {
        let empty = history([], cursor: nil)
        XCTAssertFalse(empty.older(than: "x") { _ in XCTFail() })
        XCTAssertFalse(empty.isBrowsing)
        let none = PromptHistory()
        XCTAssertFalse(none.older(than: "x") { _ in XCTFail() })
    }

    func testAnotherAgentStartsAgain() async {
        let history = history(["mine"], cursor: nil)
        _ = await up(history, from: "")
        XCTAssertTrue(history.isBrowsing)
        history.sessionId = "codex:s2"
        XCTAssertFalse(history.isBrowsing)
        XCTAssertTrue(history.prompts.isEmpty)
    }

    func testWhatCountsAsSaid() {
        func message(_ role: Role, _ text: String?, tool: Bool = false, sidechain: Bool = false) -> Message {
            Message(id: "m", sessionId: "s", role: role, ts: 0, text: text,
                    tool: tool ? ToolCall(name: "Bash") : nil, isSidechain: sidechain)
        }
        XCTAssertTrue(PromptHistory.isPrompt(message(.user, "fix the tests")))
        XCTAssertFalse(PromptHistory.isPrompt(message(.assistant, "done")))
        XCTAssertFalse(PromptHistory.isPrompt(message(.user, "a result", tool: true)))
        XCTAssertFalse(PromptHistory.isPrompt(message(.user, "a subagent's", sidechain: true)))
        XCTAssertFalse(PromptHistory.isPrompt(message(.user, " \n")))
        XCTAssertFalse(PromptHistory.isPrompt(message(.user, nil)))
        XCTAssertFalse(PromptHistory.isPrompt(message(.user, "[Request interrupted by user]")))
        XCTAssertFalse(PromptHistory.isPrompt(message(.user, "[Request interrupted by user for tool use]")))
        let summary = "This session is being continued from a previous conversation that ran out of context."
        XCTAssertFalse(PromptHistory.isPrompt(message(.user, summary)))
        XCTAssertFalse(PromptHistory.isPrompt(message(.user, "<artifact-content-authored-by-others/>\nThe summarized conversation included Artifact content.\n" + summary)),
                       "a summary with Claude Code's newer note before it")
        XCTAssertTrue(PromptHistory.isPrompt(message(.user, "<div> in the header is off by a pixel")))
        XCTAssertTrue(PromptHistory.isPrompt(message(.user, "why did it say \"" + summary + "\"?")))
        XCTAssertEqual(QueuedMessage(words: "look", images: 2).label, "2 images · look")
        XCTAssertEqual(QueuedMessage(words: "look", images: 1).label, "1 image · look")
        XCTAssertEqual(QueuedMessage(words: "look").label, "look")
        XCTAssertEqual(QueuedMessage(words: "", images: 3).label, "3 images")
    }

    // The box gets the words back, not the pictures that went with them.
    func testOnlyThePromptsWordsComeBack() {
        let kept = NSHomeDirectory() + "/.agentman/images/claude-s1/a1.png"
        XCTAssertEqual(PromptHistory.words("\(kept) \(kept) fix this"), "fix this", "the daemon's paths ahead of the words")
        XCTAssertEqual(PromptHistory.words("[Image #21] fix this\n[image]"), "[Image #21] fix this", "Claude's own label stays")
        XCTAssertEqual(PromptHistory.words("look\n[image]\nand this\n[image]"), "look\nand this")
        XCTAssertNil(PromptHistory.words("\(kept)"))
        XCTAssertNil(PromptHistory.words("[image]\n"))
        XCTAssertEqual(PromptHistory.words("@/Users/me/shop/main.go tidy it"), "@/Users/me/shop/main.go tidy it", "files stay, as chips")
        XCTAssertEqual(PromptHistory.words("compare /tmp/a.png with the design"), "compare /tmp/a.png with the design",
                       "a picture of the user's own, named in the words, is words")
    }

    // A message queued for a busy agent is in its history by its words,
    // whatever they are, and leaves the queue when the agent's copy comes,
    // pictures or not.
    func testQueuedMessagesKeepTheirWords() async throws {
        let client = DaemonClient(socketPath: "/nonexistent/agentman-history.sock")
        client.answer = { request in
            guard request.type == .sendMessage else { return nil }
            var reply = DaemonEvent(type: "send_result")
            reply.status = .queued
            return reply
        }
        let store = AgentsStore(client: client)
        let picture = InlineImage(mime: "image/png", data: "iVBORw0KGgo=")
        await store.send("2 images", to: "claude:s1")
        await store.send("1 image · fix this", images: [picture], to: "claude:s1")
        await store.send("", images: [picture, picture], to: "claude:s1")
        XCTAssertEqual(store.queued["claude:s1"]?.map(\.label), ["2 images", "1 image · 1 image · fix this", "2 images"])
        XCTAssertEqual(store.loadedPrompts("claude:s1").prompts, ["1 image · fix this", "2 images"],
                       "the words as written, newest first; pictures alone aren't")

        // The agent's copies arrive: the pictures' paths ahead of the words.
        var arrived = DaemonEvent(type: "messages")
        arrived.sessionId = "claude:s1"
        arrived.messages = [
            Message(id: "a", sessionId: "claude:s1", role: .user, ts: 1, text: "2 images"),
            Message(id: "b", sessionId: "claude:s1", role: .user, ts: 2, text: "/Users/me/.agentman/images/claude-s1/x.png 1 image · fix this"),
            Message(id: "c", sessionId: "claude:s1", role: .user, ts: 3, text: "/Users/me/.agentman/images/claude-s1/y.png"),
        ]
        store.apply(arrived)
        XCTAssertEqual(store.queued["claude:s1"] ?? [], [], "each found by its words, not its label")
    }

    // Typing while ↑ waits on an older page keeps what was typed.
    func testTypingWhileAPageIsReadKeepsTheWords() async throws {
        let history = history(["only"], older: ["page 1": .init(prompts: ["older"], cursor: nil, hasMore: false)])
        let slow = history.fetch
        history.fetch = { id, cursor in
            try? await Task.sleep(for: .milliseconds(150))
            return await slow(id, cursor)
        }
        var shown: [String] = []
        XCTAssertTrue(history.older(than: "draft") { shown.append($0) })
        history.showing("only")
        XCTAssertTrue(history.older(than: "only") { shown.append($0) }, "waits on the page")
        history.edited()
        try await Task.sleep(for: .milliseconds(400))
        XCTAssertEqual(shown, ["only"], "the page came, and the typed words stayed")
        XCTAssertEqual(history.prompts, ["only", "older"], "though what it read is kept for the next ↑")
    }

    // MARK: - In the box

    @Observable
    final class Draft {
        var text = ""
    }

    private struct Box: View {
        let draft: Draft
        let history: PromptHistory
        @State private var focused = false
        var body: some View {
            MessageField(text: Binding(get: { draft.text }, set: { draft.text = $0 }), placeholder: "Message",
                         focused: $focused, onSubmit: {}, history: history)
                .frame(width: 540)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private var window: NSWindow!

    override func tearDown() {
        window?.close()
        window = nil
        super.tearDown()
    }

    private func arrow(_ text: MessageTextView, up: Bool, shift: Bool = false) {
        let key = up ? NSUpArrowFunctionKey : NSDownArrowFunctionKey
        let characters = String(Character(UnicodeScalar(UInt16(key))!))
        let event = NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: shift ? [.numericPad, .function, .shift] : [.numericPad, .function],
                                     timestamp: 0, windowNumber: window.windowNumber, context: nil,
                                     characters: characters, charactersIgnoringModifiers: characters, isARepeat: false,
                                     keyCode: up ? 126 : 125)!
        text.keyDown(with: event)
        RunLoop.main.run(until: Date().addingTimeInterval(0.03))
    }

    private func box(_ history: PromptHistory, draft: Draft) throws -> MessageTextView {
        let host = NSHostingView(rootView: Box(draft: draft, history: history))
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 600, height: 400), styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.contentView = host
        host.layoutSubtreeIfNeeded()
        RunLoop.main.run(until: Date().addingTimeInterval(0.06))
        func find(_ view: NSView) -> MessageTextView? {
            (view as? MessageTextView) ?? view.subviews.lazy.compactMap(find).first
        }
        let text = try XCTUnwrap(find(host))
        window.makeFirstResponder(text)
        return text
    }

    func testTheArrowsStepThroughWhatWasSaid() throws {
        let draft = Draft()
        let text = try box(history(["fix the tests", "two lines\nof it"], cursor: nil), draft: draft)
        text.insertText("unfinished", replacementRange: text.selectedRange())

        arrow(text, up: true)
        XCTAssertEqual(draft.text, "fix the tests", "↑ shows the newest, and the draft is told")
        XCTAssertEqual(text.selectedRange(), NSRange(location: 0, length: 0), "going back, the caret is at the start")
        arrow(text, up: true)
        XCTAssertEqual(text.string, "two lines\nof it")
        arrow(text, up: true)
        XCTAssertEqual(text.string, "two lines\nof it", "nothing further back")

        // On the first of two lines, ↓ moves the caret; on the last, it goes on.
        arrow(text, up: false)
        XCTAssertEqual(text.string, "two lines\nof it")
        XCTAssertTrue(text.caretLine(.last))
        arrow(text, up: false)
        XCTAssertEqual(text.string, "fix the tests")
        XCTAssertEqual(text.selectedRange().location, ("fix the tests" as NSString).length, "coming forward, the caret is at the end")
        arrow(text, up: false)
        XCTAssertEqual(text.string, "unfinished", "past the newest, the draft comes back")
        XCTAssertEqual(draft.text, "unfinished")

        // Shift-↑ selects, as it always has.
        arrow(text, up: true, shift: true)
        XCTAssertEqual(text.string, "unfinished")
    }

    // A draft of several lines: ↑ goes up its lines first, and only from
    // the first one into the history.
    func testUpClimbsADraftsLinesFirst() throws {
        let draft = Draft()
        let text = try box(history(["said before"], cursor: nil), draft: draft)
        text.insertText("one\ntwo\nthree", replacementRange: text.selectedRange())
        XCTAssertFalse(text.caretLine(.first))
        arrow(text, up: true)
        arrow(text, up: true)
        XCTAssertEqual(text.string, "one\ntwo\nthree", "two lines up, still the draft")
        XCTAssertTrue(text.caretLine(.first))
        arrow(text, up: true)
        XCTAssertEqual(text.string, "said before")
        arrow(text, up: false)
        XCTAssertEqual(text.string, "one\ntwo\nthree")
    }

    // In the box: typing while ↑ waits on a page is the user's, and stays.
    func testTypingInTheBoxWhileAPageIsReadStays() throws {
        let draft = Draft()
        let history = history(["only"], older: ["page 1": .init(prompts: ["older"], cursor: nil, hasMore: false)])
        let slow = history.fetch
        history.fetch = { id, cursor in
            try? await Task.sleep(for: .milliseconds(150))
            return await slow(id, cursor)
        }
        let text = try box(history, draft: draft)
        arrow(text, up: true)
        XCTAssertEqual(text.string, "only")
        arrow(text, up: true)
        text.insertText(" and more", replacementRange: NSRange(location: (text.string as NSString).length, length: 0))
        RunLoop.main.run(until: Date().addingTimeInterval(0.5))
        XCTAssertEqual(text.string, "only and more")
        XCTAssertEqual(draft.text, "only and more")
    }

    // A long line wraps: its first drawn line is the first line.
    func testAWrappedLineCountsByWhatIsDrawn() throws {
        let draft = Draft()
        let text = try box(history(["said before"], cursor: nil), draft: draft)
        text.insertText(String(repeating: "a long line that wraps ", count: 12), replacementRange: text.selectedRange())
        XCTAssertFalse(text.caretLine(.first), "the caret at the end is lines below the start")
        text.setSelectedRange(NSRange(location: 3, length: 0))
        XCTAssertTrue(text.caretLine(.first))
    }
}
