import XCTest
@testable import Agentman

@MainActor
final class EditorSplitTests: XCTestCase {
    private var root: URL!

    override func setUpWithError() throws {
        root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        for name in ["a.go", "b.go", "c.go", "d.go"] {
            try Data("package \(name.prefix(1))\n".utf8).write(to: root.appendingPathComponent(name))
        }
    }

    override func tearDown() {
        try? FileManager.default.removeItem(at: root)
        super.tearDown()
    }

    private func names(_ store: EditorStore) -> [String] {
        store.panes.map { id in store.documents.first { $0.id == id }?.path ?? "-" }
    }

    // ⌘\ splits the file in front to the right; a file opened goes to the
    // split with the keyboard.
    func testSplittingAndOpening() {
        let store = EditorStore(root: root)
        store.open("a.go")
        store.split()
        XCTAssertEqual(names(store), ["a.go", "a.go"], "the same file, beside itself")
        XCTAssertEqual(store.focusedPane, 1)
        store.open("b.go")
        XCTAssertEqual(names(store), ["a.go", "b.go"])
        XCTAssertEqual(store.active?.path, "b.go")
        store.focusPane(0)
        XCTAssertEqual(store.active?.path, "a.go", "the file in front is the focused split's")
        store.open("c.go")
        XCTAssertEqual(names(store), ["c.go", "b.go"])
        // At most four.
        for _ in 0..<5 { store.split() }
        XCTAssertEqual(store.panes.count, EditorStore.maxPanes)
    }

    // Closing a file a split shows: the split shows another, one no split
    // shows; with none, it closes.
    func testClosingAFileInASplit() throws {
        let store = EditorStore(root: root)
        for name in ["a.go", "b.go", "c.go"] { store.open(name) }
        store.focusPane(0)
        store.activeID = store.documents[0].id
        store.openToSide(store.documents[1])
        XCTAssertEqual(names(store), ["a.go", "b.go"])
        store.close(try XCTUnwrap(store.documents.first { $0.path == "b.go" }))
        XCTAssertEqual(names(store), ["a.go", "c.go"], "c.go, not a.go, which the other split shows")
        store.close(try XCTUnwrap(store.documents.first { $0.path == "c.go" }))
        XCTAssertEqual(names(store), ["a.go"], "nothing else to show: the split closed")
        XCTAssertEqual(store.focusedPane, 0)
    }

    func testClosingASplitKeepsItsFiles() {
        let store = EditorStore(root: root)
        store.open("a.go")
        store.split()
        store.open("b.go")
        store.split()
        store.open("c.go")
        XCTAssertEqual(names(store), ["a.go", "b.go", "c.go"])
        store.focusPane(1)
        store.closePane(1)
        XCTAssertEqual(names(store), ["a.go", "c.go"])
        XCTAssertEqual(store.focusedPane, 0)
        XCTAssertEqual(store.documents.count, 3, "its files stay in their tabs")
        store.closePane(0)
        store.closePane(0)
        XCTAssertEqual(store.panes.count, 1, "the last split stays")
    }

    // The tab strip's menu: all, the saved, all but the one in front.
    func testClosingTabsInBulk() throws {
        let store = EditorStore(root: root)
        for name in ["a.go", "b.go", "c.go", "d.go"] { store.open(name) }
        let b = try XCTUnwrap(store.documents.first { $0.path == "b.go" })
        b.text = "package edited\n"
        store.activate(try XCTUnwrap(store.documents.first { $0.path == "c.go" }))
        store.closeOthers(than: try XCTUnwrap(store.active))
        XCTAssertEqual(store.documents.map(\.path), ["b.go", "c.go"], "unsaved edits stay")
        store.closeSaved()
        XCTAssertEqual(store.documents.map(\.path), ["b.go"])
        store.closeAll()
        XCTAssertEqual(store.closing?.path, "b.go", "unsaved edits ask before they go")
    }
}
