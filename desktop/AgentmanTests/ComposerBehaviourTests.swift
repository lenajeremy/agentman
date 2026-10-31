import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// The message box as the user drives it, in an offscreen window: typing,
/// Shift-Return and Return, growing to its limit and shrinking back,
/// scrolling a long draft with the wheel though it shows no scroller,
/// selecting, and an error over it coming and going without the draft.
@MainActor
final class ComposerBehaviourTests: XCTestCase {
    @Observable
    final class Draft {
        var text = ""
        var sent = 0
        var error: String?
    }

    private struct Box: View {
        let draft: Draft
        var body: some View {
            Composer(text: Binding(get: { draft.text }, set: { draft.text = $0 }),
                     placeholder: "Message", enabled: true, onSend: { draft.sent += 1 })
                .modifier(ComposerFeedback(message: draft.error) { draft.error = nil })
                .frame(width: 540)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private var window: NSWindow!
    private var host: NSHostingView<AnyView>!

    private func show(_ view: some View, store: AgentsStore = AgentsStore()) {
        host = NSHostingView(rootView: AnyView(view.environment(store)))
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 600, height: 700), styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.contentView = host
        settle()
    }

    override func tearDown() {
        window?.close()
        window = nil
        host = nil
        super.tearDown()
    }

    private func settle() {
        host.layoutSubtreeIfNeeded()
        RunLoop.main.run(until: Date().addingTimeInterval(0.06))
        host.layoutSubtreeIfNeeded()
    }

    private func find<T: NSView>(_ type: T.Type, in view: NSView) -> T? {
        if let found = view as? T { return found }
        return view.subviews.lazy.compactMap { self.find(type, in: $0) }.first
    }

    private func key(_ text: MessageTextView, shift: Bool) {
        let event = NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: shift ? [.shift] : [],
                                     timestamp: 0, windowNumber: window.windowNumber, context: nil,
                                     characters: "\r", charactersIgnoringModifiers: "\r", isARepeat: false, keyCode: 36)!
        text.keyDown(with: event)
    }

    func testTypingGrowingScrollingAndShrinking() throws {
        let draft = Draft()
        show(Box(draft: draft))
        let field = try XCTUnwrap(find(MessageScrollView.self, in: host))
        let text = try XCTUnwrap(field.documentView as? MessageTextView)
        window.makeFirstResponder(text)

        // Typing reaches the draft; Shift-Return is a new line, Return sends.
        text.insertText("hello", replacementRange: text.selectedRange())
        XCTAssertEqual(draft.text, "hello")
        key(text, shift: true)
        XCTAssertEqual(draft.text, "hello\n")
        XCTAssertEqual(draft.sent, 0, "Shift-Return must not send")
        text.insertText("world", replacementRange: text.selectedRange())
        key(text, shift: false)
        XCTAssertEqual(draft.sent, 1, "Return sends")
        settle()
        let two = field.frame.height

        // It grows a line at a time, to ten, then holds and scrolls.
        for _ in 0..<3 { key(text, shift: true); text.insertText("more", replacementRange: text.selectedRange()) }
        settle()
        let five = field.frame.height
        XCTAssertGreaterThan(five, two + 20, "Three more lines grow the box")
        for _ in 0..<20 { key(text, shift: true); text.insertText("line", replacementRange: text.selectedRange()) }
        settle()
        let full = field.frame.height
        let font = MessageField.font(14)
        let line = ceil(font.ascender - font.descender + font.leading + MessageField.lineSpacing)
        XCTAssertEqual(full, line * 10, accuracy: 2, "Ten lines at most")
        XCTAssertGreaterThan(text.frame.height, field.contentSize.height + line * 5, "The rest is there to scroll to")

        // No scroller shows, and the wheel still scrolls.
        XCTAssertFalse(field.hasVerticalScroller)
        XCTAssertFalse(field.hasHorizontalScroller)
        field.contentView.scroll(to: NSPoint(x: 0, y: text.frame.height - field.contentSize.height))
        field.reflectScrolledClipView(field.contentView)
        let bottom = field.contentView.bounds.origin.y
        let wheel = try XCTUnwrap(CGEvent(scrollWheelEvent2Source: nil, units: .pixel, wheelCount: 1, wheel1: 60, wheel2: 0, wheel3: 0))
        field.scrollWheel(with: try XCTUnwrap(NSEvent(cgEvent: wheel)))
        RunLoop.main.run(until: Date().addingTimeInterval(0.3))
        XCTAssertLessThan(field.contentView.bounds.origin.y, bottom - 20, "The wheel scrolls toward the top")

        // Selecting keeps to what is selected.
        text.setSelectedRange(NSRange(location: 0, length: 5))
        XCTAssertEqual((text.string as NSString).substring(with: text.selectedRange()), "hello")

        // Deleting lines shrinks it back.
        text.setSelectedRange(NSRange(location: 11, length: (text.string as NSString).length - 11))
        text.delete(nil)
        settle()
        XCTAssertEqual(draft.text, "hello\nworld")
        XCTAssertEqual(field.frame.height, two, accuracy: 2, "Two lines again")
    }

    // A file named with @ shows as its icon and name, one piece the caret
    // steps over and Backspace takes whole; the draft, what is sent and a
    // copy keep its path. A path still being typed stays letters.
    func testAFileNamedWithAtIsAChip() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        let files = ["src/util/format.ts", "README.md"]
        for file in files {
            let url = root.appendingPathComponent(file)
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            try Data("x".utf8).write(to: url)
        }
        let mentions = FileMentions(root: root, files: { files }, suggested: { [] }, load: {})
        let draft = Draft()
        show(Box(draft: draft).environment(\.fileMentions, mentions))
        let field = try XCTUnwrap(find(MessageScrollView.self, in: host))
        let text = try XCTUnwrap(field.documentView as? MessageTextView)
        window.makeFirstResponder(text)
        func chips() -> [String] {
            var paths: [String] = []
            text.textStorage?.enumerateAttribute(.attachment, in: NSRange(location: 0, length: text.textStorage?.length ?? 0)) { value, _, _ in
                if let chip = (value as? NSTextAttachment)?.attachmentCell as? FileChip { paths.append(chip.path) }
            }
            return paths
        }

        // Picked from the list.
        text.insertText("fix @form", replacementRange: text.selectedRange())
        XCTAssertTrue(mentions.isOpen)
        mentions.choose()
        settle()
        XCTAssertEqual(chips(), ["src/util/format.ts"])
        XCTAssertEqual(text.string, "fix \u{FFFC} ", "One character in the box")
        XCTAssertEqual(draft.text, "fix @src/util/format.ts ", "The path in the draft")

        // Typed in full: letters while the caret ends it, a chip after.
        settle()
        text.insertText("and @README.md", replacementRange: text.selectedRange())
        XCTAssertEqual(chips(), ["src/util/format.ts"])
        settle()
        text.insertText(" now", replacementRange: text.selectedRange())
        settle()
        XCTAssertEqual(chips(), ["src/util/format.ts", "README.md"])
        XCTAssertEqual(draft.text, "fix @src/util/format.ts and @README.md now")
        XCTAssertEqual(text.selectedRange().location, (text.string as NSString).length, "The caret stays at the end")

        // A copy holds the paths.
        let board = NSPasteboard(name: NSPasteboard.Name("agentman-test-" + UUID().uuidString))
        defer { board.releaseGlobally() }
        text.selectAll(nil)
        XCTAssertTrue(text.writeSelection(to: board, types: [.string]))
        XCTAssertEqual(board.string(forType: .string), "fix @src/util/format.ts and @README.md now")

        // Backspace after a chip takes it whole. Undo and redo find every
        // step where it was: offscreen, with no events between them, the
        // steps are one, so undo takes the box back to empty.
        text.setSelectedRange(NSRange(location: 5, length: 0))
        text.deleteBackward(nil)
        XCTAssertEqual(draft.text, "fix  and @README.md now")
        let undo = try XCTUnwrap(text.undoManager)
        undo.undo()
        XCTAssertEqual(draft.text, "")
        undo.redo()
        XCTAssertEqual(draft.text, "fix  and @README.md now")
        XCTAssertEqual(chips(), ["README.md"])

        // A draft set from outside, as one waiting for the user's return.
        draft.text = "Explain @src/util/format.ts to me"
        settle()
        XCTAssertEqual(chips(), ["src/util/format.ts"])
        XCTAssertEqual(text.plainText, "Explain @src/util/format.ts to me")

        // The box measures a chip as wide as it draws: lines of them wrap
        // where the text view wraps them, and the box is as tall.
        draft.text = Array(repeating: "@src/util/format.ts", count: 12).joined(separator: " ")
        settle()
        let layout = try XCTUnwrap(text.layoutManager), container = try XCTUnwrap(text.textContainer)
        layout.ensureLayout(for: container)
        var lines = 0
        layout.enumerateLineFragments(forGlyphRange: layout.glyphRange(for: container)) { _, _, _, _, _ in lines += 1 }
        XCTAssertGreaterThan(lines, 1, "Twelve chips wrap")
        let font = MessageField.font(14)
        let line = ceil(font.ascender - font.descender + font.leading + MessageField.lineSpacing)
        XCTAssertEqual(field.frame.height, line * CGFloat(lines), accuracy: 3, "As tall as its lines")
    }

    // A sent message shows the files it names as the box did: only words
    // that name a file, inside the folder or in full; punctuation after
    // one isn't part of it.
    func testASentMessageShowsItsFilesAsTags() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        try FileManager.default.createDirectory(at: root.appendingPathComponent("src"), withIntermediateDirectories: true)
        try Data("x".utf8).write(to: root.appendingPathComponent("src/a.ts"))
        try Data("x".utf8).write(to: root.appendingPathComponent("README.md"))
        let text = "Use @src/a.ts and @README.md, not @lena or @missing.ts"
        XCTAssertEqual(FileTags.segments(text, folder: root.path), [
            .text("Use "), .file(path: "src/a.ts", directory: false), .text(" and "),
            .file(path: "README.md", directory: false), .text(", not @lena or @missing.ts"),
        ])
        XCTAssertEqual(FileTags.segments("See @\(root.path)/src", folder: nil),
                       [.text("See "), .file(path: root.path + "/src", directory: true)], "In full, and a folder")
        XCTAssertEqual(FileTags.segments("See @src/a.ts", folder: nil), [.text("See @src/a.ts")], "No folder to find it in")
    }

    // MARK: - File previews

    private func files(_ files: [String: String]) throws -> URL {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString).resolvingSymlinksInPath()
        addTeardownBlock { try? FileManager.default.removeItem(at: root) }
        for (path, text) in files {
            let url = root.appendingPathComponent(path)
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            try Data(text.utf8).write(to: url)
        }
        return root
    }

    func testAPreviewShowsTheStartOfAFile() throws {
        let long = (1...40).map { "line \($0)" }.joined(separator: "\n")
        let root = try files(["a.ts": "let a = 1\n\tindented\n", "long.txt": long, "empty.txt": "", "src/b.ts": "x"])
        try Data([0x50, 0x00, 0x51]).write(to: root.appendingPathComponent("blob.bin"))
        XCTAssertEqual(FilePreview.read(root.appendingPathComponent("a.ts")), .text(["let a = 1", "    indented", ""], more: false))
        guard case .text(let lines, let more) = FilePreview.read(root.appendingPathComponent("long.txt")) else { return XCTFail("not text") }
        XCTAssertEqual(lines.count, FilePreview.lines)
        XCTAssertTrue(more)
        XCTAssertEqual(FilePreview.read(root.appendingPathComponent("blob.bin")), .nothing("This file isn't text."))
        XCTAssertEqual(FilePreview.read(root.appendingPathComponent("empty.txt")), .nothing("An empty file."))
        XCTAssertEqual(FilePreview.read(root.appendingPathComponent("gone.txt")), .nothing("This file isn't there any more."))
        XCTAssertEqual(FilePreview.read(root), .folder(["a.ts", "blob.bin", "empty.txt", "long.txt", "src/"], more: 0))
    }

    /// Rests the pointer on the first tag in a text view, then moves it off.
    private func restOnTag(_ text: NSTextView, hover: ChipHover) throws -> (shown: Bool, closed: Bool) {
        let layout = try XCTUnwrap(text.layoutManager), container = try XCTUnwrap(text.textContainer)
        let storage = try XCTUnwrap(text.textStorage)
        layout.ensureLayout(for: container)
        var index = -1
        storage.enumerateAttribute(.attachment, in: NSRange(location: 0, length: storage.length)) { value, range, stop in
            if value != nil { index = range.location; stop.pointee = true }
        }
        XCTAssertGreaterThanOrEqual(index, 0, "No tag")
        let glyph = layout.glyphIndexForCharacter(at: index)
        let rect = layout.boundingRect(forGlyphRange: NSRange(location: glyph, length: 1), in: container)
        let origin = text.textContainerOrigin
        func move(to point: NSPoint) {
            let inWindow = text.convert(point, to: nil)
            text.mouseMoved(with: NSEvent.mouseEvent(with: .mouseMoved, location: inWindow, modifierFlags: [], timestamp: 0,
                                                     windowNumber: text.window?.windowNumber ?? 0, context: nil,
                                                     eventNumber: 0, clickCount: 0, pressure: 0)!)
        }
        move(to: NSPoint(x: rect.midX + origin.x, y: rect.midY + origin.y))
        RunLoop.main.run(until: Date().addingTimeInterval(ChipHover.delay + 0.3))
        let shown = hover.popover?.isShown == true
        move(to: NSPoint(x: text.bounds.maxX - 2, y: text.bounds.maxY - 2))
        RunLoop.main.run(until: Date().addingTimeInterval(0.1))
        return (shown, hover.popover == nil)
    }

    // Resting the pointer on a file's tag in the box shows the file; moving
    // off takes it away.
    func testRestingOnATagInTheBoxShowsItsFile() throws {
        let root = try files(["src/util/format.ts": "export const format = 1\n"])
        let mentions = FileMentions(root: root, files: { ["src/util/format.ts"] }, suggested: { [] }, load: {})
        let draft = Draft()
        draft.text = "fix @src/util/format.ts please"
        show(Box(draft: draft).environment(\.fileMentions, mentions))
        window.orderFrontRegardless()
        let text = try XCTUnwrap(find(MessageScrollView.self, in: host)?.documentView as? MessageTextView)
        XCTAssertEqual(text.hover.url("src/util/format.ts")?.path, root.appendingPathComponent("src/util/format.ts").path)
        let (shown, closed) = try restOnTag(text, hover: text.hover)
        XCTAssertTrue(shown, "Resting on the tag shows the file")
        XCTAssertTrue(closed, "Moving off takes it away")
    }

    // A sent message's tags show their files too; the conversation still
    // scrolls under the pointer; a right-click copies the message with its
    // paths.
    func testASentMessagesTagsShowTheirFiles() throws {
        let root = try files(["README.md": "# Agentman\n"])
        let rows = VStack(alignment: .trailing, spacing: 12) {
            TaggedText(text: "Read @README.md first", folder: root.path)
            ForEach(0..<60, id: \.self) { Text("Row \($0)") }
        }
        show(ScrollView { rows.frame(width: 540) }.frame(width: 560, height: 400))
        window.orderFrontRegardless()
        let text = try XCTUnwrap(find(TaggedTextView.self, in: host))
        XCTAssertEqual(text.string, "Read \u{FFFC} first")
        let (shown, closed) = try restOnTag(text, hover: text.hover)
        XCTAssertTrue(shown, "Resting on the tag shows the file")
        XCTAssertTrue(closed, "Moving off takes it away")

        let click = try XCTUnwrap(NSEvent.mouseEvent(with: .rightMouseDown, location: .zero, modifierFlags: [], timestamp: 0,
                                                     windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 1))
        let item = try XCTUnwrap(text.menu(for: click)?.items.first)
        XCTAssertEqual(item.title, "Copy")
        XCTAssertEqual(item.representedObject as? String, "Read @README.md first", "A copy holds the path")

        let scroll = try XCTUnwrap(find(NSScrollView.self, in: host))
        let before = scroll.contentView.bounds.origin.y
        let wheel = try XCTUnwrap(CGEvent(scrollWheelEvent2Source: nil, units: .pixel, wheelCount: 1, wheel1: -80, wheel2: 0, wheel3: 0))
        text.scrollWheel(with: try XCTUnwrap(NSEvent(cgEvent: wheel)))
        RunLoop.main.run(until: Date().addingTimeInterval(0.3))
        XCTAssertGreaterThan(scroll.contentView.bounds.origin.y, before + 20, "The wheel over the message scrolls the conversation")
    }

    // A skill's name typed in full becomes a chip once the caret leaves it,
    // sent as its prefix and name; resting on it shows the skill; a sent
    // message naming it shows the chip too.
    func testASkillNamedInAMessageIsAChipThatPreviews() throws {
        let root = try files(["skills/frontend-design/SKILL.md": "---\nname: frontend-design\ndescription: Design UI\n---\n# Frontend Design\n\nUse bold type.\n"])
        let skills = [SlashCommand(name: "frontend-design", description: "Design UI", source: "personal", skill: true,
                                   path: root.appendingPathComponent("skills/frontend-design/SKILL.md").path)]
        let slash = SlashCommands { .success(skills) }
        slash.prefetch()
        for _ in 0..<50 where slash.skills.isEmpty { RunLoop.main.run(until: Date().addingTimeInterval(0.01)) }
        let draft = Draft()
        show(Composer(text: Binding(get: { draft.text }, set: { draft.text = $0 }), placeholder: "Message", enabled: true,
                      slash: slash, onSend: {})
            .frame(width: 540).fixedSize(horizontal: false, vertical: true))
        window.orderFrontRegardless()
        let text = try XCTUnwrap(find(MessageScrollView.self, in: host)?.documentView as? MessageTextView)
        window.makeFirstResponder(text)

        text.insertText("Use /frontend-design", replacementRange: text.selectedRange())
        settle()
        XCTAssertFalse(text.string.contains("\u{FFFC}"), "Still being typed")
        text.insertText(" to fix it", replacementRange: text.selectedRange())
        settle()
        XCTAssertEqual(text.string, "Use \u{FFFC} to fix it")
        XCTAssertEqual(draft.text, "Use /frontend-design to fix it", "Sent as its prefix and name")

        let (shown, closed) = try restOnTag(text, hover: text.hover)
        XCTAssertTrue(shown, "Resting on the skill shows it")
        XCTAssertTrue(closed)
        XCTAssertEqual(SkillPreviewCard.instructions(skills[0].path), ["# Frontend Design", "", "Use bold type."])

        show(TaggedText(text: "Use /frontend-design to fix it, not /usr/bin", folder: nil, skills: skills).frame(width: 540))
        let sent = try XCTUnwrap(find(TaggedTextView.self, in: host))
        XCTAssertEqual(sent.string, "Use \u{FFFC} to fix it, not /usr/bin")
        XCTAssertEqual(SkillChip.title("frontend-design"), "Frontend Design")
        XCTAssertEqual(SkillChip.title("vercel:deploy"), "Vercel Deploy")
    }

    // An error over the box, short or long, then dismissed: the same text
    // view, the same draft, the focus where it was.
    func testAnErrorOverTheBoxComesAndGoesWithoutTheDraft() throws {
        let draft = Draft()
        draft.text = "Half a thought"
        show(Box(draft: draft))
        let before = try XCTUnwrap(find(MessageTextView.self, in: host))
        let plain = host.fittingSize.height

        draft.error = "Couldn't send: am serve isn't running."
        settle()
        let short = host.fittingSize.height
        XCTAssertGreaterThan(short, plain + 20, "A line of error above the box")
        XCTAssertLessThan(short, plain + 40, "A short error takes one line, about 28 points")

        draft.error = String(repeating: "The agent's terminal closed before the message reached it. ", count: 4)
        settle()
        XCTAssertGreaterThan(host.fittingSize.height, short + 20, "A long error wraps")

        // The X does this.
        draft.error = nil
        settle()
        let after = try XCTUnwrap(find(MessageTextView.self, in: host))
        XCTAssertTrue(before === after, "Dismissing the error keeps the same box")
        XCTAssertEqual(after.string, "Half a thought")
        XCTAssertEqual(host.fittingSize.height, plain, accuracy: 1)
    }

    // The whole window with an error over the box: the box stays in the
    // window. A fixed-height error text once made the split view's panes
    // taller than the window, pushing the box off its bottom.
    func testAnErrorKeepsTheWholeWindowInPlace() throws {
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        let json = """
        {"type":"sessions","sessions":[{"id":"a","kind":"claude","name":"Fix sign-in","cwd":"\(NSTemporaryDirectory())","state":"idle","inject":"tmux","lastActivityAt":\(now)}]}
        """
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-tests.sock"))
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(json.utf8)))
        store.selection = .agent("a")
        store.actionError["a"] = String(repeating: "Couldn't deliver the message: the agent's terminal closed. ", count: 3)
        host = NSHostingView(rootView: AnyView(ContentView().environment(store).frame(width: 1040, height: 640)))
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1040, height: 640), styleMask: [.titled, .resizable],
                          backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.contentView = host
        window.setContentSize(NSSize(width: 1040, height: 640))
        settle()
        RunLoop.main.run(until: Date().addingTimeInterval(0.3))
        let field = try XCTUnwrap(find(MessageScrollView.self, in: host))
        let frame = field.convert(field.bounds, to: host)
        XCTAssertTrue(host.bounds.contains(frame), "The box \(frame) is inside the window \(host.bounds)")
    }

    // The conversation shows no scroller, and still scrolls to its end.
    func testTheConversationShowsNoScroller() throws {
        let rows = (0..<40).map { Text("Row \($0)").frame(height: 30) }
        show(ScrollView { VStack { ForEach(0..<rows.count, id: \.self) { rows[$0] } } }
            .scrollIndicators(.never)
            .frame(width: 400, height: 300))
        let scroll = try XCTUnwrap(find(NSScrollView.self, in: host))
        // .hidden only fades the overlay scroller; it came back while
        // scrolling. .never takes it away.
        XCTAssertFalse(scroll.hasVerticalScroller, "No vertical scroller at all")
        XCTAssertFalse(scroll.hasHorizontalScroller)
    }
}
