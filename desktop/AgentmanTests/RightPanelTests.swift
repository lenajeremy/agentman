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

    static func client(_ asked: Asked = Asked()) -> DaemonClient {
        let client = DaemonClient(socketPath: "/nonexistent/agentman-panel.sock")
        client.answer = { request in
            asked.add(request)
            var result: [String: Any] = ["sessionId": request.sessionId ?? "", "path": request.path ?? ""]
            switch request.type {
            case .listChanges:
                result["kind"] = "changes"
                result["changes"] = try? JSONSerialization.jsonObject(with: Data(changes.utf8))
            case .fileDiff:
                result["kind"] = "diff"
                var diff = diffs[request.path ?? ""] ?? ""
                if request.ignoreWhitespace == true { diff = "" }
                result["diff"] = diff
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
         {"id":"a","kind":"codex","name":"Make a few small changes","cwd":"\(cwd)","state":"idle","inject":"tmux","lastActivityAt":\(now - 60_000)}
        ]}
        """
        let messages = """
        {"type":"messages","sessionId":"a","messages":[
         {"id":"1","sessionId":"a","role":"user","ts":\(now - 300_000),"text":"Can you make a random change to about five files? It doesn't have to be anything major."},
         {"id":"2","sessionId":"a","role":"assistant","ts":\(now - 240_000),"text":"Made small wording edits in three files. No behaviour changes; `git diff --check` passed."}
        ]}
        """
        let store = AgentsStore(client: Self.client())
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
        try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: url)
        window.close()
    }

    private func renderView<V: View>(_ view: V, size: CGSize, dark: Bool, to url: URL) throws {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentView = NSHostingView(rootView: view.frame(width: size.width, height: size.height))
        window.orderFrontRegardless()
        RunLoop.main.run(until: Date().addingTimeInterval(0.8))
        let content = try XCTUnwrap(window.contentView)
        let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
        content.cacheDisplay(in: content.bounds, to: bitmap)
        try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: url)
        window.close()
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
