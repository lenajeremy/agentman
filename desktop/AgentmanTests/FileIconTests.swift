import AppKit
import XCTest
@testable import Agentman

final class FileIconTests: XCTestCase {
    // What a Next.js project's tree shows, each by its own icon rather than
    // one sign for all code and another for all config.
    func testFilesGetTheirOwnIcons() {
        let expected = [
            "middleware.ts": "typescript", "next-env.d.ts": "typescript-def", "next.config.ts": "next",
            "package-lock.json": "nodejs", "package.json": "nodejs", "postcss.config.mjs": "postcss",
            "README.md": "readme", "skills-lock.json": "json", "test.py": "python", "tsconfig.json": "tsconfig",
            "page.tsx": "react_ts", "main.go": "go", "go.mod": "go-mod", "App.swift": "swift", "lib.rs": "rust",
            "notes.md": "markdown", "Dockerfile": "docker", ".gitignore": "git", ".env.local": "tune",
            "something.unknownext": "file",
        ]
        for (name, icon) in expected {
            XCTAssertEqual(FileIcon.asset(name), "FileIcon-" + icon, name)
        }
        XCTAssertEqual(FileIcon.asset("src", directory: true), "FileIcon-folder-src")
        XCTAssertEqual(FileIcon.asset("src", directory: true, open: true), "FileIcon-folder-src-open")
        XCTAssertEqual(FileIcon.asset("whatever", directory: true), "FileIcon-folder")
    }

    // Every icon the rules name is in the app, so none draws blank.
    func testEveryIconTheRulesNameIsBundled() {
        let named = Set(FileIconMap.fileNames.values).union(FileIconMap.extensions.values)
            .union(FileIconMap.folderNames.values).union(FileIconMap.openFolderNames.values)
            .union(["file", "folder", "folder-open"])
        for icon in named {
            XCTAssertNotNil(NSImage(named: "FileIcon-" + icon), icon)
        }
    }
}
