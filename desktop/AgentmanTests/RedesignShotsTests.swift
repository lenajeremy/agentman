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

    /// The sidebar with a folder that has a logo of its own beside one that
    /// has none, in light and dark.
    func testRenderFolderLogos() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let site = try folder("site")
        let logo = NSImage(size: NSSize(width: 64, height: 64), flipped: false) { rect in
            NSColor(srgbRed: 0.98, green: 0.42, blue: 0.17, alpha: 1).setFill()
            NSBezierPath(roundedRect: rect, xRadius: 14, yRadius: 14).fill()
            NSColor.white.setFill()
            NSBezierPath(ovalIn: rect.insetBy(dx: 18, dy: 18)).fill()
            return true
        }
        let png = try XCTUnwrap(NSBitmapImageRep(data: XCTUnwrap(logo.tiffRepresentation))?.representation(using: .png, properties: [:]))
        try FileManager.default.createDirectory(at: site.appendingPathComponent("public"), withIntermediateDirectories: true)
        try png.write(to: site.appendingPathComponent("public/logo.png"))
        let api = try folder("api").path
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        let sessions = """
        {"type":"sessions","sessions":[
         {"id":"a","kind":"claude","name":"Fix Safari sign-in","cwd":"\(site.path)","state":"busy","inject":"tmux","lastActivityAt":\(now - 60_000),"folderIcon":"public/logo.png"},
         {"id":"b","kind":"codex","name":"Rate limit the uploads","cwd":"\(api)","state":"idle","inject":"tmux","lastActivityAt":\(now - 600_000)}
        ]}
        """
        for dark in [false, true] {
            let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-shots.sock"))
            store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(sessions.utf8)))
            store.selection = .agent("a")
            try render(ContentView(), size: CGSize(width: 1040, height: 400), dark: dark, store: store,
                       to: URL(fileURLWithPath: out).appendingPathComponent("folder-logos-\(dark ? "dark" : "light").png"))
        }
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

    /// A sent message naming files, under the box naming the same ones:
    /// the tags look alike.
    func testRenderSentTags() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let files = ["src/surface.test.ts", "src/components/SettingsPanels.tsx", "docs"]
        let root = try folder("site")
        for file in ["src/surface.test.ts", "src/components/SettingsPanels.tsx", "docs/x.md"] {
            let url = root.appendingPathComponent(file)
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            try Data("x".utf8).write(to: url)
        }
        let text = "Fix the flaky test in @src/surface.test.ts and align the header with @src/components/SettingsPanels.tsx before shipping."
        let mentions = FileMentions(root: root, files: { files }, suggested: { [] }, load: {})
        for dark in [false, true] {
            let page = VStack(spacing: 28) {
                MessageRow(message: Message(id: "u", sessionId: "a", role: .user, ts: 1, text: text))
                Composer(text: .constant(text), attachments: nil, placeholder: "Message", enabled: true, onSend: {})
                    .environment(\.fileMentions, mentions)
            }
            .environment(\.threadFolder, root.path)
            .frame(width: 640)
            .padding(24)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
            .background(Palette.paper)
            try render(page, size: CGSize(width: 688, height: 300), dark: dark, store: try store().0,
                       to: URL(fileURLWithPath: out).appendingPathComponent("sent-tags-\(dark ? "dark" : "light").png"))
        }

        // Files of many kinds, each icon in its own colours.
        let kinds = ["notes.md", "src/main.rs", "server.go", "app.py", "package.json", "styles.css", "Dockerfile",
                     "logo.png", "Sidebar.swift", "build.sh", "config.yaml", "index.html"]
        for file in kinds {
            let url = root.appendingPathComponent(file)
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            try Data("x".utf8).write(to: url)
        }
        let many = "Compare " + kinds.map { "@" + $0 }.joined(separator: " ") + " and say what changed."
        for dark in [false, true] {
            let page = VStack(spacing: 28) {
                MessageRow(message: Message(id: "u", sessionId: "a", role: .user, ts: 1, text: many))
                Composer(text: .constant(many), attachments: nil, placeholder: "Message", enabled: true, onSend: {})
                    .environment(\.fileMentions, FileMentions(root: root, files: { kinds }, suggested: { [] }, load: {}))
            }
            .environment(\.threadFolder, root.path)
            .frame(width: 640)
            .padding(24)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
            .background(Palette.paper)
            try render(page, size: CGSize(width: 688, height: 400), dark: dark, store: try store().0,
                       to: URL(fileURLWithPath: out).appendingPathComponent("sent-tag-kinds-\(dark ? "dark" : "light").png"))
        }
    }

    /// The preview of a file, shown over a sent message where the pointer
    /// rests on its tag: the window and the popover, each captured as it is
    /// drawn and put together where the popover opened. Then the preview of
    /// a picture and a folder.
    func testRenderFilePreview() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let root = try folder("site")
        let code = """
        import { describe, expect, it } from "vitest"
        import { renderSurface } from "../src/surface"

        describe("surface", () => {
        \tit("lays out the header before the panels", async () => {
        \t\tconst surface = await renderSurface({ width: 1280 })
        \t\texpect(surface.header.bottom).toBeLessThan(surface.panels.top)
        \t})

        \tit("keeps the settings panel in view", async () => {
        \t\tconst surface = await renderSurface({ width: 720 })
        \t\texpect(surface.settings.visible).toBe(true)
        \t})
        })

        """
        let pictures = ["src/surface.test.ts": code, "src/components/SettingsPanels.tsx": "export function SettingsPanels() {}\n",
                        "docs/intro.md": "x", "docs/setup.md": "x", "docs/images/x.png": "x"]
        for (file, text) in pictures {
            let url = root.appendingPathComponent(file)
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            try Data(text.utf8).write(to: url)
        }
        let logo = NSImage(size: NSSize(width: 480, height: 300), flipped: false) { rect in
            NSColor(calibratedRed: 0.2, green: 0.4, blue: 0.96, alpha: 1).setFill(); rect.fill()
            NSColor.white.setFill()
            NSBezierPath(roundedRect: rect.insetBy(dx: 150, dy: 90), xRadius: 24, yRadius: 24).fill()
            return true
        }
        try NSBitmapImageRep(data: logo.tiffRepresentation!)!.representation(using: .png, properties: [:])!
            .write(to: root.appendingPathComponent("logo.png"))

        let text = "Fix the flaky test in @src/surface.test.ts and align the header with @src/components/SettingsPanels.tsx before shipping."
        for dark in [false, true] {
            let look = dark ? "dark" : "light"
            let page = VStack(spacing: 0) {
                Spacer(minLength: 0)
                MessageRow(message: Message(id: "u", sessionId: "a", role: .user, ts: 1, text: text))
            }
            .environment(\.threadFolder, root.path)
            .frame(width: 640)
            .padding(24)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Palette.paper)
            let size = CGSize(width: 688, height: 520)
            let window = NSWindow(contentRect: NSRect(origin: NSPoint(x: 200, y: 200), size: size), styleMask: [.borderless], backing: .buffered, defer: false)
            window.isReleasedWhenClosed = false
            window.alphaValue = 0
            window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
            window.contentView = NSHostingView(rootView: page.environment(try store().0).frame(width: size.width, height: size.height))
            window.orderFrontRegardless()
            RunLoop.main.run(until: Date().addingTimeInterval(1))
            let content = try XCTUnwrap(window.contentView)
            func views<T: NSView>(_ type: T.Type, in view: NSView) -> [T] {
                ((view as? T).map { [$0] } ?? []) + view.subviews.flatMap { views(type, in: $0) }
            }
            let tagged = try XCTUnwrap(views(TaggedTextView.self, in: content).first)
            let layout = try XCTUnwrap(tagged.layoutManager), container = try XCTUnwrap(tagged.textContainer)
            layout.ensureLayout(for: container)
            let chip = (tagged.string as NSString).range(of: "\u{FFFC}").location
            let rect = layout.boundingRect(forGlyphRange: NSRange(location: layout.glyphIndexForCharacter(at: chip), length: 1), in: container)
            let point = tagged.convert(NSPoint(x: rect.midX, y: rect.midY), to: nil)
            tagged.mouseMoved(with: try XCTUnwrap(NSEvent.mouseEvent(with: .mouseMoved, location: point, modifierFlags: [], timestamp: 0,
                                                                    windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 0, pressure: 0)))
            RunLoop.main.run(until: Date().addingTimeInterval(ChipHover.delay + 0.8))
            let popover = try XCTUnwrap(tagged.hover.popover, "No preview")
            let shown = try XCTUnwrap(popover.contentViewController?.view.window)
            shown.appearance = window.appearance
            RunLoop.main.run(until: Date().addingTimeInterval(0.3))
            let card = try XCTUnwrap(popover.contentViewController?.view)
            let cardFrame = shown.convertToScreen(card.convert(card.bounds, to: nil))
            let at = NSRect(x: cardFrame.minX - window.frame.minX, y: cardFrame.minY - window.frame.minY,
                            width: cardFrame.width, height: cardFrame.height)

            // The window, with the preview over it where the popover opened.
            // Offscreen, AppKit draws a popover's blur and frame as white,
            // so the same card is drawn in a window of its own, on a plain
            // frame that stands in for the popover's.
            let scale: CGFloat = 2
            let base = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
            content.cacheDisplay(in: content.bounds, to: base)
            // About a popover's own colours in this look.
            let fill = dark ? NSColor(white: 0.12, alpha: 1) : NSColor(white: 1, alpha: 1)
            let edge = dark ? NSColor(white: 0.24, alpha: 1) : NSColor(white: 0.88, alpha: 1)
            let cardWindow = NSWindow(contentRect: NSRect(origin: .zero, size: at.size), styleMask: [.borderless], backing: .buffered, defer: false)
            cardWindow.isReleasedWhenClosed = false
            cardWindow.alphaValue = 0
            cardWindow.appearance = window.appearance
            cardWindow.contentView = NSHostingView(rootView: FilePreviewCard(url: root.appendingPathComponent("src/surface.test.ts"), base: root.path)
                .frame(width: at.width, height: at.height).background(Color(nsColor: fill)))
            cardWindow.orderFrontRegardless()
            RunLoop.main.run(until: Date().addingTimeInterval(0.5))
            let cardView = try XCTUnwrap(cardWindow.contentView)
            let over = try XCTUnwrap(cardView.bitmapImageRepForCachingDisplay(in: cardView.bounds))
            cardView.cacheDisplay(in: cardView.bounds, to: over)
            cardWindow.orderOut(nil)
            let picture = NSImage(size: size, flipped: false) { _ in
                base.draw(in: NSRect(origin: .zero, size: size))
                let frame = NSBezierPath(roundedRect: at, xRadius: 10, yRadius: 10)
                NSGraphicsContext.saveGraphicsState()
                let shadow = NSShadow()
                shadow.shadowColor = .black.withAlphaComponent(0.25)
                shadow.shadowBlurRadius = 16
                shadow.shadowOffset = NSSize(width: 0, height: -4)
                shadow.set()
                fill.setFill()
                frame.fill()
                NSGraphicsContext.restoreGraphicsState()
                NSGraphicsContext.saveGraphicsState()
                frame.addClip()
                over.draw(in: at)
                NSGraphicsContext.restoreGraphicsState()
                edge.setStroke()
                frame.stroke()
                return true
            }
            let rep = try XCTUnwrap(NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(size.width * scale), pixelsHigh: Int(size.height * scale),
                                                     bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                                     colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0))
            rep.size = size
            NSGraphicsContext.saveGraphicsState()
            NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
            NSAppearance(named: dark ? .darkAqua : .aqua)?.performAsCurrentDrawingAppearance {
                picture.draw(in: NSRect(origin: .zero, size: size))
            }
            NSGraphicsContext.restoreGraphicsState()
            try XCTUnwrap(rep.representation(using: .png, properties: [:]))
                .write(to: URL(fileURLWithPath: out).appendingPathComponent("file-preview-\(look).png"))
            tagged.hover.close()
            window.orderOut(nil)

            // A picture's preview, and a folder's.
            for (name, path) in [("picture", "logo.png"), ("folder", "docs")] {
                try render(FilePreviewCard(url: root.appendingPathComponent(path), base: root.path).background(Palette.surface)
                            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading).background(Palette.paper),
                           size: CGSize(width: 480, height: name == "picture" ? 270 : 170), dark: dark, store: try store().0,
                           to: URL(fileURLWithPath: out).appendingPathComponent("file-preview-\(name)-\(look).png"))
            }
        }
    }

    /// Skills named in a message: the "/" list mid-sentence, offering skills
    /// alone; a message naming a file and a skill, as typed and as sent; and
    /// the glance at a skill the pointer rests on.
    func testRenderSkillTags() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let root = try folder("site")
        let skillFile = root.appendingPathComponent("skills/frontend-design/SKILL.md")
        for (path, text) in [
            "src/surface.test.ts": "x",
            "skills/frontend-design/SKILL.md": """
            ---
            name: frontend-design
            description: Create distinctive, production-grade frontend interfaces with high design quality.
            ---
            # Frontend Design

            Commit to a bold aesthetic direction before writing any code.
            - Typography: pick a characterful display face and a refined body face.
            - Colour: a dominant colour with sharp accents beats an even palette.
            - Motion: one well-orchestrated page load beats scattered effects.

            Match the implementation's complexity to the vision.
            """,
        ] {
            let url = root.appendingPathComponent(path)
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            try Data(text.utf8).write(to: url)
        }
        let skills = [
            SlashCommand(name: "frontend-design", description: "Create distinctive, production-grade frontend interfaces with high design quality.",
                         source: "personal", skill: true, path: skillFile.path),
            SlashCommand(name: "frontend-review", description: "Review a page for layout and accessibility problems", source: "project", skill: true, path: nil),
            SlashCommand(name: "vercel:deploy", description: "Deploy to Vercel", source: "plugin", skill: true, path: nil),
            SlashCommand(name: "compact", source: "builtin"),
        ]
        let mentions = FileMentions(root: root, files: { ["src/surface.test.ts"] }, suggested: { [] }, load: {})
        func waitedSlash() -> SlashCommands {
            let slash = SlashCommands { .success(skills) }
            slash.prefetch()
            for _ in 0..<50 where slash.skills.isEmpty { RunLoop.main.run(until: Date().addingTimeInterval(0.01)) }
            return slash
        }
        let sent = "Fix the flaky test in @src/surface.test.ts and polish the header with /frontend-design before shipping."
        for dark in [false, true] {
            let look = dark ? "dark" : "light"

            // The list, mid-sentence: skills alone.
            let typing = "Fix the flaky test in @src/surface.test.ts and polish the header with /front"
            let menu = waitedSlash()
            menu.update(text: typing, caret: (typing as NSString).length)
            let typed = VStack {
                Spacer(minLength: 0)
                Composer(text: .constant(typing), attachments: nil, placeholder: "Message", enabled: true, slash: menu, onSend: {})
                    .environment(\.fileMentions, mentions)
            }
            .frame(width: 640).padding(24).frame(maxWidth: .infinity, maxHeight: .infinity).background(Palette.paper)
            try render(typed, size: CGSize(width: 688, height: 380), dark: dark, store: try store().0,
                       to: URL(fileURLWithPath: out).appendingPathComponent("skill-menu-\(look).png"))

            // As typed and as sent.
            let both = VStack(spacing: 28) {
                MessageRow(message: Message(id: "u", sessionId: "a", role: .user, ts: 1, text: sent))
                Composer(text: .constant(sent), attachments: nil, placeholder: "Message", enabled: true, slash: waitedSlash(), onSend: {})
                    .environment(\.fileMentions, mentions)
            }
            .environment(\.threadFolder, root.path)
            .environment(\.threadSkills, skills)
            .frame(width: 640).padding(24).frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top).background(Palette.paper)
            try render(both, size: CGSize(width: 688, height: 300), dark: dark, store: try store().0,
                       to: URL(fileURLWithPath: out).appendingPathComponent("skill-tags-\(look).png"))

            // The glance at a skill.
            try render(SkillPreviewCard(command: skills[0]).background(dark ? Color(white: 0.12) : Color.white)
                        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading).background(Palette.paper),
                       size: CGSize(width: 480, height: 270), dark: dark, store: try store().0,
                       to: URL(fileURLWithPath: out).appendingPathComponent("skill-preview-\(look).png"))
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
