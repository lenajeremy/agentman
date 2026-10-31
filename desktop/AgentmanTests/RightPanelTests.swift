import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// The right panel's diff, read from a stand-in for am serve: the changed
/// files in tree order, each file's diff read once until the list is read
/// again, a fold opened from the file, and whitespace left out on request.
/// With TEST_RUNNER_AGENTMAN_SHOTS=<folder>, it also renders the panel.
@MainActor
final class RightPanelTests: XCTestCase {
    nonisolated static let diffs: [String: String] = [
        "docs/architecture.md": """
        diff --git a/docs/architecture.md b/docs/architecture.md
        --- a/docs/architecture.md
        +++ b/docs/architecture.md
        @@ -4,7 +4,7 @@ Agentman is three programs
         relay reachable from the internet, and a mobile app. The daemon owns all agent
         integration logic. The relay only authenticates peers and forwards live frames.

        -## System overview
        +## Runtime overview

         ```mermaid
         flowchart LR
        """,
        "desktop/Agentman/Views/Sidebar.swift": """
        diff --git a/desktop/Agentman/Views/Sidebar.swift b/desktop/Agentman/Views/Sidebar.swift
        --- a/desktop/Agentman/Views/Sidebar.swift
        +++ b/desktop/Agentman/Views/Sidebar.swift
        @@ -12,8 +12,9 @@ struct Sidebar: View {
             static let widthKey = "sidebarWidth"
             static let hiddenKey = "sidebarHidden"
        -    static let defaultWidth: Double = 240
        +    static let defaultWidth: Double = 256
             static let minWidth: Double = 208
        +    // Room for the conversation beside it.

             static func clamp(_ width: Double, window: CGFloat) -> CGFloat {
        -        CGFloat(min(max(width, minWidth), Double(window) - 600))
        +        CGFloat(min(max(width, minWidth), max(minWidth, Double(window) - 640)))
             }
        @@ -40,3 +41,3 @@ struct Sidebar: View {
                 List {
        -            ForEach(store.sessions) { session in
        +            ForEach(store.visibleSessions) { session in
                         SessionRow(session: session)
        """,
        "internal/daemon/git.go": """
        --- /dev/null
        +++ b/internal/daemon/git.go
        @@ -0,0 +1,6 @@
        +package daemon
        +
        +// commit records what the agent changed, as the panel's Commit asks.
        +func commit(cwd, message string) error {
        +\treturn runGitAction(cwd, "commit", "-m", message)
        +}
        """,
    ]

    nonisolated static let changes = """
    [{"path":"internal/daemon/git.go","status":"??","added":6},
     {"path":"docs/architecture.md","status":" M","added":1,"removed":1},
     {"path":"desktop/Agentman/Views/Sidebar.swift","status":" M","added":4,"removed":3}]
    """

    /// What the stand-in was asked, in order.
    final class Asked: @unchecked Sendable {
        private let lock = NSLock()
        private var requests: [DaemonRequest] = []
        func add(_ request: DaemonRequest) { lock.lock(); requests.append(request); lock.unlock() }
        var all: [DaemonRequest] { lock.lock(); defer { lock.unlock() }; return requests }
    }

    static func client(_ asked: Asked = Asked(), previewHistory: Bool = false) -> DaemonClient {
        let client = DaemonClient(socketPath: "/nonexistent/agentman-panel.sock")
        client.answer = { request in
            asked.add(request)
            var result: [String: Any] = ["sessionId": request.sessionId ?? "", "path": request.path ?? ""]
            switch request.type {
            case .listChanges, .commitFiles:
                result["kind"] = "changes"
                result["changes"] = try? JSONSerialization.jsonObject(with: Data(changes.utf8))
            case .fileDiff, .commitDiff:
                result["kind"] = "diff"
                var diff = diffs[request.path ?? ""] ?? ""
                if request.ignoreWhitespace == true { diff = "" }
                result["diff"] = diff
                if request.type == .commitDiff, request.withFile == true {
                    result["text"] = (1...12).map { "historical \($0)" }.joined(separator: "\n")
                }
            case .listBranches:
                result["kind"] = "branches"
                result["branches"] = [["name": "main", "ref": "refs/heads/main", "current": true, "updatedAt": 0],
                                      ["name": "feature/history", "ref": "refs/heads/feature/history", "updatedAt": 0]]
            case .listCommits:
                result["kind"] = "commits"
                result["branch"] = "main"
                result["truncated"] = request.offset == 0
                result["commits"] = [["hash": String(repeating: request.ref == nil ? (request.offset == 0 ? "a" : "b") : "c", count: 40),
                                       "subject": "Keep history beside the conversation", "author": "Jeremiah", "authoredAt": 1793448000000,
                                       "added": 11, "removed": 4]]
                if previewHistory {
                    let subjects = ["Mac: history beside the diff and files", "Mac: conversations scroll smoothly, and up when scrolled up",
                                    "Mac: a right panel with the diff, files and git actions", "Redesign desktop workspace and product pages",
                                    "Ship iOS build 41 (0.14.1)", "Editor: what changed since git, beside the line numbers",
                                    "Phone: Markdown files read as documents", "Mac: a blue and pink loader while an agent works"]
                    result["commits"] = subjects.enumerated().map { index, subject in
                        ["hash": String(repeating: String(index + 1), count: 40), "subject": subject,
                         "author": index == 4 ? "Lena Jeremy" : "Jeremiah Lena", "authoredAt": 1793448000000 - index * 3600000,
                         "added": [546, 432, 3102, 4366, 5, 693, 779, 163][index],
                         "removed": [78, 47, 49, 1271, 5, 1, 70, 3][index]] as [String: Any]
                    }
                }
            case .readFile:
                result["kind"] = "file"
                result["text"] = (1...12).map { "line \($0)" }.joined(separator: "\n")
            case .gitStatus, .gitCommit, .gitPush, .gitCreatePR:
                var git: [String: Any] = ["sessionId": request.sessionId ?? "", "branch": "feature/diff-panel", "defaultBranch": "main",
                                          "remote": "origin", "ahead": 0, "behind": 0, "aheadOfDefault": 2, "changes": 3,
                                          "upstream": "origin/feature/diff-panel"]
                if request.type == .gitCommit {
                    git["done"] = "committed"
                    git["commit"] = "1a2b3c4"
                    git["message"] = request.text?.isEmpty == false ? request.text! : "Describe the change"
                }
                let data = try! JSONSerialization.data(withJSONObject: ["type": "git", "git": git])
                return try? JSONDecoder().decode(DaemonEvent.self, from: data)
            default:
                return nil
            }
            let data = try! JSONSerialization.data(withJSONObject: ["type": "workspace", "workspace": result])
            return try? JSONDecoder().decode(DaemonEvent.self, from: data)
        }
        return client
    }

    func testTheDiffIsReadFileByFile() async throws {
        let asked = Asked()
        let store = AgentsStore(client: Self.client(asked))
        let model = DiffPanelModel()
        await model.refresh(store: store, session: "a", ignoreWhitespace: false)
        XCTAssertNil(model.problem)
        XCTAssertEqual(model.changes.map(\.path),
                       ["desktop/Agentman/Views/Sidebar.swift", "docs/architecture.md", "internal/daemon/git.go"])
        XCTAssertEqual(model.added, 11)
        XCTAssertEqual(model.removed, 4)

        let path = "docs/architecture.md"
        await model.load(path, store: store, session: "a")
        let diff = try XCTUnwrap(model.diffs[path])
        guard case .fold(let fold) = diff.items.first else { return XCTFail("The diff starts folded: \(diff.items)") }
        XCTAssertEqual(fold.count, 3)

        // Read once until the list is read again.
        await model.load(path, store: store, session: "a")
        XCTAssertEqual(asked.all.filter { $0.type == .fileDiff }.count, 1)

        // Opening the fold reads the file and shows its first three lines.
        await model.open(fold, in: path, store: store, session: "a")
        let opened = try XCTUnwrap(model.diffs[path]).items.prefix(3).compactMap { item -> String? in
            if case .line(let line) = item { line.row.text } else { nil }
        }
        XCTAssertEqual(opened, ["line 1", "line 2", "line 3"])

        // A new reading of the list reads the diffs again.
        await model.refresh(store: store, session: "a", ignoreWhitespace: true)
        await model.load(path, store: store, session: "a")
        let requests = asked.all.filter { $0.type == .fileDiff }
        XCTAssertEqual(requests.count, 2)
        XCTAssertEqual(requests.last?.ignoreWhitespace, true, "The whitespace button reaches the daemon")
        XCTAssertEqual(model.diffs[path]?.items.isEmpty, true)
    }

    func testHistoryPagesAndSwitchesBranchesWithoutCheckingOut() async {
        let asked = Asked()
        let store = AgentsStore(client: Self.client(asked))
        let model = HistoryPanelModel()
        await model.readBranches(store: store, session: "a")
        XCTAssertEqual(model.branches.count, 2)
        await model.reload(store: store, session: "a", ref: nil)
        XCTAssertEqual(model.commits.count, 1)
        XCTAssertTrue(model.more)
        await model.page(store: store, session: "a")
        XCTAssertEqual(model.commits.count, 2)
        XCTAssertFalse(model.more)
        await model.reload(store: store, session: "a", ref: "refs/heads/feature/history")
        XCTAssertEqual(model.commits.map(\.hash), [String(repeating: "c", count: 40)])
        XCTAssertFalse(model.loading)
        XCTAssertNil(model.problem)
        let pages = asked.all.filter { $0.type == .listCommits }
        XCTAssertEqual(pages.map { $0.offset }, [0, 1, 0])
        XCTAssertEqual(pages.last?.ref, "refs/heads/feature/history")
        XCTAssertTrue(asked.all.allSatisfy { [.listBranches, .listCommits].contains($0.type) })
    }

    func testHistoricalDiffUsesTheSharedRendererAndHistoricalContext() async throws {
        let asked = Asked()
        let store = AgentsStore(client: Self.client(asked))
        let model = DiffPanelModel()
        let hash = String(repeating: "a", count: 40)
        await model.refresh(store: store, session: "a", ignoreWhitespace: false, commit: hash)
        let path = "docs/architecture.md"
        await model.load(path, store: store, session: "a")
        XCTAssertFalse(asked.all.contains { $0.withFile == true }, "the file is read only when a fold opens")
        let diff = try XCTUnwrap(model.diffs[path])
        guard case .fold(let fold) = diff.items.first else { return XCTFail("Expected folded context") }
        await model.open(fold, in: path, store: store, session: "a")
        XCTAssertEqual(asked.all.last?.type, .commitDiff)
        XCTAssertEqual(asked.all.last?.withFile, true)
        XCTAssertEqual(asked.all.last?.commit, hash)
        let context = model.diffs[path]?.items.prefix(3).compactMap { item -> String? in
            if case .line(let line) = item { line.row.text } else { nil }
        }
        XCTAssertEqual(context, ["historical 1", "historical 2", "historical 3"])
        XCTAssertFalse(asked.all.contains { $0.type == .readFile || $0.type == .fileDiff })
        XCTAssertEqual(asked.all.first { $0.type == .commitFiles }?.commit, hash)
        await model.refresh(store: store, session: "a", ignoreWhitespace: true, commit: hash)
        await model.load(path, store: store, session: "a")
        let sent = try XCTUnwrap(asked.all.last { $0.type == .commitDiff })
        XCTAssertEqual(sent.commit, hash)
        XCTAssertEqual(sent.ignoreWhitespace, true)
        XCTAssertEqual(model.diffs[path]?.items.isEmpty, true)
    }

    // A commit the agent makes goes on top of the pages already read; history
    // rewritten underneath is read again from the start.
    func testHistoryKeepsUpWithNewCommits() async {
        final class Log: @unchecked Sendable {
            private let lock = NSLock()
            private var hashes: [String]
            init(_ hashes: [String]) { self.hashes = hashes }
            var all: [String] { get { lock.lock(); defer { lock.unlock() }; return hashes }
                                set { lock.lock(); hashes = newValue; lock.unlock() } }
        }
        func hash(_ name: String) -> String { String(repeating: name, count: 40) }
        let log = Log(["3", "2", "1"].map(hash))
        let client = DaemonClient(socketPath: "/nonexistent/agentman-history.sock")
        client.answer = { request in
            // Two a page, newest first, as git log pages them.
            let all = log.all, from = request.offset ?? 0
            let page = Array(all.dropFirst(from).prefix(2))
            let commits = page.map { ["hash": $0, "subject": "Commit " + String($0.prefix(1)), "author": "Jeremiah", "authoredAt": 0] as [String: Any] }
            let result: [String: Any] = ["sessionId": "a", "path": "", "kind": "commits", "branch": "main",
                                         "commits": commits, "truncated": from + 2 < all.count]
            let data = try! JSONSerialization.data(withJSONObject: ["type": "workspace", "workspace": result])
            return try? JSONDecoder().decode(DaemonEvent.self, from: data)
        }
        let store = AgentsStore(client: client)
        let model = HistoryPanelModel()
        await model.reload(store: store, session: "a", ref: nil)
        await model.page(store: store, session: "a")
        XCTAssertEqual(model.commits.map(\.hash), ["3", "2", "1"].map(hash))
        log.all = ["4", "3", "2", "1"].map(hash)
        await model.update(store: store, session: "a")
        XCTAssertEqual(model.commits.map(\.hash), ["4", "3", "2", "1"].map(hash), "new on top, older pages kept")
        XCTAssertFalse(model.more)
        log.all = ["5", "3", "2", "1"].map(hash)
        await model.update(store: store, session: "a")
        XCTAssertEqual(model.commits.map(\.hash), ["5", "3"].map(hash), "an amend reads the list again")
        XCTAssertTrue(model.more)
        await model.page(store: store, session: "a")
        XCTAssertEqual(model.commits.map(\.hash), ["5", "3", "2", "1"].map(hash))
    }

    func testHistoryCanBeOpenedAsAWorkspaceTab() {
        set([RightPanel.tabsKey: "diff,files", RightPanel.tabKey: "diff", RightPanel.openKey: false])
        RightPanel.show(.history)
        XCTAssertEqual(RightPanel.tabs(UserDefaults.standard.string(forKey: RightPanel.tabsKey) ?? ""), [.diff, .files, .history])
        XCTAssertEqual(UserDefaults.standard.string(forKey: RightPanel.tabKey), "history")
        XCTAssertTrue(UserDefaults.standard.bool(forKey: RightPanel.openKey))
        RightPanel.show(.history)
        XCTAssertEqual(RightPanel.tabs(UserDefaults.standard.string(forKey: RightPanel.tabsKey) ?? "").count, 3)
    }

    func testCollapseAllFoldsEveryFileThenOpensThem() async {
        let store = AgentsStore(client: Self.client())
        let model = DiffPanelModel()
        await model.refresh(store: store, session: "a", ignoreWhitespace: false)
        model.toggle("docs/architecture.md")
        model.toggleAll()
        XCTAssertEqual(model.collapsed.count, 3)
        model.toggleAll()
        XCTAssertTrue(model.collapsed.isEmpty)
    }

    func testACommitNamesItsFilesAndMessage() async throws {
        let asked = Asked()
        let store = AgentsStore(client: Self.client(asked))
        let state = try await store.git(.gitCommit, session: "a", message: "", paths: ["docs/architecture.md", "internal/daemon/git.go"])
        XCTAssertEqual(state.done, "committed")
        XCTAssertEqual(state.message, "Describe the change")
        let sent = try XCTUnwrap(asked.all.last)
        XCTAssertEqual(sent.paths, ["docs/architecture.md", "internal/daemon/git.go"])
        XCTAssertEqual(sent.text, "")
    }

    func testTheRowOffersWhatFitsTheBranch() {
        func state(branch: String? = "feature", upstream: String? = "origin/feature", remote: String? = "origin",
                   ahead: Int = 0, behind: Int = 0, ofDefault: Int = 0, changes: Int = 0) -> GitState {
            GitState(sessionId: "a", branch: branch, defaultBranch: "main", upstream: upstream, ahead: ahead, behind: behind,
                     aheadOfDefault: ofDefault, remote: remote, changes: changes)
        }
        XCTAssertEqual(GitAction.quick(state(changes: 2)), .commit(push: true))
        XCTAssertEqual(GitAction.quick(state(upstream: nil, remote: nil, changes: 2)), .commit(push: false), "Nowhere to push")
        XCTAssertEqual(GitAction.quick(state(ahead: 2, ofDefault: 2)), .push)
        XCTAssertEqual(GitAction.quick(state(upstream: nil, ahead: 1, ofDefault: 1)), .push, "A first push")
        XCTAssertEqual(GitAction.quick(state(ofDefault: 3)), .createPR)
        XCTAssertEqual(GitAction.quick(state(branch: "main", upstream: "origin/main")).label, "Up to date")
        XCTAssertEqual(GitAction.quick(state(ahead: 1, behind: 1)).label, "Diverged")
        XCTAssertEqual(GitAction.quick(state(branch: nil, changes: 1)).label, "Commit")
        XCTAssertFalse(GitAction.canCreatePR(state(branch: "main", ofDefault: 3)), "Not from the default branch")
        XCTAssertFalse(GitAction.canPush(state(ahead: 1, behind: 2)), "Not while behind")
    }

    /// Against a real am serve: TEST_RUNNER_AGENTMAN_TEST_SOCKET names its
    /// socket and TEST_RUNNER_AGENTMAN_TEST_SESSION a session in a repository
    /// on a branch, with a.txt, w.go and c.txt changed and an origin to push to.
    func testGitActionsAgainstADaemon() async throws {
        let environment = ProcessInfo.processInfo.environment
        guard let socket = environment["AGENTMAN_TEST_SOCKET"], let session = environment["AGENTMAN_TEST_SESSION"] else {
            throw XCTSkip("needs a test daemon")
        }
        let client = DaemonClient(socketPath: socket)
        client.start()
        defer { client.stop() }
        let store = AgentsStore(client: client)
        var before: GitState?
        for _ in 0..<50 where before == nil {
            before = try? await store.git(.gitStatus, session: session)
            if before == nil { try await Task.sleep(for: .milliseconds(100)) }
        }
        let state = try XCTUnwrap(before)
        XCTAssertEqual(state.branch, "feature")
        XCTAssertEqual(state.changes, 3)
        XCTAssertEqual(GitAction.quick(state), .commit(push: true))

        let quiet = try await store.workspace(.fileDiff, session: session, path: "w.go", ignoreWhitespace: true)
        XCTAssertEqual(quiet.diff ?? "", "", "Only its indent changed")

        let committed = try await store.git(.gitCommit, session: session, message: "Change a and add c", paths: ["a.txt", "c.txt"])
        XCTAssertEqual(committed.done, "committed")
        XCTAssertEqual(committed.changes, 1, "w.go wasn't named")
        XCTAssertEqual(GitAction.quick(committed), .commit(push: true))

        let pushed = try await store.git(.gitPush, session: session)
        XCTAssertEqual(pushed.upstream, "origin/feature")
        XCTAssertEqual(pushed.ahead, 0)

        let pr = try await store.git(.gitCreatePR, session: session)
        XCTAssertEqual(pr.done, "pr_created")
        XCTAssertEqual(pr.url, "https://github.com/test/repo/pull/7")

        do {
            _ = try await store.git(.gitCommit, session: session, message: "", paths: ["w.go"])
            XCTFail("No key, no message")
        } catch {
            XCTAssertTrue(error.localizedDescription.hasPrefix("Write a commit message"), error.localizedDescription)
        }
    }

    func testThePanelKeepsTheConversationRoom() {
        XCTAssertEqual(RightPanel.clamp(540, available: 1300, room: 380), 540)
        XCTAssertEqual(RightPanel.clamp(900, available: 1000, room: 380), 620)
        XCTAssertEqual(RightPanel.clamp(200, available: 1300, room: 380), 360)
        XCTAssertEqual(RightPanel.clamp(540, available: 600, room: 380), 360, "Never under its least")
    }

    // MARK: - Screenshots

    private var folders: [URL] = []
    private var restore: [String: Any?] = [:]

    override func tearDown() {
        for folder in folders { try? FileManager.default.removeItem(at: folder) }
        for (key, value) in restore {
            if let value { UserDefaults.standard.set(value, forKey: key) } else { UserDefaults.standard.removeObject(forKey: key) }
        }
        super.tearDown()
    }

    private func set(_ values: [String: Any]) {
        for (key, value) in values {
            if restore[key] == nil { restore[key] = .some(UserDefaults.standard.object(forKey: key)) }
            UserDefaults.standard.set(value, forKey: key)
        }
    }

    private func project() throws -> String {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
            .appendingPathComponent("agentman", isDirectory: true)
        folders.append(root.deletingLastPathComponent())
        let files = [
            ".gitignore": "node_modules/\n",
            "README.md": "# Agentman\n",
            "go.mod": "module agentman\n",
            "docs/architecture.md": "# Architecture\n",
            "docs/cli.md": "# CLI\n",
            "internal/daemon/git.go": "package daemon\n",
            "node_modules/left-pad/index.js": "module.exports = 1\n",
            "desktop/Agentman/Views/Sidebar.swift": "struct Sidebar {}\n",
        ]
        for (path, text) in files {
            let url = root.appendingPathComponent(path)
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            try Data(text.utf8).write(to: url)
        }
        for arguments in [["init", "-q"], ["add", "README.md", "go.mod", "docs"],
                          ["-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-qm", "base"]] {
            let git = Process()
            git.executableURL = URL(fileURLWithPath: "/usr/bin/git")
            git.arguments = ["-C", root.path] + arguments
            try git.run()
            git.waitUntilExit()
        }
        try Data("# CLI\n\nam serve\n".utf8).write(to: root.appendingPathComponent("docs/cli.md"))
        return root.resolvingSymlinksInPath().path
    }

    private func store(cwd: String) throws -> AgentsStore {
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        let sessions = """
        {"type":"sessions","sessions":[
         {"id":"a","kind":"codex","name":"Make a few small changes","cwd":"\(cwd)","state":"idle","inject":"tmux","lastActivityAt":\(now - 60_000),"servers":[{"port":8766,"command":"python3","title":"Agentman"}]}
        ]}
        """
        let messages = """
        {"type":"messages","sessionId":"a","messages":[
         {"id":"1","sessionId":"a","role":"user","ts":\(now - 300_000),"text":"Can you make a random change to about five files? It doesn't have to be anything major."},
         {"id":"2","sessionId":"a","role":"assistant","ts":\(now - 240_000),"text":"Made small wording edits in three files. No behaviour changes; `git diff --check` passed."}
        ]}
        """
        let store = AgentsStore(client: Self.client(previewHistory: true))
        for json in [sessions, messages] {
            store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(json.utf8)))
        }
        store.selection = .agent("a")
        return store
    }

    private func render(size: CGSize, dark: Bool, store: AgentsStore, to url: URL) throws {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.titled, .closable, .resizable],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentView = NSHostingView(rootView: ContentView().environment(store).frame(width: size.width, height: size.height))
        window.setContentSize(size)
        window.orderFrontRegardless()
        RunLoop.main.run(until: Date().addingTimeInterval(2))
        let content = try XCTUnwrap(window.contentView)
        let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
        content.cacheDisplay(in: content.bounds, to: bitmap)
        drawTerminals(in: content, onto: bitmap)
        try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: url)
        // Put away rather than closed: closing the last titled window ends
        // the app the tests run in, and the next test with it.
        window.orderOut(nil)
    }

    /// A shell's screen comes out blank in a capture of the whole window,
    /// though it draws in the app and in a capture of its own: each is
    /// captured alone and laid over the window's.
    private func drawTerminals(in content: NSView, onto bitmap: NSBitmapImageRep) {
        func terminals(in view: NSView) -> [AgentmanTerminalView] {
            (view as? AgentmanTerminalView).map { [$0] } ?? view.subviews.flatMap(terminals)
        }
        guard let context = NSGraphicsContext(bitmapImageRep: bitmap) else { return }
        NSGraphicsContext.saveGraphicsState()
        NSGraphicsContext.current = context
        for terminal in terminals(in: content) where !terminal.isHiddenOrHasHiddenAncestor && terminal.bounds.width > 0 {
            guard let own = terminal.bitmapImageRepForCachingDisplay(in: terminal.bounds) else { continue }
            terminal.cacheDisplay(in: terminal.bounds, to: own)
            var rect = terminal.convert(terminal.bounds, to: content)
            if content.isFlipped { rect.origin.y = content.bounds.height - rect.maxY }
            own.draw(in: rect)
        }
        NSGraphicsContext.restoreGraphicsState()
    }

    private func renderView<V: View>(_ view: V, size: CGSize, dark: Bool, to url: URL) throws {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentView = NSHostingView(rootView: view.frame(width: size.width, height: size.height).background(Palette.paper))
        window.orderFrontRegardless()
        RunLoop.main.run(until: Date().addingTimeInterval(0.8))
        let content = try XCTUnwrap(window.contentView)
        let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
        content.cacheDisplay(in: content.bounds, to: bitmap)
        try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: url)
        window.close()
    }

    // The terminal button opens a shell in the agent's folder in the main
    // window's panel, not the editor: the folder's own shell, the one its
    // editor shows too.
    func testTheTerminalOpensInTheMainWindow() throws {
        let cwd = try project()
        let editor = EditorRegistry.shared.store(for: cwd)
        defer { for terminal in editor.terminals { editor.closeTerminal(terminal) } }
        set([RightPanel.openKey: true, RightPanel.tabsKey: "diff,terminal", RightPanel.tabKey: "terminal",
             RightPanel.widthKey: 620.0, Sidebar.hiddenKey: false, RightPanel.terminalDockKey: "side",
             RightPanel.bottomTerminalKey: false])
        let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"].map(URL.init(fileURLWithPath:))
            ?? FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: out, withIntermediateDirectories: true)
        for dark in [false, true] {
            try render(size: CGSize(width: 1500, height: 900), dark: dark, store: try store(cwd: cwd),
                       to: out.appendingPathComponent("panel-terminal-\(dark ? "dark" : "light").png"))
        }
        XCTAssertEqual(editor.terminals.count, 1, "One shell, kept across the two renders")
        XCTAssertEqual(editor.terminals.first?.folder.resolvingSymlinksInPath().path, URL(fileURLWithPath: cwd).resolvingSymlinksInPath().path)
        XCTAssertNil(editor.terminals.first?.command, "A shell, not an agent's terminal")

        // Moved under the conversation: out of the side panel, the same shell.
        RightPanel.dockTerminal(.bottom)
        XCTAssertEqual(UserDefaults.standard.string(forKey: RightPanel.tabsKey), "diff")
        XCTAssertEqual(UserDefaults.standard.string(forKey: RightPanel.tabKey), "diff")
        XCTAssertTrue(UserDefaults.standard.bool(forKey: RightPanel.bottomTerminalKey))
        set([RightPanel.openKey: false])
        for dark in [false, true] {
            try render(size: CGSize(width: 1500, height: 900), dark: dark, store: try store(cwd: cwd),
                       to: out.appendingPathComponent("bottom-terminal-\(dark ? "dark" : "light").png"))
        }
        XCTAssertEqual(editor.terminals.count, 1, "The same shell, moved")
        set([RightPanel.openKey: true])
        try render(size: CGSize(width: 1500, height: 900), dark: false, store: try store(cwd: cwd),
                   to: out.appendingPathComponent("bottom-terminal-with-diff-light.png"))

        // And back into the side panel.
        RightPanel.dockTerminal(.side)
        XCTAssertFalse(UserDefaults.standard.bool(forKey: RightPanel.bottomTerminalKey))
        XCTAssertEqual(UserDefaults.standard.string(forKey: RightPanel.tabKey), "terminal")
    }

    func testRenderThePanel() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let shots = URL(fileURLWithPath: out)
        let cwd = try project()
        let large = CGSize(width: 1680, height: 1000)
        set([RightPanel.openKey: true, RightPanel.tabsKey: "diff,files", RightPanel.widthKey: 560.0,
             Sidebar.hiddenKey: false, "diffWrap": true, "diffFileTree": true, "diffIgnoreWhitespace": false])
        for dark in [false, true] {
            let look = dark ? "dark" : "light"
            set([RightPanel.tabKey: "diff", "diffSplit": false])
            try render(size: large, dark: dark, store: try store(cwd: cwd), to: shots.appendingPathComponent("panel-diff-\(look).png"))
            set(["diffSplit": true])
            try render(size: large, dark: dark, store: try store(cwd: cwd), to: shots.appendingPathComponent("panel-split-\(look).png"))
            set([RightPanel.tabKey: "files", "diffSplit": false])
            try render(size: large, dark: dark, store: try store(cwd: cwd), to: shots.appendingPathComponent("panel-files-\(look).png"))
        }
        for dark in [false, true] {
            let look = dark ? "dark" : "light"
            set([RightPanel.tabsKey: "diff,files,history", RightPanel.tabKey: "history"])
            try render(size: large, dark: dark, store: try store(cwd: cwd), to: shots.appendingPathComponent("panel-history-\(look).png"))
            try renderView(HistoryPanel(sessionID: "a", cwd: cwd, state: .idle, activity: nil)
                            .environment(AgentsStore(client: Self.client(previewHistory: true))),
                           size: CGSize(width: 360, height: 760), dark: dark,
                           to: shots.appendingPathComponent("history-list-360-\(look).png"))
            let commit = WorkspaceCommit(hash: String(repeating: "a", count: 40), subject: "Keep history beside the conversation",
                                         body: (1...7).map { "A line of the message that says what changed and why, number \($0)." }
                                             .joined(separator: "\n"),
                                         author: "Jeremiah", authoredAt: 1793448000000)
            for width in [360.0, 540.0, 900.0] {
                let split = width == 900
                set(["diffSplit": split])
                try renderView(DiffPanel(sessionID: "a", cwd: cwd, state: .idle, activity: nil, commit: commit, back: {}).environment(AgentsStore(client: Self.client())),
                               size: CGSize(width: width, height: 760), dark: dark,
                               to: shots.appendingPathComponent("history-commit-\(Int(width))-\(look).png"))
            }
        }
        set([RightPanel.tabKey: "history"])
        try render(size: CGSize(width: 1180, height: 720), dark: false, store: try store(cwd: cwd),
                   to: shots.appendingPathComponent("panel-history-compact-light.png"))
        // The card's git row, and the commit sheet over the window.
        set([RightPanel.openKey: false])
        try render(size: CGSize(width: 1180, height: 720), dark: false, store: try store(cwd: cwd),
                   to: shots.appendingPathComponent("card-git-light.png"))
        let changes = try JSONDecoder().decode([WorkspaceChange].self, from: Data(Self.changes.utf8))
        for dark in [false, true] {
            let sheet = CommitSheet(state: GitState(sessionId: "a", branch: "main", defaultBranch: "main", upstream: "origin/main",
                                                    ahead: 0, behind: 0, aheadOfDefault: 0, remote: "origin", changes: 3),
                                    changes: changes, hidden: 1, then: .commitAndPush, confirm: { _, _, _ in }, cancel: {})
            try renderView(sheet, size: CGSize(width: 520, height: 560), dark: dark,
                           to: shots.appendingPathComponent("commit-sheet-\(dark ? "dark" : "light").png"))
        }
        set([RightPanel.openKey: true])
        // A smaller window: the card makes way for the panel.
        set([RightPanel.tabKey: "diff"])
        try render(size: CGSize(width: 1180, height: 720), dark: false, store: try store(cwd: cwd),
                   to: shots.appendingPathComponent("panel-compact-light.png"))
    }
}
