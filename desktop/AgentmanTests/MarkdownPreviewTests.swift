import SwiftUI
import XCTest
@testable import Agentman

@MainActor
final class MarkdownPreviewTests: XCTestCase {
    private func kinds(_ source: String) -> [String] {
        MarkdownView(source).blocks.map { block in
            let mark = block.centered ? "^" : ""
            switch block.kind {
            case .paragraph(let text): return mark + "p:" + String(text.characters)
            case .heading(let level, let text): return mark + "h\(level):" + String(text.characters)
            case .images(let images):
                return mark + "img:" + images.map { image in
                    image.source + (image.link.map { "→" + $0.absoluteString } ?? "") + (image.width.map { "@\(Int($0))" } ?? "")
                }.joined(separator: ",")
            case .code: return mark + "code"
            case .quote: return mark + "quote"
            case .list: return mark + "list"
            case .table: return mark + "table"
            case .rule: return mark + "rule"
            }
        }
    }

    // A picture on a line of its own is a picture; a row of badges, each a
    // picture in a link, is a row of them; one among words stays words.
    func testPicturesOnTheirOwnAreShown() {
        XCTAssertEqual(kinds("""
        ![Logo](docs/logo.png)

        [![CI](https://img.shields.io/ci.svg)](https://ci.example) [![Go](https://img.shields.io/go.svg)](https://go.dev)

        Words with ![a picture](a.png) among them.
        """), [
            "img:docs/logo.png",
            "img:https://img.shields.io/ci.svg→https://ci.example,https://img.shields.io/go.svg→https://go.dev",
            "p:Words with a picture among them.",
        ])
    }

    // A README's opening HTML reads as GitHub shows it: its banner, its
    // heading and its badges, centred, and the tags gone.
    func testAReadmesHTMLReadsAsItShows() {
        XCTAssertEqual(kinds("""
        <div align="center">

        <img src="docs/hero.png" alt="Agentman on a phone" width="720">

        # agentman

        **Monitor your agents.**

        [![License](https://img.shields.io/license.svg)](LICENSE)

        </div>

        After the banner.

        <p align="center"><a href="https://example.com"><img src="logo.png" alt="Logo"></a><br>Made with &amp; for agents</p>

        <!-- a note to editors -->
        <h2>Install</h2>
        """), [
            "^img:docs/hero.png@720",
            "^h1:agentman",
            "^p:Monitor your agents.",
            "^img:https://img.shields.io/license.svg→LICENSE",
            "p:After the banner.",
            "^img:logo.png→https://example.com",
            "^p:Made with & for agents",
            "h2:Install",
        ])
    }

    func testABreakInLineBreaksTheLine() {
        XCTAssertEqual(kinds("One<br>two <kbd>K</kbd>"), ["p:One\ntwo K"])
    }

    func testOnlyMarkdownFilesPreview() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        try Data("# Notes\n".utf8).write(to: root.appendingPathComponent("notes.md"))
        try Data("package main\n".utf8).write(to: root.appendingPathComponent("main.go"))
        let store = EditorStore(root: root)
        store.open("main.go")
        XCTAssertFalse(try XCTUnwrap(store.active).isMarkdown)
        store.togglePreview()
        XCTAssertFalse(try XCTUnwrap(store.active).previewing)
        store.open("notes.md")
        let notes = try XCTUnwrap(store.active)
        XCTAssertTrue(notes.isMarkdown)
        store.togglePreview()
        XCTAssertTrue(notes.previewing)
        store.togglePreview()
        XCTAssertFalse(notes.previewing)
    }

    // A README's links to the folder's files open them in the editor, as
    // GitHub opens them; the web opens as it would.
    func testLinksToTheFoldersFilesOpenThem() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        defer { try? FileManager.default.removeItem(at: root) }
        for (path, text) in ["README.md": "# Shop\n", "LICENSE": "MIT\n", "docs/setup.md": "# Setup\n", "docs/api/x.go": "package api\n"] {
            let url = root.appendingPathComponent(path)
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            try Data(text.utf8).write(to: url)
        }
        let store = EditorStore(root: root)
        store.open("docs/setup.md")
        let setup = try XCTUnwrap(store.active)
        func follow(_ link: String) -> String {
            let result = store.followLink(try! XCTUnwrap(URL(string: link)), from: setup)
            let system = String(describing: result).contains("systemAction")
            return "\(system ? "system" : "handled") \(store.active?.path ?? "-")\(store.active?.previewing == true ? " previewing" : "")"
        }
        XCTAssertEqual(follow("../LICENSE"), "handled LICENSE")
        XCTAssertEqual(follow("/README.md"), "handled README.md previewing", "from / is from the folder; Markdown opens rendered")
        XCTAssertEqual(follow("api/x.go#L1"), "handled docs/api/x.go")
        XCTAssertEqual(follow("https://example.com/docs"), "system docs/api/x.go")
        XCTAssertEqual(follow("../../elsewhere.md"), "handled docs/api/x.go", "outside the folder: nothing opens")
        XCTAssertEqual(follow("missing.md"), "handled docs/api/x.go")
        _ = follow("api")
        XCTAssertEqual(store.selection, "docs/api")
        XCTAssertTrue(store.isExpanded("docs/api"))
    }

    // For screenshots: the repo's README and a sampler, rendered as the
    // editor's preview shows them. Runs with TEST_RUNNER_AGENTMAN_SHOTS=<folder>.
    func testRenderPreview() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let repo = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let readme = try String(contentsOf: repo.appendingPathComponent("README.md"), encoding: .utf8)
        let sampler = """
        # Release checklist

        Before tagging, check the **daemon** and the *apps*:

        - [x] Go tests pass with `-race`
        - [ ] Mac app notarized
        - [ ] Phone build in TestFlight

        | Part | Version |
        | --- | --- |
        | am | 0.29.0 |
        | Mac app | 0.29.0 (33) |

        ```go
        // Greet says hello.
        func Greet(name string) string {
            return "hello " + name // politely
        }
        ```

        > Ship it once the checks are green.
        """
        for (name, source, height) in [("readme", String(readme.prefix(2600)), 1500.0), ("sampler", sampler, 900.0)] {
            for (theme, appearance) in [("light", NSAppearance.Name.aqua), ("dark", .darkAqua)] {
                let page = ScrollView {
                    MarkdownView(source)
                        .environment(\.markdownFolder, repo)
                        .frame(maxWidth: 760, alignment: .leading)
                        .padding(.horizontal, 40)
                        .padding(.vertical, 32)
                        .frame(maxWidth: .infinity)
                }
                .frame(width: 860, height: height)
                .background(Palette.surface)
                let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 860, height: height),
                                      styleMask: [.borderless], backing: .buffered, defer: false)
                window.isReleasedWhenClosed = false
                window.alphaValue = 0
                window.ignoresMouseEvents = true
                window.appearance = NSAppearance(named: appearance)
                window.contentView = NSHostingView(rootView: page)
                window.orderFrontRegardless()
                RunLoop.main.run(until: Date().addingTimeInterval(name == "readme" ? 4 : 0.8))
                let view = try XCTUnwrap(window.contentView)
                let bitmap = try XCTUnwrap(view.bitmapImageRepForCachingDisplay(in: view.bounds))
                view.cacheDisplay(in: view.bounds, to: bitmap)
                try XCTUnwrap(bitmap.representation(using: .png, properties: [:]))
                    .write(to: URL(fileURLWithPath: out).appendingPathComponent("markdown-\(name)-\(theme).png"))
                window.close()
            }
        }
    }
}
