import AppKit
import SwiftUI
import WebKit
import XCTest
@testable import Agentman

/// The bars at the top of the windows take their own clicks in a normal
/// window. There the window's title bar is a strip of the window's own
/// content, and whatever sits below a bar (a conversation, a page, the
/// editor's panes) must not reach up under it and take a click meant for
/// one of its buttons. In full screen there is no title bar, which is why
/// the bars only worked there.
extension SiteShotsTests {
    func testTopBarsTakeTheirClicksInANormalWindow() throws {
        let world = try world()
        let size = CGSize(width: 1280, height: 800)
        var report: [String] = []
        for (name, item) in [("thread", SidebarItem.agent("a")), ("usage", .usage), ("agentman", .manager)] {
            world.store.selection = item
            let window = normalWindow(ContentView(), size: size, store: world.store)
            // The sidebar's header beside the window's buttons, then the page's.
            report += covered(in: window, name: name + " sidebar", from: 90, to: 250)
            report += covered(in: window, name: name, from: 270, to: size.width - 20)
            window.orderOut(nil)
        }
        // The diff and files panel open beside a thread: its tabs too.
        UserDefaults.standard.set(true, forKey: "rightPanelOpen")
        world.store.selection = .agent("a")
        let panel = normalWindow(ContentView(), size: size, store: world.store, wait: 2.4)
        report += covered(in: panel, name: "thread with panel", from: 270, to: size.width - 20)
        panel.orderOut(nil)
        UserDefaults.standard.set(false, forKey: "rightPanelOpen")
        let root = try XCTUnwrap(world.folders["storefront"])
        let editor = EditorRegistry.shared.store(for: root.path)
        editors.append(editor)
        editor.open("src/checkout/CheckoutSheet.tsx")
        let window = normalWindow(EditorWindow(folder: root.path, client: world.store.client), size: size, store: world.store, wait: 3)
        report += covered(in: window, name: "editor", from: 300, to: size.width - 20)
        window.orderOut(nil)
        print("TOPBAR-HITS\n" + report.joined(separator: "\n"))
        XCTAssertTrue(report.isEmpty, "Clicks on a top bar landed on what is below it:\n" + report.joined(separator: "\n"))
    }

    /// A window as the app's are: titled, its title bar hidden over content
    /// that fills it; transparent and out of the pointer's way.
    func normalWindow(_ view: some View, size: CGSize, store: AgentsStore, wait: TimeInterval = 1.6) -> NSWindow {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size),
                              styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        let hosting = NSHostingView(rootView: view.environment(store).frame(maxWidth: .infinity, maxHeight: .infinity).ignoresSafeArea())
        hosting.sizingOptions = []
        window.contentView = hosting
        window.setFrame(NSRect(origin: .zero, size: size), display: false)
        window.orderFrontRegardless()
        RunLoop.main.run(until: Date().addingTimeInterval(wait))
        return window
    }

    /// Where along the top bar a click would go to a scroller, a split view
    /// or a web page instead of the bar: every 20 points from `from` to `to`,
    /// 16 points down, in the bar's middle.
    private func covered(in window: NSWindow, name: String, from: CGFloat, to: CGFloat) -> [String] {
        guard let content = window.contentView, let frame = content.superview else { return ["\(name): no content"] }
        var found: [String] = []
        var x = from
        while x <= to {
            // 16 points from the top, in the coordinates hitTest takes: its
            // superview's, the window's frame view, which counts from the bottom.
            let point = content.convert(NSPoint(x: x, y: content.isFlipped ? 16 : content.bounds.height - 16), to: frame)
            // From the window's frame view, as a real click goes: the title
            // bar's own views come first there.
            guard let hit = frame.hitTest(point), hit.isDescendant(of: content) else {
                found.append("\(name) x=\(Int(x)): the title bar")
                x += 20
                continue
            }
            var view: NSView? = hit
            while let current = view {
                if current is NSScrollView || current is NSClipView || current is NSSplitView || current is WKWebView {
                    found.append("\(name) x=\(Int(x)): \(type(of: current))")
                    break
                }
                view = current.superview
            }
            x += 20
        }
        return found
    }
}
