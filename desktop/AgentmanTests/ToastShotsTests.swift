import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// Agentman's toasts, and its chat with a card drawn small. The news comes in
/// as it would from the daemon while the user looks at another agent: a card
/// for an agent that needs them, and word that another finished. The toasts
/// themselves are tested too; the screenshots run with
/// TEST_RUNNER_AGENTMAN_SHOTS=<folder> set.
@MainActor
final class ToastShotsTests: XCTestCase {
    private func store() throws -> AgentsStore {
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        let sessions = """
        {"type":"sessions","sessions":[
         {"id":"a","kind":"claude","name":"Fix Safari sign-in","cwd":"/tmp/site","state":"idle","inject":"tmux","lastActivityAt":\(now - 60_000)},
         {"id":"b","kind":"codex","name":"Rate limit the uploads","cwd":"/tmp/api","state":"waiting_input","inject":"tmux","lastActivityAt":\(now - 120_000)},
         {"id":"c","kind":"opencode","name":"Write the changelog","cwd":"/tmp/site","state":"busy","inject":"api","lastActivityAt":\(now - 30_000)}
        ]}
        """
        let card = """
        {"type":"manager_entry","entry":{"id":"card-1","role":"card","ts":\(now - 20_000),"sessionId":"b","text":"Rate limit the uploads wants to run git push",
         "card":{"id":"card-1","kind":"question","state":"open","sessionId":"b","agentName":"Rate limit the uploads",
          "title":"wants to run `git push origin main`","detail":"Run it\\n\\ngit push origin main","questionId":"q1",
          "options":[{"key":"1","label":"Yes"},{"key":"2","label":"Yes, and don't ask again for git push"},{"key":"3","label":"No, and tell Codex what to do differently","withText":true}]}}}
        """
        let brief = """
        {"type":"manager_entry","entry":{"id":"brief-1","role":"brief","ts":\(now - 10_000),"sessionId":"a",
         "text":"Fixed sign-in on Safari 15 and later: login.tsx now uses the polyfill for crypto.randomUUID. 48 tests pass."}}
        """
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-toasts.sock"))
        store.selection = .agent("c")
        for json in [sessions, card, brief] {
            store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(json.utf8)))
        }
        return store
    }

    // News from Agentman becomes toasts while the user is elsewhere in the
    // app; opening an agent takes its toasts away, and opening Agentman's
    // chat takes them all.
    func testNewsBecomesToastsUntilItsAgentIsOpened() throws {
        let store = try store()
        XCTAssertEqual(store.toasts.map(\.id), ["card-1", "brief-1"])
        XCTAssertEqual(store.toasts.first?.card, true)
        store.selection = .agent("a")
        XCTAssertEqual(store.toasts.map(\.id), ["card-1"], "opening the agent that finished takes its toast")
        store.selection = .manager
        XCTAssertTrue(store.toasts.isEmpty, "Agentman's chat shows it all")
    }

    // Three at most; news makes way before a card that still waits.
    func testThreeToastsAtMostAndCardsStayLongest() throws {
        let store = try store()
        for n in 2...4 {
            store.showToast(ManagerEntry(id: "brief-\(n)", role: "brief", text: "News \(n)", ts: 0, sessionId: "a"))
        }
        XCTAssertEqual(store.toasts.count, AgentsStore.maxToasts)
        XCTAssertTrue(store.toasts.contains { $0.id == "card-1" }, "the card stays")
        XCTAssertEqual(store.toasts.last?.id, "brief-4")
    }

    func testRenderTheToasts() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let shots = URL(fileURLWithPath: out)
        for dark in [false, true] {
            let look = dark ? "dark" : "light"
            let store = try store()
            try render(ContentView(), size: CGSize(width: 1180, height: 760), dark: dark, store: store,
                       to: shots.appendingPathComponent("toasts-\(look).png"))
            store.selection = .manager
            try render(ContentView(), size: CGSize(width: 1180, height: 760), dark: dark, store: store,
                       to: shots.appendingPathComponent("toasts-chat-\(look).png"))
        }
    }

    private func render<V: View>(_ view: V, size: CGSize, dark: Bool, store: AgentsStore, to url: URL) throws {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.titled, .closable, .resizable],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentView = NSHostingView(rootView: view.environment(store).frame(width: size.width, height: size.height))
        window.setContentSize(size)
        window.orderFrontRegardless()
        RunLoop.main.run(until: Date().addingTimeInterval(1.5))
        let content = try XCTUnwrap(window.contentView)
        let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
        content.cacheDisplay(in: content.bounds, to: bitmap)
        try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: url)
        // Put away rather than closed: closing the last titled window ends
        // the app the tests run in.
        window.orderOut(nil)
    }
}
