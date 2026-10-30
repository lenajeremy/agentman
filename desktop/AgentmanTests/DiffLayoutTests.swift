import XCTest
@testable import Agentman

/// A diff as the right panel lays it out: unchanged lines folded before each
/// hunk, the lines a fold stands for shown when it is opened, removed lines
/// beside added ones when split, and the words that changed marked.
final class DiffLayoutTests: XCTestCase {
    private let twoHunks = """
    diff --git a/docs/architecture.md b/docs/architecture.md
    --- a/docs/architecture.md
    +++ b/docs/architecture.md
    @@ -4,4 +4,4 @@ intro
     relay reachable from the internet

    -## System overview
    +## Runtime overview

    @@ -20,3 +20,4 @@ more
     a
    +b
     c
     d
    """

    private func lines(_ items: [DiffLayout.Item]) -> [DiffLayout.Line] {
        items.compactMap { if case .line(let line) = $0 { line } else { nil } }
    }

    func testTheLinesBeforeEachHunkAreFolded() {
        let items = DiffLayout.items(DiffRow.parse(twoHunks))
        guard case .fold(let first) = items.first else { return XCTFail("starts with a fold: \(items)") }
        XCTAssertEqual(first.count, 3)
        XCTAssertEqual(first.label, "3 unmodified lines")
        let folds = items.compactMap { if case .fold(let fold) = $0 { fold } else { nil } }
        XCTAssertEqual(folds.count, 2)
        // The first hunk ends at line 7; the second starts at 20.
        XCTAssertEqual(folds[1].count, 12)
        XCTAssertEqual(folds[1].oldStart, 8)
        XCTAssertEqual(folds[1].newStart, 8)
        XCTAssertFalse(items.contains { if case .line(let line) = $0 { line.row.kind == .hunk } else { false } },
                       "No hunk headers")
    }

    func testAnOpenedFoldShowsTheFilesLines() {
        let file = (1...30).map { "line \($0)" }
        let items = DiffLayout.items(DiffRow.parse(twoHunks), expanded: [0], file: file)
        let shown = lines(items).prefix(3)
        XCTAssertEqual(shown.map(\.row.text), ["line 1", "line 2", "line 3"])
        XCTAssertEqual(shown.map(\.row.newNumber), [1, 2, 3])
        XCTAssertEqual(shown.map(\.row.oldNumber), [1, 2, 3])
        XCTAssertEqual(Set(items.map(\.id)).count, items.count, "Every item has its own id")
    }

    func testANewFileHasNothingToFold() {
        let diff = "--- /dev/null\n+++ b/new.go\n@@ -0,0 +1,2 @@\n+package main\n+func main() {}\n"
        let items = DiffLayout.items(DiffRow.parse(diff))
        XCTAssertEqual(items.count, 2)
        XCTAssertTrue(lines(items).allSatisfy { $0.row.kind == .add })
    }

    func testSplitPutsRemovedLinesBesideAddedOnes() {
        let diff = "@@ -1,4 +1,3 @@\n a\n-b\n-c\n+B\n d\n"
        let split = DiffLayout.split(DiffLayout.items(DiffRow.parse(diff)))
        let pairs = split.compactMap { if case .pair(let pair) = $0 { pair } else { nil } }
        XCTAssertEqual(pairs.count, 4)
        XCTAssertEqual(pairs[0].old?.row.text, "a")
        XCTAssertEqual(pairs[0].new?.row.text, "a")
        XCTAssertEqual(pairs[1].old?.row.text, "b")
        XCTAssertEqual(pairs[1].new?.row.text, "B")
        XCTAssertEqual(pairs[2].old?.row.text, "c")
        XCTAssertNil(pairs[2].new, "One more line went than came")
        XCTAssertEqual(pairs[3].old?.row.text, "d")
    }

    func testTheWordThatChangedIsMarked() {
        let items = DiffLayout.items(DiffRow.parse(twoHunks))
        let changed = lines(items).filter { !$0.changed.isEmpty }
        XCTAssertEqual(changed.count, 2)
        func marked(_ line: DiffLayout.Line) -> String {
            let text = Array(line.row.text.utf16)
            return String(utf16CodeUnits: Array(text[line.changed[0]]), count: line.changed[0].count)
        }
        XCTAssertEqual(marked(changed[0]), "System")
        XCTAssertEqual(marked(changed[1]), "Runtime")
    }

    func testLinesWithLittleInCommonAreNotMarked() {
        let (old, new) = DiffLayout.changedWords("return nil", "throw ParseError.empty")
        XCTAssertNil(old)
        XCTAssertNil(new)
        // Only added to: nothing is marked on the old side, the whole
        // number on the new.
        let (was, now) = DiffLayout.changedWords("let count = 4", "let count = 40")
        XCTAssertEqual(was?.isEmpty, true)
        XCTAssertEqual(now, 12..<14)
    }

    func testGitStatusCodesReadAsLetters() {
        XCTAssertEqual(ChangeStatus.letter(" M"), "M")
        XCTAssertEqual(ChangeStatus.letter("MM"), "M")
        XCTAssertEqual(ChangeStatus.letter("??"), "U")
        XCTAssertEqual(ChangeStatus.letter("A "), "A")
        XCTAssertEqual(ChangeStatus.letter("AM"), "A")
        XCTAssertEqual(ChangeStatus.letter(" D"), "D")
        XCTAssertEqual(ChangeStatus.letter("R "), "R")
    }

    func testPathsSortAsATree() {
        let paths = ["main.go", "docs/z.md", "a.txt", "docs/api/x.md", "docs/b.md"]
        XCTAssertEqual(paths.sorted(by: ChangeStatus.treeOrder), ["docs/api/x.md", "docs/b.md", "docs/z.md", "a.txt", "main.go"])
    }
}
