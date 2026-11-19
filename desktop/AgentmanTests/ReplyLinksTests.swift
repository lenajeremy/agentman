import SwiftUI
import XCTest
@testable import Agentman

/// Links agents write to files on the Mac, which macOS refused with -50.
final class ReplyLinksTests: XCTestCase {
    private var folder: URL!

    override func setUpWithError() throws {
        folder = FileManager.default.temporaryDirectory.appendingPathComponent("reply-links-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: folder.appendingPathComponent("src/checkout"), withIntermediateDirectories: true)
        try "x".write(to: folder.appendingPathComponent("src/checkout/Sheet.tsx"), atomically: true, encoding: .utf8)
        try "x".write(to: folder.appendingPathComponent("app.ts"), atomically: true, encoding: .utf8)
        try "x".write(to: folder.appendingPathComponent("Design notes.md"), atomically: true, encoding: .utf8)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: folder)
    }

    private func target(_ link: String) -> ReplyLinks.Target? {
        URL(string: link).flatMap { ReplyLinks.target(of: $0, folder: folder.path) }
    }

    private func path(_ relative: String) -> String {
        folder.appendingPathComponent(relative).standardizedFileURL.path
    }

    func testAFileInTheAgentsFolder() {
        XCTAssertEqual(target("src/checkout/Sheet.tsx")?.url.path, path("src/checkout/Sheet.tsx"))
        XCTAssertEqual(target("src/checkout/Sheet.tsx")?.isDirectory, false)
        XCTAssertNil(target("src/checkout/Sheet.tsx")?.line)
    }

    func testAnAbsolutePathAndAFileURL() {
        let full = path("src/checkout/Sheet.tsx")
        XCTAssertEqual(target(full)?.url.path, full)
        XCTAssertEqual(target(URL(fileURLWithPath: full).absoluteString)?.url.path, full)
        // From the folder, as GitHub reads "/src/…".
        XCTAssertEqual(target("/src/checkout/Sheet.tsx")?.url.path, full)
    }

    func testALinkThatNamesALine() {
        XCTAssertEqual(target("src/checkout/Sheet.tsx:42")?.line, 42)
        XCTAssertEqual(target("src/checkout/Sheet.tsx:42:7")?.line, 42)
        XCTAssertEqual(target("src/checkout/Sheet.tsx#L42")?.line, 42)
        XCTAssertEqual(target("src/checkout/Sheet.tsx:42")?.url.path, path("src/checkout/Sheet.tsx"))
    }

    /// "app.ts:42" is a scheme and a resource to URL.
    func testANameURLTakesForAScheme() {
        XCTAssertEqual(target("app.ts:42")?.url.path, path("app.ts"))
        XCTAssertEqual(target("app.ts:42")?.line, 42)
        XCTAssertEqual(target("app.ts")?.url.path, path("app.ts"))
    }

    func testAFolderAndSpaces() {
        XCTAssertEqual(target("src/checkout")?.isDirectory, true)
        XCTAssertEqual(target("Design%20notes.md")?.url.path, path("Design notes.md"))
    }

    func testTheWebAndNothing() {
        XCTAssertNil(target("https://example.com/src/app.ts"))
        XCTAssertNil(target("mailto:someone@example.com"))
        XCTAssertNil(target("src/missing.ts"))
        XCTAssertNil(target("/no/such/file"))
    }

    func testHome() throws {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        guard folder.path.hasPrefix(home) else { throw XCTSkip("temporary folder outside home") }
        let tilde = "~" + path("app.ts").dropFirst(home.count)
        XCTAssertEqual(target(tilde)?.url.path, path("app.ts"))
    }

    /// A file link is never passed to macOS, which refused it with -50.
    func testWhatFollowingALinkDoes() throws {
        XCTAssertEqual(ReplyLinks.decide(try XCTUnwrap(URL(string: "app.ts:3")), folder: folder.path),
                       .file(.init(url: URL(fileURLWithPath: path("app.ts")), line: 3, isDirectory: false)))
        XCTAssertEqual(ReplyLinks.decide(try XCTUnwrap(URL(string: "src/missing.ts")), folder: folder.path), .missing)
        XCTAssertEqual(ReplyLinks.decide(try XCTUnwrap(URL(string: "Makefile:12")), folder: folder.path), .missing)
        XCTAssertEqual(ReplyLinks.decide(try XCTUnwrap(URL(string: "https://example.com")), folder: folder.path), .elsewhere)
        XCTAssertEqual(ReplyLinks.decide(try XCTUnwrap(URL(string: "agentman://agent/x")), folder: folder.path), .elsewhere)
    }

    /// In the editor's panel a reply's file opens in a tab, at its line.
    @MainActor
    func testTheEditorOpensAFile() throws {
        let editor = EditorStore(root: folder)
        _ = editor.followReplyLink(try XCTUnwrap(URL(string: "src/checkout/Sheet.tsx:9")), folder: folder.path)
        XCTAssertEqual(editor.active?.path, "src/checkout/Sheet.tsx")
        _ = editor.followReplyLink(try XCTUnwrap(URL(string: "src/checkout")), folder: folder.path)
        XCTAssertEqual(editor.selection, "src/checkout")
        // The web leaves the tabs as they were.
        _ = editor.followReplyLink(try XCTUnwrap(URL(string: "https://example.com")), folder: folder.path)
        XCTAssertEqual(editor.documents.count, 1)
    }
}
