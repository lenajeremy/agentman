import AppKit
import SwiftUI
import WebKit
import XCTest
@testable import Agentman

/// Every screen and state of the app as it is, for a UI audit rather than
/// for show: the main window, the editor and Settings at their smallest
/// size and at common ones, empty, crowded, waiting, failing and
/// disconnected. Each picture is listed in index.jsonl beside it.
///
/// The main window is drawn in a window with a title bar, hidden as the
/// app hides it; the editor both so ("windowed") and without one, as in
/// full screen ("fullscreen"), since the two differ at the top. Settings is
/// drawn without its title bar.
///
/// Runs with TEST_RUNNER_AGENTMAN_AUDIT=<folder> set; skipped otherwise.
extension SiteShotsTests {
    var auditFolder: URL {
        get throws {
            guard let path = ProcessInfo.processInfo.environment["AGENTMAN_AUDIT"] else { throw XCTSkip("for the audit") }
            return URL(fileURLWithPath: path)
        }
    }

    /// The main window's smallest (its frame's minimum), about 1024×700
    /// (no narrower than the minimum allows), a 13-inch screen's points,
    /// and a MacBook's full screen.
    static let mainSizes = [CGSize(width: 1040, height: 640), CGSize(width: 1040, height: 700),
                            CGSize(width: 1280, height: 800), CGSize(width: 1512, height: 982)]
    static let common = CGSize(width: 1280, height: 800)

    /// One picture, listed in the index.
    func audit<V: View>(_ view: V, area: String, screen: String, state: String, size: CGSize, dark: Bool = false,
                        store: AgentsStore, wait: TimeInterval = 1.8, titled: Bool = true, buttons: Bool = true,
                        webPages: Bool = true, overlay: (@MainActor (Bool) -> Void)? = nil,
                        prepare: (@MainActor (NSWindow) -> Void)? = nil) throws {
        let folder = try auditFolder
        let look = dark ? "dark" : "light"
        // Named for the size the window took, which its content can make
        // larger than the one asked for.
        let drawing = folder.appendingPathComponent("drawing-\(UUID().uuidString).png")
        let drawn = try shoot(view, size: size, dark: dark, store: store, wait: wait, titled: titled, buttons: buttons,
                              webPages: webPages, overlay: overlay, prepare: prepare, to: drawing)
        let name = "\(area)-\(screen)-\(state)-\(Int(drawn.width))x\(Int(drawn.height))-\(look).png"
        let target = folder.appendingPathComponent(name)
        try? FileManager.default.removeItem(at: target)
        try FileManager.default.moveItem(at: drawing, to: target)
        var entry: [String: Any] = ["file": name, "area": area, "screen": screen, "state": state,
                                    "width": Int(drawn.width), "height": Int(drawn.height), "appearance": look]
        if drawn != size { entry["requested"] = "\(Int(size.width))x\(Int(size.height))" }
        var line = try JSONSerialization.data(withJSONObject: entry, options: [.sortedKeys])
        line.append(0x0A)
        let index = folder.appendingPathComponent("index.jsonl")
        if let handle = try? FileHandle(forWritingTo: index) {
            handle.seekToEndOfFile()
            handle.write(line)
            try handle.close()
        } else {
            try line.write(to: index)
        }
    }

    /// Runs with variables set that the app reads to open a screen in a
    /// given state, as screenshots do.
    func withEnvironment<T>(_ values: [String: String], _ body: () throws -> T) rethrows -> T {
        for (key, value) in values { setenv(key, value, 1) }
        defer { for key in values.keys { unsetenv(key) } }
        return try body()
    }

    /// Sets a setting for the next windows, put back with the rest after.
    func set(_ value: Any?, _ key: String) {
        if let value { UserDefaults.standard.set(value, forKey: key) } else { UserDefaults.standard.removeObject(forKey: key) }
    }

    // MARK: - Main window

    /// The main screens at every size: Agentman's chat with cards waiting,
    /// an agent's finished thread, the same with the diff panel open, the
    /// Usage page and its Limits tab; dark at one size.
    func testAuditMainSizes() throws {
        _ = try auditFolder
        for (index, size) in Self.mainSizes.enumerated() {
            for dark in index == 2 ? [false, true] : [false] {
                let chat = try world(more: extras)
                chat.store.selection = .manager
                try audit(ContentView(), area: "main", screen: "chat", state: "cards", size: size, dark: dark, store: chat.store)

                let thread = try world(more: extras)
                thread.store.selection = .agent("a")
                try audit(ContentView(), area: "main", screen: "thread", state: "finished", size: size, dark: dark, store: thread.store)

                set(true, "rightPanelOpen")
                set("diff", "rightPanelTab")
                set("diff", "rightPanelTabs")
                let diff = try world(more: extras)
                diff.store.selection = .agent("a")
                try audit(ContentView(), area: "main", screen: "thread", state: "diffpanel", size: size, dark: dark, store: diff.store, wait: 2.4)
                set(false, "rightPanelOpen")

                set(30, "usageDays")
                set("cost", "usageMetric")
                let usage = try world(more: extras)
                usage.store.selection = .usage
                try audit(ContentView(), area: "main", screen: "usage", state: "cost30d", size: size, dark: dark, store: usage.store, wait: 2.0)

                set("limits", "usageMetric")
                let limits = try world(more: extras)
                limits.store.selection = .usage
                try audit(ContentView(), area: "main", screen: "usage", state: "limits", size: size, dark: dark, store: limits.store, wait: 2.0)
                set("cost", "usageMetric")
            }
        }
    }

    /// Agentman's chat and an agent's thread in each of their states.
    func testAuditMainStates() throws {
        _ = try auditFolder
        let size = Self.common

        // Agentman.
        let empty = try world(customize: { $0.managerEntries = [] }, more: extras)
        try audit(ContentView(), area: "main", screen: "chat", state: "empty", size: size, store: empty.store)
        let talk = try world(customize: { $0.managerEntries = Array($0.managerEntries.prefix(5)) }, more: extras)
        try audit(ContentView(), area: "main", screen: "chat", state: "conversation", size: size, store: talk.store)
        let many = try world(customize: Self.moreCards, more: extras)
        try audit(ContentView(), area: "main", screen: "chat", state: "manycards", size: size, store: many.store)
        let thinking = try world(more: extras)
        try audit(ContentView(), area: "main", screen: "chat", state: "thinking", size: size, store: thinking.store, prepare: { _ in
            var event = DaemonEvent(type: "manager_status")
            event.managerStatus = ManagerStatus(state: "thinking", keySet: true)
            thinking.store.apply(event)
        })
        let noKey = try world(customize: { $0.managerSettings.keySet = false; $0.managerEntries = [] }, more: extras)
        try audit(ContentView(), area: "main", screen: "chat", state: "nokey", size: size, store: noKey.store)
        let noKeyTalk = try world(customize: { $0.managerSettings.keySet = false }, more: extras)
        try audit(ContentView(), area: "main", screen: "chat", state: "nokey-conversation", size: size, store: noKeyTalk.store)

        // An agent's thread.
        func thread(_ state: String, _ id: String, customize: ((inout Fixtures) -> Void)? = nil,
                    setUp: ((AgentsStore) -> Void)? = nil) throws {
            let world = try world(customize: { fixtures in
                Self.moreThreads(&fixtures)
                customize?(&fixtures)
            }, more: extras)
            world.store.selection = .agent(id)
            setUp?(world.store)
            try audit(ContentView(), area: "main", screen: "thread", state: state, size: size, store: world.store, wait: 2.0)
        }
        try thread("working", "d")
        try thread("approval", "c")
        try thread("question", "b")
        try thread("plan", "f")
        try thread("long-message", "e")
        try thread("error-over-composer", "a", setUp: { $0.actionError["a"] = "Couldn't deliver the message: the agent's terminal closed before it arrived." })
        try thread("queued", "a", customize: { $0.sessions[0].queued = ["Also add Apple Pay to the buy box on product pages.", "Then open a pull request."] })
        try thread("ended", "h")
        try thread("watched-readonly", "g")
        try thread("servers", "b", customize: { $0.sessions[1].state = .busy; $0.sessions[1].question = nil })
        try thread("slash-menu", "a", setUp: { $0.saveDraft("/", for: "a") })
        try thread("draft-long", "a", setUp: {
            $0.saveDraft(String(repeating: "Please also check the receipt page, the order history and the emails we send after a payment. ", count: 6), for: "a")
        })
        try thread("modes-models", "a", customize: {
            $0.sessions[0].modes = ["default", "acceptEdits", "plan", "bypassPermissions"]
            $0.sessions[0].mode = "acceptEdits"
        })
        try thread("no-messages", "h", customize: { $0.messages["h"] = [] })
    }

    /// The sidebar: crowded, pinned, archived, filtered, searching, empty,
    /// hidden, renaming.
    func testAuditSidebar() throws {
        _ = try auditFolder
        let size = Self.common
        func sidebar(_ state: String, customize: ((inout Fixtures) -> Void)? = nil, setUp: ((AgentsStore, [String: URL]) -> Void)? = nil,
                     wait: TimeInterval = 2.0) throws {
            let world = try world(customize: customize, more: extras)
            world.store.selection = .agent("a")
            setUp?(world.store, world.folders)
            try audit(ContentView(), area: "main", screen: "sidebar", state: state, size: size, store: world.store, wait: wait)
        }
        try sidebar("many-agents", customize: Self.manyAgents)
        let now = Date().timeIntervalSince1970
        set(["pinned": ["c", "e"]], "sidebarChoices")
        try sidebar("pinned")
        set(["archived": ["f": now, "g": now]], "sidebarChoices")
        set(true, "sidebarArchivedOpen")
        try sidebar("archived-open")
        set(false, "sidebarArchivedOpen")
        try sidebar("archived-closed")
        set(nil, "sidebarChoices")
        try sidebar("folder-filter", setUp: { store, folders in store.folderFilter = folders["storefront"]!.path }, wait: 2.4)
        try sidebar("state-filter-working", setUp: { store, _ in store.stateFilter = .busy })
        try sidebar("state-filter-needsyou", setUp: { store, _ in store.stateFilter = .waitingInput })
        try sidebar("search-hits", setUp: { store, _ in store.search = "apple pay" }, wait: 2.4)
        try sidebar("search-nothing", setUp: { store, _ in store.search = "kubernetes" }, wait: 2.4)
        try sidebar("renaming", setUp: { store, _ in store.renaming = "a" })
        try sidebar("empty-connected", customize: { $0.sessions = []; $0.managerEntries = [] }, setUp: { store, _ in store.selection = .manager })
        set(true, "sidebarHidden")
        try sidebar("hidden")
        set(false, "sidebarHidden")
        set(208.0, "sidebarWidth")
        try sidebar("narrowest-208")
        set(nil, "sidebarWidth")
    }

    /// The workspace card's sections and the right panel's tabs, and the
    /// terminal under a thread.
    func testAuditWorkspace() throws {
        _ = try auditFolder
        let size = Self.common
        func panel(_ state: String, env: [String: String] = [:], setUp: ((AgentsStore, [String: URL]) -> Void)? = nil) throws {
            try withEnvironment(env) {
                let world = try world(more: extras)
                world.store.selection = .agent("a")
                setUp?(world.store, world.folders)
                try audit(ContentView(), area: "main", screen: "workspace", state: state, size: size, store: world.store, wait: 2.4,
                          prepare: { _ in self.feedTerminals(world.folders) })
            }
        }
        try panel("card-changes", env: ["AGENTMAN_FILES": "changes"])
        try panel("card-files", env: ["AGENTMAN_FILES": "files"])
        try panel("card-history", env: ["AGENTMAN_FILES": "history"])
        try panel("card-diff", env: ["AGENTMAN_FILES_OPEN": "diff:src/checkout/ApplePayButton.tsx"])
        try panel("card-file", env: ["AGENTMAN_FILES_OPEN": "file:src/checkout/CheckoutSheet.tsx"])
        set(true, "rightPanelOpen")
        for tab in ["files", "history"] {
            set(tab, "rightPanelTab")
            set(tab, "rightPanelTabs")
            try panel("panel-\(tab)")
        }
        set("diff,files,history", "rightPanelTabs")
        set("diff", "rightPanelTab")
        set(540.0, "rightPanelWidth")
        try panel("panel-three-tabs-default-width")
        set("terminal", "rightPanelTab")
        set("diff,terminal", "rightPanelTabs")
        try panel("panel-terminal", setUp: { _, folders in self.terminal(in: folders["storefront"]!) })
        set(false, "rightPanelOpen")
        set(true, "bottomTerminalOpen")
        set("bottom", "terminalDock")
        try panel("bottom-terminal", setUp: { _, folders in self.terminal(in: folders["storefront"]!) })
        set(false, "bottomTerminalOpen")
        set(nil, "terminalDock")
        set(nil, "rightPanelTabs")
        set(nil, "rightPanelTab")
        set(nil, "rightPanelWidth")
        // With the plan the agent wrote.
        try withEnvironment(["AGENTMAN_FILES": "plans"]) {
            let world = try world(customize: Self.moreThreads, more: extras)
            world.store.selection = .agent("f")
            try audit(ContentView(), area: "main", screen: "workspace", state: "card-plans", size: size, store: world.store, wait: 2.4)
        }
    }

    /// The palette, starting an agent, and what shows over the window:
    /// toasts, the catch-up prompt, setting up, the summary on return, a
    /// suggested note, a picture full window; and not connected.
    func testAuditOverlays() throws {
        _ = try auditFolder
        let size = Self.common
        for (state, query) in [("empty", ""), ("agents", "fix"), ("commands", "usage"), ("messages", "apple pay"), ("nothing", "zzqx")] {
            let world = try world(more: extras)
            world.store.selection = .agent("a")
            try audit(ZStack { ContentView(); CommandPalette(query: query) }, area: "main", screen: "palette", state: state, size: size,
                      store: world.store, wait: 2.0, prepare: Self.caretAtEnd)
        }

        let new = try world(more: extras)
        new.store.selection = .newAgent
        try audit(ContentView(), area: "main", screen: "newagent", state: "default", size: size, store: new.store, wait: 2.0)
        let worktree = try world(more: extras)
        worktree.store.newAgentPreset = NewAgentPreset(folder: worktree.folders["storefront"]!.path, newWorktree: true)
        worktree.store.selection = .newAgent
        try audit(ContentView(), area: "main", screen: "newagent", state: "worktree", size: size, store: worktree.store, wait: 2.0)
        set("codex", "newAgentKind")
        let codex = try world(more: extras)
        codex.store.selection = .newAgent
        try audit(ContentView(), area: "main", screen: "newagent", state: "codex", size: CGSize(width: 1040, height: 640), store: codex.store, wait: 2.0)
        set(nil, "newAgentKind")

        let toasts = try world(customize: { $0.managerEntries.append(ManagerEntry(id: "m8", role: "brief", text: "Refresh the API reference published the payments guide: 6 pages, with Apple Pay test cards.", ts: Int64(Date().timeIntervalSince1970 * 1000), sessionId: "e")) }, more: extras)
        toasts.store.selection = .agent("a")
        try audit(ContentView(), area: "main", screen: "overlay", state: "toasts", size: size, store: toasts.store, prepare: { _ in
            for id in ["m7", "m8"] {
                if let entry = toasts.store.manager.entries.first(where: { $0.id == id }) { toasts.store.showToast(entry) }
            }
        })

        let catchUp = try world(more: extras)
        catchUp.store.selection = .agent("a")
        try audit(ZStack { ContentView(); CatchUpPrompt(version: "0.41.0") }, area: "main", screen: "overlay", state: "catchup-prompt", size: size, store: catchUp.store)

        let setup = try world(more: extras)
        setup.store.setup.showsScreen = true
        try audit(ContentView(), area: "main", screen: "overlay", state: "setup-screen", size: size, store: setup.store)

        let away = try world(more: extras)
        away.store.selection = .agent("a")
        try audit(ContentView(), area: "main", screen: "overlay", state: "away-summary", size: size, store: away.store, prepare: { _ in
            let since = Date().addingTimeInterval(-3600)
            away.store.away.record(.finished("Apple Pay is in the checkout sheet. 52 tests pass."), sessionId: "a", at: since.addingTimeInterval(600))
            away.store.away.record(.needsYou("Wants to push to main"), sessionId: "c", at: since.addingTimeInterval(1200))
            away.store.away.record(.finished("Published 14 endpoint pages."), sessionId: "e", at: since.addingTimeInterval(1800))
            away.store.showAway(since: since)
        })

        let note = try world(customize: { fixtures in
            let note = Note(name: "payments-tests", tier: "shared", title: "Run the payments tests before pushing",
                            always: true, body: "Before any push that touches checkout or payments, run `npm test -- payments` and say what failed.",
                            updatedAt: Int64(Date().timeIntervalSince1970 * 1000))
            let card = ManagerCard(id: "card-note", kind: "note", state: "open", sessionId: "", agentName: "", title: "Keep a note for your agents?",
                                   detail: nil, reason: "You asked two agents to do this today.", note: note)
            fixtures.managerEntries.append(ManagerEntry(id: "m9", role: "card", text: "A note to keep", ts: Int64(Date().timeIntervalSince1970 * 1000), card: card))
        }, more: extras)
        note.store.selection = .agent("a")
        try audit(ContentView(), area: "main", screen: "overlay", state: "note-suggestion", size: size, store: note.store)

        let lightbox = try world(more: extras)
        lightbox.store.selection = .agent("a")
        lightbox.store.lightbox = Lightbox(image: Self.samplePicture(), title: "checkout-ipad.png")
        try audit(ContentView(), area: "main", screen: "overlay", state: "lightbox", size: size, store: lightbox.store)

        let connecting = try world(connected: false, customize: { $0.sessions = []; $0.managerEntries = [] }, more: extras)
        try audit(ContentView(), area: "main", screen: "connection", state: "connecting", size: size, store: connecting.store)
        let down = try world(connected: false, customize: { $0.sessions = []; $0.managerEntries = [] }, more: extras)
        down.store.statusChanged(.unavailable("Connection refused"))
        try audit(ContentView(), area: "main", screen: "connection", state: "daemon-not-running", size: size, store: down.store)
        let downThread = try world(connected: false, more: extras)
        downThread.store.statusChanged(.unavailable("Connection refused"))
        downThread.store.selection = .agent("a")
        try audit(ContentView(), area: "main", screen: "connection", state: "daemon-gone-thread", size: size, store: downThread.store)

        // A panel command's sheet, on its own: sheets open as windows of
        // their own, which a picture of the window doesn't take in.
        let sheet = try world(more: extras)
        try audit(CommandPanelSheet(panel: CommandPanel(command: "/usage", text: """
        Current session
        █████████████████████▌                             38% used
        Resets 2:30pm

        Current week (all models)
        ████████████████████████████████                   56% used
        Resets Nov 10 at 9am
        """)), area: "main", screen: "sheet", state: "command-panel", size: CGSize(width: 680, height: 420), store: sheet.store,
                  titled: false, buttons: false)
    }

    // MARK: - Editor

    /// The editor at every size, windowed and full screen, with and without
    /// the agent beside the code, and in its other states at one size.
    func testAuditEditor() throws {
        _ = try auditFolder
        let smallest = try measureEditorMinimum()
        let sizes = [smallest, CGSize(width: 1024, height: 700), Self.common, CGSize(width: 1512, height: 982)]
        for (index, size) in sizes.enumerated() {
            for dark in index == 2 ? [false, true] : [false] {
                try auditEditor(screen: "code", state: "windowed", size: size, dark: dark)
                try auditEditor(screen: "agentpanel", state: "windowed", size: size, dark: dark, agent: true)
                if !dark {
                    try auditEditor(screen: "code", state: "fullscreen", size: size, titled: false)
                    try auditEditor(screen: "agentpanel", state: "fullscreen", size: size, titled: false, agent: true)
                }
            }
        }
        let size = Self.common
        try auditEditor(screen: "code", state: "terminal-closed", size: size, terminal: false)
        try auditEditor(screen: "code", state: "split", size: size) { editor, _ in
            editor.split()
            editor.open("src/checkout/useCheckout.ts")
        }
        try auditEditor(screen: "code", state: "tree-filter", size: size) { editor, _ in editor.treeFilter = "check" }
        try auditEditor(screen: "code", state: "tree-keys", size: size) { editor, _ in editor.showTreeKeys = true }
        try auditEditor(screen: "agentpanel", state: "agents-list", size: size, agent: true) { editor, _ in editor.panelRoute = .agents }
        try auditEditor(screen: "agentpanel", state: "agentman", size: size, agent: true) { editor, _ in editor.panelRoute = .agentman }
        try auditEditor(screen: "code", state: "fullscreen-terminal-closed", size: size, titled: false, terminal: false)
    }

    /// The top of each window with a title bar (a window not in full
    /// screen) and without one (full screen): the main window's thread,
    /// scrolled to its end, drawn both ways, and where the views sit in the
    /// editor and the main window each way, written to topbars-frames.json.
    func testAuditTopBars() throws {
        let folder = try auditFolder
        set(true, "rightPanelOpen")
        set("diff", "rightPanelTab")
        set("diff", "rightPanelTabs")
        for size in [CGSize(width: 1040, height: 640), Self.common] {
            let world = try world(more: extras)
            world.store.selection = .agent("a")
            try audit(ContentView(), area: "main", screen: "thread", state: "diffpanel-fullscreen", size: size, store: world.store,
                      wait: 2.4, titled: false, buttons: false)
        }
        set(false, "rightPanelOpen")
        for size in [CGSize(width: 1040, height: 640), Self.common] {
            let world = try world(more: extras)
            world.store.selection = .agent("a")
            try audit(ContentView(), area: "main", screen: "thread", state: "finished-fullscreen", size: size, store: world.store,
                      wait: 2.0, titled: false, buttons: false)
        }

        var report: [[String: Any]] = []
        func describe(_ window: NSWindow, _ label: String) {
            guard let content = window.contentView else { return }
            func rect(_ r: NSRect) -> [String: Int] {
                ["x": Int(r.minX), "y": Int(r.minY), "width": Int(r.width), "height": Int(r.height), "top": Int(r.maxY)]
            }
            let splits = Self.views(NSSplitView.self, in: content).map { rect($0.convert($0.bounds, to: nil)) }
            let scrolls = Self.views(NSScrollView.self, in: content).filter { $0.bounds.height > 100 }
                .map { scroll -> [String: Any] in
                    ["frame": rect(scroll.convert(scroll.bounds, to: nil)),
                     "contentInsetTop": Int(scroll.contentInsets.top), "automaticallyAdjustsContentInsets": scroll.automaticallyAdjustsContentInsets]
                }
            let webs = Self.views(WKWebView.self, in: content).map { rect($0.convert($0.bounds, to: nil)) }
            report.append(["window": label, "frame": rect(window.frame), "contentLayoutRect": rect(window.contentLayoutRect),
                           "titlebarHeight": Int(window.frame.height - window.contentLayoutRect.height),
                           "safeAreaTop": Int(content.safeAreaInsets.top), "splitViews": splits, "scrollViews": scrolls, "webViews": webs])
        }
        for titled in [true, false] {
            let world = try world(fresh: true, more: extras)
            let root = world.folders["storefront"]!
            let editor = EditorRegistry.shared.store(for: root.path)
            editors.append(editor)
            editor.open("src/checkout/CheckoutSheet.tsx")
            _ = try shoot(EditorWindow(folder: root.path, client: world.store.client), size: Self.common, dark: false, store: world.store,
                          wait: 4.0, titled: titled, buttons: titled, prepare: { window in
                              describe(window, "editor-\(titled ? "windowed" : "fullscreen")-1280x800")
                          }, to: folder.appendingPathComponent("drawing-frames.png"))
            for shell in editor.terminals { editor.closeTerminal(shell) }
        }
        for titled in [true, false] {
            let world = try world(more: extras)
            world.store.selection = .agent("a")
            _ = try shoot(ContentView(), size: Self.common, dark: false, store: world.store, wait: 2.0, titled: titled, buttons: titled,
                          prepare: { window in describe(window, "main-thread-\(titled ? "windowed" : "fullscreen")-1280x800") },
                          to: folder.appendingPathComponent("drawing-frames.png"))
        }
        try? FileManager.default.removeItem(at: folder.appendingPathComponent("drawing-frames.png"))
        try JSONSerialization.data(withJSONObject: report, options: [.prettyPrinted, .sortedKeys])
            .write(to: folder.appendingPathComponent("topbars-frames.json"))
    }

    /// What the editor shows over the code: nothing open, a Markdown
    /// preview, Search in Folder and Go to File.
    func testAuditEditorOverlays() throws {
        _ = try auditFolder
        let size = Self.common
        try auditEditor(screen: "code", state: "empty-no-file", size: size, terminal: false, open: [], webPages: false)
        // Not drawn: a Markdown preview, Search in Folder and Go to File.
        // With any of them over the editor, the window comes back clear but
        // for fragments, from a cached display and from its layers alike,
        // with a file open or none.
    }

    /// The editor's smallest window: no minimum is set, so it is what its
    /// panes need, as the window learns it from the content.
    func measureEditorMinimum() throws -> CGSize {
        let world = try world(fresh: true, more: extras)
        let root = world.folders["storefront"]!
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1280, height: 800),
                              styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        let hosting = NSHostingView(rootView: EditorWindow(folder: root.path, client: world.store.client).environment(world.store))
        window.contentView = hosting
        window.orderFrontRegardless()
        settle(2.0)
        let measured = window.contentMinSize
        let fitting = hosting.fittingSize
        window.orderOut(nil)
        let folder = try auditFolder
        let note = ["editorContentMinSize": ["width": measured.width, "height": measured.height],
                    "editorFittingSize": ["width": fitting.width, "height": fitting.height]]
        try JSONSerialization.data(withJSONObject: note, options: [.prettyPrinted, .sortedKeys])
            .write(to: folder.appendingPathComponent("editor-minimum.json"))
        // A size of nothing means the content set none: the panes' own
        // minimums, the tree's 240 and the code's 320, stand in.
        let width = measured.width > 100 ? measured.width : 560
        let height = measured.height > 100 ? measured.height : 400
        return CGSize(width: ceil(width), height: ceil(height))
    }

    func auditEditor(screen: String, state: String, size: CGSize, dark: Bool = false, titled: Bool = true, agent: Bool = false,
                     terminal: Bool = true, open: [String] = ["src/checkout/ApplePayButton.tsx", "src/checkout/CheckoutSheet.tsx"],
                     webPages: Bool = true, overlay: (@MainActor (EditorStore, Bool) -> Void)? = nil,
                     configure: ((EditorStore, URL) -> Void)? = nil) throws {
        let world = try world(fresh: true, more: extras)
        let root = world.folders["storefront"]!
        let editor = EditorRegistry.shared.store(for: root.path)
        editors.append(editor)
        editor.showAgentPanel = agent
        editor.panelRoute = agent ? .agent("a") : nil
        for path in open { editor.open(path) }
        editor.setExpanded("tests", true)
        editor.setExpanded("tests/checkout", true)
        editor.selection = nil
        if terminal { editor.showAgentTerminal(SessionTerminal(name: "audit", argv: ["/bin/sleep", "600"]), title: "zsh") }
        configure?(editor, root)
        var toggle: (@MainActor (Bool) -> Void)?
        if let overlay {
            toggle = { on in overlay(editor, on) }
        }
        try audit(EditorWindow(folder: root.path, client: world.store.client), area: "editor", screen: screen, state: state, size: size,
                  dark: dark, store: world.store, wait: 5.0, titled: titled, buttons: titled, webPages: webPages,
                  overlay: toggle, prepare: { _ in
                      if editor.treeFilter == nil { editor.selection = nil }
                      editor.terminals.first?.view.feed(text: Self.testRun)
                  })
        for shell in editor.terminals { editor.closeTerminal(shell) }
    }

    /// A terminal in a folder's editor store, as the right panel and the
    /// terminal under a thread show, running nothing to speak of.
    func terminal(in folder: URL) {
        let editor = EditorRegistry.shared.store(for: folder.path)
        editors.append(editor)
        if editor.terminals.isEmpty {
            editor.showAgentTerminal(SessionTerminal(name: "audit", argv: ["/bin/sleep", "600"]), title: "zsh")
        }
    }

    func feedTerminals(_ folders: [String: URL]) {
        for folder in folders.values {
            EditorRegistry.shared.store(for: folder.path).terminals.first?.view.feed(text: Self.testRun)
        }
    }

    // MARK: - Settings

    /// Every Settings page at the window's own size, light and dark, and
    /// the pages with much in them.
    func testAuditSettings() throws {
        _ = try auditFolder
        let size = CGSize(width: 880, height: 680)
        let pages = ["general", "notifications", "devices", "permissions", "personality", "knowledge", "keys", "languages"]
        for dark in [false, true] {
            for page in pages {
                set(page, "settingsTab")
                let world = try world(customize: Self.settingsData, more: extras)
                world.store.devices = Self.devices(count: 2)
                Self.reportDaemon(world.store)
                try withEnvironment(page == "knowledge" ? ["AGENTMAN_NOTE": "payments-tests"] : [:]) {
                    try audit(SettingsView(), area: "settings", screen: page, state: "default", size: size, dark: dark,
                              store: world.store, wait: 2.0, titled: false, buttons: false)
                }
            }
        }
        // Crowded and empty.
        for (page, state, crowded) in [("keys", "many", true), ("knowledge", "many", true), ("devices", "many", true),
                                       ("permissions", "many-rules", true), ("devices", "none", false), ("keys", "none", false),
                                       ("knowledge", "none", false)] {
            set(page, "settingsTab")
            let world = try world(customize: crowded ? Self.crowdedSettings : Self.emptySettings,
                                  more: { folders in try self.extras(folders, crowded: crowded, empty: !crowded) })
            world.store.devices = crowded ? Self.devices(count: 9) : []
            Self.reportDaemon(world.store)
            try audit(SettingsView(), area: "settings", screen: page, state: state, size: size, store: world.store, wait: 2.0,
                      titled: false, buttons: false)
        }
        set(nil, "settingsTab")
    }

    /// The daemon's own word on itself, as a session list brings it.
    static func reportDaemon(_ store: AgentsStore) {
        var event = DaemonEvent(type: "sessions")
        event.sessions = store.sessions
        event.daemon = DaemonInfo(version: "0.40.0", apiLevel: 1, install: "managed", latest: "0.40.0", behind: 0, agentman: true)
        store.apply(event)
    }

    // MARK: - Fixtures

    /// Answers for the screens beyond the threads: files, history, plans,
    /// folders, notes, keys, language servers, devices, commands.
    nonisolated func extras(_ folders: [String: URL]) throws -> [String: DaemonEvent] {
        try extras(folders, crowded: false, empty: false)
    }

    /// Made once for each set of folders.
    nonisolated(unsafe) static var extrasCache: [String: [String: DaemonEvent]] = [:]

    nonisolated func extras(_ folders: [String: URL], crowded: Bool, empty: Bool) throws -> [String: DaemonEvent] {
        let key = "\(folders["storefront"]?.path ?? "")|\(crowded)|\(empty)"
        if let known = Self.extrasCache[key] { return known }
        var more: [String: DaemonEvent] = [:]
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        let bySession = ["a": "storefront", "b": "storefront", "c": "api", "d": "api", "e": "docs", "f": "ios", "g": "ios", "h": "storefront"]
        for (id, project) in bySession {
            guard let root = folders[project] else { continue }
            // Every folder's listing, and some files.
            let walker = FileManager.default.enumerator(at: root, includingPropertiesForKeys: [.isDirectoryKey])
            var listings: [String: [WorkspaceEntry]] = ["": []]
            while let found = walker?.nextObject() as? URL {
                // /var and /private/var are the same folder, named either way.
                let url = found.resolvingSymlinksInPath()
                let path = String(url.path.dropFirst(root.resolvingSymlinksInPath().path.count + 1))
                if path.hasPrefix(".git") { walker?.skipDescendants(); continue }
                let directory = (try? url.resourceValues(forKeys: [.isDirectoryKey]).isDirectory) == true
                let parent = (path as NSString).deletingLastPathComponent
                listings[parent, default: []].append(WorkspaceEntry(name: url.lastPathComponent, directory: directory,
                                                                     size: directory ? nil : Int64((try? Data(contentsOf: url).count) ?? 0)))
                if directory {
                    listings[path, default: []] = listings[path] ?? []
                } else {
                    var file = DaemonEvent(type: "workspace")
                    file.workspace = WorkspaceResult(kind: "file", sessionId: id, path: path, text: try? String(contentsOf: url, encoding: .utf8))
                    more["read_file|\(id)|\(path)"] = file
                }
            }
            for (path, entries) in listings {
                var event = DaemonEvent(type: "workspace")
                event.workspace = WorkspaceResult(kind: "files", sessionId: id, path: path, entries: entries)
                more["list_files|\(id)|\(path)"] = event
            }
            var commits = DaemonEvent(type: "workspace")
            let subjects = ["Charge the cart's total in its currency", "Card form: show the card's brand", "Cart badge counts each item once",
                            "Format prices for the shopper's locale", "Checkout sheet with the card form", "Start"]
            commits.workspace = WorkspaceResult(kind: "commits", sessionId: id, commits: subjects.enumerated().map { index, subject in
                WorkspaceCommit(hash: String(format: "%07x", 0x3a5e1f9 &+ index &* 0x10f3a1) + "0000000000000000000000000000000000",
                                subject: subject, author: index % 2 == 0 ? "Storefront team" : "Claude Code",
                                authoredAt: now - Int64(index + 1) * 26 * 3_600_000, files: 2 + index, added: 40 + index * 7, removed: 3 + index)
            }, branch: try? SiteShotsTests.git(["branch", "--show-current"], in: root).trimmingCharacters(in: .whitespacesAndNewlines))
            more["list_commits|\(id)"] = commits
            var branches = DaemonEvent(type: "workspace")
            branches.workspace = WorkspaceResult(kind: "branches", sessionId: id, branches: [
                WorkspaceBranch(name: "main", ref: "refs/heads/main", updatedAt: now - 86_400_000),
                WorkspaceBranch(name: "apple-pay", ref: "refs/heads/apple-pay", current: true, updatedAt: now - 600_000),
                WorkspaceBranch(name: "origin/main", ref: "refs/remotes/origin/main", remote: true, updatedAt: now - 86_400_000),
            ])
            more["list_branches|\(id)"] = branches
        }
        // The plan the iOS agent wrote.
        var artifacts = DaemonEvent(type: "artifacts")
        artifacts.artifacts = [Artifact(name: "plan.md", kind: "plan", title: "Onboarding v2", summary: "Three screens, then the push prompt.",
                                        updatedAt: now - 300_000, size: 2400, mime: "text/markdown", review: true)]
        more["list_artifacts|f"] = artifacts
        var plan = DaemonEvent(type: "workspace")
        plan.workspace = WorkspaceResult(kind: "artifact", sessionId: "f", path: "plan.md", text: Self.planText)
        more["read_artifact|f|plan.md"] = plan

        var folderList = DaemonEvent(type: "folders")
        folderList.folders = folders.map { Folder(path: $0.value.path, agents: 2, running: 1, lastActivityAt: now - 60_000) }
        more["list_folders"] = folderList
        var past = DaemonEvent(type: "directory_sessions")
        past.sessions = [
            Session(id: "p1", kind: "claude", name: "Checkout spike", cwd: folders["storefront"]!.path, state: .ended, lastActivityAt: now - 21 * 86_400_000),
            Session(id: "p2", kind: "codex", name: "Price formatting for the euro", cwd: folders["storefront"]!.path, state: .ended, lastActivityAt: now - 9 * 86_400_000),
        ]
        more["directory_sessions"] = past

        var commands = DaemonEvent(type: "commands")
        commands.commands = [
            SlashCommand(name: "compact", description: "Summarize the conversation so far to free up context", argumentHint: "[instructions]", source: "builtin"),
            SlashCommand(name: "context", description: "Show how full the context window is", source: "builtin"),
            SlashCommand(name: "review", description: "Review the current diff for bugs", argumentHint: "[target]", source: "bundled"),
            SlashCommand(name: "frontend-design", description: "Distinctive, production-grade interfaces", source: "personal", skill: true),
            SlashCommand(name: "usage", description: "Show plan usage limits", source: "builtin", panel: true),
        ]
        more["list_commands"] = commands

        var notes = DaemonEvent(type: "notes")
        notes.notes = empty ? [] : Self.notes(count: crowded ? 24 : 4, now: now)
        more["knowledge_list"] = notes
        var note = DaemonEvent(type: "note")
        note.note = Self.notes(count: 1, now: now)[0]
        more["knowledge_read"] = note
        var keys = DaemonEvent(type: "keys")
        keys.keys = empty ? [] : Self.keys(count: crowded ? 14 : 3, now: now)
        more["keys_list"] = keys
        var languages = DaemonEvent(type: "lsp_status")
        languages.languages = LanguagesState(noDownloads: false, servers: [
            LanguageServer(id: "gopls", name: "gopls", language: "Go", source: "downloaded", size: 41_000_000, running: ["api"]),
            LanguageServer(id: "vtsls", name: "vtsls", language: "TypeScript", source: "downloaded", size: 12_000_000, running: ["storefront", "docs"]),
            LanguageServer(id: "pyright", name: "Pyright", language: "Python", source: "download", downloadSize: "18 MB"),
            LanguageServer(id: "rust-analyzer", name: "rust-analyzer", language: "Rust", source: "missing", missing: "Not downloaded yet"),
            LanguageServer(id: "sourcekit-lsp", name: "SourceKit-LSP", language: "Swift", source: "mac", path: "/usr/bin/sourcekit-lsp"),
        ], nodeSize: 98_000_000)
        more["lsp_status"] = languages
        var devices = DaemonEvent(type: "devices")
        devices.devices = empty ? [] : Self.devices(count: crowded ? 9 : 2)
        more["devices"] = devices
        Self.extrasCache[key] = more
        return more
    }

    nonisolated static func notes(count: Int, now: Int64) -> [Note] {
        let titles = ["Run the payments tests before pushing", "Commit messages say why, not what", "Never push to main without asking",
                      "Use the staging database for migrations", "Screenshots go in docs/images", "Prefer small pull requests"]
        return (0..<count).map { index in
            Note(name: index == 0 ? "payments-tests" : "note-\(index)", tier: index % 3 == 2 ? "private" : "shared",
                 title: titles[index % titles.count] + (index >= titles.count ? " (\(index / titles.count + 1))" : ""),
                 always: index % 2 == 0, body: "Before any push that touches checkout or payments, run `npm test -- payments` and say what failed.",
                 updatedAt: now - Int64(index) * 3_600_000)
        }
    }

    nonisolated static func keys(count: Int, now: Int64) -> [Key] {
        let names = ["OPENAI_API_KEY", "STRIPE_SECRET_KEY", "ELEVENLABS_API_KEY", "SENTRY_AUTH_TOKEN", "GITHUB_TOKEN", "POSTHOG_API_KEY", "MAPBOX_TOKEN"]
        let rules = ["auto", "projects", "ask"]
        return (0..<count).map { index in
            Key(name: names[index % names.count] + (index >= names.count ? "_\(index / names.count + 1)" : ""),
                description: index % 2 == 0 ? "For the storefront's payments and receipts" : nil,
                rule: rules[index % rules.count], projects: index % 3 == 1 ? ["storefront", "api"] : nil, set: index != 2,
                updatedAt: now - Int64(index) * 86_400_000)
        }
    }

    nonisolated static func devices(count: Int) -> [Device] {
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        let names = [("iPhone", "iPhone 17 Pro", "ios", "iOS 27.1"), ("iPad", "iPad Air", "ios", "iPadOS 27.1"), ("Pixel", "Pixel 10", "android", "Android 17")]
        return (0..<count).map { index in
            let (name, model, platform, os) = names[index % names.count]
            return Device(id: "device-\(index)", name: index < names.count ? name : "\(name) \(index / names.count + 1)", model: model, platform: platform, os: os,
                          firstSeen: now - Int64(index + 3) * 86_400_000, lastSeen: now - Int64(index) * 3_600_000, connected: index == 0)
        }
    }

    nonisolated static func settingsData(_ fixtures: inout Fixtures) {
        fixtures.managerSettings.rules = [
            PermissionRule(action: "ask", kind: "command", pattern: "git push *"),
            PermissionRule(action: "allow", kind: "command", pattern: "npm test*"),
            PermissionRule(action: "allow", kind: "edit", pattern: "docs/**", folder: nil),
        ]
        fixtures.managerSettings.style = "brief"
        fixtures.managerSettings.instructions = "Tell me first about anything that touches payments. Keep summaries to three lines."
    }

    nonisolated static func crowdedSettings(_ fixtures: inout Fixtures) {
        settingsData(&fixtures)
        fixtures.managerSettings.rules = (0..<16).map { index in
            PermissionRule(action: index % 3 == 0 ? "ask" : "allow", kind: ["command", "edit", "read", "network"][index % 4],
                           pattern: ["git push *", "npm test*", "docs/**", "https://api.stripe.com/*", "rm -rf *", "make migrate*"][index % 6]
                               + (index >= 6 ? " #\(index)" : ""),
                           folder: index % 4 == 1 ? fixtures.sessions.first?.cwd : nil)
        }
        fixtures.managerSettings.instructions = String(repeating: "Tell me first about anything that touches payments, and keep summaries short. ", count: 8)
    }

    nonisolated static func emptySettings(_ fixtures: inout Fixtures) {
        fixtures.managerSettings.rules = []
    }

    /// Two more cards waiting: an agent's question, and one that needs the
    /// user in words.
    nonisolated static func moreCards(_ fixtures: inout Fixtures) {
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        let question = ManagerCard(id: "card-b", kind: "question", state: "open", sessionId: "b", agentName: "Fix cart badges",
                                   title: "Which badge style should the cart use?", detail: nil, reason: "It's a design choice.",
                                   questionId: "q-badge", options: [QuestionOption(key: "1", label: "A count in a dot"),
                                                                    QuestionOption(key: "2", label: "A count beside the icon"),
                                                                    QuestionOption(key: "3", label: "No count, a dot only")])
        let needs = ManagerCard(id: "card-d", kind: "needs_you", state: "open", sessionId: "d", agentName: "Throttle webhooks",
                                title: "Needs the Stripe webhook secret for staging to test signatures", detail: nil,
                                reason: "Only you can get it from the Stripe dashboard.")
        fixtures.managerEntries.append(ManagerEntry(id: "m10", role: "card", text: "Fix cart badges asks", ts: now - 60_000, sessionId: "b", card: question))
        fixtures.managerEntries.append(ManagerEntry(id: "m11", role: "card", text: "Throttle webhooks needs you", ts: now - 30_000, sessionId: "d", card: needs))
    }

    /// Threads for the agents the site's fixtures leave without one.
    nonisolated static func moreThreads(_ fixtures: inout Fixtures) {
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        func ago(_ minutes: Double) -> Int64 { now - Int64(minutes * 60_000) }
        func said(_ id: String, _ session: String, _ minutes: Double, _ role: Role, _ text: String) -> Message {
            Message(id: id, sessionId: session, role: role, ts: ago(minutes), text: text)
        }
        func tool(_ id: String, _ session: String, _ minutes: Double, _ name: String, _ summary: String, _ title: String, _ status: String = "ok") -> Message {
            Message(id: id, sessionId: session, role: .tool, ts: ago(minutes), tool: ToolCall(name: name, summary: summary, status: status, title: title))
        }
        fixtures.messages["d"] = [
            said("d1", "d", 12, .user, "Throttle the incoming webhooks: 120 a minute per sender, and answer 429 with Retry-After past that."),
            tool("d2", "d", 11, "Read", "webhooks/handler.go", "Read handler.go"),
            tool("d3", "d", 9, "Edit", "webhooks/limit.go", "Added a token bucket per sender"),
            said("d4", "d", 6, .assistant, "The limiter is in. Each sender gets 120 deliveries a minute; past that they get a 429 with `Retry-After`. Running the tests now."),
            tool("d5", "d", 0.1, "Bash", "go test ./webhooks/...", "Running the webhook tests", "running"),
        ]
        // b asks a question with choices.
        if let index = fixtures.sessions.firstIndex(where: { $0.id == "b" }) {
            fixtures.sessions[index].state = .waitingInput
            fixtures.sessions[index].question = Question(id: "q-badge", prompt: "Which badge style should the cart use?", title: "Badge style",
                                                         detail: nil, options: [
                QuestionOption(key: "1", label: "A count in a dot", description: "The number of items, in a small red dot on the cart icon."),
                QuestionOption(key: "2", label: "A count beside the icon", description: "The number in plain text to the right of the icon."),
                QuestionOption(key: "3", label: "No count, a dot only", description: "Just a dot while the cart has anything in it."),
            ], multiple: false, custom: true)
        }
        fixtures.messages["b"] = [
            said("b1", "b", 9, .user, "The cart badge counts the same item twice when you add it from the product page. Fix it."),
            tool("b2", "b", 8, "Read", "src/cart/Cart.tsx", "Read Cart.tsx"),
            tool("b3", "b", 7, "Edit", "src/cart/Cart.tsx", "Counted each item once"),
            said("b4", "b", 6, .assistant, "Fixed: the badge now counts each item once. While I'm here, the design has no rule for how the badge looks with many items."),
        ]
        // f wrote a plan and waits for it to be approved.
        if let index = fixtures.sessions.firstIndex(where: { $0.id == "f" }) {
            fixtures.sessions[index].state = .waitingInput
            fixtures.sessions[index].artifacts = 1
            fixtures.sessions[index].artifactsToReview = 1
            fixtures.sessions[index].question = Question(id: "q-plan", prompt: "Would you like to proceed?", title: "Ready to build the plan?",
                                                         detail: planText, options: [
                QuestionOption(key: "1", label: "Yes, and auto-accept edits"),
                QuestionOption(key: "2", label: "Yes, and manually approve edits"),
                QuestionOption(key: "3", label: "No, keep planning", withText: true),
            ])
        }
        fixtures.messages["f"] = [
            said("f1", "f", 30, .user, "Plan onboarding v2: three screens, then ask for notifications. Don't write code yet."),
            tool("f2", "f", 28, "Read", "App/Onboarding/Welcome.swift", "Read Welcome.swift"),
            said("f3", "f", 25, .assistant, "Here is the plan. It's in **plan.md** beside this conversation too."),
        ]
        fixtures.messages["e"] = [
            said("e1", "e", 40, .user, "Refresh the API reference for payments, and make every example runnable."),
            said("e2", "e", 20, .assistant, longText),
        ]
        // An ended session, and one that can only be watched.
        let storefront = fixtures.sessions.first { $0.id == "a" }?.cwd ?? "/tmp"
        fixtures.sessions.append(Session(id: "h", kind: "claude", name: "Speed up the product grid", cwd: storefront, state: .ended,
                                         lastActivityAt: ago(60 * 26)))
        fixtures.messages["h"] = [
            said("h1", "h", 60 * 27, .user, "The product grid janks on scroll. Find out why."),
            said("h2", "h", 60 * 26, .assistant, "Each card decoded its image on the main thread. Moved decoding off it; scrolling holds 120 fps now."),
        ]
        if let index = fixtures.sessions.firstIndex(where: { $0.id == "g" }) {
            fixtures.sessions[index].inject = "none"
        }
        fixtures.messages["g"] = [
            said("g1", "g", 200, .user, "Ask for notifications after the second screen, not at launch."),
            said("g2", "g", 185, .assistant, "Moved the prompt after the second screen and wrote the copy for it."),
        ]
    }

    /// Thirty agents in more folders than fit.
    nonisolated static func manyAgents(_ fixtures: inout Fixtures) {
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        let kinds = ["claude", "codex", "opencode", "cursor", "antigravity", "kiro"]
        let states: [SessionState] = [.busy, .idle, .idle, .waitingInput, .busy, .idle]
        let names = ["Add a wishlist", "Fix the flaky login test", "Translate the checkout into German", "Speed up the search API",
                     "Write release notes for 0.9.3", "Move images to the CDN", "Audit the analytics events", "Remove the old cart code",
                     "Dark mode for the emails", "Paginate the order history", "Retry failed webhooks", "Make the footer accessible"]
        let folders = fixtures.sessions.map(\.cwd)
        for index in 0..<24 {
            fixtures.sessions.append(Session(id: "many-\(index)", kind: kinds[index % kinds.count],
                                             name: names[index % names.count] + (index >= names.count ? " again" : ""),
                                             cwd: folders[index % folders.count], state: states[index % states.count], inject: "tmux",
                                             lastActivityAt: now - Int64(index) * 1_900_000))
        }
    }

    nonisolated static func samplePicture() -> NSImage {
        NSImage(size: NSSize(width: 1200, height: 800), flipped: false) { rect in
            NSColor(srgbRed: 0.96, green: 0.95, blue: 0.93, alpha: 1).setFill()
            rect.fill()
            NSColor.white.setFill()
            NSBezierPath(roundedRect: NSRect(x: 360, y: 120, width: 480, height: 560), xRadius: 28, yRadius: 28).fill()
            NSColor.black.setFill()
            NSBezierPath(roundedRect: NSRect(x: 420, y: 180, width: 360, height: 64), xRadius: 14, yRadius: 14).fill()
            ("Pay $84.00" as NSString).draw(at: NSPoint(x: 530, y: 200), withAttributes: [.font: NSFont.systemFont(ofSize: 26, weight: .semibold), .foregroundColor: NSColor.white])
            return true
        }
    }

    nonisolated static let planText = """
    # Onboarding v2

    ## Screens
    1. **Welcome**: the shop's promise in one line, and Continue.
    2. **Your sizes**: shoe and clothing sizes, skippable.
    3. **Favourite brands**: pick up to five.

    ## Then
    - Ask for notifications after screen 2, with a reason: order and delivery updates.
    - Save choices on the device until the account exists.

    ## Tests
    - UI tests for each screen and for skipping.
    """

    nonisolated static let longText = """
    The payments reference is rewritten. Here is what changed and what to check.

    ## Endpoints

    | Endpoint | Method | What it does | Changed |
    |---|---|---|---|
    | `/v1/payments` | POST | Charges a card, Apple Pay or Google Pay token | Examples for all three |
    | `/v1/payments/{id}` | GET | Reads one payment | New `wallet` field |
    | `/v1/refunds` | POST | Refunds part or all of a payment | Partial refunds documented |
    | `/v1/webhooks/test` | POST | Sends a test event to your endpoint | New page |

    ## A runnable example

    ```bash
    curl https://api.storefront.example/v1/payments \\
      -H "Authorization: Bearer $STOREFRONT_KEY" \\
      -d amount=8400 -d currency=eur -d source=tok_applepay_visa -d description="Order 1042, two items, delivered to the shop's pickup point"
    ```

    ## Things I couldn't settle

    1. The old docs say refunds take "up to 5 days"; the API's own response says `estimated_arrival` in hours. I used the API's.
    2. A very long link that may not wrap well: https://docs.storefront.example/reference/payments/errors#card_declined_insufficient_funds_retry_after_settlement_window
    3. Three examples used `amount=84.00` as a decimal; the API takes cents, so they would have charged 84 cents.

    Every example now runs against the sandbox with the test keys, and the docs build passes.
    """
}
