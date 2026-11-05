import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// Losing am serve says so across the window, with the way back, and
/// stops the message box from taking messages that can't go anywhere.
extension SiteShotsTests {
    func testLosingTheDaemonShowsTheBanner() throws {
        let world = try world()
        world.store.selection = .agent("a")
        world.store.statusChanged(.unavailable("Connection refused"))
        let window = normalWindow(ContentView(), size: CGSize(width: 1280, height: 800), store: world.store)
        let content = try XCTUnwrap(window.contentView)
        let boxes = Self.views(MessageTextView.self, in: content)
        XCTAssertFalse(boxes.isEmpty, "no message box")
        XCTAssertTrue(boxes.allSatisfy { !$0.isEditable }, "the message box takes messages while disconnected")
        window.orderOut(nil)
        XCTAssertTrue(ConnectionBanner.explanation(canRestart: true).contains("until it's back"))
        XCTAssertTrue(ConnectionBanner.explanation(canRestart: false).contains("am serve"))
    }

    /// The banner as the released app shows it, which can restart its own
    /// am serve. Runs with TEST_RUNNER_AGENTMAN_AUDIT=<folder>.
    func testRenderTheBannerWithRestart() throws {
        guard let path = ProcessInfo.processInfo.environment["AGENTMAN_AUDIT"] else { throw XCTSkip("for the audit") }
        let world = try world()
        let home = FileManager.default.temporaryDirectory.appendingPathComponent("banner-" + UUID().uuidString)
        folders.append(home)
        let setup = DaemonSetup(runner: FakeAm(setup: []), shell: FakeShell(path: []),
                                paths: DaemonSetup.Paths(bundled: "/nonexistent/am", home: home.path),
                                allowed: true, automatic: false)
        let store = AgentsStore(client: world.store.client, setup: setup)
        var list = DaemonEvent(type: "sessions")
        list.sessions = world.store.sessions
        store.apply(list)
        store.selection = .agent("a")
        store.statusChanged(.unavailable("Connection refused"))
        try shoot(ContentView(), size: CGSize(width: 1280, height: 800), dark: false, store: store,
                  to: URL(fileURLWithPath: path).appendingPathComponent("main-connection-banner-restart-1280x800-light.png"))
    }
}
