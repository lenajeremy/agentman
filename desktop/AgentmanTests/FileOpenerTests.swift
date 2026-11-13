import AppKit
import XCTest
@testable import Agentman

/// Files from Finder: which editor they open in, and which files the app
/// says it opens.
@MainActor
final class FileOpenerTests: XCTestCase {
    private let home = "/Users/me"
    private func folder(_ file: String, open: [String] = [], repositories: Set<String> = []) -> String {
        EditorOpener.folder(for: file, open: open, home: home, isRepository: repositories.contains)
    }

    func testAFileOpensInItsProject() {
        let repos: Set = ["/Users/me/code/shop"]
        XCTAssertEqual(folder("/Users/me/code/shop/docs/plan.md", repositories: repos), "/Users/me/code/shop", "the top of its repository")
        XCTAssertEqual(folder("/Users/me/code/shop/README.md", repositories: repos), "/Users/me/code/shop")
        XCTAssertEqual(folder("/Users/me/Downloads/notes.txt", repositories: repos), "/Users/me/Downloads", "in no project, its own folder")
        XCTAssertEqual(folder("/Users/me/notes.txt", repositories: ["/Users/me"]), "/Users/me",
                       "a repository at home is no project: the file's folder, which is home here")
        XCTAssertEqual(folder("/Users/me/Downloads/a.txt", repositories: ["/Users/me"]), "/Users/me/Downloads",
                       "nor is home a project for what is under it")
        XCTAssertEqual(folder("/Volumes/USB/work/a.go", repositories: ["/Volumes/USB/work"]), "/Volumes/USB/work", "outside home too")
        XCTAssertEqual(folder("/tmp/a.txt", repositories: ["/"]), "/tmp", "the root is no project")
    }

    // An editor already open on a folder holding the file takes it, as a
    // tab: the deepest, when one is inside another.
    func testAnOpenEditorTakesItsFiles() {
        let repos: Set = ["/Users/me/code/shop"]
        XCTAssertEqual(folder("/Users/me/code/shop/web/src/a.ts", open: ["/Users/me/code/shop/web"], repositories: repos),
                       "/Users/me/code/shop/web", "the editor open on part of the repository")
        XCTAssertEqual(folder("/Users/me/code/shop/web/src/a.ts", open: ["/Users/me/code/shop", "/Users/me/code/shop/web"], repositories: repos),
                       "/Users/me/code/shop/web", "the deepest")
        XCTAssertEqual(folder("/Users/me/code/shop/api/main.go", open: ["/Users/me/code/shop/web"], repositories: repos),
                       "/Users/me/code/shop", "an editor on a sibling doesn't take it")
        XCTAssertEqual(folder("/Users/me/code/shopping/a.md", open: ["/Users/me/code/shop"]), "/Users/me/code/shopping",
                       "a folder whose name only begins the same doesn't hold it")
    }

    // The app says which files it opens: text of every kind by type, and
    // code no app may have given a type by its extension; never as the
    // default by itself.
    func testTheAppSaysWhichFilesItOpens() throws {
        let types = try XCTUnwrap(Bundle.main.infoDictionary?["CFBundleDocumentTypes"] as? [[String: Any]])
        XCTAssertFalse(types.isEmpty)
        for type in types {
            XCTAssertEqual(type["LSHandlerRank"] as? String, "Alternate")
            XCTAssertEqual(type["CFBundleTypeRole"] as? String, "Editor")
        }
        let contentTypes = types.flatMap { $0["LSItemContentTypes"] as? [String] ?? [] }
        for wanted in ["public.text", "net.daringfireball.markdown", "public.json", "public.yaml"] {
            XCTAssertTrue(contentTypes.contains(wanted), wanted)
        }
        let extensions = types.flatMap { $0["CFBundleTypeExtensions"] as? [String] ?? [] }
        for wanted in ["go", "rs", "toml"] {
            XCTAssertTrue(extensions.contains(wanted), wanted)
        }
    }
}
