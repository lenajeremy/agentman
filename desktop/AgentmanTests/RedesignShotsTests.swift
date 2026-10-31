import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// The redesigned windows for review, rendered offscreen: the main window
/// with sample agents at a compact and a large size, in light and dark, with
/// an error over the box; Agentman's chat; and the editor with its agent
/// panel. Nothing reaches a daemon: the store's socket doesn't exist.
/// Runs with TEST_RUNNER_AGENTMAN_SHOTS=<folder> set.
@MainActor
final class RedesignShotsTests: XCTestCase {
    private var folders: [URL] = []

    override func tearDown() {
        for folder in folders { try? FileManager.default.removeItem(at: folder) }
        super.tearDown()
    }

    private func folder(_ name: String, files: [String: String] = [:]) throws -> URL {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
            .appendingPathComponent(name, isDirectory: true)
        folders.append(root.deletingLastPathComponent())
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        for (path, text) in files {
            try Data(text.utf8).write(to: root.appendingPathComponent(path))
        }
        return root.resolvingSymlinksInPath()
    }

    private func store() throws -> (AgentsStore, String) {
        let site = try folder("site", files: ["README.md": "# Site\n"]).path
        let api = try folder("api").path
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        let sessions = """
        {"type":"sessions","sessions":[
         {"id":"a","kind":"claude","name":"Fix Safari sign-in","cwd":"\(site)","state":"busy","inject":"tmux","lastActivityAt":\(now - 60_000),"model":"claude-opus-5-5","servers":[{"port":3000,"command":"node","title":"Site"}]},
         {"id":"b","kind":"codex","name":"Rate limit the uploads","cwd":"\(api)","state":"waiting_input","inject":"tmux","lastActivityAt":\(now - 600_000)},
         {"id":"c","kind":"opencode","name":"Write the changelog","cwd":"\(site)","state":"idle","inject":"api","lastActivityAt":\(now - 3_600_000)}
        ]}
        """
        let messages = """
        {"type":"messages","sessionId":"a","messages":[
         {"id":"1","sessionId":"a","role":"user","ts":\(now - 300_000),"text":"The login page breaks on Safari. Can you find out why and fix it?"},
         {"id":"2","sessionId":"a","role":"tool","ts":\(now - 280_000),"tool":{"name":"Read","summary":"src/login.tsx","status":"ok","title":"Read login.tsx"}},
         {"id":"3","sessionId":"a","role":"tool","ts":\(now - 260_000),"tool":{"name":"Bash","summary":"npm test -- login","status":"ok","title":"Ran the login tests"}},
         {"id":"4","sessionId":"a","role":"assistant","ts":\(now - 240_000),"text":"Found it: `login.tsx` reads `window.crypto.randomUUID`, which Safari 15 doesn't have. I swapped in the polyfill we already ship and added a test.\\n\\n- **Fixed:** sign-in on Safari 15 and later\\n- **Tests:** 48 pass"}
        ]}
        """
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-shots.sock"))
        for json in [sessions, messages] {
            store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(json.utf8)))
        }
        store.selection = .agent("a")
        return (store, site)
    }

    private func render<V: View>(_ view: V, size: CGSize, dark: Bool, store: AgentsStore, to url: URL) throws {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.titled, .closable, .resizable, .miniaturizable],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentView = NSHostingView(rootView: view.environment(store).frame(width: size.width, height: size.height))
        window.setContentSize(size)
        window.orderFrontRegardless()
        RunLoop.main.run(until: Date().addingTimeInterval(1.2))
        let content = try XCTUnwrap(window.contentView)
        let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
        content.cacheDisplay(in: content.bounds, to: bitmap)
        try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: url)
        // Put away rather than closed: closing the last titled window ends
        // the app the tests run in, and the next test with it.
        window.orderOut(nil)
    }

    func testRenderTheRedesign() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let shots = URL(fileURLWithPath: out)
        for dark in [false, true] {
            let look = dark ? "dark" : "light"
            for (name, size) in [("compact", CGSize(width: 1040, height: 640)), ("large", CGSize(width: 1560, height: 980))] {
                let (store, _) = try store()
                try render(ContentView(), size: size, dark: dark, store: store,
                           to: shots.appendingPathComponent("thread-\(name)-\(look).png"))
            }
            let (failing, _) = try store()
            failing.actionError["a"] = "Couldn't deliver the message: the agent's terminal closed before it arrived."
            try render(ContentView(), size: CGSize(width: 1040, height: 640), dark: dark, store: failing,
                       to: shots.appendingPathComponent("error-\(look).png"))
            // The sidebar hidden: its setting kept as it was found.
            let hiddenKey = Sidebar.hiddenKey
            let wasHidden = UserDefaults.standard.object(forKey: hiddenKey)
            UserDefaults.standard.set(true, forKey: hiddenKey)
            let (collapsed, _) = try store()
            try render(ContentView(), size: CGSize(width: 1040, height: 640), dark: dark, store: collapsed,
                       to: shots.appendingPathComponent("sidebar-hidden-\(look).png"))
            if let wasHidden { UserDefaults.standard.set(wasHidden, forKey: hiddenKey) } else { UserDefaults.standard.removeObject(forKey: hiddenKey) }
            let (manager, _) = try store()
            manager.selection = .manager
            try render(ContentView(), size: CGSize(width: 1040, height: 640), dark: dark, store: manager,
                       to: shots.appendingPathComponent("manager-\(look).png"))
        }
        try renderEditor(shots)
    }

    /// The message box with a picture and a few lines in it, alone, to see
    /// the room around its text.
    func testRenderTheMessageBox() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let json = #"{"id":"a","kind":"claude","name":"Fix the sidebar","cwd":"/tmp","state":"idle","inject":"tmux","modes":["default","auto"],"mode":"auto"}"#
        let session = try JSONDecoder().decode(Session.self, from: Data(json.utf8))
        let picture = NSImage(size: CGSize(width: 220, height: 140), flipped: false) { rect in
            NSColor.white.setFill(); rect.fill()
            NSColor(white: 0.85, alpha: 1).setFill()
            NSRect(x: rect.midX - 40, y: rect.midY - 6, width: 80, height: 12).fill()
            return true
        }
        let text = "The problem is this is a bug. When you open the terminal in the chat page, switch to the terminal that shows "
            + "below the chat, and then quit the terminal, you still see \"no terminals open in this folder\" and a \"new terminal\" button."
        for dark in [false, true] {
            let box = Composer(text: .constant(text), attachments: .constant([Attachment(image: picture, data: Data(), mime: "image/png")]),
                               session: session, placeholder: "Message Fix the sidebar", enabled: true, onSend: {})
                .frame(width: 640)
                .padding(24)
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
                .background(Palette.paper)
            try render(box, size: CGSize(width: 688, height: 300), dark: dark, store: try store().0,
                       to: URL(fileURLWithPath: out).appendingPathComponent("message-box-\(dark ? "dark" : "light").png"))
        }
    }

    private func renderEditor(_ shots: URL) throws {
        let key = "agentPanelOpen"
        let was = UserDefaults.standard.object(forKey: key)
        defer {
            if let was { UserDefaults.standard.set(was, forKey: key) } else { UserDefaults.standard.removeObject(forKey: key) }
        }
        for dark in [false, true] {
            let (store, site) = try store()
            try Data("export function login() {\n  return crypto.randomUUID()\n}\n".utf8)
                .write(to: URL(fileURLWithPath: site).appendingPathComponent("login.ts"))
            let editor = EditorRegistry.shared.store(for: site)
            editor.showAgentPanel = true
            editor.panelRoute = .agent("a")
            editor.open("login.ts")
            try render(EditorWindow(folder: site, client: DaemonClient(socketPath: "/nonexistent/agentman-shots.sock")),
                       size: CGSize(width: 1180, height: 720), dark: dark, store: store,
                       to: shots.appendingPathComponent("editor-compact-\(dark ? "dark" : "light").png"))
        }
    }
}
