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
        let history = history(["third", "second", "first"], older: [
            "page 1": .init(prompts: ["older two", "older one"], cursor: "page 2", hasMore: true),
            "page 2": .init(prompts: ["the very first"], cursor: nil, hasMore: false),
        ], fetches: { fetched += 1 })

        let third = await up(history, from: "half a thought")
        XCTAssertEqual(third, "third")
        let second = await up(history, from: "third")
        XCTAssertEqual(second, "second")
        let first = await up(history, from: "second")
        XCTAssertEqual(first, "first")
        XCTAssertEqual(fetched, 0, "the thread's own messages need no daemon")
        let older = await up(history, from: "first")
        XCTAssertEqual(older, "older two")
        XCTAssertEqual(fetched, 1, "past the thread, a page is asked for")
        let olderStill = await up(history, from: "older two")
        XCTAssertEqual(olderStill, "older one")
        XCTAssertEqual(fetched, 1, "that page held it")
        let veryFirst = await up(history, from: "older one")
        XCTAssertEqual(veryFirst, "the very first")
        XCTAssertFalse(history.older(than: "the very first") { _ in XCTFail("nothing further back") })

        // ↓ comes forward again, and past the newest gives the draft back.
        XCTAssertEqual(down(history, from: "the very first"), "older one")
        XCTAssertEqual(down(history, from: "older one"), "older two")
        XCTAssertEqual(down(history, from: "older two"), "first")
        XCTAssertEqual(down(history, from: "first"), "second")
        XCTAssertEqual(down(history, from: "second"), "third")
        XCTAssertEqual(down(history, from: "third"), "half a thought")
        XCTAssertFalse(history.isBrowsing)
        XCTAssertTrue(history.prompts.isEmpty, "back on the draft, nothing is held")
        XCTAssertNil(down(history, from: "half a thought"), "↓ on the draft is the caret's")
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
        XCTAssertEqual(AgentsStore.queuedWords(AgentsStore.queuedLabel("look", images: 2)), "look")
        XCTAssertEqual(AgentsStore.queuedWords(AgentsStore.queuedLabel("look", images: 1)), "look")
        XCTAssertEqual(AgentsStore.queuedWords(AgentsStore.queuedLabel("look\nagain", images: 0)), "look\nagain")
        XCTAssertNil(AgentsStore.queuedWords(AgentsStore.queuedLabel("", images: 3)))
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
