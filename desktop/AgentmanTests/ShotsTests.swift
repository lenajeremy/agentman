import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// Pictures of the app for the user, rendered in the test host: the Keys page
/// with the Celery sign-in, a message box with the mic, and a conversation at
/// the larger text size. Runs with TEST_RUNNER_AGENTMAN_SHOTS=<folder> set.
@MainActor
final class ShotsTests: XCTestCase {
    private func render<V: View>(_ view: V, size: CGSize, dark: Bool, to url: URL) throws {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentView = NSHostingView(rootView: view.environment(AgentsStore()).frame(width: size.width, height: size.height))
        window.orderFrontRegardless()
        RunLoop.main.run(until: Date().addingTimeInterval(0.8))
        let content = try XCTUnwrap(window.contentView)
        let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
        content.cacheDisplay(in: content.bounds, to: bitmap)
        try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: url)
        window.close()
    }

    func testRenderShots() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let folder = URL(fileURLWithPath: out)
        for dark in [false, true] {
            let look = dark ? "dark" : "light"
            try render(KeysSettings().background(Palette.paper), size: CGSize(width: 760, height: 520), dark: dark,
                       to: folder.appendingPathComponent("keys-\(look).png"))
            let messages = [
                Message(id: "1", sessionId: "s", role: .user, ts: 1, text: "The login page breaks on Safari. Can you find out why and fix it?"),
                Message(id: "2", sessionId: "s", role: .assistant, ts: 2, text: "Found it: `login.tsx` reads `window.crypto.randomUUID`, which Safari 15 doesn't have. I swapped in the polyfill we already ship and added a test.\n\n- **Fixed:** sign-in on Safari 15 and later\n- **Tests:** 48 pass"),
            ]
            let thread = VStack(alignment: .leading, spacing: 24) {
                ForEach(messages) { MessageRow(message: $0) }
                Spacer(minLength: 0)
                Composer(text: .constant(""), attachments: .constant([]), placeholder: "Message site", enabled: true, onSend: {})
            }
            .padding(28)
            .background(Palette.paper)
            try render(thread, size: CGSize(width: 720, height: 460), dark: dark, to: folder.appendingPathComponent("thread-\(look).png"))
        }
    }
}
