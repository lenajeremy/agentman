import AppKit
import SwiftUI
import XCTest
@testable import Agentman

@MainActor
final class BoardTests: XCTestCase {
    /// Windows are put away and kept: one closed, or freed, as the last the
    /// test app has can end the app and the run with it.
    private static var kept: [NSWindow] = []

    private func session(_ id: String, _ state: SessionState, question: Question? = nil, active: Date = .now, cwd: String = "/work/api") -> Session {
        Session(id: id, kind: "claude", name: "Agent \(id)", cwd: cwd, state: state,
                lastActivityAt: Int64(active.timeIntervalSince1970 * 1000), question: question)
    }

    private func sort(_ sessions: [Session], hidden: Set<String> = [], waiting: Set<String> = [], folder: String? = nil,
                      choices: SidebarSections.Choices = .init()) -> BoardSections {
        BoardSections.make(sessions, choices: choices, hidden: hidden, waiting: waiting, folder: folder)
    }

    private let question = Question(prompt: "Run it?", options: [QuestionOption(key: "1", label: "Yes")])

    func testAnAgentIsInTheColumnForWhatItWants() {
        let sections = sort([session("a", .busy), session("b", .waitingInput), session("c", .idle), session("d", .idle, question: question)])
        XCTAssertEqual(Set(sections.needsYou.map(\.id)), ["b", "d"])
        XCTAssertEqual(sections.working.map(\.id), ["a"])
        XCTAssertEqual(sections.idle.map(\.id), ["c"])
    }

    func testAgentmansCardPutsAnAgentInNeedsYou() {
        let sections = sort([session("a", .idle)], waiting: ["a"])
        XCTAssertEqual(sections.needsYou.map(\.id), ["a"])
        XCTAssertTrue(sections.idle.isEmpty)
    }

    func testEndedAndArchivedAgentsAreNotOnTheBoard() {
        let old = Date.now.addingTimeInterval(-5 * 24 * 3600)
        let sections = sort([session("a", .ended), session("b", .idle, active: old)])
        XCTAssertEqual(sections.shown, 0)
        XCTAssertTrue(sections.hidden.isEmpty)
    }

    func testHidingTakesAWorkingOrIdleAgentOffButNeverOneThatNeedsYou() {
        let sections = sort([session("a", .busy), session("b", .idle), session("c", .waitingInput)], hidden: ["a", "b", "c"])
        XCTAssertEqual(Set(sections.hidden.map(\.id)), ["a", "b"])
        XCTAssertEqual(sections.needsYou.map(\.id), ["c"])
        XCTAssertEqual(sections.working.count + sections.idle.count, 0)
    }

    func testAFolderNarrowsTheBoard() {
        let sections = sort([session("a", .busy, cwd: "/work/api"), session("b", .busy, cwd: "/work/site")], folder: "/work/site")
        XCTAssertEqual(sections.working.map(\.id), ["b"])
    }

    func testWorkingCardsKeepTheirPlaceAsTheyTick() {
        var first = session("a", .busy), second = session("b", .busy)
        first.startedAt = 200
        second.startedAt = 100
        second.lastActivityAt = (first.lastActivityAt ?? 0) + 5000
        XCTAssertEqual(sort([second, first]).working.map(\.id), ["a", "b"])
    }

    func testTheBriefSaysWhatTheColumnsShow() {
        let waiting = sort([session("a", .waitingInput), session("b", .waitingInput), session("c", .busy)])
        XCTAssertEqual(waiting.headline(connected: true), "2 agents are waiting on you. The rest are fine.")
        XCTAssertEqual(sort([session("a", .waitingInput)]).headline(connected: true), "1 agent is waiting on you.")
        XCTAssertEqual(sort([session("a", .busy)]).headline(connected: true), "Nothing needs you. 1 agent is working.")
        XCTAssertEqual(sort([session("a", .idle)]).headline(connected: true), "All quiet. Nothing needs you.")
        XCTAssertEqual(sort([]).headline(connected: true), "No agents running.")
        // Quiet is only known while connected.
        XCTAssertEqual(sort([]).headline(connected: false), "Waiting to reach your agents.")
        XCTAssertEqual(sort([session("a", .busy), session("b", .idle)]).detail, "Agent a is working, 1 is idle.")
    }

    func testAgentmansAnswerOutlinesTheAgentsItIsAbout() {
        let entries = [
            ManagerEntry(id: "1", role: "assistant", text: "Agent b did something earlier.", ts: 1),
            ManagerEntry(id: "2", role: "user", text: "What's left?", ts: 2),
            ManagerEntry(id: "3", role: "activity", text: "Read", ts: 3, sessionId: "a"),
            ManagerEntry(id: "4", role: "assistant", text: "Only agent c is left.", ts: 4)
        ]
        XCTAssertEqual(BoardSections.mentioned(in: entries, sessions: [session("a", .idle), session("b", .idle), session("c", .idle)]), ["a", "c"])
    }

    func testAHiddenAgentComesBackForGoodWhenItNeedsYou() throws {
        UserDefaults.standard.removeObject(forKey: "boardHidden")
        defer { UserDefaults.standard.removeObject(forKey: "boardHidden") }
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-board.sock"))
        var list = DaemonEvent(type: "sessions")
        list.sessions = [session("a", .busy)]
        store.apply(list)
        store.hideFromBoard("a")
        XCTAssertEqual(store.board.hidden.map(\.id), ["a"])

        var update = DaemonEvent(type: "session_update")
        update.session = session("a", .waitingInput)
        store.apply(update)
        XCTAssertEqual(store.board.needsYou.map(\.id), ["a"])

        update.session = session("a", .busy)
        store.apply(update)
        XCTAssertEqual(store.board.working.map(\.id), ["a"])
        XCTAssertTrue(store.boardHidden.isEmpty)
    }

    func testACheckOfAnAgentsWorkReachesItsCardAndCanBeDropped() {
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-board.sock"))
        var event = DaemonEvent(type: "manager_verification")
        event.verification = Verification(sessionId: "a", state: "failed", summary: "Tests fail", updatedAt: 1)
        store.apply(event)
        XCTAssertEqual(store.manager.verifications["a"]?.state, "failed")
        event.verification = Verification(sessionId: "a", state: "none", updatedAt: 2)
        store.apply(event)
        XCTAssertNil(store.manager.verifications["a"])
    }

    func testTrustIsTheClosestFoldersOrFollowsTheSwitch() {
        var settings = ManagerSettings(chatModel: "m", briefModel: "m", effort: "low", briefs: true, autoApprove: true, style: "", instructions: "", keySet: true)
        settings.trust = [FolderTrust(folder: "/w", level: "watch"), FolderTrust(folder: "/w/api", level: "suggest")]
        XCTAssertEqual(FolderTrust.level(for: "/w/api/cmd", in: settings), "suggest")
        XCTAssertEqual(FolderTrust.level(for: "/w/site", in: settings), "watch")
        XCTAssertEqual(FolderTrust.level(for: "/w2", in: settings), "handle", "not a folder inside /w")
        settings.autoApprove = false
        XCTAssertEqual(FolderTrust.level(for: "/elsewhere", in: settings), "watch")
    }

    func testAnOfferIsAQuestionTheBoardCanShow() {
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-board.sock"))
        var event = DaemonEvent(type: "manager_entry")
        let card = ManagerCard(id: "c", kind: "offer", state: "open", sessionId: "a", agentName: "Agentman", title: "Always allow?",
                               options: [QuestionOption(key: "here", label: "Always allow in api")])
        event.entry = ManagerEntry(id: "c", role: "card", text: "Always allow?", ts: 1, card: card)
        store.apply(event)
        XCTAssertEqual(store.manager.offers.map(\.id), ["c"])
        XCTAssertEqual(store.manager.offers.first?.question?.options.first?.key, "here")
    }

    func testTheBoardsSidebarGoesWithTheNextVisit() {
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-board.sock"))
        store.selection = .manager
        store.boardShowsSidebar = true
        store.selection = .usage
        store.selection = .manager
        XCTAssertFalse(store.boardShowsSidebar)
    }

    func testOnlyTheOpenConversationCountsAsReadingAgentman() {
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-board.sock"))
        store.selection = .manager
        XCTAssertFalse(store.viewingManager)
        store.boardChatOpen = true
        XCTAssertTrue(store.viewingManager)
        store.selection = .usage
        XCTAssertFalse(store.viewingManager)
        store.open(URL(string: "agentman://home/conversation")!)
        XCTAssertEqual(store.selection, .manager)
        XCTAssertTrue(store.boardChatOpen)
    }

    private func tool(_ id: String, _ name: String, _ title: String?, summary: String? = nil, status: String = "ok", text: String? = nil, ts: Int64) -> Message {
        Message(id: id, sessionId: "a", role: .tool, ts: ts, text: text, tool: ToolCall(name: name, summary: summary, status: status, title: title))
    }

    func testADigestReadsTheTurnTheAgentIsOn() {
        let digest = BoardDigest.make([
            tool("0", "Read", "Read the old turn", ts: 1),
            Message(id: "1", sessionId: "a", role: .user, ts: 2, text: "Go"),
            tool("2", "Read", "Read the handler", ts: 3),
            tool("3", "Edit", "Added a limiter", ts: 4),
            tool("4", "TodoWrite", nil, text: "✔ One\n◐ Two\n○ Three", ts: 5),
            tool("5", "Bash", "Running the tests", summary: "go test ./...", status: "running", ts: 6)
        ])
        XCTAssertEqual(digest.steps.map(\.text), ["Read the handler", "Added a limiter", "Running the tests"])
        XCTAssertEqual(digest.steps.last?.state, .running)
        XCTAssertEqual(digest.command, "go test ./...")
        XCTAssertEqual(digest.progress, BoardDigest.Progress(done: 1, total: 3, now: "Two"))
    }

    func testNoChecklistMeansNoBar() {
        XCTAssertNil(BoardDigest.checklist("Todos have been modified successfully."))
        XCTAssertNil(BoardDigest.checklist("✔ Only one"))
        XCTAssertEqual(BoardDigest.checklist("✔ A\n✕ Dropped\n○ B"), BoardDigest.Progress(done: 1, total: 2))
    }

    func testWhatAnAgentSaidIsItsFirstSentence() {
        XCTAssertEqual(BoardDigest.firstLine("# Done\n\n```\ncode\n```\n- Apple Pay is in the checkout.\n- More"), "Apple Pay is in the checkout.")
        XCTAssertNil(BoardDigest.firstLine("```\ncode\n```"))
    }

    // MARK: - Screenshots

    private func sampleStore() throws -> AgentsStore {
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        let sessions = """
        {"type":"sessions","sessions":[
         {"id":"m","kind":"codex","name":"Migrate orders to Postgres 17","cwd":"/work/api","state":"waiting_input","inject":"tmux","lastActivityAt":\(now - 120_000),
          "question":{"id":"q1","title":"Wants to push to main","prompt":"Run git push origin main?","detail":"git push origin main","options":[{"key":"1","label":"Yes"},{"key":"2","label":"Yes, and don't ask again"},{"key":"3","label":"No, tell Codex what to do instead","withText":true}]}},
         {"id":"b","kind":"claude","name":"Fix cart badges","cwd":"/work/storefront","state":"waiting_input","inject":"tmux","lastActivityAt":\(now - 300_000),
          "question":{"id":"q2","title":"Which badge style should the cart use?","prompt":"","options":[{"key":"1","label":"A count in a dot"},{"key":"2","label":"A count beside the icon"},{"key":"3","label":"A dot only, no count"}]}},
         {"id":"t","kind":"claude","name":"Throttle webhooks","cwd":"/work/api","state":"busy","inject":"tmux","startedAt":\(now - 900_000),"lastActivityAt":\(now - 2_000)},
         {"id":"d","kind":"antigravity","name":"Refresh the API reference","cwd":"/work/docs","state":"busy","inject":"tmux","startedAt":\(now - 1_900_000),"lastActivityAt":\(now - 4_000)},
         {"id":"p","kind":"claude","name":"Add Apple Pay to checkout","cwd":"/work/storefront","state":"idle","inject":"tmux","lastActivityAt":\(now - 120_000),"servers":[{"port":3000,"command":"node","title":"Site"}]},
         {"id":"o","kind":"opencode","name":"Onboarding screens for v2","cwd":"/work/ios","state":"idle","inject":"api","lastActivityAt":\(now - 3_100_000)}
        ]}
        """
        let working = """
        {"type":"messages","sessionId":"t","messages":[
         {"id":"1","sessionId":"t","role":"user","ts":\(now - 720_000),"text":"Throttle the webhooks"},
         {"id":"2","sessionId":"t","role":"tool","ts":\(now - 700_000),"tool":{"name":"Read","summary":"webhooks/handler.go","status":"ok","title":"Read the webhook handler"}},
         {"id":"3","sessionId":"t","role":"tool","ts":\(now - 500_000),"tool":{"name":"Edit","summary":"webhooks/limit.go","status":"ok","title":"Added a limiter: 120 a minute"}},
         {"id":"4","sessionId":"t","role":"tool","ts":\(now - 20_000),"tool":{"name":"Bash","summary":"go test ./webhooks/...","status":"running","title":"Running the webhook tests"}}
        ]}
        """
        let docs = """
        {"type":"messages","sessionId":"d","messages":[
         {"id":"1","sessionId":"d","role":"user","ts":\(now - 1_860_000),"text":"Refresh the API reference"},
         {"id":"2","sessionId":"d","role":"tool","ts":\(now - 40_000),"text":"✔ Authentication\\n✔ Webhooks\\n✔ Orders\\n◐ Writing the refunds page\\n○ Customers","tool":{"name":"TodoWrite","status":"ok"}}
        ]}
        """
        let idle = """
        {"type":"messages","sessionId":"p","messages":[
         {"id":"1","sessionId":"p","role":"assistant","ts":\(now - 120_000),"text":"Apple Pay is in the checkout sheet. 52 tests pass."}
        ]}
        """
        let idle2 = """
        {"type":"messages","sessionId":"o","messages":[
         {"id":"1","sessionId":"o","role":"assistant","ts":\(now - 3_100_000),"text":"Three screens built; ready for the plan review."}
        ]}
        """
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-board.sock"))
        for json in [sessions, working, docs, idle, idle2] {
            store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(json.utf8)))
        }
        store.statusChanged(.connected)
        store.selection = .manager
        var verified = DaemonEvent(type: "manager_verification")
        verified.verification = Verification(sessionId: "p", state: "verified", summary: "Apple Pay shows in the checkout sheet, as asked. 52 tests pass.", updatedAt: now)
        store.apply(verified)
        var concerns = DaemonEvent(type: "manager_verification")
        concerns.verification = Verification(sessionId: "o", state: "concerns", summary: "Only two of the three screens are wired up.",
                                             issues: ["WelcomeView never opens PermissionsView"], updatedAt: now)
        store.apply(concerns)
        var offer = DaemonEvent(type: "manager_entry")
        offer.entry = ManagerEntry(id: "offer", role: "card", text: "Always allow?", ts: now, card: ManagerCard(
            id: "offer", kind: "offer", state: "open", sessionId: "t", agentName: "Agentman", title: "You've approved `go vet*` 3 times in api. Always allow it?",
            options: [QuestionOption(key: "here", label: "Always allow in api"), QuestionOption(key: "everywhere", label: "Always allow everywhere"), QuestionOption(key: "no", label: "Keep asking me")]))
        store.apply(offer)
        var handled = DaemonEvent(type: "manager_entry")
        handled.entry = ManagerEntry(approved: ApprovedRequest(kind: "command", target: "go test ./..."), id: "h", role: "activity", text: "Approved for api: run `go test ./...`", ts: now, tool: "approve")
        store.apply(handled)
        return store
    }

    private func render(_ store: AgentsStore, size: CGSize, dark: Bool, to url: URL) throws {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.titled, .closable, .resizable, .miniaturizable],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        Self.kept.append(window)
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentView = NSHostingView(rootView: ContentView().environment(store).frame(width: size.width, height: size.height))
        window.setContentSize(size)
        window.orderFrontRegardless()
        RunLoop.main.run(until: Date().addingTimeInterval(1.5))
        let content = try XCTUnwrap(window.contentView)
        let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
        content.cacheDisplay(in: content.bounds, to: bitmap)
        try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: url)
        window.orderOut(nil)
    }

    // MARK: - Clicks

    private func window(_ store: AgentsStore) -> NSWindow {
        let size = NSSize(width: 1280, height: 800)
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.titled, .closable, .resizable, .fullSizeContentView],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        let hosting = NSHostingView(rootView: BoardView().environment(store).frame(width: size.width, height: size.height))
        hosting.sizingOptions = []
        window.contentView = hosting
        window.setContentSize(size)
        Self.kept.append(window)
        window.orderFrontRegardless()
        window.makeKey()
        RunLoop.main.run(until: Date().addingTimeInterval(1.2))
        return window
    }

    /// A real click, from the top left of the window's content.
    private func click(_ window: NSWindow, x: CGFloat, fromTop y: CGFloat) {
        let point = NSPoint(x: x, y: (window.contentView?.bounds.height ?? 0) - y)
        for type in [NSEvent.EventType.leftMouseDown, .leftMouseUp] {
            let event = NSEvent.mouseEvent(with: type, location: point, modifierFlags: [], timestamp: ProcessInfo.processInfo.systemUptime,
                                           windowNumber: window.windowNumber, context: nil, eventNumber: 1, clickCount: 1, pressure: 1)
            window.sendEvent(XCTUnwrapped(event))
            RunLoop.main.run(until: Date().addingTimeInterval(0.15))
        }
        RunLoop.main.run(until: Date().addingTimeInterval(0.3))
    }

    private func XCTUnwrapped(_ event: NSEvent?) -> NSEvent { event! }

    private func views<T: NSView>(_ type: T.Type, in view: NSView) -> [T] {
        ((view as? T).map { [$0] } ?? []) + view.subviews.flatMap { views(type, in: $0) }
    }

    /// Runs with TEST_RUNNER_AGENTMAN_CLICKS=1, on its own: a window taking
    /// real clicks can end the test app part way through a full run.
    func testActingOnACardDoesNotOpenIt() throws {
        guard ProcessInfo.processInfo.environment["AGENTMAN_CLICKS"] != nil else { throw XCTSkip("takes real clicks; run on its own") }
        let store = try sampleStore()
        let window = window(store)
        defer { window.orderOut(nil) }
        // Down the Needs you column to its options, going back to the Board
        // from any part of the card that opens the agent.
        for y in stride(from: 120 as CGFloat, to: 420, by: 8) where store.actionError["m"] == nil {
            store.selection = .manager
            click(window, x: 200, fromTop: y)
        }
        XCTAssertNotNil(store.actionError["m"], "the answer was tried, and failed for want of a daemon")
        XCTAssertEqual(store.selection, .manager)

        var update = DaemonEvent(type: "session_update")
        update.session = Session(id: "m", kind: "codex", name: "Migrate orders", cwd: "/work/api", state: .waitingInput, inject: "tmux",
                                 lastActivityAt: Int64(Date().timeIntervalSince1970 * 1000))
        store.apply(update)
        RunLoop.main.run(until: Date().addingTimeInterval(0.6))
        let host = try XCTUnwrap(window.contentView)
        let field = try XCTUnwrap(views(NSTextField.self, in: host).first { $0.isEditable && $0.placeholderString?.hasPrefix("Reply to") == true })
        let center = field.convert(NSPoint(x: field.bounds.midX, y: field.bounds.midY), to: nil)
        click(window, x: center.x, fromTop: host.bounds.height - center.y)
        XCTAssertEqual(store.selection, .manager)
        XCTAssertTrue((window.firstResponder as? NSTextView)?.delegate === field, "focus: \(String(describing: window.firstResponder))")
    }

    func testRenderTheBoard() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let shots = URL(fileURLWithPath: out)
        try FileManager.default.createDirectory(at: shots, withIntermediateDirectories: true)
        let store = try sampleStore()
        store.hideFromBoard("nobody")
        try render(store, size: CGSize(width: 1280, height: 800), dark: false, to: shots.appendingPathComponent("board-1280.png"))
        try render(store, size: CGSize(width: 1560, height: 980), dark: false, to: shots.appendingPathComponent("board-1560.png"))
        try render(store, size: CGSize(width: 1040, height: 640), dark: false, to: shots.appendingPathComponent("board-min.png"))
        try render(store, size: CGSize(width: 1560, height: 980), dark: true, to: shots.appendingPathComponent("board-dark.png"))
        try render(store, size: CGSize(width: 1920, height: 1100), dark: false, to: shots.appendingPathComponent("board-wide.png"))

        var user = DaemonEvent(type: "manager_entry")
        user.entry = ManagerEntry(id: "u", role: "user", text: "What's left before the storefront can ship?", ts: 10)
        store.apply(user)
        var reply = DaemonEvent(type: "manager_entry")
        reply.entry = ManagerEntry(id: "r", role: "assistant", text: "Two things. **Fix cart badges** is waiting on you to pick a style, and **Add Apple Pay to checkout** is done with 5 files not yet committed.", ts: 11)
        store.apply(reply)
        store.boardChatOpen = true
        try render(store, size: CGSize(width: 1280, height: 800), dark: false, to: shots.appendingPathComponent("board-chat-1280.png"))
        try render(store, size: CGSize(width: 1560, height: 980), dark: false, to: shots.appendingPathComponent("board-chat-1560.png"))
        UserDefaults.standard.removeObject(forKey: "boardHidden")

        let empty = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-board.sock"))
        var none = DaemonEvent(type: "sessions")
        none.sessions = []
        empty.apply(none)
        empty.statusChanged(.connected)
        empty.selection = .manager
        try render(empty, size: CGSize(width: 1280, height: 800), dark: false, to: shots.appendingPathComponent("board-empty.png"))
    }
}
