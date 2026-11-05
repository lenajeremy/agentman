import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// The editor's window can't be made smaller than its columns: the tree and
/// room for the code, and the agent's panel when it is open. It used to have
/// no minimum, and its columns ran past the window's right edge, taking the
/// buttons at the end of its top bar with them.
extension SiteShotsTests {
    func testTheEditorIsNeverSmallerThanItsColumns() throws {
        let world = try world(fresh: true)
        let root = try XCTUnwrap(world.folders["storefront"])
        let editor = EditorRegistry.shared.store(for: root.path)
        editors.append(editor)
        editor.open("src/checkout/CheckoutSheet.tsx")
        for panel in [false, true] {
            editor.showAgentPanel = panel
            let hosting = NSHostingView(rootView: EditorWindow(folder: root.path, client: world.store.client).environment(world.store))
            hosting.sizingOptions = [.minSize]
            let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1280, height: 800),
                                  styleMask: [.titled, .resizable, .fullSizeContentView], backing: .buffered, defer: false)
            window.isReleasedWhenClosed = false
            window.alphaValue = 0
            window.contentView = hosting
            window.orderFrontRegardless()
            RunLoop.main.run(until: Date().addingTimeInterval(1))
            let least = window.contentMinSize
            let wanted = panel ? EditorWindow.minimumWithPanel : EditorWindow.minimumWidth
            XCTAssertGreaterThanOrEqual(least.width, wanted, "panel \(panel): the window could be \(least.width) wide")
            XCTAssertGreaterThanOrEqual(least.height, EditorWindow.minimumHeight, "panel \(panel): the window could be \(least.height) tall")
            window.orderOut(nil)
        }
    }
}
