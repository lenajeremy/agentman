import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// Moving a session between Agentman and a terminal: the "…" menu's entry
/// for each kind of session, and the rule behind it.
@MainActor
final class HandoffShotsTests: XCTestCase {
    private func session(_ json: String) throws -> Session {
        try JSONDecoder().decode(Session.self, from: Data(json.utf8))
    }

    // Open in Terminal for a session Agentman runs itself, Take Over (which
    // asks first) for one in Agentman's pane, neither mid-turn or while a
    // move is under way, and nothing for a session that cannot move.
    func testTheMenuOffersTheMoveBetweenTurns() throws {
        let direct = try session(#"{"id":"a","kind":"kiro","name":"website","cwd":"/tmp","state":"idle","inject":"api","handoff":"terminal"}"#)
        let pane = try session(#"{"id":"b","kind":"kiro","name":"api","cwd":"/tmp","state":"idle","inject":"tmux","handoff":"agentman"}"#)
        let working = try session(#"{"id":"c","kind":"kiro","name":"website","cwd":"/tmp","state":"busy","inject":"api","handoff":"terminal"}"#)
        let waiting = try session(#"{"id":"e","kind":"kiro","name":"api","cwd":"/tmp","state":"waiting_input","inject":"tmux","handoff":"agentman","question":{"id":"q","prompt":"Run ls?","options":[]}}"#)
        let own = try session(#"{"id":"d","kind":"kiro","name":"own","cwd":"/tmp","state":"idle","inject":"none"}"#)

        let open = try XCTUnwrap(AgentThreadView.handoffItem(direct, moving: false))
        XCTAssertEqual(open.title, "Open in Terminal")
        XCTAssertTrue(open.enabled)
        XCTAssertFalse(open.confirms)
        let take = try XCTUnwrap(AgentThreadView.handoffItem(pane, moving: false))
        XCTAssertEqual(take.title, "Take Over from Terminal…")
        XCTAssertTrue(take.enabled)
        XCTAssertTrue(take.confirms)
        let later = try XCTUnwrap(AgentThreadView.handoffItem(working, moving: false))
        XCTAssertEqual(later.title, "Open in Terminal After This Turn")
        XCTAssertFalse(later.enabled)
        let answering = try XCTUnwrap(AgentThreadView.handoffItem(waiting, moving: false))
        XCTAssertEqual(answering.title, "Take Over After This Turn")
        XCTAssertFalse(answering.enabled)
        XCTAssertEqual(AgentThreadView.handoffItem(direct, moving: true)?.enabled, false)
        XCTAssertNil(AgentThreadView.handoffItem(own, moving: false))
    }

    // A session that changed only how it is reached, or where it can move,
    // is a changed session: the thread redraws its menu and composer.
    func testAMoveIsAChange() throws {
        let before = try session(#"{"id":"a","kind":"kiro","name":"website","cwd":"/tmp","state":"idle","inject":"api","handoff":"terminal"}"#)
        let after = try session(#"{"id":"a","kind":"kiro","name":"website","cwd":"/tmp","state":"idle","inject":"tmux","handoff":"agentman"}"#)
        XCTAssertNotEqual(before, after)
    }

    /// The thread with the "…" menu as it opens, for review. macOS draws
    /// the menu itself and an offscreen render never shows it, so it is drawn
    /// here from the same items. Runs with TEST_RUNNER_AGENTMAN_SHOTS=<folder>.
    func testRenderTheMoveInTheMenu() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        let website = root.appendingPathComponent("website").path, api = root.appendingPathComponent("api-server").path
        for folder in [website, api] { try FileManager.default.createDirectory(atPath: folder, withIntermediateDirectories: true) }
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        func store(websiteState: String) throws -> AgentsStore {
            let sessions = """
            {"type":"sessions","sessions":[
             {"id":"kiro:w","kind":"kiro","name":"website","cwd":"\(website)","state":"\(websiteState)","inject":"api","handoff":"terminal","lastActivityAt":\(now - 60_000),"model":"auto","mode":"kiro_default"},
             {"id":"kiro:a","kind":"kiro","name":"api-server","cwd":"\(api)","state":"idle","inject":"tmux","handoff":"agentman","lastActivityAt":\(now - 900_000),"model":"auto","mode":"kiro_default"}
            ]}
            """
            let messages = """
            {"type":"messages","sessionId":"kiro:w","messages":[
             {"id":"1","sessionId":"kiro:w","role":"user","ts":\(now - 300_000),"text":"Make the hero section load faster on mobile"},
             {"id":"2","sessionId":"kiro:w","role":"tool","ts":\(now - 290_000),"tool":{"name":"read","summary":"src/Hero.tsx","status":"ok"}},
             {"id":"3","sessionId":"kiro:w","role":"tool","ts":\(now - 280_000),"tool":{"name":"shell","summary":"npx lighthouse http://localhost:3000 --preset=mobile","status":"ok"}},
             {"id":"4","sessionId":"kiro:w","role":"assistant","ts":\(now - 240_000),"text":"The hero image is a 2.4 MB PNG loaded before anything else. I converted it to AVIF at three sizes and gave it `fetchpriority=\\"high\\"` with a blurred placeholder.\\n\\n- **Largest paint on mobile:** 4.1 s → 1.6 s\\n- **Hero image:** 2.4 MB → 180 KB"}
            ]}
            """
            let paneMessages = """
            {"type":"messages","sessionId":"kiro:a","messages":[
             {"id":"5","sessionId":"kiro:a","role":"user","ts":\(now - 900_000),"text":"Rate limit the uploads endpoint to 10 a minute per user"},
             {"id":"6","sessionId":"kiro:a","role":"tool","ts":\(now - 880_000),"tool":{"name":"write","summary":"routes/uploads.ts","status":"ok"}},
             {"id":"7","sessionId":"kiro:a","role":"tool","ts":\(now - 870_000),"tool":{"name":"shell","summary":"npm test -- uploads","status":"ok"}},
             {"id":"8","sessionId":"kiro:a","role":"assistant","ts":\(now - 850_000),"text":"Uploads are now limited to 10 a minute per user, with a `429` and a `Retry-After` header past that. Tests for the limit and the reset pass."}
            ]}
            """
            let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-shots.sock"))
            for json in [sessions, messages, paneMessages] {
                store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(json.utf8)))
            }
            return store
        }

        let shots = URL(fileURLWithPath: out)
        for dark in [false, true] {
            let look = dark ? "dark" : "light"
            for (name, id, state) in [("open-in-terminal", "kiro:w", "idle"), ("open-in-terminal-busy", "kiro:w", "busy"),
                                      ("take-over", "kiro:a", "idle")] {
                let store = try store(websiteState: state)
                store.selection = .agent(id)
                let session = try XCTUnwrap(store.sessions.first { $0.id == id })
                var rows: [MenuStandIn.Row] = [.item("Open in Editor", true)]
                if session.inject == "tmux" { rows.append(.item("Show the Agent's Own Terminal", true)) }
                if let move = AgentThreadView.handoffItem(session, moving: false) {
                    rows += [.divider, .item(move.title, move.enabled)]
                }
                if AgentThreadView.endable(session) { rows.append(.item("End Session…", true)) }
                let window = ContentView().overlay(alignment: .topTrailing) {
                    MenuStandIn(rows: rows).padding(.top, 36).padding(.trailing, 10)
                }
                try render(window, size: CGSize(width: 1040, height: 640), dark: dark, store: store,
                           to: shots.appendingPathComponent("\(name)-\(look).png"))
            }
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
        RunLoop.main.run(until: Date().addingTimeInterval(1.2))
        let content = try XCTUnwrap(window.contentView)
        let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
        content.cacheDisplay(in: content.bounds, to: bitmap)
        try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: url)
        window.orderOut(nil)
    }
}

/// A macOS menu as it looks open, for screenshots only.
private struct MenuStandIn: View {
    enum Row: Hashable {
        case item(String, Bool)
        case divider
    }

    let rows: [Row]
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
                switch row {
                case .item(let title, let enabled):
                    Text(title)
                        .font(.system(size: 13))
                        .foregroundStyle(enabled ? Color.primary : Color.secondary.opacity(0.6))
                        .padding(.horizontal, 12)
                        .frame(height: 22, alignment: .leading)
                case .divider:
                    Rectangle().fill(Color.primary.opacity(0.1)).frame(height: 1).padding(.horizontal, 12).padding(.vertical, 5)
                }
            }
        }
        .padding(.vertical, 5)
        .frame(minWidth: 236, alignment: .leading)
        .fixedSize()
        .background(RoundedRectangle(cornerRadius: 10, style: .continuous)
            .fill(scheme == .dark ? Color(white: 0.17) : Color(white: 0.97)))
        .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Color.primary.opacity(0.12), lineWidth: 0.5))
        .shadow(color: .black.opacity(0.22), radius: 12, y: 6)
    }
}
