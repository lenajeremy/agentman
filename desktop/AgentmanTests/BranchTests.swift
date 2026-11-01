import XCTest
@testable import Agentman

/// The branch under each agent in the sidebar: read from git once, then
/// again only when the folder's HEAD file changes.
@MainActor
final class BranchTests: XCTestCase {
    private var root: URL!

    override func setUpWithError() throws {
        root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString).resolvingSymlinksInPath()
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
    }

    override func tearDown() {
        try? FileManager.default.removeItem(at: root)
        super.tearDown()
    }

    @discardableResult
    private func git(_ args: [String], in folder: URL) throws -> Int32 {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/git")
        process.arguments = ["-c", "user.name=Test", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"] + args
        process.currentDirectoryURL = folder
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice
        try process.run()
        process.waitUntilExit()
        return process.terminationStatus
    }

    private func store(_ folders: [URL]) throws -> AgentsStore {
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-branches.sock"))
        var event = DaemonEvent(type: "sessions")
        event.sessions = folders.enumerated().map { index, folder in
            Session(id: "s\(index)", kind: "claude", name: folder.lastPathComponent, cwd: folder.path, state: .idle)
        }
        store.apply(event)
        return store
    }

    // What git says, read from HEAD itself: a branch, nothing when detached,
    // and git's own word when the file can't say.
    func testAHeadFileNamesItsBranch() {
        XCTAssertEqual(GitHead.branch(in: "ref: refs/heads/main\n"), "main")
        XCTAssertEqual(GitHead.branch(in: "ref: refs/heads/feature/sidebar-cache\n"), "feature/sidebar-cache")
        XCTAssertEqual(GitHead.branch(in: "4e838782a1b9c0d6f2e4b8a7c3d1e5f6a7b8c9d0\n"), "")
        XCTAssertNil(GitHead.branch(in: "ref: refs/heads/.invalid\n"), "reftable: only git can say")
        XCTAssertNil(GitHead.branch(in: "ref: refs/remotes/origin/main\n"))
        XCTAssertNil(GitHead.branch(in: ""))
    }

    // A repository's branch, a worktree's own, none outside a repository
    // or on a detached HEAD; and a switch is seen at the next read.
    func testBranchesAreReadAndFollowed() async throws {
        let repo = root.appendingPathComponent("site"), plain = root.appendingPathComponent("notes")
        let worktree = root.appendingPathComponent("site-sidebar")
        for folder in [repo, plain] { try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true) }
        try Data("# Site\n".utf8).write(to: repo.appendingPathComponent("README.md"))
        try git(["init", "-q", "-b", "main"], in: repo)
        try git(["add", "-A"], in: repo)
        try git(["commit", "-qm", "Start"], in: repo)
        try git(["worktree", "add", "-q", "-b", "sidebar", worktree.path], in: repo)

        let store = try store([repo, plain, worktree])
        await store.readBranches()
        XCTAssertEqual(store.branches, [repo.path: "main", worktree.path: "sidebar"])

        try git(["checkout", "-q", "-b", "faster-sidebar"], in: repo)
        await store.readBranches()
        XCTAssertEqual(store.branches[repo.path], "faster-sidebar")

        try git(["checkout", "-q", "--detach"], in: worktree)
        await store.readBranches()
        XCTAssertNil(store.branches[worktree.path], "detached")

        // A folder that becomes a repository is seen as one.
        try git(["init", "-q", "-b", "drafts"], in: plain)
        await store.readBranches()
        XCTAssertEqual(store.branches[plain.path], "drafts")
    }
}
