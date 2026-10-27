import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// Pictures of the app for the user, rendered in the test host: the Keys page
/// with the Celery sign-in, a message box with the mic, and a conversation at
/// the larger text size. Runs with TEST_RUNNER_AGENTMAN_SHOTS=<folder> set.
@MainActor
final class ShotsTests: XCTestCase {
    private func render<V: View>(_ view: V, size: CGSize, dark: Bool, to url: URL, store: AgentsStore = AgentsStore()) throws {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentView = NSHostingView(rootView: view.environment(store).frame(width: size.width, height: size.height))
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
            try renderSlash(dark: dark, look: look, folder: folder)
            try renderPictures(dark: dark, look: look, folder: folder)
        }
    }

    /// Two images read one after another: one run, the pictures side by side.
    /// AGENTMAN_SHOT_IMAGES names the two pictures, comma-separated.
    private func renderPictures(dark: Bool, look: String, folder: URL) throws {
        guard let list = ProcessInfo.processInfo.environment["AGENTMAN_SHOT_IMAGES"] else { return }
        let paths = list.split(separator: ",").map(String.init)
        let store = AgentsStore()
        for path in paths {
            store.manager.images["s\u{0}" + path] = NSImage(contentsOfFile: path)
        }
        let calls = paths.enumerated().map { index, path in
            Message(id: "r\(index)", sessionId: "s", role: .tool, ts: 2,
                    tool: ToolCall(name: "Read", summary: path, status: "ok"))
        }
        let thread = VStack(alignment: .leading, spacing: 24) {
            MessageRow(message: Message(id: "a", sessionId: "s", role: .assistant, ts: 1, text: "I'll look at both screenshots."))
            PicturesRun(sessionId: "s", calls: calls, paths: paths, said: "")
            MessageRow(message: Message(id: "b", sessionId: "s", role: .assistant, ts: 3, text: "The first is Agentman, the second the Claude app."))
            Spacer(minLength: 0)
        }
        .padding(28)
        .background(Palette.paper)
        try render(thread, size: CGSize(width: 720, height: 520), dark: dark, to: folder.appendingPathComponent("pictures-\(look).png"), store: store)
    }

    /// A command and what it printed in a thread, the "/" menu open over the
    /// box, and a panel command's sheet.
    private func renderSlash(dark: Bool, look: String, folder: URL) throws {
        let rows = try JSONDecoder().decode([Message].self, from: Data("""
        [{"id":"1","sessionId":"s","role":"user","ts":1,"text":"/rename checkout fix"},
         {"id":"2","sessionId":"s","role":"tool","ts":2,"tool":{"name":"Command","status":"ok","title":"Session renamed to: checkout fix"}},
         {"id":"3","sessionId":"s","role":"user","ts":3,"text":"/context"},
         {"id":"4","sessionId":"s","role":"tool","ts":4,"text":" Context Usage\\n⛁ ⛁ ⛀ ⛶ ⛶ ⛶  Opus 5.5\\n⛶ ⛶ ⛶ ⛶ ⛶ ⛶  34.6k/1m tokens (3%)","tool":{"name":"Command","status":"ok","title":"Context Usage"}}]
        """.utf8))
        let commands = [
            SlashCommand(name: "compact", description: "Summarize the conversation so far to free up context", argumentHint: "[instructions]", source: "builtin"),
            SlashCommand(name: "context", description: "Show how full the context window is", argumentHint: "[all]", source: "builtin"),
            SlashCommand(name: "color", description: "Set this session's prompt bar colour", argumentHint: "[color|default]", source: "builtin"),
            SlashCommand(name: "config", description: "Change a setting, such as theme=dark", argumentHint: "<key=value>", source: "builtin"),
            SlashCommand(name: "code-review", description: "Review the current diff for bugs", argumentHint: "[level] [target]", source: "bundled"),
        ]
        let slash = SlashCommands { .success(commands) }
        slash.update(text: "/co")
        RunLoop.main.run(until: Date().addingTimeInterval(0.3))
        let thread = VStack(alignment: .leading, spacing: 18) {
            ForEach(rows) { MessageRow(message: $0) }
            Spacer(minLength: 0)
            Composer(text: .constant("/co"), attachments: .constant([]), placeholder: "Message checkout", enabled: true, slash: slash, onSend: {})
        }
        .padding(28)
        .background(Palette.paper)
        try render(thread, size: CGSize(width: 720, height: 620), dark: dark, to: folder.appendingPathComponent("slash-\(look).png"))

        let usage = """
        Session
        Total cost:            $0.3525
        Total duration (wall): 28m 48s

        Current session
        █▌                                                 3% used
        Resets 11am (Europe/Dublin)

        Current week (all models)
        ██████████████████████████████████                 68% used
        Resets Oct 30 at 10pm (Europe/Dublin)

        Usage credits
        Usage credits are off · /usage-credits to turn them on
        """
        try render(CommandPanelSheet(panel: CommandPanel(command: "/usage", text: usage)), size: CGSize(width: 680, height: 420),
                   dark: dark, to: folder.appendingPathComponent("usage-sheet-\(look).png"))
    }
}
