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
