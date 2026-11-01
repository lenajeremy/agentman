import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// Search: what the daemon answers, how the sidebar shows it, and the
/// thread a hit opens, with its find bar. The pictures run with
/// TEST_RUNNER_AGENTMAN_SHOTS=<folder> set.
@MainActor
final class SearchTests: XCTestCase {
    nonisolated private static let now = Int64(Date().timeIntervalSince1970 * 1000)

    nonisolated private static let sessions = """
    {"type":"sessions","sessions":[
     {"id":"a","kind":"claude","name":"Fix Safari sign-in","cwd":"/tmp/site","state":"idle","inject":"tmux","lastActivityAt":\(now - 60_000)},
     {"id":"b","kind":"codex","name":"Rate limit the uploads","cwd":"/tmp/api","state":"waiting_input","inject":"tmux","lastActivityAt":\(now - 600_000)},
     {"id":"c","kind":"opencode","name":"Write the changelog","cwd":"/tmp/site","state":"idle","inject":"api","lastActivityAt":\(now - 3_600_000)}
    ]}
    """

    nonisolated private static let messages = """
    {"type":"page","page":{"sessionId":"a","hasMore":false,"messages":[
     {"id":"1","sessionId":"a","role":"user","ts":\(now - 300_000),"text":"The login page breaks on Safari. Can you find out why and fix it?"},
     {"id":"2","sessionId":"a","role":"tool","ts":\(now - 280_000),"tool":{"name":"Read","summary":"src/login.tsx","status":"ok","title":"Read login.tsx"}},
     {"id":"3","sessionId":"a","role":"assistant","ts":\(now - 240_000),"text":"Found it: `login.tsx` reads `window.crypto.randomUUID`, which Safari 15 doesn't have. I swapped in the polyfill we already ship and added a test.\\n\\n- **Fixed:** sign-in on Safari 15 and later\\n- **Tests:** 48 pass"},
     {"id":"4","sessionId":"a","role":"user","ts":\(now - 120_000),"text":"Great. Does it still work in Chrome?"},
     {"id":"5","sessionId":"a","role":"assistant","ts":\(now - 100_000),"text":"Yes: the tests run in Chrome and Firefox as well, and all 48 pass."}
    ]}}
    """

    nonisolated private static let everywhere = """
    {"type":"search","search":{"query":"safari","hits":[
     {"session":{"id":"a","kind":"claude","name":"Fix Safari sign-in","cwd":"/tmp/site","state":"idle","lastActivityAt":\(now - 60_000)},
      "messageId":"3","role":"assistant","ts":\(now - 240_000),"snippet":"…randomUUID, which Safari 15 doesn't have. I swapped in the polyfill we already ship and added a test.","matches":3},
     {"session":{"id":"e","kind":"claude","name":"Browser support for the beta","cwd":"/tmp/site","state":"ended","lastActivityAt":\(now - 9 * 86_400_000)},
      "messageId":"x9","role":"user","ts":\(now - 9 * 86_400_000),"snippet":"…which browsers do we support? Safari 15 is the oldest anyone on the beta uses, so start there.","matches":1},
     {"session":{"id":"c","kind":"opencode","name":"Write the changelog","cwd":"/tmp/site","state":"idle","lastActivityAt":\(now - 3_600_000)},
      "messageId":"c4","role":"assistant","ts":\(now - 3_600_000),"snippet":"…added a line for the Safari sign-in fix under 0.9.2, with the issue it closes.","matches":1}
    ]}}
    """

    nonisolated private static let inThread = """
    {"type":"search","search":{"query":"safari","sessionId":"a","hits":[
     {"messageId":"1","role":"user","ts":\(now - 300_000),"snippet":"The login page breaks on Safari."},
     {"messageId":"3","role":"assistant","ts":\(now - 240_000),"snippet":"Found it…"}
    ]}}
    """

    /// A store whose daemon answers from the samples above.
    private func store() throws -> AgentsStore {
        let client = DaemonClient(socketPath: "/nonexistent/agentman-search.sock")
        client.answer = { request in
            let json: String? = switch request.type {
            case .search: request.sessionId == nil ? Self.everywhere : Self.inThread
            case .fetchMessages: Self.messages
            default: nil
            }
            return json.flatMap { try? JSONDecoder().decode(DaemonEvent.self, from: Data($0.utf8)) }
        }
        let store = AgentsStore(client: client)
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(Self.sessions.utf8)))
        return store
    }

    func testResultsAreRead() throws {
        let event = try JSONDecoder().decode(DaemonEvent.self, from: Data(Self.everywhere.utf8))
        let results = try XCTUnwrap(event.search)
        XCTAssertEqual(results.hits.map(\.messageId), ["3", "x9", "c4"])
        XCTAssertEqual(results.hits[1].session?.state, .ended)
        XCTAssertEqual(results.hits[0].matches, 3)
        var request = DaemonRequest(.search, sessionId: "a")
        request.text = "safari"
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(request)) as? [String: Any])
        XCTAssertEqual(json["type"] as? String, "search")
        XCTAssertEqual(json["text"] as? String, "safari")
    }

    func testOnlyWordsOfThreeLettersAreLookedUp() {
        XCTAssertTrue(AgentsStore.searchable("go notarize"))
        XCTAssertFalse(AgentsStore.searchable("to a"))
        XCTAssertFalse(AgentsStore.searchable("   "))
    }

    /// Words are found as the daemon found them: case and accents aside,
    /// the longest first, each place once.
    func testWordsAreFoundAgain() {
        let text = "Notarized the Café build; notarize again"
        let found = SearchWords.ranges(of: SearchWords.of("notarize cafe"), in: text).map { String(text[$0]) }
        XCTAssertEqual(found, ["Notarize", "Café", "notarize"])
        XCTAssertEqual(SearchWords.of("a notarize no"), ["notarize", "no"])
    }

    /// An agent listed by name shows what was said under it; the others
    /// found only in their messages follow, the ended among them.
    func testTheSidebarSplitsHits() async throws {
        let store = try store()
        store.search = "safari"
        store.messageHits = await store.searchMessages("safari")
        let found = store.sidebarHits
        XCTAssertEqual(found.bySession["a"]?.messageId, "3")
        XCTAssertEqual(found.others.map(\.messageId), ["x9", "c4"])
        XCTAssertFalse(found.searching)
        store.folderFilter = "/tmp/api"
        XCTAssertTrue(store.sidebarHits.others.isEmpty, "a folder filter applies to what was said as well")
    }

    /// Opening an ended conversation keeps it, so it can show, and asks its
    /// thread to scroll to the message.
    func testOpeningAHit() throws {
        let store = try store()
        let event = try JSONDecoder().decode(DaemonEvent.self, from: Data(Self.everywhere.utf8))
        let ended = try XCTUnwrap(event.search?.hits[1])
        store.open(ended, query: "safari")
        XCTAssertEqual(store.selection, .agent("e"))
        XCTAssertEqual(store.session("e")?.state, .ended)
        XCTAssertEqual(store.jump?.messageId, "x9")
        XCTAssertEqual(store.jump?.query, "safari")
    }

    // MARK: Pictures

    func testRenderSearch() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let folder = URL(fileURLWithPath: out)
        for dark in [false, true] {
            let look = dark ? "dark" : "light"
            let searching = try store()
            searching.selection = .agent("b")
            searching.search = "safari"
            try render(ContentView(), size: CGSize(width: 1100, height: 640), dark: dark, store: searching,
                       to: folder.appendingPathComponent("search-sidebar-\(look).png"))

            let opened = try store()
            opened.search = "safari"
            opened.jump = ThreadJump(sessionId: "a", messageId: "3", query: "safari")
            opened.selection = .agent("a")
            try render(ContentView(), size: CGSize(width: 1100, height: 640), dark: dark, store: opened,
                       to: folder.appendingPathComponent("search-opened-\(look).png"))

            let palette = try store()
            palette.selection = .agent("b")
            try render(ZStack { ContentView(); CommandPalette(query: "safari") }, size: CGSize(width: 1100, height: 640), dark: dark,
                       store: palette, to: folder.appendingPathComponent("search-palette-\(look).png"))
        }
    }

    private func render<V: View>(_ view: V, size: CGSize, dark: Bool, store: AgentsStore, to url: URL) throws {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.titled, .closable, .resizable, .miniaturizable],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentView = NSHostingView(rootView: view.environment(store).frame(width: size.width, height: size.height))
        window.setContentSize(size)
        window.orderFrontRegardless()
        RunLoop.main.run(until: Date().addingTimeInterval(1.6))
        let content = try XCTUnwrap(window.contentView)
        let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
        content.cacheDisplay(in: content.bounds, to: bitmap)
        try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: url)
        // Put away rather than closed: closing the last titled window ends
        // the app the tests run in, and the next test with it.
        window.orderOut(nil)
    }
}
