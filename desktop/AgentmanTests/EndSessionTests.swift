import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// Ending the agent whose page is open leaves the page open: ended, to be
/// read, and resumed when that can be done.
@MainActor
final class EndSessionTests: XCTestCase {
    private func session(_ json: String) throws -> Session {
        try JSONDecoder().decode(Session.self, from: Data(json.utf8))
    }

    private func store(closing closed: Bool, sessions: String) throws -> AgentsStore {
        let client = DaemonClient(socketPath: "/nonexistent/agentman-end.sock")
        client.answer = { request in
            guard request.type == .endSession else { return nil }
            var reply = DaemonEvent(type: "session_ended")
            reply.sessionId = request.sessionId
            reply.closed = closed
            return reply
        }
        let store = AgentsStore(client: client)
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(sessions.utf8)))
        return store
    }

    /// The daemon lists an ended conversation under its own id, not the
    /// pane it ran in.
    func testAnEndedCopyIsUnderTheConversationsOwnId() throws {
        let running = try session(#"{"id":"codex:tmux-agentman-codex-1-resume-T9","kind":"codex","nativeId":"T9","name":"api","cwd":"/w/api","state":"waiting_input","inject":"tmux","queued":["next"],"question":{"prompt":"Run it?","options":[]}}"#)
        let ended = AgentsStore.ended(running)
        XCTAssertEqual(ended.id, "codex:T9")
        XCTAssertEqual(ended.state, .ended)
        XCTAssertEqual(ended.inject, "none")
        XCTAssertNil(ended.question)
        XCTAssertNil(ended.queued)
        XCTAssertTrue(AgentThreadView.resumable(ended))
    }

    func testEndingTheOpenAgentKeepsItsPageToResume() async throws {
        let store = try store(closing: true, sessions: #"{"type":"sessions","sessions":[{"id":"claude:abc","kind":"claude","nativeId":"abc","name":"site","cwd":"/w/site","state":"idle","inject":"tmux"}]}"#)
        store.selection = .agent("claude:abc")
        let problem = await store.end("claude:abc")
        XCTAssertNil(problem)
        XCTAssertEqual(store.selection, .agent("claude:abc"), "the page stays")
        // The daemon takes it off the list.
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(#"{"type":"sessions","sessions":[]}"#.utf8)))
        let shown = try XCTUnwrap(store.session("claude:abc"))
        XCTAssertEqual(shown.state, .ended)
        XCTAssertTrue(AgentThreadView.resumable(shown))
        XCTAssertFalse(store.stillRunning.contains("claude:abc"))
    }

    /// One the user started in their own terminal runs on there: ending it
    /// only took it off the list, so there is nothing to resume.
    func testOneStillRunningInItsOwnTerminalIsNotOfferedResume() async throws {
        let store = try store(closing: false, sessions: #"{"type":"sessions","sessions":[{"id":"claude:own","kind":"claude","nativeId":"own","name":"mine","cwd":"/w/mine","state":"idle","inject":"hook"}]}"#)
        store.selection = .agent("claude:own")
        _ = await store.end("claude:own")
        XCTAssertEqual(store.selection, .agent("claude:own"))
        XCTAssertTrue(store.stillRunning.contains("claude:own"))
    }

    /// Ended here but still running in its own terminal, it then leaves the
    /// daemon's list: the page keeps saying it runs on there, not "ended,
    /// resume", which would start a second copy of it.
    func testOneStillRunningStaysSoWhenItLeavesTheList() async throws {
        let store = try store(closing: false, sessions: #"{"type":"sessions","sessions":[{"id":"claude:own","kind":"claude","nativeId":"own","name":"mine","cwd":"/w/mine","state":"idle","inject":"hook"}]}"#)
        store.selection = .agent("claude:own")
        _ = await store.end("claude:own")
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(#"{"type":"session_gone","sessionId":"claude:own"}"#.utf8)))
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(#"{"type":"sessions","sessions":[]}"#.utf8)))
        XCTAssertEqual(store.selection, .agent("claude:own"))
        XCTAssertTrue(store.stillRunning.contains("claude:own"), "it still runs in its terminal")
        XCTAssertEqual(store.session("claude:own")?.state, .ended)
    }

    /// Ending one from elsewhere, while another's page is open, moves nothing.
    func testEndingAnotherAgentLeavesThePageAlone() async throws {
        let store = try store(closing: true, sessions: #"{"type":"sessions","sessions":[{"id":"claude:a","kind":"claude","name":"a","cwd":"/w/a","state":"idle","inject":"tmux"},{"id":"claude:b","kind":"claude","name":"b","cwd":"/w/b","state":"idle","inject":"tmux"}]}"#)
        store.selection = .agent("claude:a")
        _ = await store.end("claude:b")
        XCTAssertEqual(store.selection, .agent("claude:a"))
        XCTAssertNil(store.foundSessions["claude:b"])
    }

    /// Quit outside the app while its page is open (Ctrl-C or /exit in its
    /// terminal, a crash, the phone): the page stays, ended, to resume, as
    /// when it is ended here, rather than "This agent has ended".
    func testAnOpenAgentThatQuitsElsewhereKeepsItsPage() throws {
        let store = try store(closing: true, sessions: #"{"type":"sessions","sessions":[{"id":"claude:abc","kind":"claude","nativeId":"abc","name":"site","cwd":"/w/site","state":"idle","inject":"tmux"}]}"#)
        store.selection = .agent("claude:abc")
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(#"{"type":"sessions","sessions":[]}"#.utf8)))
        XCTAssertEqual(store.selection, .agent("claude:abc"), "the page stays")
        let shown = try XCTUnwrap(store.session("claude:abc"), "not the gone page")
        XCTAssertEqual(shown.state, .ended)
        XCTAssertEqual(shown.name, "site")
        XCTAssertTrue(AgentThreadView.resumable(shown))
        XCTAssertFalse(store.stillRunning.contains("claude:abc"))
    }

    func testOneTheDaemonSaysIsGoneKeepsItsPageToo() throws {
        let store = try store(closing: true, sessions: #"{"type":"sessions","sessions":[{"id":"codex:tmux-agentman-codex-1-T9","kind":"codex","nativeId":"T9","name":"api","cwd":"/w/api","state":"busy","inject":"tmux"}]}"#)
        store.selection = .agent("codex:tmux-agentman-codex-1-T9")
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(#"{"type":"session_gone","sessionId":"codex:tmux-agentman-codex-1-T9"}"#.utf8)))
        let shown = try XCTUnwrap(store.session("codex:tmux-agentman-codex-1-T9"))
        XCTAssertEqual(shown.state, .ended)
        XCTAssertEqual(shown.id, "codex:T9", "under the conversation's own id, which Resume reopens")
        XCTAssertNotNil(store.session("codex:T9"))
    }

    /// Resumed, the agent is live again: the live one comes first.
    func testWhenItComesBackTheLiveOneIsShown() throws {
        let store = try store(closing: true, sessions: #"{"type":"sessions","sessions":[{"id":"claude:abc","kind":"claude","nativeId":"abc","name":"site","cwd":"/w/site","state":"idle","inject":"tmux"}]}"#)
        store.selection = .agent("claude:abc")
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(#"{"type":"sessions","sessions":[]}"#.utf8)))
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(#"{"type":"sessions","sessions":[{"id":"claude:abc","kind":"claude","nativeId":"abc","name":"site","cwd":"/w/site","state":"busy","inject":"tmux"}]}"#.utf8)))
        XCTAssertEqual(store.session("claude:abc")?.state, .busy)
    }

    /// Another agent quitting, while this one's page is open, keeps nothing.
    func testAnotherAgentQuittingKeepsNothing() throws {
        let store = try store(closing: true, sessions: #"{"type":"sessions","sessions":[{"id":"claude:a","kind":"claude","name":"a","cwd":"/w/a","state":"idle","inject":"tmux"},{"id":"claude:b","kind":"claude","name":"b","cwd":"/w/b","state":"idle","inject":"tmux"}]}"#)
        store.selection = .agent("claude:a")
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(#"{"type":"sessions","sessions":[{"id":"claude:a","kind":"claude","name":"a","cwd":"/w/a","state":"idle","inject":"tmux"}]}"#.utf8)))
        XCTAssertNil(store.foundSessions["claude:b"])
        XCTAssertNil(store.session("claude:b"))
    }

    // MARK: Pictures

    /// The bar under an ended agent: to resume, and for one still running in
    /// its own terminal. Runs with TEST_RUNNER_AGENTMAN_SHOTS=<folder>.
    func testRenderTheEndedBars() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let ended = AgentsStore.ended(try session(#"{"id":"claude:x","kind":"claude","name":"site","cwd":"/w/site","state":"idle","inject":"tmux"}"#))
        let view = VStack(spacing: 16) {
            ResumeBar(session: ended, resuming: false) {}
            ResumeBar(session: ended, resuming: false, stillRunning: true) {}
        }
        .padding(24)
        .frame(width: 640)
        for (look, appearance) in [("light", NSAppearance.Name.aqua), ("dark", .darkAqua)] {
            let host = NSHostingView(rootView: view.background(Palette.paper))
            host.appearance = NSAppearance(named: appearance)
            host.frame = NSRect(origin: .zero, size: host.fittingSize)
            let bitmap = try XCTUnwrap(host.bitmapImageRepForCachingDisplay(in: host.bounds))
            host.cacheDisplay(in: host.bounds, to: bitmap)
            try XCTUnwrap(bitmap.representation(using: .png, properties: [:]))
                .write(to: URL(fileURLWithPath: out).appendingPathComponent("ended-bars-\(look).png"))
        }
    }
}
