import AppKit
import XCTest
@testable import Agentman

@MainActor
final class EditorGitBarsTests: XCTestCase {
    private var root: URL!

    override func setUpWithError() throws {
        root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
    }

    override func tearDown() {
        try? FileManager.default.removeItem(at: root)
        super.tearDown()
    }

    private func git(_ arguments: String...) throws {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/git")
        process.arguments = ["-C", root.path, "-c", "user.name=Test", "-c", "user.email=test@example.com",
                             "-c", "commit.gpgsign=false"] + arguments
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice
        try process.run()
        process.waitUntilExit()
        XCTAssertEqual(process.terminationStatus, 0, "git \(arguments.joined(separator: " "))")
    }

    private func write(_ path: String, _ text: String) throws {
        let url = root.appendingPathComponent(path)
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try Data(text.utf8).write(to: url)
    }

    // The bars compare a file with git's index: what a commit has, then
    // what is staged; a file git hasn't seen is all new, one it ignores
    // has none.
    func testOpenFilesAreComparedWithGitsIndex() async throws {
        try git("init", "-q")
        try write("main.go", "package main\n")
        try write(".gitignore", "build/\n")
        try git("add", ".")
        try git("commit", "-q", "-m", "first")
        try write("main.go", "package main\n\nfunc main() {}\n")
        try write("notes.md", "# Notes\n")
        try write("build/out.txt", "made\n")

        let store = EditorStore(root: root)
        await store.refresh()
        let main = store.openInBackground("main.go")
        let notes = store.openInBackground("notes.md")
        let built = store.openInBackground("build/out.txt")
        await store.updateGitBases()
        XCTAssertEqual(main.gitBase, "package main\n", "what the commit has")
        XCTAssertEqual(notes.gitBase, "", "untracked: every line new")
        XCTAssertNil(built.gitBase, "ignored: nothing to compare with")
        let version = main.gitBaseVersion

        // Nothing changed in git: nothing is read again.
        await store.updateGitBases()
        XCTAssertEqual(main.gitBaseVersion, version)

        // Staged, the file is git's as it is now: no changes to show.
        try await Task.sleep(for: .milliseconds(20))
        try git("add", "main.go")
        await store.refresh()
        XCTAssertEqual(main.gitBase, "package main\n\nfunc main() {}\n")
        XCTAssertGreaterThan(main.gitBaseVersion, version)
    }

    func testAFolderOutsideARepositoryHasNoBars() async throws {
        try write("a.txt", "a\n")
        let store = EditorStore(root: root)
        await store.refresh()
        let document = store.openInBackground("a.txt")
        await store.updateGitBases()
        XCTAssertNil(document.gitBase)
    }

    // For screenshots of the editor's page: the editor's colours, light and
    // dark, as the app sends them. Runs with TEST_RUNNER_AGENTMAN_SHOTS=<folder>.
    func testWriteEditorThemes() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        for (name, appearance) in [("light", NSAppearance.Name.aqua), ("dark", .darkAqua)] {
            let theme = EditorTheme.make(for: try XCTUnwrap(NSAppearance(named: appearance)))
            let data = try JSONSerialization.data(withJSONObject: theme, options: [.sortedKeys])
            try data.write(to: URL(fileURLWithPath: out).appendingPathComponent("theme-\(name).json"))
        }
    }
}
