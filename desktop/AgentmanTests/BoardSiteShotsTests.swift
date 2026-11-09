import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// The Board for the website, drawn from a made-up fleet of agents: full,
/// dark, colored by project, with Agentman's conversation open, the toasts
/// that come while the user is inside one agent, and two hundred agents at
/// once. Runs with TEST_RUNNER_AGENTMAN_SHOTS=<folder> set; skipped otherwise.
private struct FleetAgent: Sendable {
    var id: String
    var kind: String
    var name: String
    var folder: String
    var state = "busy"
    var ask = ""
    var said = ""
    var step = ""
    var command = ""
    var asked: String?
    var detail = ""
    var options: [String] = ["Yes", "No"]
    var minutes: Int64 = 3
}

private let fleet: [FleetAgent] = [
    FleetAgent(id: "n1", kind: "codex", name: "Migrate orders to Postgres 17", folder: "api", state: "waiting_input", asked: "Run this command?", detail: "git push origin main", minutes: 2),
    FleetAgent(id: "n2", kind: "claude", name: "Fix cart badges", folder: "storefront", state: "waiting_input", asked: "Which badge style should the cart use?", options: ["A count in a dot", "A count beside the icon", "No count"], minutes: 5),
    FleetAgent(id: "n3", kind: "opencode", name: "Rotate the webhook secret", folder: "billing", state: "waiting_input", asked: "Run this command?", detail: "./scripts/rotate-secret.sh --live", minutes: 8),
    FleetAgent(id: "n4", kind: "cursor", name: "Ship the pricing page", folder: "site", state: "waiting_input", asked: "Run this command?", detail: "npm run deploy -- --prod", minutes: 11),

    FleetAgent(id: "w1", kind: "claude", name: "Throttle webhooks", folder: "api", ask: "Throttle the webhooks to 120 a minute", step: "Running the webhook tests", command: "go test ./webhooks/...", minutes: 14),
    FleetAgent(id: "w2", kind: "antigravity", name: "Refresh the API reference", folder: "docs", ask: "Bring the API reference up to date", step: "Writing the refunds page", command: "docs/api/refunds.md", minutes: 32),
    FleetAgent(id: "w3", kind: "codex", name: "Add Apple Pay to checkout", folder: "storefront", ask: "Add Apple Pay to the checkout sheet", step: "Editing CheckoutSheet.tsx", command: "src/checkout/CheckoutSheet.tsx", minutes: 6),
    FleetAgent(id: "w4", kind: "kiro", name: "Dark mode for settings", folder: "ios", ask: "Add dark mode to the settings screens", step: "Updating the colors", command: "Settings/Theme.swift", minutes: 19),
    FleetAgent(id: "w5", kind: "claude", name: "Speed up the search index", folder: "api", ask: "Search is slow on large accounts", step: "Profiling the slow query", command: "go test -bench=Search ./search/...", minutes: 41),
    FleetAgent(id: "w6", kind: "cursor", name: "Retry failed invoices", folder: "billing", ask: "Retry failed invoices up to three times", step: "Writing the retry job", command: "jobs/retry_invoices.go", minutes: 9),
    FleetAgent(id: "w7", kind: "opencode", name: "Empty states for the inbox", folder: "ios", ask: "Design the inbox's empty states", step: "Building EmptyInboxView", command: "Inbox/EmptyInboxView.swift", minutes: 23),
    FleetAgent(id: "w8", kind: "claude", name: "Translate the checkout", folder: "storefront", ask: "Translate the checkout into French and German", step: "Adding the German strings", command: "locales/de/checkout.json", minutes: 3),
    FleetAgent(id: "w9", kind: "codex", name: "Split the auth service", folder: "api", ask: "Move sessions out of the auth service", step: "Reading the session store", command: "auth/sessions/store.go", minutes: 1),
    FleetAgent(id: "w10", kind: "antigravity", name: "Blog post for the launch", folder: "site", ask: "Draft the launch post", step: "Writing the second section", command: "blog/launch.md", minutes: 12),

    FleetAgent(id: "i1", kind: "claude", name: "Fix Safari sign-in", folder: "site", state: "idle", ask: "Sign-in fails on Safari", said: "Sign-in works on Safari 15 and later. 48 tests pass.", minutes: 4),
    FleetAgent(id: "i2", kind: "codex", name: "Refund receipts", folder: "billing", state: "idle", ask: "Email a receipt for every refund", said: "Refunds now send a receipt. 31 tests pass.", minutes: 12),
    FleetAgent(id: "i3", kind: "opencode", name: "Onboarding screens for v2", folder: "ios", state: "idle", ask: "Build the three onboarding screens", said: "All three screens are built and wired up.", minutes: 26),
    FleetAgent(id: "i4", kind: "kiro", name: "Tidy the logging", folder: "api", state: "idle", ask: "Make the logs readable", said: "Every request logs one line now.", minutes: 40),
    FleetAgent(id: "i5", kind: "claude", name: "Image sizes on the home page", folder: "site", state: "idle", ask: "The home page loads huge images", said: "Images are a third of the size. The page is 1.2s faster.", minutes: 52),
    FleetAgent(id: "i6", kind: "cursor", name: "Export orders as CSV", folder: "storefront", state: "idle", ask: "Let merchants export orders", said: "Orders export as CSV from the Orders page.", minutes: 75),
]

extension SiteShotsTests {
    private func messages(_ agent: FleetAgent, now: Int64) -> [[String: Any]] {
        var out: [[String: Any]] = []
        let start = now - agent.minutes * 60_000
        if !agent.ask.isEmpty {
            out.append(["id": "\(agent.id)-u", "sessionId": agent.id, "role": "user", "ts": start - 300_000, "text": agent.ask])
        }
        if agent.id == "w1" {
            out.append(["id": "w1-a", "sessionId": "w1", "role": "assistant", "ts": start - 200_000, "text": "The handler has no limit today, so I'll add one per merchant and cover it with tests."])
            out.append(["id": "w1-r", "sessionId": "w1", "role": "tool", "ts": start - 180_000, "tool": ["name": "Read", "summary": "webhooks/handler.go", "status": "ok", "title": "Read the webhook handler"]])
            out.append(["id": "w1-e", "sessionId": "w1", "role": "tool", "ts": start - 90_000, "tool": ["name": "Edit", "summary": "webhooks/limit.go", "status": "ok", "title": "Added a limiter: 120 a minute"]])
            out.append(["id": "w1-b", "sessionId": "w1", "role": "assistant", "ts": start - 60_000, "text": "The limiter is in. Each merchant gets 120 a minute, and anything over waits its turn instead of failing. Running the tests now."])
        }
        if !agent.step.isEmpty {
            out.append(["id": "\(agent.id)-t", "sessionId": agent.id, "role": "tool", "ts": now - 20_000,
                        "tool": ["name": "Bash", "summary": agent.command, "status": "running", "title": agent.step]])
        }
        if !agent.said.isEmpty {
            out.append(["id": "\(agent.id)-a", "sessionId": agent.id, "role": "assistant", "ts": start, "text": agent.said])
        }
        return out
    }

    private func fleetStore(_ agents: [FleetAgent]) throws -> AgentsStore {
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        let sessions: [[String: Any]] = agents.map { agent in
            var session: [String: Any] = ["id": agent.id, "kind": agent.kind, "name": agent.name, "cwd": "/work/" + agent.folder,
                                          "state": agent.state, "inject": "tmux", "startedAt": now - 1_200_000,
                                          "lastActivityAt": now - agent.minutes * 60_000]
            if let asked = agent.asked {
                let options = agent.options.enumerated().map { ["key": String($0 + 1), "label": $1] }
                session["question"] = ["id": "q-" + agent.id, "title": agent.detail.isEmpty ? asked : "Command",
                                       "prompt": agent.detail.isEmpty ? "" : asked, "detail": agent.detail, "options": options]
            }
            return session
        }
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-board-site.sock"))
        func apply(_ object: [String: Any]) throws {
            store.apply(try JSONDecoder().decode(DaemonEvent.self, from: JSONSerialization.data(withJSONObject: object)))
        }
        try apply(["type": "sessions", "sessions": sessions])
        for agent in agents {
            try apply(["type": "messages", "sessionId": agent.id, "messages": messages(agent, now: now)])
        }
        store.statusChanged(.connected)
        store.selection = .manager
        return store
    }

    /// Two hundred agents, from the fleet's own work across more projects.
    private var twoHundred: [FleetAgent] {
        let projects = ["api", "storefront", "ios", "docs", "billing", "site", "android", "search", "growth", "infra"]
        return (0..<200).map { index in
            var agent = fleet[index % fleet.count]
            agent.id = "a\(index)"
            agent.folder = projects[index % projects.count]
            return agent
        }
    }

    func testRenderBoardPage() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let shots = URL(fileURLWithPath: out)
        try FileManager.default.createDirectory(at: shots, withIntermediateDirectories: true)
        let colorBy = UserDefaults.standard.object(forKey: AgentColors.settingKey)
        defer { set(colorBy, AgentColors.settingKey) }
        set(nil, AgentColors.settingKey)
        let size = CGSize(width: 1560, height: 1000)
        for dark in [false, true] {
            try shoot(ContentView(), size: size, dark: dark, store: fleetStore(fleet), wait: 2.5,
                      to: shots.appendingPathComponent("board-fleet-\(dark ? "dark" : "light").png"))
        }

        // Colored by project, with the sidebar brought back.
        set("project", AgentColors.settingKey)
        let colored = try fleetStore(fleet)
        colored.boardShowsSidebar = true
        try shoot(ContentView(), size: size, dark: false, store: colored, wait: 2.5, to: shots.appendingPathComponent("board-color.png"))
        set(nil, AgentColors.settingKey)

        // Asking Agentman, with the cards it talks about outlined.
        let asking = try fleetStore(fleet)
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        var user = DaemonEvent(type: "manager_entry")
        user.entry = ManagerEntry(id: "u", role: "user", text: "What's left before the storefront can ship?", ts: now - 40_000)
        asking.apply(user)
        var reply = DaemonEvent(type: "manager_entry")
        reply.entry = ManagerEntry(id: "r", role: "assistant", text: "Two things. **Fix cart badges** is waiting on you to pick a style. **Add Apple Pay to checkout** is still running its tests, and should be done in a few minutes.", ts: now - 10_000)
        asking.apply(reply)
        asking.boardChatOpen = true
        try shoot(ContentView(), size: size, dark: false, store: asking, wait: 2.5, to: shots.appendingPathComponent("board-ask.png"))

        // News while the user is inside one agent.
        let inside = try fleetStore(fleet)
        inside.selection = .agent("w1")
        let card = """
        {"type":"manager_entry","entry":{"id":"card-1","role":"card","ts":\(now - 20_000),"sessionId":"n1","text":"Migrate orders to Postgres 17 wants to run git push",
         "card":{"id":"card-1","kind":"question","state":"open","sessionId":"n1","agentName":"Migrate orders to Postgres 17",
          "title":"wants to run `git push origin main`","detail":"git push origin main","questionId":"q-n1",
          "options":[{"key":"1","label":"Yes"},{"key":"2","label":"No"}]}}}
        """
        let done = """
        {"type":"manager_entry","entry":{"id":"brief-1","role":"brief","ts":\(now - 10_000),"sessionId":"i1",
         "text":"Sign-in works on Safari 15 and later. 48 tests pass."}}
        """
        for json in [card, done] {
            inside.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(json.utf8)))
        }
        XCTAssertEqual(inside.toasts.count, 2)
        try shoot(ContentView(), size: CGSize(width: 1280, height: 820), dark: false, store: inside, wait: 2.5,
                  to: shots.appendingPathComponent("board-toasts.png"))

        // Scrolled down: the headline and the columns' headings stay.
        func scrollViews(in view: NSView) -> [NSScrollView] {
            ((view as? NSScrollView).map { [$0] } ?? []) + view.subviews.flatMap(scrollViews)
        }
        try shoot(ContentView(), size: size, dark: false, store: fleetStore(fleet), wait: 2.5, prepare: { window in
            for scroll in scrollViews(in: window.contentView!) where (scroll.documentView?.frame.height ?? 0) > scroll.contentSize.height + 200 {
                let clip = scroll.contentView
                clip.scroll(to: NSPoint(x: 0, y: (scroll.documentView?.isFlipped ?? true) ? 420 : 0))
                scroll.reflectScrolledClipView(clip)
            }
        }, to: shots.appendingPathComponent("board-scrolled.png"))

        // Searching: by name, and by what was said.
        let searching = try fleetStore(fleet)
        searching.search = "checkout"
        try shoot(ContentView(), size: size, dark: false, store: searching, wait: 1.0, prepare: { _ in
            let said = searching.sessions.first { $0.id == "n2" }
            var ended = searching.sessions.first { $0.id == "i6" }
            ended?.id = "past"
            ended?.name = "Checkout copy review"
            ended?.state = .ended
            searching.messageHits = MessageSearchResults(query: "checkout", hits: [
                SearchHit(session: said, messageId: "m", role: .assistant, ts: nil, snippet: "The badge sits on the checkout button, so the count needs to stay legible.", matches: 2),
                SearchHit(session: ended, messageId: "o", role: .assistant, ts: nil, snippet: "Tightened the checkout copy and removed the second confirmation step.", matches: 1),
            ])
        }, to: shots.appendingPathComponent("board-search.png"))

        // Two hundred: how long sorting them takes, and how it looks.
        let crowd = try fleetStore(twoHundred)
        let started = Date()
        let sections = crowd.board
        let sorted = Date().timeIntervalSince(started)
        let drawStart = Date()
        try shoot(ContentView(), size: size, dark: false, store: crowd, wait: 2.5, to: shots.appendingPathComponent("board-200.png"))
        let report = "agents \(crowd.sessions.count) sorted in \(Int(sorted * 1000))ms; shot took \(Int(Date().timeIntervalSince(drawStart) * 1000))ms including a 2500ms settle\nsections \(String(describing: sections).prefix(0))"
        try report.write(to: shots.appendingPathComponent("board-200.txt"), atomically: true, encoding: .utf8)
    }
}
