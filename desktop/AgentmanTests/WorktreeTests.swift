import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// Worktrees on the Mac: Agentman's folders read as their repository and
/// name, the requests that make, list and remove one, and, with
/// TEST_RUNNER_AGENTMAN_SHOTS=<folder>, New Agent set to start in one and a
/// worktree agent in the sidebar.
@MainActor
final class WorktreeTests: XCTestCase {
    /// What the stand-in daemon was asked.
    final class Asked: @unchecked Sendable {
        private let lock = NSLock()
        private var requests: [DaemonRequest] = []
        func add(_ request: DaemonRequest) { lock.lock(); requests.append(request); lock.unlock() }
        var all: [DaemonRequest] { lock.lock(); defer { lock.unlock() }; return requests }
    }

    private static func client(_ asked: Asked, repo: String) -> DaemonClient {
        let client = DaemonClient(socketPath: "/nonexistent/agentman-worktrees.sock")
        let made = Worktrees.root + "/agentman/rate-limits"
        client.answer = { request in
            asked.add(request)
            var event: [String: Any]
            switch request.type {
            case .createWorktree:
                event = ["type": "worktree", "worktree": ["path": made, "name": "rate-limits", "repo": "agentman",
                                                          "branch": "rate-limits", "agentman": true, "copied": [".env"]]]
            case .listWorktrees:
                event = ["type": "worktrees", "worktrees": [
                    ["path": repo, "name": "agentman", "repo": "agentman", "branch": "feature/diff-panel", "main": true],
                    ["path": made, "name": "rate-limits", "repo": "agentman", "branch": "rate-limits", "agentman": true],
                ]]
            case .removeWorktree:
                event = request.force == true
                    ? ["type": "worktrees", "worktrees": []]
                    : ["type": "error", "error": "workspace: it has uncommitted changes; commit them, or remove the worktree anyway to lose them"]
            case .listFolders:
                event = ["type": "folders", "folders": [["path": repo, "agents": 1]]]
            default:
                return nil
            }
            let data = try! JSONSerialization.data(withJSONObject: event)
            return try? JSONDecoder().decode(DaemonEvent.self, from: data)
        }
        return client
    }

    func testAgentmansWorktreesReadAsTheirRepository() {
        let made = Worktrees.root + "/agentman/rate-limits"
        XCTAssertEqual(Worktrees.parts(of: made)?.repo, "agentman")
        XCTAssertEqual(Worktrees.parts(of: made + "/desktop")?.name, "rate-limits", "A folder inside one is in it")
        XCTAssertEqual(Worktrees.label(made), "agentman · rate-limits")
        XCTAssertEqual(Worktrees.label(NSHomeDirectory() + "/code/site"), "site")
        XCTAssertNil(Worktrees.parts(of: Worktrees.root + "/agentman"), "The repository's folder there isn't a worktree")
        XCTAssertEqual(Worktrees.relative(made), "Agentman/Worktrees/agentman/rate-limits")
        XCTAssertNil(Worktrees.relative("/tmp/x"))
        XCTAssertEqual(NewAgentView.branchName("  rate limits  "), "rate-limits")
        XCTAssertEqual(NewAgentView.branchName("fix/sign in"), "fix/sign-in")
    }

    func testTheStoreMakesListsAndRemovesWorktrees() async throws {
        let asked = Asked()
        let repo = NSHomeDirectory() + "/Desktop/all/agentman"
        let store = AgentsStore(client: Self.client(asked, repo: repo))

        let made = try await store.createWorktree(in: repo, name: "rate limits")
        XCTAssertEqual(made.name, "rate-limits")
        XCTAssertEqual(made.copied, [".env"])
        let create = try XCTUnwrap(asked.all.last)
        XCTAssertEqual(create.path, "Desktop/all/agentman")
        XCTAssertEqual(create.text, "rate limits")

        let list = try await store.listWorktrees(in: repo)
        XCTAssertEqual(list.map(\.name), ["agentman", "rate-limits"])
        XCTAssertEqual(list.first?.main, true)

        do {
            try await store.removeWorktree(made.path, force: false)
            XCTFail("Uncommitted work was removed without asking")
        } catch {
            XCTAssertTrue(error.localizedDescription.hasPrefix("It has uncommitted changes"), error.localizedDescription)
        }
        try await store.removeWorktree(made.path, force: true)
        XCTAssertEqual(asked.all.last?.force, true)
        XCTAssertEqual(asked.all.last?.path, "Agentman/Worktrees/agentman/rate-limits")

        do {
            _ = try await store.createWorktree(in: "/tmp/elsewhere", name: "x")
            XCTFail("A folder out of home was sent")
        } catch {}
    }

    // MARK: - Screenshots

    func testRenderWorktrees() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"].map(URL.init(fileURLWithPath:)) else {
            throw XCTSkip("for screenshots")
        }
        // This repository, read only: worktrees are offered for folders in
        // home, and the stand-in daemon answers for it.
        let repo = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().path
        try XCTSkipUnless(repo.hasPrefix(NSHomeDirectory() + "/"), "the repository isn't in home")

        let keys = ["newAgentFolder", "newAgentKind", RightPanel.openKey, RightPanel.bottomTerminalKey, Sidebar.hiddenKey]
        let saved = keys.map { ($0, UserDefaults.standard.object(forKey: $0)) }
        defer { for (key, value) in saved { if let value { UserDefaults.standard.set(value, forKey: key) } else { UserDefaults.standard.removeObject(forKey: key) } } }
        UserDefaults.standard.set("codex", forKey: "newAgentKind")
        UserDefaults.standard.set(false, forKey: RightPanel.openKey)
        UserDefaults.standard.set(false, forKey: RightPanel.bottomTerminalKey)
        UserDefaults.standard.set(false, forKey: Sidebar.hiddenKey)

        UserDefaults.standard.set(repo, forKey: "newAgentFolder")
        for dark in [false, true] {
            let look = dark ? "dark" : "light"
            let plain = try self.store(repo: repo)
            try renderView(NewAgentView().environment(plain), dark: dark, to: out.appendingPathComponent("new-agent-\(look).png"))
            let named = try self.store(repo: repo)
            try renderView(NewAgentView(startingPlace: .newWorktree("rate-limits")).environment(named), dark: dark,
                           to: out.appendingPathComponent("new-agent-worktree-\(look).png"))
            try renderView(WorktreeNamer(repo: "agentman", from: "feature/diff-panel", suggestion: "rate-limit-the-uploads") { _ in },
                           size: CGSize(width: 300, height: 170), dark: dark, to: out.appendingPathComponent("worktree-namer-\(look).png"))
            let thread = try self.store(repo: repo)
            thread.selection = .agent("w")
            try render(store: thread, dark: dark, to: out.appendingPathComponent("worktree-agent-\(look).png"))
        }
    }

    private func store(repo: String) throws -> AgentsStore {
        let store = AgentsStore(client: Self.client(Asked(), repo: repo))
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        let made = Worktrees.root + "/agentman/rate-limits"
        let json = """
        {"type":"sessions","sessions":[
         {"id":"a","kind":"claude","name":"Redesign the sidebar","cwd":"\(repo)","state":"idle","inject":"tmux","lastActivityAt":\(now - 120_000)},
         {"id":"w","kind":"codex","name":"Rate limit the uploads","cwd":"\(made)","state":"busy","inject":"tmux","lastActivityAt":\(now - 30_000)}
        ]}
        """
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(json.utf8)))
        return store
    }

    private func renderView<V: View>(_ view: V, size: CGSize = CGSize(width: 900, height: 520), dark: Bool, to url: URL) throws {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentView = NSHostingView(rootView: view.frame(width: size.width, height: size.height).background(Palette.paper))
        window.orderFrontRegardless()
        RunLoop.main.run(until: Date().addingTimeInterval(1))
        let content = try XCTUnwrap(window.contentView)
        let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
        content.cacheDisplay(in: content.bounds, to: bitmap)
        try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: url)
        window.orderOut(nil)
    }

    private func render(store: AgentsStore, dark: Bool, to url: URL) throws {
        let size = CGSize(width: 1280, height: 780)
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.titled, .closable, .resizable],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
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
}
