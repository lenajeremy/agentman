import AppKit
import SwiftUI
import WebKit
import XCTest
@testable import Agentman

/// A terminal opened from a button takes the keyboard at once: under the
/// conversation, in the side panel, and in the editor. It used to show
/// without it, and typing went nowhere until it was hidden and shown again.
extension SiteShotsTests {
    func testAnOpenedTerminalTakesTheKeyboard() throws {
        let size = CGSize(width: 1280, height: 800)
        var report: [String] = []
        // A new shell runs `sleep`, not the user's own.
        TerminalSession.testCommand = ["/bin/sleep", "600"]
        defer { TerminalSession.testCommand = nil }
        for dock in ["bottom", "side"] {
            let world = try world(fresh: true)
            let session = try XCTUnwrap(world.store.session("a"))
            let editor = EditorRegistry.shared.store(for: session.cwd)
            editors.append(editor)
            UserDefaults.standard.set(dock, forKey: "terminalDock")
            world.store.selection = .agent("a")
            let window = normalWindow(ContentView(), size: size, store: world.store)
            // Typing a message first, as you would be, then the terminal
            // button: the folder's first shell starts with it.
            let box = Self.views(MessageTextView.self, in: try XCTUnwrap(window.contentView)).first
            window.makeFirstResponder(box)
            XCTAssertNotNil(box, "no message box")
            XCTAssertTrue(editor.terminals.isEmpty)
            RightPanel.showTerminal(in: session.cwd)
            RunLoop.main.run(until: Date().addingTimeInterval(1.5))
            if let problem = typing(in: window, goesTo: editor) { report.append("\(dock): \(problem)") }
            window.orderOut(nil)
            UserDefaults.standard.set(false, forKey: "rightPanelOpen")
            UserDefaults.standard.set(false, forKey: "bottomTerminalOpen")
        }
        // The editor: the code has the keyboard, then the terminal button
        // starts the folder's first shell.
        let world = try world(fresh: true)
        let root = try XCTUnwrap(world.folders["storefront"])
        let editor = EditorRegistry.shared.store(for: root.path)
        editors.append(editor)
        editor.open("src/checkout/CheckoutSheet.tsx")
        let window = normalWindow(EditorWindow(folder: root.path, client: world.store.client), size: size, store: world.store, wait: 3)
        if let code = Self.views(WKWebView.self, in: try XCTUnwrap(window.contentView)).first { window.makeFirstResponder(code) }
        XCTAssertTrue(editor.terminals.isEmpty)
        editor.showTerminal = true
        RunLoop.main.run(until: Date().addingTimeInterval(1.5))
        if let problem = typing(in: window, goesTo: editor) { report.append("editor: \(problem)") }
        window.orderOut(nil)
        XCTAssertTrue(report.isEmpty, "A terminal opened without the keyboard:\n" + report.joined(separator: "\n"))
    }

    /// Nil when the window's keyboard goes to the shell in front, else what
    /// has it instead.
    private func typing(in window: NSWindow, goesTo editor: EditorStore) -> String? {
        guard let terminal = editor.activeTerminal?.view else { return "no terminal" }
        guard terminal.window === window else { return "the terminal isn't in the window" }
        let responder = window.firstResponder
        if let view = responder as? NSView, view === terminal || view.isDescendant(of: terminal) { return nil }
        return "the keyboard went to \(responder.map { String(describing: type(of: $0)) } ?? "nothing")"
    }
}
