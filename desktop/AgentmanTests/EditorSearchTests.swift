import SwiftUI
import XCTest
@testable import Agentman

@MainActor
final class EditorSearchTests: XCTestCase {
    private var folders: [URL] = []

    override func tearDown() {
        for folder in folders { try? FileManager.default.removeItem(at: folder) }
        folders = []
        super.tearDown()
    }

    private func folder(_ files: [String: String]) throws -> URL {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        folders.append(root)
        for (path, text) in files {
            let url = root.appendingPathComponent(path)
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            try Data(text.utf8).write(to: url)
        }
        return root.standardizedFileURL
    }

    private func found(_ results: SearchResults) -> [String] {
        results.files.flatMap { $0.matches.map { "\($0.path):\($0.line):\($0.column)" } }
    }

    func testWordsAreFoundAsAsked() throws {
        let files = ["a.go": "package a\n\nfunc Greet() {}\n// greet them\n", "b.md": "GREET the world, greeting all\n"]
        let root = try folder(files)
        let paths = files.keys.sorted()
        func run(_ query: String, _ options: SearchOptions = SearchOptions()) -> [String] {
            found(FolderSearch.run(query, options: options, files: paths, root: root))
        }
        XCTAssertEqual(run("greet"), ["a.go:3:6", "a.go:4:4", "b.md:1:1"], "a line once, however often")
        let both = try XCTUnwrap(FolderSearch.run("greet", options: SearchOptions(), files: paths, root: root).files.last?.matches.first)
        XCTAssertEqual(both.count, 2)
        XCTAssertEqual(both.highlights.map { (both.preview as NSString).substring(with: $0) }, ["GREET", "greet"])
        XCTAssertEqual(FolderSearch.run("greet", options: SearchOptions(), files: paths, root: root).total, 4)
        XCTAssertEqual(run("Greet", SearchOptions(caseSensitive: true)), ["a.go:3:6"])
        XCTAssertEqual(run("greet", SearchOptions(wholeWord: true)), ["a.go:3:6", "a.go:4:4", "b.md:1:1"], "greeting isn't the word")
        XCTAssertEqual(run("gre+t\\b", SearchOptions(regex: true)), ["a.go:3:6", "a.go:4:4", "b.md:1:1"])
        XCTAssertEqual(run("gre(t", SearchOptions(regex: true)), [])
        XCTAssertEqual(FolderSearch.run("gre(t", options: SearchOptions(regex: true), files: paths, root: root).problem,
                       "Not a regular expression")
        // Without regex the words are words.
        XCTAssertEqual(run("Greet()"), ["a.go:3:6"])
    }

    // Columns count as the editor counts, in UTF-16, after wide characters.
    func testColumnsAndPreviewsAreTheEditors() throws {
        let long = String(repeating: "x", count: 200)
        let root = try folder(["c.swift": "\t\tlet face = \"😀\"; let hit = 1\n\(long) needle \(long)\n"])
        let results = FolderSearch.run("hit", options: SearchOptions(), files: ["c.swift"], root: root)
        let match = try XCTUnwrap(results.files.first?.matches.first)
        XCTAssertEqual(match.column, 24, "the face is two units")
        XCTAssertEqual(match.preview, "let face = \"😀\"; let hit = 1", "the indent is trimmed")
        XCTAssertEqual((match.preview as NSString).substring(with: match.highlights[0]), "hit")

        let far = try XCTUnwrap(FolderSearch.run("needle", options: SearchOptions(), files: ["c.swift"], root: root).files.first?.matches.first)
        XCTAssertTrue(far.preview.hasPrefix("…") && far.preview.hasSuffix("…"), far.preview)
        XCTAssertEqual((far.preview as NSString).substring(with: far.highlights[0]), "needle")
        XCTAssertLessThanOrEqual(far.preview.count, FolderSearch.previewChars + 2)
    }

    // What git would ignore when the folder was walked, and binary files, are
    // left out; an open file is searched as edited.
    func testOnlyTextThatMattersIsSearched() throws {
        var binary = Data("needle".utf8)
        binary.append(0)
        let root = try folder(["src/app.ts": "const needle = 1\n", "node_modules/lib/x.js": "needle\n"])
        try binary.write(to: root.appendingPathComponent("logo.png"))
        let paths = ["logo.png", "node_modules/lib/x.js", "src/app.ts"]
        XCTAssertEqual(found(FolderSearch.run("needle", options: SearchOptions(), files: paths, root: root)), ["src/app.ts:1:7"])
        let edited = FolderSearch.run("needle", options: SearchOptions(), files: paths, root: root,
                                      edited: ["src/app.ts": "// moved\nlet needle = 2\n"])
        XCTAssertEqual(found(edited), ["src/app.ts:2:5"])
    }

    func testResultsStopAtTheLimit() throws {
        let text = String(repeating: "aa aa\n", count: FolderSearch.maxMatches / 2 + 10)
        let root = try folder(["many.txt": text, "more.txt": "aa\n"])
        let results = FolderSearch.run("aa", options: SearchOptions(), files: ["many.txt", "more.txt"], root: root)
        XCTAssertEqual(results.total, FolderSearch.maxMatches)
        XCTAssertTrue(results.truncated)
        XCTAssertEqual(results.files.map(\.path), ["many.txt"])
        XCTAssertEqual(results.files[0].matches.count, FolderSearch.maxMatches / 2, "two a line")
    }

    func testTheEditorSearchesItsFolder() async throws {
        let root = try folder(["lib/one.go": "func One() { Two() }\n", "two.go": "func Two() {}\n"])
        let store = EditorStore(root: root)
        store.runSearch("Two")
        for _ in 0..<100 where store.searchRunning || store.searchResults == nil {
            try await Task.sleep(for: .milliseconds(20))
        }
        XCTAssertEqual(found(try XCTUnwrap(store.searchResults)), ["lib/one.go:1:14", "two.go:1:6"])
        XCTAssertEqual(store.searchQuery, "Two")
    }

    // For screenshots: the search palette over the editor. Runs with
    // TEST_RUNNER_AGENTMAN_SHOTS=<folder>.
    func testRenderSearch() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let root = try folder([
            "internal/source/opencode.go": "// OpenCodeSource talks to a local OpenCode server.\nfunc NewOpenCodeSource(baseURL string) *OpenCodeSource {\n",
            "cmd/am/opencode.go": "func runOpenCode(ctx context.Context, args []string) error {\n\tsrc := source.NewOpenCodeSource(\"\")\n",
            "README.md": "Start OpenCode with `am opencode`, and OpenCodeSource finds it.\n",
        ])
        let store = EditorStore(root: root)
        store.searchQuery = "OpenCodeSource"
        store.searchResults = FolderSearch.run("OpenCodeSource", options: SearchOptions(), files: ["README.md", "cmd/am/opencode.go", "internal/source/opencode.go"], root: root)
        for (theme, appearance) in [("light", NSAppearance.Name.aqua), ("dark", .darkAqua)] {
            let page = GoToText(store: store)
                .frame(width: 760, height: 460)
                .background(Palette.paper)
            let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 760, height: 460),
                                  styleMask: [.borderless], backing: .buffered, defer: false)
            window.isReleasedWhenClosed = false
            window.alphaValue = 0
            window.ignoresMouseEvents = true
            window.appearance = NSAppearance(named: appearance)
            window.contentView = NSHostingView(rootView: page)
            window.orderFrontRegardless()
            RunLoop.main.run(until: Date().addingTimeInterval(0.8))
            let view = try XCTUnwrap(window.contentView)
            let bitmap = try XCTUnwrap(view.bitmapImageRepForCachingDisplay(in: view.bounds))
            view.cacheDisplay(in: view.bounds, to: bitmap)
            try XCTUnwrap(bitmap.representation(using: .png, properties: [:]))
                .write(to: URL(fileURLWithPath: out).appendingPathComponent("search-\(theme).png"))
            window.close()
        }
    }
}
