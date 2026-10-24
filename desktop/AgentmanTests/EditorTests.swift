import AppKit
import XCTest
@testable import Agentman

final class EditorTests: XCTestCase {
    func testFuzzyFindsAFileByLettersInItsName() {
        let files = ["src/app.ts", "src/main.py", "README.md", "notes.txt"]
        let ranked = files.compactMap { path in Fuzzy.score("mpy", path).map { (path, $0) } }.sorted { $0.1 > $1.1 }
        XCTAssertEqual(ranked.map(\.0), ["src/main.py"])
        XCTAssertNil(Fuzzy.score("mpy", "src/app.ts"))
    }

    func testFuzzyPrefersTheNameAndWordStarts() {
        let name = Fuzzy.score("atv", "Agentman/Views/AgentThreadView.swift")
        let path = Fuzzy.score("atv", "Agentman/Tests/vendor.txt")
        XCTAssertNotNil(name)
        XCTAssertGreaterThan(name ?? 0, path ?? 0)
    }

    func testGitStatusKeepsTheFolderAndReadsEachMark() {
        let output = [
            " M desktop/Agentman/App.swift",
            "?? desktop/notes.txt",
            "?? desktop/new/",
            "R  desktop/b.swift", "desktop/a.swift",
            "D  desktop/gone.swift",
            "UU desktop/both.swift",
            " M mobile/app.tsx",
        ].joined(separator: "\0") + "\0"
        let parsed = GitStatus.parse(output, prefix: "desktop/")
        XCTAssertEqual(parsed.marks["Agentman/App.swift"], .modified)
        XCTAssertEqual(parsed.marks["notes.txt"], .untracked)
        XCTAssertEqual(parsed.marks["b.swift"], .renamed)
        XCTAssertNil(parsed.marks["a.swift"], "a rename's old path is not a file of its own")
        XCTAssertEqual(parsed.marks["gone.swift"], .deleted)
        XCTAssertEqual(parsed.marks["both.swift"], .conflicted)
        XCTAssertNil(parsed.marks["app.tsx"], "outside the folder")
        XCTAssertEqual(parsed.untrackedFolders, ["new"])
        XCTAssertTrue(parsed.changedFolders.contains("Agentman"))
    }

    func testIndentFollowsTheFile() {
        XCTAssertEqual(EditorDocument.indentUnit(of: "a {\n  b\n    c\n}\n", path: "x.swift"), "  ")
        XCTAssertEqual(EditorDocument.indentUnit(of: "a {\n    b\n}\n", path: "x.ts"), "    ")
        XCTAssertEqual(EditorDocument.indentUnit(of: "a {\n\tb\n}\n", path: "x.c"), "\t")
        XCTAssertEqual(EditorDocument.indentUnit(of: "plain\n", path: "x.ts"), "  ")
        XCTAssertEqual(EditorDocument.indentUnit(of: "plain\n", path: "x.go"), "\t")
    }

    @MainActor
    private func editor(_ text: String, selection: NSRange, comment: String? = "//") -> EditorTextView {
        let view = EditorTextView(frame: NSRect(x: 0, y: 0, width: 400, height: 300))
        view.isEditable = true
        view.isRichText = false
        view.allowsUndo = true
        view.indentUnit = "  "
        view.lineComment = comment
        view.string = text
        view.setSelectedRange(selection)
        return view
    }

    @MainActor
    func testCommentTogglesTheSelectedLines() {
        let view = editor("let a = 1\n  let b = 2\nlet c = 3\n", selection: NSRange(location: 0, length: 20))
        view.toggleComment()
        XCTAssertEqual(view.string, "// let a = 1\n//   let b = 2\nlet c = 3\n")
        view.toggleComment()
        XCTAssertEqual(view.string, "let a = 1\n  let b = 2\nlet c = 3\n")
    }

    @MainActor
    func testTabShiftsLinesInAndOut() {
        let view = editor("a\nb\n", selection: NSRange(location: 0, length: 3))
        view.shiftLines(by: 1)
        XCTAssertEqual(view.string, "  a\n  b\n")
        view.shiftLines(by: -1)
        XCTAssertEqual(view.string, "a\nb\n")
    }

    @MainActor
    func testNewLineKeepsTheIndentAndStepsInAfterABracket() {
        let view = editor("  if x {}", selection: NSRange(location: 8, length: 0))
        view.insertNewline(nil)
        XCTAssertEqual(view.string, "  if x {\n    \n  }")
        XCTAssertEqual(view.selectedRange().location, 13)

        let plain = editor("  call()", selection: NSRange(location: 8, length: 0))
        plain.insertNewline(nil)
        XCTAssertEqual(plain.string, "  call()\n  ")
    }

    @MainActor
    func testTheTerminalSaysWhatItIsAndLeavesTmuxOut() {
        let environment = TerminalSession.environment
        XCTAssertTrue(environment.contains("TERM=xterm-256color"))
        XCTAssertTrue(environment.contains("TERM_PROGRAM=Agentman"))
        XCTAssertFalse(environment.contains { $0.hasPrefix("TMUX=") })
        XCTAssertTrue(FileManager.default.isExecutableFile(atPath: TerminalSession.shell))
    }

    func testLettersTogetherBeatLettersScattered() {
        let titles = ["Open mac in the Editor", "Show Agents That Need You", "Open intro-to-hacking in the Editor"]
        let best = titles.compactMap { title in Fuzzy.score("need", title).map { (title, $0) } }.max { $0.1 < $1.1 }
        XCTAssertEqual(best?.0, "Show Agents That Need You")
        let files = ["src/snapper.ts", "src/app.ts", "docs/apple.md"]
        let first = files.compactMap { path in Fuzzy.score("app", path).map { (path, $0) } }.max { $0.1 < $1.1 }
        XCTAssertEqual(first?.0, "src/app.ts")
    }
}
