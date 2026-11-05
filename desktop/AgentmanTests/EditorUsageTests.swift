import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// ⌘K and Usage in the editor, without going back to the main window.
extension SiteShotsTests {
    func testThePaletteOpensOverTheEditorInFront() throws {
        let world = try world(fresh: true)
        let root = try XCTUnwrap(world.folders["storefront"])
        let editor = EditorRegistry.shared.store(for: root.path)
        editors.append(editor)
        editor.open("src/checkout/CheckoutSheet.tsx")
        world.store.showPalette = true
        world.store.paletteEditor = editor.root.path
        defer { world.store.showPalette = false; world.store.paletteEditor = nil }

        let editorWindow = normalWindow(EditorWindow(folder: root.path, client: world.store.client), size: CGSize(width: 1280, height: 800),
                                        store: world.store, wait: 2)
        XCTAssertTrue(hasPalette(in: editorWindow), "no palette over the editor")
        editorWindow.orderOut(nil)

        let main = normalWindow(ContentView(), size: CGSize(width: 1280, height: 800), store: world.store)
        XCTAssertFalse(hasPalette(in: main), "the main window opened the palette meant for the editor")
        main.orderOut(nil)

        // Chosen over an editor, agents, Agentman and Usage stay in it.
        XCTAssertTrue(CommandPalette.staysInEditor("agent:a"))
        XCTAssertTrue(CommandPalette.staysInEditor("agentman"))
        XCTAssertTrue(CommandPalette.staysInEditor("usage"))
        XCTAssertFalse(CommandPalette.staysInEditor("new"))
        XCTAssertFalse(CommandPalette.staysInEditor("settings"))
    }

    private func hasPalette(in window: NSWindow) -> Bool {
        guard let content = window.contentView else { return false }
        return Self.views(NSTextField.self, in: content).contains { ($0.placeholderString ?? "").hasPrefix("Go to an agent") }
    }

    /// The palette over the editor, and the Usage popover's content. Runs
    /// with TEST_RUNNER_AGENTMAN_AUDIT=<folder>.
    func testRenderTheEditorsPaletteAndUsage() throws {
        guard let path = ProcessInfo.processInfo.environment["AGENTMAN_AUDIT"] else { throw XCTSkip("for the audit") }
        let folder = URL(fileURLWithPath: path)
        let world = try world(fresh: true, more: extras)
        let root = try XCTUnwrap(world.folders["storefront"])
        let editor = EditorRegistry.shared.store(for: root.path)
        editors.append(editor)
        editor.open("src/checkout/CheckoutSheet.tsx")
        world.store.showPalette = true
        world.store.paletteEditor = editor.root.path
        // The code's web page left out: a view over it draws in pieces
        // offscreen, as Go to File's does, though not on screen.
        try shoot(EditorWindow(folder: root.path, client: world.store.client), size: CGSize(width: 1280, height: 800), dark: false,
                  store: world.store, wait: 4, titled: false, buttons: false, webPages: false,
                  to: folder.appendingPathComponent("editor-palette-1280x800-light.png"))
        world.store.showPalette = false
        world.store.paletteEditor = nil
        for dark in [false, true] {
            try shoot(UsagePopover {}.background(Palette.paper), size: CGSize(width: 560, height: 500), dark: dark, store: world.store,
                      wait: 2.5, titled: false, buttons: false,
                      to: folder.appendingPathComponent("editor-usage-popover-\(dark ? "dark" : "light").png"))
        }
    }
}
