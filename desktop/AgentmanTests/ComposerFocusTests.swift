import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// Opening a writable chat takes the keyboard once. Later redraws,
/// reconnects and replies must respect the field the user moved to.
@MainActor
final class ComposerFocusTests: XCTestCase {
    @Observable final class State {
        var text = ""
        var enabled = true
        var focused = false
    }

    private struct Field: View {
        @Bindable var state: State
        var autofocus: Bool
        var body: some View {
            MessageField(text: $state.text, placeholder: "Message", enabled: state.enabled,
                         focused: $state.focused, autofocus: autofocus, onSubmit: {})
                .frame(width: 400, height: 80)
        }
    }

    private var window: NSWindow!
    private var host: NSHostingView<Field>!

    private func show(_ state: State, autofocus: Bool = true) throws -> MessageTextView {
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 500, height: 200),
                          styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        host = NSHostingView(rootView: Field(state: state, autofocus: autofocus))
        window.contentView = host
        settle()
        return try XCTUnwrap(SiteShotsTests.views(MessageTextView.self, in: host).first)
    }

    private func settle() {
        host.layoutSubtreeIfNeeded()
        RunLoop.main.run(until: Date().addingTimeInterval(0.08))
    }

    override func tearDown() {
        window?.close()
        window = nil
        host = nil
        super.tearDown()
    }

    private func focusAnotherField() -> NSResponder? {
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 100, height: 24))
        host.addSubview(field)
        window.makeFirstResponder(field)
        return window.firstResponder
    }

    func testAChatTakesFocusAndTypingReachesItsDraft() throws {
        let state = State()
        let text = try show(state)
        XCTAssertTrue(window.firstResponder === text)
        text.insertText("Ready to type", replacementRange: text.selectedRange())
        XCTAssertEqual(state.text, "Ready to type")
        XCTAssertTrue(state.focused)
    }

    func testOrdinaryTextAreasDoNotAutofocus() throws {
        let text = try show(State(), autofocus: false)
        XCTAssertFalse(window.firstResponder === text)
    }

    func testUpdatesAndReenablingDoNotStealFocus() throws {
        let state = State()
        _ = try show(state)
        let other = focusAnotherField()
        state.text = "Updated draft"
        settle()
        state.enabled = false
        settle()
        state.enabled = true
        settle()
        XCTAssertTrue(window.firstResponder === other)
        XCTAssertFalse(state.focused)
    }

    func testAChatInitiallyDisabledTakesFocusWhenReady() throws {
        let state = State()
        state.enabled = false
        let text = try show(state)
        XCTAssertFalse(window.firstResponder === text)
        state.enabled = true
        settle()
        XCTAssertTrue(window.firstResponder === text)
    }

    func testConnectingDoesNotStealFocusFromAnotherField() throws {
        let state = State()
        state.enabled = false
        let text = try show(state)
        let other = focusAnotherField()
        state.enabled = true
        settle()
        XCTAssertFalse(window.firstResponder === text)
        XCTAssertTrue(window.firstResponder === other)
    }
}

extension SiteShotsTests {
    func testOpeningAndSwitchingAgentChatsFocusesTheirMessageBox() throws {
        let world = try world()
        world.store.selection = .agent("a")
        let window = normalWindow(ContentView(), size: Self.common, store: world.store, wait: 0.4)
        defer { window.close() }
        let first = try XCTUnwrap(Self.views(MessageTextView.self, in: try XCTUnwrap(window.contentView)).first)
        XCTAssertTrue(window.firstResponder === first)
        first.insertText("Draft for the first agent", replacementRange: first.selectedRange())
        settle(0.1)
        XCTAssertEqual(world.store.drafts["a"], "Draft for the first agent")
        world.store.selection = .agent("d")
        settle(0.3)
        let next = try XCTUnwrap(Self.views(MessageTextView.self, in: try XCTUnwrap(window.contentView)).first)
        XCTAssertFalse(first === next)
        XCTAssertTrue(window.firstResponder === next)
        next.insertText("Draft for the next agent", replacementRange: next.selectedRange())
        settle(0.1)
        XCTAssertEqual(world.store.drafts["d"], "Draft for the next agent")
        XCTAssertEqual(world.store.drafts["a"], "Draft for the first agent")
    }

    func testBothOrchestratorChatLayoutsAutofocus() throws {
        for empty in [false, true] {
            let world = try world(fresh: true, customize: { if empty { $0.managerEntries = [] } })
            world.store.selection = .manager
            let window = normalWindow(ContentView(), size: Self.common, store: world.store, wait: 0.5)
            let text = try XCTUnwrap(Self.views(MessageTextView.self, in: try XCTUnwrap(window.contentView)).first)
            XCTAssertTrue(window.firstResponder === text, "Agentman \(empty ? "welcome" : "conversation"): \(String(describing: window.firstResponder))")
            window.close()
        }
    }

    func testOrchestratorChangingLayoutPreservesWhereTheUserIsTyping() throws {
        for moveAway in [false, true] {
            let world = try world(fresh: true, customize: { $0.managerEntries = [] })
            world.store.selection = .manager
            let window = normalWindow(ContentView(), size: Self.common, store: world.store, wait: 0.4)
            let host = try XCTUnwrap(window.contentView)
            let first = try XCTUnwrap(Self.views(MessageTextView.self, in: host).first)
            XCTAssertTrue(window.firstResponder === first)
            var other: NSResponder?
            if moveAway {
                let search = NSTextField(frame: NSRect(x: 0, y: 0, width: 120, height: 24))
                host.addSubview(search)
                window.makeFirstResponder(search)
                other = window.firstResponder
                settle(0.1)
            }
            var event = DaemonEvent(type: "manager_entry")
            event.entry = ManagerEntry(id: "reply", role: "assistant", text: "Ready.", ts: 1)
            world.store.apply(event)
            settle(0.4)
            let next = try XCTUnwrap(Self.views(MessageTextView.self, in: host).first)
            XCTAssertFalse(first === next, "Welcome is replaced by the conversation")
            if moveAway {
                XCTAssertTrue(window.firstResponder === other, "A reply must not take focus from search")
            } else {
                XCTAssertTrue(window.firstResponder === next, "Keep typing through the layout change: \(String(describing: window.firstResponder))")
            }
            window.close()
        }
    }

    func testOpeningChatsInTheEditorPanelAutofocuses() throws {
        let world = try world()
        let root = try XCTUnwrap(world.folders["storefront"])
        let editor = EditorRegistry.shared.store(for: root.path)
        editors.append(editor)
        let window = normalWindow(EditorWindow(folder: root.path, client: world.store.client),
                                  size: Self.common, store: world.store, wait: 0.4)
        defer { window.close() }
        editor.panelRoute = .agent("a")
        editor.showAgentPanel = true
        settle(0.4)
        let agent = try XCTUnwrap(Self.views(MessageTextView.self, in: try XCTUnwrap(window.contentView)).first)
        XCTAssertTrue(window.firstResponder === agent)
        editor.panelRoute = .agentman
        settle(0.4)
        let manager = try XCTUnwrap(Self.views(MessageTextView.self, in: try XCTUnwrap(window.contentView)).first)
        XCTAssertFalse(agent === manager)
        XCTAssertTrue(window.firstResponder === manager)
    }
}

extension SiteShotsTests {
    /// A preview before installing: the real chat views, with a draft
    /// inserted through the actual first responder rather than prefilled.
    func testRenderChatAutofocus() throws {
        let folder = try out
        for dark in [false, true] {
            for (name, selection) in [("agent", SidebarItem.agent("a")), ("agentman", .manager)] {
                let world = try world(fresh: true)
                world.store.selection = selection
                try shoot(ContentView(), size: Self.common, dark: dark, store: world.store,
                          prepare: { window in
                    guard let text = window.firstResponder as? MessageTextView else {
                        XCTFail("The \(name) chat did not autofocus")
                        return
                    }
                    text.insertText("Ready to type as soon as this chat opens.", replacementRange: text.selectedRange())
                    self.settle(0.2)
                }, to: folder.appendingPathComponent("autofocus-\(name)-\(dark ? "dark" : "light").png"))
            }
        }
    }
}
