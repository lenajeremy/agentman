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

    /// Hidden and shown again, over and over, a terminal has the keyboard
    /// every time, and at once.
    func testATerminalShownAgainTakesTheKeyboard() throws {
        let size = CGSize(width: 1280, height: 800)
        var report: [String] = []
        TerminalSession.testCommand = ["/bin/sleep", "600"]
        defer { TerminalSession.testCommand = nil }
        let world = try world(fresh: true)
        let root = try XCTUnwrap(world.folders["storefront"])
        let editor = EditorRegistry.shared.store(for: root.path)
        editors.append(editor)
        editor.open("src/checkout/CheckoutSheet.tsx")
        let window = normalWindow(EditorWindow(folder: root.path, client: world.store.client), size: size, store: world.store)
        let content = try XCTUnwrap(window.contentView)
        RunLoop.main.run(until: Date().addingTimeInterval(1.5))
        for round in 1...4 {
            if let code = Self.views(WKWebView.self, in: content).first { window.makeFirstResponder(code) }
            editor.showTerminal = true
            for wait in [0.15, 1.2] {
                RunLoop.main.run(until: Date().addingTimeInterval(wait))
                let view = editor.activeTerminal?.view
                if wait > 1, let problem = typing(in: window, goesTo: editor) { report.append("editor round \(round): \(problem)") }
                if wait > 1, (view?.frame.width ?? 0) < 10 { report.append("editor round \(round): no size") }
            }
            editor.showTerminal = false
            RunLoop.main.run(until: Date().addingTimeInterval(0.6))
        }
        window.orderOut(nil)
        XCTAssertTrue(report.isEmpty, "Shown again without the keyboard:\n" + report.joined(separator: "\n"))
    }

    func testATerminalInTheMainWindowShownAgainTakesTheKeyboard() throws {
        let size = CGSize(width: 1280, height: 800)
        var report: [String] = []
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
            let content = try XCTUnwrap(window.contentView)
            RunLoop.main.run(until: Date().addingTimeInterval(1.0))
            for round in 1...4 {
                window.makeFirstResponder(Self.views(MessageTextView.self, in: content).first)
                RightPanel.showTerminal(in: session.cwd)
                for wait in [0.15, 1.2] {
                    RunLoop.main.run(until: Date().addingTimeInterval(wait))
                    if wait > 1, let problem = typing(in: window, goesTo: editor) { report.append("\(dock) round \(round): \(problem)") }
                }
                if dock == "bottom" {
                    UserDefaults.standard.set(false, forKey: "bottomTerminalOpen")
                } else {
                    UserDefaults.standard.set(false, forKey: "rightPanelOpen")
                }
                RunLoop.main.run(until: Date().addingTimeInterval(0.6))
            }
            window.orderOut(nil)
            UserDefaults.standard.set(false, forKey: "rightPanelOpen")
            UserDefaults.standard.set(false, forKey: "bottomTerminalOpen")
        }
        XCTAssertTrue(report.isEmpty, "Shown again without the keyboard:\n" + report.joined(separator: "\n"))
    }

    /// The main window and the editor show the same folder's shells, and a
    /// shell's view can only be in one of them: it stays where it was last
    /// opened, and goes back when that's hidden.
    func testATerminalIsWhereItWasLastOpened() throws {
        let size = CGSize(width: 1280, height: 800)
        var report: [String] = []
        TerminalSession.testCommand = ["/bin/sleep", "600"]
        defer { TerminalSession.testCommand = nil }
        for dock in ["side", "bottom"] {
            let world = try world(fresh: true)
            let session = try XCTUnwrap(world.store.session("a"))
            let editor = EditorRegistry.shared.store(for: session.cwd)
            editors.append(editor)
            UserDefaults.standard.set(dock, forKey: "terminalDock")
            world.store.selection = .agent("a")
            let main = normalWindow(ContentView(), size: size, store: world.store)
            RightPanel.showTerminal(in: session.cwd)
            RunLoop.main.run(until: Date().addingTimeInterval(1.0))
            let code = normalWindow(EditorWindow(folder: session.cwd, client: world.store.client), size: size, store: world.store)
            RunLoop.main.run(until: Date().addingTimeInterval(1.0))
            func step(_ name: String, _ window: NSWindow, keyboard: Bool = true) {
                RunLoop.main.run(until: Date().addingTimeInterval(1.0))
                let view = editor.activeTerminal?.view
                let place = view?.window === code ? "editor" : view?.window === main ? "main" : "nowhere"
                if view?.window !== window {
                    report.append("\(dock) \(name): the shell is in \(place)")
                } else if keyboard, let problem = typing(in: window, goesTo: editor) {
                    report.append("\(dock) \(name): \(problem)")
                }
            }
            for round in 1...3 {
                editor.showTerminal = true
                step("round \(round) opened in the editor", code)
                // Anything that redraws the main window's terminal, as an
                // agent working does, mustn't take the shell back.
                for _ in 0..<3 {
                    editor.activeTerminalID = editor.activeTerminalID
                    RunLoop.main.run(until: Date().addingTimeInterval(0.15))
                }
                step("round \(round) main window redrawn", code)
                // What the main window shows meanwhile, and a click on it.
                if round == 2, let view = editor.activeTerminal?.view {
                    let waiting = Self.views(TerminalContainer.self, in: try XCTUnwrap(main.contentView)).first { $0.session.view === view }
                    XCTAssertEqual(waiting?.owns, false)
                    waiting?.bringHere()
                    step("round 2 clicked in the main window", main)
                    Self.views(TerminalContainer.self, in: try XCTUnwrap(code.contentView)).first { $0.session.view === view }?.bringHere()
                    step("round 2 clicked back in the editor", code)
                }
                editor.showTerminal = false
                step("round \(round) hidden in the editor", main, keyboard: false)
            }
            code.orderOut(nil)
            main.orderOut(nil)
            UserDefaults.standard.set(false, forKey: "bottomTerminalOpen")
            UserDefaults.standard.set(false, forKey: "rightPanelOpen")
        }
        XCTAssertTrue(report.isEmpty, "\n" + report.joined(separator: "\n"))
    }

    /// Every shell goes with you between the editor and the agent's page,
    /// both ways, and the keyboard comes back to the shell it was in.
    func testTerminalsFollowTheWindowYouSwitchTo() throws {
        let size = CGSize(width: 1280, height: 800)
        var report: [String] = []
        TerminalSession.testCommand = ["/bin/sleep", "600"]
        defer { TerminalSession.testCommand = nil }
        for dock in ["side", "bottom"] {
            let world = try world(fresh: true)
            let session = try XCTUnwrap(world.store.session("a"))
            let editor = EditorRegistry.shared.store(for: session.cwd)
            editors.append(editor)
            UserDefaults.standard.set(dock, forKey: "terminalDock")
            world.store.selection = .agent("a")
            let main = normalWindow(ContentView(), size: size, store: world.store)
            RightPanel.showTerminal(in: session.cwd)
            RunLoop.main.run(until: Date().addingTimeInterval(1.0))
            let code = normalWindow(EditorWindow(folder: session.cwd, client: world.store.client), size: size, store: world.store)
            editor.showTerminal = true
            editor.newTerminal()
            RunLoop.main.run(until: Date().addingTimeInterval(1.0))
            func switchTo(_ window: NSWindow, from other: NSWindow, _ name: String) {
                NotificationCenter.default.post(name: NSWindow.didResignKeyNotification, object: other)
                NotificationCenter.default.post(name: NSWindow.didBecomeKeyNotification, object: window)
                RunLoop.main.run(until: Date().addingTimeInterval(0.5))
                for shell in editor.terminals where shell.view.window !== window {
                    report.append("\(dock) \(name): \(shell.title) stayed behind")
                }
            }
            for round in 1...3 {
                switchTo(main, from: code, "round \(round) to the agent")
                switchTo(code, from: main, "round \(round) to the editor")
            }
            // Typing in a shell in the editor, away and back.
            code.makeFirstResponder(editor.activeTerminal?.view)
            switchTo(main, from: code, "away")
            switchTo(code, from: main, "back")
            if let problem = typing(in: code, goesTo: editor) { report.append("\(dock) back in the editor: \(problem)") }
            code.orderOut(nil)
            main.orderOut(nil)
            UserDefaults.standard.set(false, forKey: "bottomTerminalOpen")
            UserDefaults.standard.set(false, forKey: "rightPanelOpen")
        }
        XCTAssertTrue(report.isEmpty, "\n" + report.joined(separator: "\n"))
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
