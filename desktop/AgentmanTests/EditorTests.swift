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

    // MARK: - Monaco's edits

    @MainActor
    private func document(_ text: String) throws -> EditorDocument {
        let folder = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        try Data(text.utf8).write(to: folder.appendingPathComponent("file.txt"))
        return EditorDocument(root: folder, path: "file.txt")
    }

    @MainActor
    func testMonacosEditsLandInUTF16Offsets() throws {
        let document = try document("emoji 😀 here\nnext é\n")
        // Last first, as Monaco sends them: after the emoji (two UTF-16
        // units), then at the start.
        let changes = [
            EditorDocument.Change(offset: 8, length: 0, text: "!"),
            EditorDocument.Change(offset: 0, length: 5, text: "smile"),
        ]
        XCTAssertTrue(document.apply(changes, expecting: 22))
        XCTAssertEqual(document.text, "smile 😀! here\nnext é\n")
        XCTAssertTrue(document.isDirty)
    }

    @MainActor
    func testEditsThatDontFitLeaveTheTextAlone() throws {
        let document = try document("short")
        XCTAssertFalse(document.apply([.init(offset: 10, length: 1, text: "x")], expecting: 6))
        XCTAssertFalse(document.apply([.init(offset: 0, length: 1, text: "x")], expecting: 99), "the page's length says they fell apart")
        XCTAssertEqual(document.text, "short")
        XCTAssertFalse(document.isDirty)
    }

    @MainActor
    func testEvenedOutLineEndingsDontMakeAFileUnsaved() throws {
        let document = try document("one\r\ntwo\nthree\r\n")
        document.adopt("one\r\ntwo\r\nthree\r\n")
        XCTAssertFalse(document.isDirty)
        document.text += "four"
        document.adopt("one\r\ntwo\r\nthree\r\nfour!")
        XCTAssertTrue(document.isDirty, "edits stay unsaved")
    }

    @MainActor
    func testALanguageServersEditsLandByLineAndCharacter() throws {
        let document = try document("func a() {}\r\nfunc b() { a() }\n")
        // A rename of a to c: both places, given first to last.
        let edits = [
            EditorDocument.LineEdit(startLine: 0, startCharacter: 5, endLine: 0, endCharacter: 6, text: "c"),
            EditorDocument.LineEdit(startLine: 1, startCharacter: 11, endLine: 1, endCharacter: 12, text: "c"),
        ]
        XCTAssertTrue(document.apply(edits))
        XCTAssertEqual(document.text, "func c() {}\r\nfunc b() { c() }\n")
        // Past the end of a line means its end, before the line break.
        XCTAssertTrue(document.apply([.init(startLine: 0, startCharacter: 99, endLine: 0, endCharacter: 99, text: " // c")]))
        XCTAssertEqual(document.text, "func c() {} // c\r\nfunc b() { c() }\n")
        // Two inserts at one place go in the order given.
        XCTAssertTrue(document.apply([
            .init(startLine: 2, startCharacter: 0, endLine: 2, endCharacter: 0, text: "x"),
            .init(startLine: 2, startCharacter: 0, endLine: 2, endCharacter: 0, text: "y"),
        ]))
        XCTAssertTrue(document.text.hasSuffix("\nxy"))
        XCTAssertFalse(document.apply([.init(startLine: 9, startCharacter: 0, endLine: 9, endCharacter: 0, text: "z")]), "no such line")
    }

    @MainActor
    func testAFileFromOutsideTheFolderIsNeverChanged() throws {
        let folder = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let file = folder.appendingPathComponent("dep.go")
        try Data("package dep\n".utf8).write(to: file)
        let document = EditorDocument(root: URL(fileURLWithPath: "/"), path: String(file.path.dropFirst()), readOnly: true)
        XCTAssertEqual(document.url.standardizedFileURL, file.standardizedFileURL)
        XCTAssertFalse(document.apply([.init(startLine: 0, startCharacter: 0, endLine: 0, endCharacter: 0, text: "x")]))
        document.text = "changed"
        XCTAssertFalse(document.save())
        XCTAssertEqual(try String(contentsOf: file, encoding: .utf8), "package dep\n")
    }

    @MainActor
    func testGoToDefinitionOpensFilesInsideAndOutsideTheFolder() throws {
        let folder = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: folder.appendingPathComponent("pkg"), withIntermediateDirectories: true)
        try Data("package pkg\n".utf8).write(to: folder.appendingPathComponent("pkg/a.go"))
        let outside = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + ".go")
        try Data("package std\n".utf8).write(to: outside)
        let store = EditorStore(root: folder)
        store.open(fileAt: folder.appendingPathComponent("pkg/a.go"))
        XCTAssertEqual(store.active?.path, "pkg/a.go")
        XCTAssertEqual(store.active?.readOnly, false)
        store.open(fileAt: outside)
        XCTAssertEqual(store.active?.readOnly, true)
        store.open(fileAt: outside)
        XCTAssertEqual(store.documents.count, 2, "the same file opens once")
    }
}
