import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// The editor's agent panel, `@` file mentions in its message box, and the
/// file tree's Vim keys.
@MainActor
final class EditorPanelTests: XCTestCase {
    private var folders: [URL] = []

    override func tearDown() {
        for folder in folders { try? FileManager.default.removeItem(at: folder) }
        folders = []
        super.tearDown()
    }

    /// A folder with files in it, removed after the test.
    private func folder(_ files: [String]) throws -> URL {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        folders.append(root)
        for file in files {
            let url = root.appendingPathComponent(file)
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            try Data(file.utf8).write(to: url)
        }
        return root.standardizedFileURL
    }

    private func session(_ id: String, _ cwd: String, _ state: SessionState, minutesAgo: Double = 0) -> Session {
        Session(id: id, kind: "claude", name: id, cwd: cwd, state: state,
                lastActivityAt: Int64(Date().addingTimeInterval(-minutesAgo * 60).timeIntervalSince1970 * 1000))
    }

    // MARK: - The panel

    func testThePanelOpensOnTheAgentWorkingOnTheFolder() {
        var sessions = [
            session("elsewhere", "/work/other", .waitingInput),
            session("idle-here", "/work/app", .idle, minutesAgo: 1),
            session("busy-inside", "/work/app/web", .busy, minutesAgo: 5),
            session("ended-here", "/work/app", .ended),
        ]
        // At work beats idle, and an agent in a folder inside counts.
        XCTAssertEqual(AgentPanel.agent(for: "/work/app", in: sessions)?.id, "busy-inside")
        // None there: the panel lists them all instead.
        XCTAssertNil(AgentPanel.agent(for: "/work/nothing", in: sessions))
        XCTAssertNil(AgentPanel.agent(for: "/work/ap", in: sessions), "a folder whose name starts the same isn't it")
        // One waiting on the user comes first, even working around the folder.
        sessions.append(session("waiting-around", "/work", .waitingInput, minutesAgo: 30))
        XCTAssertEqual(AgentPanel.agent(for: "/work/app", in: sessions)?.id, "waiting-around")
        // Two alike: the folder's own, then the latest.
        let alike = [
            session("inside", "/work/app/web", .idle, minutesAgo: 1),
            session("own-older", "/work/app", .idle, minutesAgo: 9),
            session("own-newer", "/work/app", .idle, minutesAgo: 2),
        ]
        XCTAssertEqual(AgentPanel.agent(for: "/work/app/", in: alike)?.id, "own-newer")
    }

    // am open: the editor shows the agent working on the folder, once the
    // daemon has said which agents run.
    func testAmOpenShowsTheFoldersAgent() throws {
        let saved = UserDefaults.standard.object(forKey: "agentPanelOpen")
        defer { UserDefaults.standard.set(saved, forKey: "agentPanelOpen") }
        let root = try folder(["main.go"])
        let store = EditorStore(root: root)
        store.showAgentPanel = false
        store.showsFolderAgent = true
        let sessions = [session("elsewhere", "/work/other", .busy), session("here", root.path, .idle)]

        store.showFolderAgent(in: [], loaded: false)
        XCTAssertTrue(store.showsFolderAgent, "an empty list before the daemon answers says nothing")
        XCTAssertFalse(store.showAgentPanel)
        store.showFolderAgent(in: sessions, loaded: true)
        XCTAssertEqual(store.panelRoute, .agent("here"))
        XCTAssertTrue(store.showAgentPanel)
        XCTAssertFalse(store.showsFolderAgent, "asked once, done once")

        // No agent there: the panel stays as it was.
        let quiet = EditorStore(root: try folder(["a.ts"]))
        quiet.showAgentPanel = false
        quiet.showsFolderAgent = true
        quiet.showFolderAgent(in: sessions, loaded: true)
        XCTAssertFalse(quiet.showAgentPanel)
        XCTAssertNil(quiet.panelRoute)

        // The store knows when the daemon has listed its sessions.
        let agents = AgentsStore()
        XCTAssertFalse(agents.sessionsLoaded)
        agents.apply(DaemonEvent(type: "sessions"))
        XCTAssertTrue(agents.sessionsLoaded)
    }

    // MARK: - @ mentions

    private func mentions(_ root: URL, _ files: [String], front: String? = nil) -> FileMentions {
        FileMentions(root: root, files: { files }, suggested: { front.map { [$0] } ?? [] }, load: {})
    }

    func testAtAndLettersOfferTheFolderFiles() throws {
        let files = ["src/main.ts", "src/util/format.ts", "README.md"]
        let root = try folder(files)
        let picker = mentions(root, files, front: "README.md")
        picker.update(text: "look at @mai", caret: 12)
        XCTAssertTrue(picker.isOpen)
        XCTAssertEqual(picker.results.first, "src/main.ts")
        XCTAssertEqual(picker.query, "mai")
        // Just @: the file in front first.
        picker.update(text: "@", caret: 1)
        XCTAssertEqual(picker.results, ["README.md"])
        // Not at the end of the word, not after a space, or a chip already
        // made: nothing to pick.
        picker.update(text: "@mai later", caret: 2)
        XCTAssertFalse(picker.isOpen)
        picker.update(text: "me@mai", caret: 6)
        XCTAssertFalse(picker.isOpen)
        picker.update(text: "@src/main.ts", caret: 12)
        XCTAssertFalse(picker.isOpen)
    }

    func testPickingPutsTheFileInTheBox() throws {
        let files = ["src/main.ts", "src/util/format.ts"]
        let root = try folder(files)
        let picker = mentions(root, files)
        let box = NSTextView()
        box.string = "fix @form"
        picker.textView = box
        picker.update(text: box.string, caret: 9)
        picker.move(1)
        picker.move(-1)
        picker.choose()
        XCTAssertEqual(box.string, "fix @src/util/format.ts ")
        XCTAssertFalse(picker.isOpen)
    }

    func testSentFilesAreNamedByPathsTheReaderCanOpen() throws {
        let files = ["src/main.ts", "README.md"]
        let root = try folder(files)
        let picker = mentions(root, files)
        let text = "Compare @src/main.ts with @README.md, not @nothing.ts"
        let chips = picker.chips(in: text)
        XCTAssertEqual(chips.map(\.path), ["src/main.ts", "README.md"])
        XCTAssertEqual((text as NSString).substring(with: chips[1].range), "@README.md", "the comma isn't part of it")
        // An agent in the folder reads them as its own; one in src from
        // there; Agentman, and an agent elsewhere, get full paths.
        XCTAssertEqual(picker.resolve(text, folder: root.path), text)
        XCTAssertEqual(picker.resolve(text, folder: root.appendingPathComponent("src").path),
                       "Compare @main.ts with @\(root.path)/README.md, not @nothing.ts")
        XCTAssertEqual(picker.resolve(text, folder: nil),
                       "Compare @\(root.path)/src/main.ts with @\(root.path)/README.md, not @nothing.ts")
        XCTAssertEqual(picker.resolve("No files here", folder: nil), "No files here")
    }

    // MARK: - The tree's Vim keys

    private func key(_ characters: String, code: UInt16 = 0, flags: NSEvent.ModifierFlags = []) -> NSEvent {
        NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: flags, timestamp: 0, windowNumber: 0, context: nil,
                         characters: characters, charactersIgnoringModifiers: characters, isARepeat: false, keyCode: code)!
    }

    func testTreeKeysMoveOpenAndClose() throws {
        let root = try folder(["a.ts", "b.ts", "lib/c.ts", "lib/d.ts"])
        let store = EditorStore(root: root)
        XCTAssertEqual(store.visibleTreeRows().map(\.path), ["lib", "a.ts", "b.ts"])
        XCTAssertTrue(store.handleTreeKey(key("j")))
        XCTAssertEqual(store.selection, "lib")
        // l opens the folder, and again goes into it.
        XCTAssertTrue(store.handleTreeKey(key("l")))
        XCTAssertTrue(store.isExpanded("lib"))
        XCTAssertEqual(store.visibleTreeRows().map(\.path), ["lib", "lib/c.ts", "lib/d.ts", "a.ts", "b.ts"])
        _ = store.handleTreeKey(key("j"))
        XCTAssertEqual(store.selection, "lib/c.ts")
        XCTAssertTrue(store.documents.isEmpty, "moving onto a file doesn't open it")
        // G and gg: bottom and top.
        _ = store.handleTreeKey(key("G"))
        XCTAssertEqual(store.selection, "b.ts")
        _ = store.handleTreeKey(key("g"))
        _ = store.handleTreeKey(key("g"))
        XCTAssertEqual(store.selection, "lib")
        // h on an open folder closes it; on a file, goes to its folder.
        _ = store.handleTreeKey(key("l"))
        _ = store.handleTreeKey(key("j"))
        _ = store.handleTreeKey(key("h"))
        XCTAssertEqual(store.selection, "lib")
        _ = store.handleTreeKey(key("h"))
        XCTAssertFalse(store.isExpanded("lib"))
        // Return on a folder opens it, and again closes it: the cursor stays.
        XCTAssertTrue(store.handleTreeKey(key("\r", code: 36)))
        XCTAssertTrue(store.isExpanded("lib"))
        XCTAssertEqual(store.selection, "lib")
        XCTAssertTrue(store.handleTreeKey(key("\r", code: 36)))
        XCTAssertFalse(store.isExpanded("lib"))
        XCTAssertEqual(store.selection, "lib")
        // Return on a file opens it.
        store.selectInTree("a.ts")
        XCTAssertTrue(store.handleTreeKey(key("\r", code: 36)))
        XCTAssertEqual(store.active?.path, "a.ts")
        // Ctrl-B goes back to the editor, as it came from there.
        XCTAssertTrue(store.handleTreeKey(key("b", flags: .control)))
        // A key the tree doesn't know is left to it.
        XCTAssertFalse(store.handleTreeKey(key("z")))
    }

    func testMarksAndEscape() throws {
        let root = try folder(["a.ts", "b.ts", "c.ts"])
        let store = EditorStore(root: root)
        store.selectInTree("a.ts")
        _ = store.handleTreeKey(key(" "))
        _ = store.handleTreeKey(key("v"))
        XCTAssertEqual(store.marked, ["a.ts", "b.ts"])
        XCTAssertEqual(store.selection, "c.ts")
        XCTAssertEqual(store.treeTargets, ["a.ts", "b.ts"], "the marks, not the cursor")
        _ = store.handleTreeKey(key("d"))
        XCTAssertEqual(store.trashing.map(\.path), ["a.ts", "b.ts"], "d asks first")
        // While it asks, other keys do nothing, and n keeps the files.
        XCTAssertTrue(store.handleTreeKey(key("j")))
        XCTAssertEqual(store.selection, "c.ts")
        XCTAssertTrue(store.handleTreeKey(key("n")))
        XCTAssertTrue(store.trashing.isEmpty)
        XCTAssertTrue(FileManager.default.fileExists(atPath: root.appendingPathComponent("a.ts").path))
        XCTAssertTrue(store.handleTreeKey(key("\u{1b}", code: 53)))
        XCTAssertTrue(store.marked.isEmpty)
        XCTAssertFalse(store.handleTreeKey(key("\u{1b}", code: 53)), "nothing left to clear")
        // y answers yes. (To a file that isn't there, so the test never
        // puts anything in the real Trash: it says why it couldn't.)
        store.trashing = [FileEntry(path: "gone.ts", name: "gone.ts", isDirectory: false)]
        XCTAssertTrue(store.handleTreeKey(key("y")))
        XCTAssertTrue(store.trashing.isEmpty)
        XCTAssertNotNil(store.problem)
    }

    func testAMakesFilesAndFolders() throws {
        let root = try folder(["lib/c.ts"])
        let store = EditorStore(root: root)
        XCTAssertNil(store.createInTree("app/routes/home.tsx", in: ""))
        XCTAssertTrue(FileManager.default.fileExists(atPath: root.appendingPathComponent("app/routes/home.tsx").path))
        XCTAssertTrue(store.isExpanded("app/routes"))
        XCTAssertEqual(store.active?.path, "app/routes/home.tsx", "a new file opens")
        XCTAssertNil(store.createInTree("parts/", in: "lib"))
        var isFolder: ObjCBool = false
        XCTAssertTrue(FileManager.default.fileExists(atPath: root.appendingPathComponent("lib/parts").path, isDirectory: &isFolder))
        XCTAssertTrue(isFolder.boolValue)
        XCTAssertEqual(store.selection, "lib/parts")
        XCTAssertNotNil(store.createInTree("c.ts", in: "lib"), "already there")
        XCTAssertNotNil(store.createInTree("../out.ts", in: ""), "outside the folder")
    }

    func testCopyAndMoveWithPaste() throws {
        let root = try folder(["a.ts", "lib/c.ts", ".env"])
        let store = EditorStore(root: root)
        // A copy next to a file of the same name gets a name of its own.
        store.treeClipboard = TreeClipboard(paths: ["a.ts"], cut: false)
        store.selectInTree("a.ts")
        XCTAssertNil(store.pasteInTree())
        XCTAssertTrue(FileManager.default.fileExists(atPath: root.appendingPathComponent("a copy.ts").path))
        XCTAssertNil(store.pasteInTree())
        XCTAssertTrue(FileManager.default.fileExists(atPath: root.appendingPathComponent("a copy 2.ts").path))
        // Into a folder.
        store.selectInTree("lib")
        XCTAssertNil(store.pasteInTree())
        XCTAssertTrue(FileManager.default.fileExists(atPath: root.appendingPathComponent("lib/a.ts").path))
        // A move takes open tabs along, and is done once.
        store.open("lib/c.ts")
        store.treeClipboard = TreeClipboard(paths: ["lib/c.ts"], cut: true)
        store.selectInTree("a.ts")
        XCTAssertNil(store.pasteInTree())
        XCTAssertTrue(FileManager.default.fileExists(atPath: root.appendingPathComponent("c.ts").path))
        XCTAssertFalse(FileManager.default.fileExists(atPath: root.appendingPathComponent("lib/c.ts").path))
        XCTAssertEqual(store.documents.first?.path, "c.ts")
        XCTAssertNil(store.treeClipboard)
        // A folder can't move inside itself, nor onto a name taken.
        store.treeClipboard = TreeClipboard(paths: ["lib"], cut: true)
        store.setExpanded("lib", true)
        store.selectInTree("lib/a.ts")
        XCTAssertNotNil(store.pasteInTree())
        XCTAssertEqual(EditorStore.freeName(for: ".env", in: "", root: root), ".env copy")
        XCTAssertEqual(EditorStore.freeName(for: "new.ts", in: "lib", root: root), "lib/new.ts")
    }

    // For screenshots: the message box with `@` open, and with chips.
    // Runs with TEST_RUNNER_AGENTMAN_SHOTS=<folder> set.
    func testRenderMentions() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let files = ["src/main.ts", "src/manager/mailbox.ts", "docs/maintaining.md", "README.md"]
        let root = try folder(files)
        let open = mentions(root, files)
        open.update(text: "Why does this fail? @mai", caret: 24)
        let shots: [(String, AnyView)] = [
            ("open", AnyView(Composer(text: .constant("Why does this fail? @mai"), attachments: nil, placeholder: "Message",
                                      enabled: true, onSend: {}).environment(\.fileMentions, open))),
            ("chips", AnyView(Composer(text: .constant("Explain @src/main.ts and @README.md to me"), attachments: nil,
                                       placeholder: "Message", enabled: true, onSend: {}).environment(\.fileMentions, mentions(root, files)))),
        ]
        for (shot, composer) in shots {
            for (name, appearance) in [("light", NSAppearance.Name.aqua), ("dark", .darkAqua)] {
                let page = VStack { Spacer(minLength: 0); composer }
                    .padding(16)
                    .frame(width: 400, height: shot == "open" ? 260 : 110)
                    .background(Palette.paper)
                    .environment(AgentsStore())
                let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 400, height: shot == "open" ? 260 : 110),
                                      styleMask: [.borderless], backing: .buffered, defer: false)
                window.isReleasedWhenClosed = false
                window.alphaValue = 0
                window.ignoresMouseEvents = true
                window.appearance = NSAppearance(named: appearance)
                window.contentView = NSHostingView(rootView: page)
                window.orderFrontRegardless()
                RunLoop.main.run(until: Date().addingTimeInterval(0.6))
                let view = try XCTUnwrap(window.contentView)
                let bitmap = try XCTUnwrap(view.bitmapImageRepForCachingDisplay(in: view.bounds))
                view.cacheDisplay(in: view.bounds, to: bitmap)
                try XCTUnwrap(bitmap.representation(using: .png, properties: [:]))
                    .write(to: URL(fileURLWithPath: out).appendingPathComponent("mentions-\(shot)-\(name).png"))
                window.close()
            }
        }
    }

    func testTreeChangesUndoAndRedo() throws {
        continueAfterFailure = false
        let root = try folder(["a.ts", "lib/c.ts"])
        let store = EditorStore(root: root)
        let exists = { (path: String) in FileManager.default.fileExists(atPath: root.appendingPathComponent(path).path) }
        // A new file, with the folders on its way: u takes it all away,
        // Ctrl-R brings it back.
        XCTAssertNil(store.createInTree("app/routes/home.tsx", in: ""))
        XCTAssertTrue(store.handleTreeKey(key("u")))
        XCTAssertFalse(exists("app"))
        XCTAssertTrue(store.handleTreeKey(key("r", flags: .control)))
        XCTAssertTrue(exists("app/routes/home.tsx"))
        // A rename, with its open tab following.
        store.open("a.ts")
        XCTAssertNil(store.rename(try XCTUnwrap(store.treeEntry("a.ts")), to: "b.ts"))
        store.undoTreeChange()
        XCTAssertTrue(exists("a.ts"))
        XCTAssertFalse(exists("b.ts"))
        XCTAssertEqual(store.documents.first?.path, "a.ts")
        store.redoTreeChange()
        XCTAssertTrue(exists("b.ts"))
        XCTAssertEqual(store.documents.first?.path, "b.ts")
        // A move and a copy.
        store.treeClipboard = TreeClipboard(paths: ["lib/c.ts"], cut: true)
        store.selectInTree("b.ts")
        XCTAssertNil(store.pasteInTree())
        XCTAssertTrue(exists("c.ts"))
        store.undoTreeChange()
        XCTAssertTrue(exists("lib/c.ts"))
        XCTAssertFalse(exists("c.ts"))
        XCTAssertEqual(store.selection, "lib/c.ts", "undo puts the cursor on what came back")
        store.treeClipboard = TreeClipboard(paths: ["b.ts"], cut: false)
        store.selectInTree("b.ts")
        XCTAssertNil(store.pasteInTree())
        XCTAssertTrue(exists("b copy.ts"))
        store.undoTreeChange()
        XCTAssertFalse(exists("b copy.ts"))
        // Redone, out of the Trash: undo of something made trashes it, and
        // the test leaves nothing in the real one.
        store.redoTreeChange()
        XCTAssertTrue(exists("b copy.ts"))
        // The Trash, and back: the file ends where it began.
        store.selectInTree("b.ts")
        _ = store.handleTreeKey(key("d"))
        _ = store.handleTreeKey(key("y"))
        XCTAssertFalse(exists("b.ts"))
        store.undoTreeChange()
        XCTAssertTrue(exists("b.ts"))
        // A new change ends what could be redone; nothing left says so.
        XCTAssertNil(store.createInTree("d.ts", in: ""))
        store.redoTreeChange()
        XCTAssertEqual(store.notice, "Nothing to redo")
        store.undoTreeChange()
        XCTAssertFalse(exists("d.ts"))
        store.redoTreeChange()
        XCTAssertTrue(exists("d.ts"))
    }

    // MARK: - The editor's colours

    // Every role the editor page colours code by (editor-web/src/roles.ts)
    // has a colour from the palette, in both appearances.
    func testEveryRoleHasAColour() throws {
        let roles = ["text", "comment", "string", "string.escape", "regexp", "number", "constant", "keyword", "operator",
                     "type", "type.primitive", "function", "property", "tag", "delimiter.tag", "heading", "markup.bold",
                     "markup.italic", "markup.strike", "link", "diff.inserted", "diff.deleted", "diff.changed", "invalid"]
        for name in [NSAppearance.Name.aqua, .darkAqua] {
            let theme = EditorTheme.make(for: try XCTUnwrap(NSAppearance(named: name)))
            let syntax = try XCTUnwrap(theme["syntax"] as? [String: String])
            for role in roles {
                XCTAssertNotNil(syntax[role], "\(role) in \(name.rawValue)")
            }
            XCTAssertNotEqual(syntax["function"], syntax["text"], "functions stand out from names")
            XCTAssertNotEqual(syntax["property"], syntax["type"], "keys stand out from types")
        }
    }
}
