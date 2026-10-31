import AppKit
import SwiftUI
import XCTest
@testable import Agentman

@MainActor
final class SidebarSectionsTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)

    private func session(_ id: String, _ state: SessionState, minutesAgo: Double, asking: Bool = false) -> Session {
        var session = Session(id: id, kind: "claude", name: id, cwd: "/work/\(id)", state: state)
        session.lastActivityAt = Int64((now.timeIntervalSince1970 - minutesAgo * 60) * 1000)
        if asking { session.question = Question(prompt: "Push to main?", options: []) }
        return session
    }

    private func names(_ sections: SidebarSections) -> [String] {
        ["pinned:" + sections.pinned.map(\.id).joined(separator: ","),
         "active:" + sections.active.map(\.id).joined(separator: ","),
         "working:" + sections.working.map(\.id).joined(separator: ","),
         "archived:" + sections.archived.map(\.id).joined(separator: ",")]
    }

    // What came back to you is on top, what asks for you first; what is
    // still running folds under Working; what is done, under Archived.
    func testEachAgentHasItsSection() {
        let sessions = [
            session("idle-recent", .idle, minutesAgo: 5),
            session("idle-older", .idle, minutesAgo: 90),
            session("asking", .idle, minutesAgo: 30, asking: true),
            session("waiting", .waitingInput, minutesAgo: 60),
            session("busy", .busy, minutesAgo: 1),
            session("busy-older", .busy, minutesAgo: 20),
            session("ended", .ended, minutesAgo: 10),
            session("quiet", .idle, minutesAgo: 4 * 24 * 60),
        ]
        XCTAssertEqual(names(SidebarSections.make(sessions, choices: .init(), now: now)), [
            "pinned:",
            "active:asking,waiting,idle-recent,idle-older",
            "working:busy,busy-older",
            "archived:ended,quiet",
        ])
    }

    // Archived by hand until it does something again; brought back by hand,
    // kept back until it does something and the usual rules resume.
    func testArchivingByHandAndBringingBack() {
        var choices = SidebarSections.Choices()
        var done = session("done", .idle, minutesAgo: 10)
        choices.archived["done"] = now
        XCTAssertEqual(names(SidebarSections.make([done], choices: choices, now: now))[3], "archived:done")

        // It works again: back where it belongs.
        done.lastActivityAt = Int64(now.addingTimeInterval(60).timeIntervalSince1970 * 1000)
        done.state = .busy
        XCTAssertEqual(names(SidebarSections.make([done], choices: choices, now: now))[2], "working:done")

        // An ended agent brought back stays in the active list.
        let ended = session("ended", .ended, minutesAgo: 10)
        choices.unarchived["ended"] = now
        XCTAssertEqual(names(SidebarSections.make([ended], choices: choices, now: now))[1], "active:ended")

        // Asking you something is never archived, even archived by hand.
        let asking = session("asking", .waitingInput, minutesAgo: 1)
        choices.archived["asking"] = now
        XCTAssertEqual(names(SidebarSections.make([asking], choices: choices, now: now))[1], "active:asking")
    }

    // Pins keep the order they were pinned in, whatever the agents do.
    func testPinsStayOnTopInTheirOrder() {
        let sessions = [session("a", .idle, minutesAgo: 1), session("b", .busy, minutesAgo: 2), session("c", .idle, minutesAgo: 3)]
        let sections = SidebarSections.make(sessions, choices: .init(pinned: ["c", "b"]), now: now)
        XCTAssertEqual(names(sections), ["pinned:c,b", "active:a", "working:", "archived:"])
        XCTAssertEqual(sections.shown(working: false, archived: false).map(\.id), ["c", "b", "a"])
    }

    // The store's actions: pinning, archiving (which unpins), bringing back.
    func testTheStoresChoices() throws {
        let key = "sidebarChoices"
        let was = UserDefaults.standard.object(forKey: key)
        defer { if let was { UserDefaults.standard.set(was, forKey: key) } else { UserDefaults.standard.removeObject(forKey: key) } }
        UserDefaults.standard.removeObject(forKey: key)
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-sidebar.sock"))
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data("""
        {"type":"sessions","sessions":[{"id":"a","kind":"claude","name":"A","cwd":"/work/a","state":"idle","lastActivityAt":\(Int64(Date().timeIntervalSince1970 * 1000))}]}
        """.utf8)))
        store.pin("a")
        XCTAssertEqual(store.sidebar.pinned.map(\.id), ["a"])
        store.archive("a")
        XCTAssertFalse(store.isPinned("a"), "archiving takes the pin")
        XCTAssertEqual(store.sidebar.archived.map(\.id), ["a"])
        store.unarchive("a")
        XCTAssertEqual(store.sidebar.active.map(\.id), ["a"])
        // Kept for the next launch.
        let saved = try XCTUnwrap(UserDefaults.standard.dictionary(forKey: key))
        XCTAssertNotNil((saved["unarchived"] as? [String: Double])?["a"])
    }

    // For screenshots: the main window with agents in every section, light
    // and dark, then with one pinned and Archived open. Runs with
    // TEST_RUNNER_AGENTMAN_SHOTS=<folder>.
    func testRenderTheSidebar() async throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        func folder(_ name: String, branch: String?) throws -> String {
            let url = root.appendingPathComponent(name)
            try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
            if let branch {
                let git = Process()
                git.executableURL = URL(fileURLWithPath: "/usr/bin/git")
                git.arguments = ["-C", url.path, "init", "-q", "-b", branch]
                try git.run()
                git.waitUntilExit()
            }
            return url.resolvingSymlinksInPath().path
        }
        let api = try folder("api", branch: "feat/rate-limit")
        let site = try folder("site", branch: "fix/safari-sign-in")
        let docs = try folder("docs", branch: nil)
        let old = try folder("billing", branch: "main")
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        let minute: Int64 = 60_000
        let sessions = """
        {"type":"sessions","sessions":[
         {"id":"shot-a","kind":"claude","name":"Fix Safari sign-in","cwd":"\(site)","state":"busy","inject":"tmux","lastActivityAt":\(now - minute),"servers":[{"port":3000,"command":"node","title":"Site"}]},
         {"id":"shot-b","kind":"codex","name":"Rate limit the uploads","cwd":"\(api)","state":"waiting_input","inject":"tmux","lastActivityAt":\(now - 10 * minute),"question":{"prompt":"Push to main?","title":"Push to main?","options":[]}},
         {"id":"shot-c","kind":"opencode","name":"Write the changelog","cwd":"\(site)","state":"idle","inject":"api","lastActivityAt":\(now - 60 * minute)},
         {"id":"shot-d","kind":"claude","name":"Tidy the setup guide","cwd":"\(docs)","state":"idle","inject":"tmux","lastActivityAt":\(now - 5 * minute)},
         {"id":"shot-e","kind":"codex","name":"Add the export button","cwd":"\(api)","state":"ended","inject":"none","lastActivityAt":\(now - 2 * 1440 * minute)},
         {"id":"shot-f","kind":"claude","name":"Move invoices to Stripe","cwd":"\(old)","state":"idle","inject":"tmux","lastActivityAt":\(now - 5 * 1440 * minute)},
         {"id":"shot-g","kind":"claude","name":"Review the API docs","cwd":"\(api)","state":"idle","inject":"tmux","lastActivityAt":\(now - 25 * minute)}
        ]}
        """
        let keys = ["sidebarChoices", "sidebarWorkingOpen", "sidebarArchivedOpen"]
        let saved = keys.map { UserDefaults.standard.object(forKey: $0) }
        defer {
            for (key, value) in zip(keys, saved) {
                if let value { UserDefaults.standard.set(value, forKey: key) } else { UserDefaults.standard.removeObject(forKey: key) }
            }
        }
        // One window throughout, put away at the end rather than closed:
        // closing it ends the app the tests run in.
        let size = CGSize(width: 1040, height: 700)
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.titled, .closable, .resizable],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        defer { window.orderOut(nil) }
        for (name, archivedOpen, pin) in [("sidebar", false, false), ("sidebar-open", true, true)] {
            for (look, appearance) in [("light", NSAppearance.Name.aqua), ("dark", .darkAqua)] {
                UserDefaults.standard.removeObject(forKey: "sidebarChoices")
                UserDefaults.standard.set(true, forKey: "sidebarWorkingOpen")
                UserDefaults.standard.set(archivedOpen, forKey: "sidebarArchivedOpen")
                let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-shots.sock"))
                store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(sessions.utf8)))
                if pin { store.pin("shot-g") }
                await store.readBranches()
                store.selection = .agent("shot-a")
                window.appearance = NSAppearance(named: appearance)
                window.contentView = NSHostingView(rootView: ContentView().environment(store).frame(width: size.width, height: size.height))
                window.setContentSize(size)
                window.orderFrontRegardless()
                try await Task.sleep(for: .seconds(1.2))
                let content = try XCTUnwrap(window.contentView)
                let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
                content.cacheDisplay(in: content.bounds, to: bitmap)
                try XCTUnwrap(bitmap.representation(using: .png, properties: [:]))
                    .write(to: URL(fileURLWithPath: out).appendingPathComponent("\(name)-\(look).png"))
            }
        }
    }
}

