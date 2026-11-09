import AppKit
import SwiftUI
import WebKit
import XCTest
@testable import Agentman

/// Pictures of Agentman for Mac for the website, rendered offscreen from
/// made-up projects and agents: the main window with an agent's thread, its
/// diff, an agent waiting for approval, Agentman's chat, search, the command
/// palette, the Usage page and its Limits tab, and the editor. Nothing
/// reaches a daemon: a fake one answers every request from the fixtures
/// below, and the projects are git repositories in a temporary folder, so
/// branches, changes and diffs are real.
///
/// Each picture is drawn at twice its size in points, in sRGB, with the
/// window's buttons drawn in (offscreen they are not part of the content),
/// and the code editor's page snapshotted and laid over the window, as
/// caching a view's display leaves a web view blank.
///
/// Runs with TEST_RUNNER_AGENTMAN_SHOTS=<folder> set; skipped otherwise.
/// TEST_RUNNER_AGENTMAN_SHOTS_LOOKS=light (or dark) draws one look alone.
@MainActor
final class SiteShotsTests: XCTestCase {
    var folders: [URL] = []
    var savedDefaults: [(String, Any?)] = []
    var editors: [EditorStore] = []
    var sharedFolders: [String: URL]?

    /// Every setting the windows read, kept as found and put back after.
    private static let settingKeys = [
        "rightPanelOpen", "rightPanelTab", "rightPanelTabs", "rightPanelWidth", "sidebarHidden", "sidebarWidth",
        "bottomTerminalOpen", "bottomTerminalHeight", "terminalDock", "sidebarWorkingOpen", "sidebarArchivedOpen",
        "agentPanelOpen", "agentPanelWidth", "terminalHeight", "usageDays", "usageMetric", "usageBreakdown",
        "diffSplit", "diffWrap", "diffFileTree", "diffIgnoreWhitespace", "editorVim", "editorFontSize", "drafts",
        "managerLastRead", "sidebarChoices", "legacyEditor", "settingsTab", "newAgentKind", "newAgentFolder",
    ]

    override func setUp() {
        super.setUp()
        continueAfterFailure = false
        let defaults = UserDefaults.standard
        savedDefaults = Self.settingKeys.map { ($0, defaults.object(forKey: $0)) }
        for key in Self.settingKeys { defaults.removeObject(forKey: key) }
        defaults.set(false, forKey: "rightPanelOpen")
        defaults.set(false, forKey: "sidebarHidden")
        defaults.set(false, forKey: "bottomTerminalOpen")
        defaults.set(true, forKey: "sidebarWorkingOpen")
        defaults.set(false, forKey: "agentPanelOpen")
        defaults.set(false, forKey: "editorVim")
        defaults.set(false, forKey: "legacyEditor")
    }

    override func tearDown() {
        for editor in editors {
            for terminal in editor.terminals { editor.closeTerminal(terminal) }
        }
        editors = []
        let defaults = UserDefaults.standard
        for (key, value) in savedDefaults {
            if let value { defaults.set(value, forKey: key) } else { defaults.removeObject(forKey: key) }
        }
        for folder in folders { try? FileManager.default.removeItem(at: folder) }
        folders = []
        super.tearDown()
    }

    var out: URL {
        get throws {
            guard let path = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
            return URL(fileURLWithPath: path)
        }
    }

    // MARK: - The pictures

    /// The looks to draw: TEST_RUNNER_AGENTMAN_SHOTS_LOOKS=light (or dark)
    /// draws one alone; both by default.
    private var looks: [Bool] {
        switch ProcessInfo.processInfo.environment["AGENTMAN_SHOTS_LOOKS"] {
        case "light": [false]
        case "dark": [true]
        default: [false, true]
        }
    }

    /// The four on the home page: Agentman's chat with a card waiting
    /// (mac-agentman), the editor with the tests passing (mac-editor), the
    /// main window with an agent's finished thread beside its workspace card
    /// (desktop-overview), and the editor with the agent beside the code
    /// (desktop-editor).
    func testRenderHomePage() throws {
        let shots = try out
        for dark in looks {
            let look = dark ? "dark" : "light"
            let manager = try world()
            manager.store.selection = .manager
            try shoot(ContentView(), size: CGSize(width: 1280, height: 820), dark: dark, store: manager.store,
                      to: shots.appendingPathComponent("mac-agentman-\(look).png"))

            try shootEditor(withAgent: false, size: CGSize(width: 1280, height: 824), dark: dark,
                            to: shots.appendingPathComponent("mac-editor-\(look).png"))

            let overview = try world()
            overview.store.selection = .agent("a")
            try shoot(ContentView(), size: CGSize(width: 1440, height: 900), dark: dark, store: overview.store,
                      to: shots.appendingPathComponent("desktop-overview-\(look).png"))

            try shootEditor(withAgent: true, size: CGSize(width: 1440, height: 900), dark: dark,
                            to: shots.appendingPathComponent("desktop-editor-\(look).png"))
        }
    }

    /// Newer features: an agent's changes in the diff panel, an agent
    /// waiting for approval, search, and the command palette.
    func testRenderFeatures() throws {
        let shots = try out
        for dark in looks {
            let look = dark ? "dark" : "light"
            UserDefaults.standard.set(true, forKey: "rightPanelOpen")
            UserDefaults.standard.set("diff", forKey: "rightPanelTab")
            UserDefaults.standard.set("diff", forKey: "rightPanelTabs")
            UserDefaults.standard.set(720.0, forKey: "rightPanelWidth")
            // Long lines run on rather than wrap, which breaks indented code
            // awkwardly at this width.
            UserDefaults.standard.set(false, forKey: "diffWrap")
            let diff = try world()
            diff.store.selection = .agent("a")
            try shoot(ContentView(), size: CGSize(width: 1440, height: 900), dark: dark, store: diff.store, wait: 2.4,
                      to: shots.appendingPathComponent("mac-diff-\(look).png"))
            UserDefaults.standard.set(false, forKey: "rightPanelOpen")

            let approval = try world()
            approval.store.selection = .agent("c")
            try shoot(ContentView(), size: CGSize(width: 1280, height: 800), dark: dark, store: approval.store,
                      to: shots.appendingPathComponent("mac-approval-\(look).png"))

            let search = try world()
            search.store.search = "apple pay"
            search.store.jump = ThreadJump(sessionId: "a", messageId: "a8", query: "apple pay")
            search.store.selection = .agent("a")
            try shoot(ContentView(), size: CGSize(width: 1280, height: 800), dark: dark, store: search.store, wait: 2.4,
                      prepare: Self.caretAtEnd, to: shots.appendingPathComponent("mac-search-\(look).png"))

            let palette = try world()
            palette.store.selection = .manager
            try shoot(ZStack { ContentView(); CommandPalette(query: "apple pay") }, size: CGSize(width: 1280, height: 800),
                      dark: dark, store: palette.store, wait: 2.0, prepare: Self.caretAtEnd,
                      to: shots.appendingPathComponent("mac-palette-\(look).png"))
        }
    }

    /// The Usage page over the last 30 days, and its Limits tab.
    func testRenderEditorChanges() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let shots = URL(fileURLWithPath: out)
        try FileManager.default.createDirectory(at: shots, withIntermediateDirectories: true)
        try shootEditor(withAgent: true, changes: true, size: CGSize(width: 1440, height: 900), dark: false,
                        to: shots.appendingPathComponent("editor-changes-light.png"))
    }

    func testRenderUsage() throws {
        let shots = try out
        let defaults = UserDefaults.standard
        for dark in looks {
            let look = dark ? "dark" : "light"
            defaults.set(30, forKey: "usageDays")
            defaults.set("cost", forKey: "usageMetric")
            defaults.set("day", forKey: "usageBreakdown")
            let usage = try world()
            usage.store.selection = .usage
            try shoot(ContentView(), size: CGSize(width: 1440, height: 900), dark: dark, store: usage.store, wait: 2.0,
                      to: shots.appendingPathComponent("mac-usage-\(look).png"))

            defaults.set("limits", forKey: "usageMetric")
            let limits = try world()
            limits.store.selection = .usage
            try shoot(ContentView(), size: CGSize(width: 1280, height: 800), dark: dark, store: limits.store, wait: 2.0,
                      to: shots.appendingPathComponent("mac-limits-\(look).png"))
        }
    }

    /// The editor on the agent's folder: the tree with what changed, the
    /// changed file open and the tests passing in its terminal; with the
    /// agent's conversation beside the code, or without.
    private func shootEditor(withAgent: Bool, changes: Bool = false, size: CGSize, dark: Bool, to url: URL) throws {
        let world = try world(fresh: true)
        let root = world.folders["storefront"]!
        let editor = EditorRegistry.shared.store(for: root.path)
        editors.append(editor)
        editor.showAgentPanel = withAgent
        editor.panelRoute = withAgent ? .agent("a") : nil
        editor.open("src/checkout/ApplePayButton.tsx")
        editor.open("src/checkout/CheckoutSheet.tsx")
        editor.setExpanded("tests", true)
        editor.setExpanded("tests/checkout", true)
        // The tree's selection is drawn by AppKit's material, which caching
        // the display draws black: the file stays open, the row unselected.
        editor.selection = nil
        // A terminal with no shell of its own to speak of, written to as the
        // tests would.
        editor.showAgentTerminal(SessionTerminal(name: "shots", argv: ["/bin/sleep", "600"]), title: "zsh")
        if changes { editor.showChanges(of: "a") }
        defer { editor.closeChanges() }
        try shoot(EditorWindow(folder: root.path, client: world.store.client), size: size, dark: dark, store: world.store,
                  wait: 5.0, titled: false, webPages: !changes, prepare: { _ in
                      editor.selection = nil
                      if let terminal = editor.terminals.first {
                          terminal.view.feed(text: Self.testRun)
                      }
                  }, to: url)
        for terminal in editor.terminals { editor.closeTerminal(terminal) }
    }

    // MARK: - Rendering

    /// Puts the caret at the end of the field being typed in, rather than
    /// the text it opened with selected.
    static func caretAtEnd(_ window: NSWindow) {
        if let field = window.firstResponder as? NSTextView {
            field.setSelectedRange(NSRange(location: (field.string as NSString).length, length: 0))
        }
    }

    func settle(_ seconds: TimeInterval) {
        RunLoop.main.run(until: Date().addingTimeInterval(seconds))
    }

    /// Draws a view in a window of its own, never seen (fully transparent,
    /// ignoring the mouse), and writes it as a PNG at twice its size.
    ///
    /// The main window is drawn in a titled window, whose buttons say where
    /// to draw them. The editor is drawn in a borderless one: under a title
    /// bar of its own, the panes beside the code take the bar for room to
    /// fill and paint over the editor's header, which the app's windows,
    /// their title bars hidden, don't.
    @discardableResult
    func shoot<V: View>(_ view: V, size: CGSize, dark: Bool, store: AgentsStore, wait: TimeInterval = 1.6, titled: Bool = true,
                        buttons: Bool = true, webPages: Bool = true, overlay: (@MainActor (Bool) -> Void)? = nil,
                        prepare: (@MainActor (NSWindow) -> Void)? = nil, to url: URL) throws -> CGSize {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size),
                              styleMask: titled ? [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView] : [.borderless],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.colorSpace = .sRGB
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        // The view fills the window, title bar and all, as the app's do; the
        // hosting view is kept from sizing the window to its own liking.
        let hosting = NSHostingView(rootView: view.environment(store).frame(maxWidth: .infinity, maxHeight: .infinity).ignoresSafeArea())
        hosting.sizingOptions = []
        window.contentView = hosting
        window.setFrame(NSRect(origin: .zero, size: size), display: false)
        window.orderFrontRegardless()
        settle(wait)
        if window.frame.size != size {
            window.setFrame(NSRect(origin: .zero, size: size), display: true)
            settle(0.6)
        }
        // A web view in a window no one can see takes its page for hidden,
        // and the code editor's page draws nothing while hidden: it is told
        // to go by whether the window is ordered in instead.
        let pages = Self.views(WKWebView.self, in: hosting)
        let occlusion = NSSelectorFromString("_setWindowOcclusionDetectionEnabled:")
        for page in pages where page.responds(to: occlusion) {
            typealias Setter = @convention(c) (AnyObject, Selector, Bool) -> Void
            unsafeBitCast(page.method(for: occlusion), to: Setter.self)(page, occlusion, false)
            // Hidden and shown again, so it looks again.
            page.isHidden = true
            settle(0.2)
            page.isHidden = false
        }
        if !pages.isEmpty { settle(2.5) }
        if let prepare {
            prepare(window)
            settle(0.6)
        }
        let drawn = try capture(window, size: size, buttons: buttons, webPages: webPages, overlay: overlay, to: url)
        // Put away rather than closed: closing the last titled window ends
        // the app the tests run in, and the next test with it.
        window.orderOut(nil)
        return drawn
    }

    /// Writes what the window shows and returns its size, which is the one
    /// asked for unless its content would not fit in that.
    ///
    /// The code editor's page is drawn in from its own snapshot. Where a
    /// view of the app covers it whole (no file open, a Markdown preview),
    /// `webPages` false leaves it out; where one floats over it (Go to File,
    /// Search), `overlay` shows and hides that view, and what changes with
    /// it is drawn over the page.
    @discardableResult
    func capture(_ window: NSWindow, size: CGSize, buttons: Bool = true, webPages: Bool = true,
                 overlay: (@MainActor (Bool) -> Void)? = nil, to url: URL) throws -> CGSize {
        let content = try XCTUnwrap(window.contentView)
        let bounds = content.bounds
        let scale: CGFloat = 2
        let pixels = (Int(bounds.width * scale), Int(bounds.height * scale))

        // The window's own drawing: at its backing scale when that is 2,
        // else into a bitmap twice the size.
        func drawing() throws -> NSBitmapImageRep {
            var rep = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: bounds))
            if rep.pixelsWide != pixels.0 || rep.pixelsHigh != pixels.1 {
                rep = try blank(pixels: pixels, size: bounds.size)
            }
            content.cacheDisplay(in: bounds, to: rep)
            // Views drawn over the editor's page come back clear from a
            // cached display: the layers drawn as they are, instead.
            if Self.mostlyClear(rep), let layer = content.layer {
                let layered = try blank(pixels: pixels, size: bounds.size)
                NSGraphicsContext.saveGraphicsState()
                if let context = NSGraphicsContext(bitmapImageRep: layered) {
                    NSGraphicsContext.current = context
                    if content.isFlipped {
                        context.cgContext.translateBy(x: 0, y: bounds.height)
                        context.cgContext.scaleBy(x: 1, y: -1)
                    }
                    layer.render(in: context.cgContext)
                }
                NSGraphicsContext.restoreGraphicsState()
                return layered
            }
            return rep
        }
        let base = try drawing()
        var floating: NSBitmapImageRep?
        if let overlay {
            overlay(false)
            settle(0.6)
            let under = try drawing()
            overlay(true)
            settle(0.6)
            floating = Self.difference(base, under)
        }

        // Web views draw nothing into a cached display: each one's own
        // snapshot, for where it sits.
        var pages: [(NSImage, NSRect)] = []
        for web in Self.views(WKWebView.self, in: content) where webPages && !web.isHiddenOrHasHiddenAncestor && web.bounds.width > 0 {
            let box = SnapshotBox()
            web.takeSnapshot(with: nil) { image, _ in
                box.image = image
                box.done = true
            }
            let deadline = Date().addingTimeInterval(8)
            while !box.done, Date() < deadline { settle(0.05) }
            XCTAssertNotNil(box.image, "the editor's page couldn't be snapshotted")
            if let shot = box.image { pages.append((shot, web.convert(web.bounds, to: nil))) }
        }

        let final = try blank(pixels: pixels, size: bounds.size)
        NSGraphicsContext.saveGraphicsState()
        NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: final)
        NSGraphicsContext.current?.imageInterpolation = .high
        base.draw(in: NSRect(origin: .zero, size: bounds.size))
        for (image, rect) in pages { image.draw(in: rect) }
        floating?.draw(in: NSRect(origin: .zero, size: bounds.size))
        if buttons { drawWindowButtons(window) }
        NSGraphicsContext.restoreGraphicsState()
        try XCTUnwrap(final.representation(using: .png, properties: [:])).write(to: url)
        return bounds.size
    }

    /// The pixels of one drawing that another lacks, the rest clear, in a
    /// bitmap of its own (a copy of a bitmap can share its pixels).
    static func difference(_ with: NSBitmapImageRep, _ without: NSBitmapImageRep) -> NSBitmapImageRep? {
        guard let mine = with.bitmapData, let other = without.bitmapData, with.bitsPerPixel == 32, without.bitsPerPixel == 32,
              with.bytesPerRow == without.bytesPerRow, with.pixelsHigh == without.pixelsHigh,
              let kept = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: with.pixelsWide, pixelsHigh: with.pixelsHigh,
                                          bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                          colorSpaceName: .calibratedRGB, bytesPerRow: 0, bitsPerPixel: 0)?.retagging(with: .sRGB)
        else { return nil }
        kept.size = with.size
        let rowBytes = with.bytesPerRow
        for row in 0..<with.pixelsHigh {
            for column in 0..<with.pixelsWide {
                let at = row * rowBytes + column * 4
                if mine[at] != other[at] || mine[at + 1] != other[at + 1] || mine[at + 2] != other[at + 2] || mine[at + 3] != other[at + 3],
                   let color = with.colorAt(x: column, y: row) {
                    kept.setColor(color, atX: column, y: row)
                }
            }
        }
        return kept
    }

    /// Whether most of a drawing is clear, as a failed one is.
    static func mostlyClear(_ rep: NSBitmapImageRep) -> Bool {
        var clear = 0, looked = 0
        for row in stride(from: 0, to: rep.pixelsHigh, by: max(rep.pixelsHigh / 24, 1)) {
            for column in stride(from: 0, to: rep.pixelsWide, by: max(rep.pixelsWide / 24, 1)) {
                looked += 1
                if (rep.colorAt(x: column, y: row)?.alphaComponent ?? 1) < 0.05 { clear += 1 }
            }
        }
        return looked > 0 && clear * 2 > looked
    }

    /// What a web view's snapshot handed back, once it has.
    @MainActor final class SnapshotBox {
        var image: NSImage?
        var done = false
    }

    /// An empty sRGB bitmap of these pixels for this many points.
    func blank(pixels: (Int, Int), size: CGSize) throws -> NSBitmapImageRep {
        let rep = try XCTUnwrap(NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: pixels.0, pixelsHigh: pixels.1,
                                                 bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                                 colorSpaceName: .calibratedRGB, bytesPerRow: 0, bitsPerPixel: 0)?
            .retagging(with: .sRGB))
        rep.size = size
        return rep
    }

    /// Close, minimise and zoom, in their colours, where the window has
    /// them: an unseen window's are drawn inactive. A borderless window has
    /// none; they go where a titled window of this system puts them.
    private func drawWindowButtons(_ window: NSWindow) {
        let buttons: [(NSWindow.ButtonType, UInt32, UInt32)] = [
            (.closeButton, 0xFF5F57, 0xE0443E),
            (.miniaturizeButton, 0xFEBC2E, 0xDEA123),
            (.zoomButton, 0x28C840, 0x1AAB29),
        ]
        let height = window.frame.height
        for (index, (type, fill, edge)) in buttons.enumerated() {
            let frame: NSRect
            if let button = window.standardWindowButton(type), button.superview != nil {
                frame = button.convert(button.bounds, to: nil)
            } else {
                frame = NSRect(x: 9 + CGFloat(index) * 23, y: height - 9 - 14, width: 14, height: 14)
            }
            let diameter = min(frame.width, frame.height, 14)
            let circle = NSRect(x: frame.midX - diameter / 2, y: frame.midY - diameter / 2, width: diameter, height: diameter)
            Self.color(fill).setFill()
            NSBezierPath(ovalIn: circle).fill()
            Self.color(edge).setStroke()
            let ring = NSBezierPath(ovalIn: circle.insetBy(dx: 0.25, dy: 0.25))
            ring.lineWidth = 0.5
            ring.stroke()
        }
    }

    private static func color(_ rgb: UInt32) -> NSColor {
        NSColor(srgbRed: CGFloat((rgb >> 16) & 0xFF) / 255, green: CGFloat((rgb >> 8) & 0xFF) / 255,
                blue: CGFloat(rgb & 0xFF) / 255, alpha: 1)
    }

    static func views<T: NSView>(_ type: T.Type, in view: NSView) -> [T] {
        ((view as? T).map { [$0] } ?? []) + view.subviews.flatMap { views(type, in: $0) }
    }

    // MARK: - The world

    struct World {
        let store: AgentsStore
        let folders: [String: URL]
    }

    /// The four projects, the agents in them, and a daemon that answers
    /// for them, connected.
    /// The projects and agents, with a daemon answering for them. The
    /// projects are made once for each test and shared by its pictures,
    /// unless `fresh` asks for new ones: the editor keeps its state for
    /// each folder, so each picture of it gets folders of its own.
    func world(connected: Bool = true, fresh: Bool = false, customize: ((inout Fixtures) -> Void)? = nil,
               more: (([String: URL]) throws -> [String: DaemonEvent])? = nil) throws -> World {
        var made: [String: URL] = [:]
        if !fresh, let shared = sharedFolders {
            made = shared
        } else {
            let base = FileManager.default.temporaryDirectory.appendingPathComponent("site-shots-" + UUID().uuidString)
            folders.append(base)
            for (name, project) in Self.projects {
                made[name] = try repository(project, named: name, in: base)
            }
            if !fresh { sharedFolders = made }
        }
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        // What arrived in the last hour shows as new in Agentman's chat.
        UserDefaults.standard.set(now - 3_600_000, forKey: "managerLastRead")
        var fixtures = Fixtures(folders: made, now: now)
        customize?(&fixtures)
        var built = try FakeDaemon(fixtures: fixtures, now: now)
        if let more { built.more = try more(made) }
        let daemon = built
        let client = DaemonClient(socketPath: "/nonexistent/agentman-site-shots.sock")
        client.answer = { request in daemon.answer(request) }
        let store = AgentsStore(client: client)
        store.apply(daemon.sessions)
        for (id, page) in daemon.pages {
            var event = DaemonEvent(type: "messages")
            event.sessionId = id
            event.messages = page.page?.messages
            store.apply(event)
        }
        // Connected, as to a running am serve: the session list and
        // Agentman's conversation are asked for, and answered above.
        if connected { store.statusChanged(.connected) }
        return World(store: store, folders: made)
    }

    /// A project as a git repository: its files committed on main, its
    /// branch checked out, and what the agents changed left uncommitted.
    private func repository(_ project: Project, named name: String, in base: URL) throws -> URL {
        let root = base.appendingPathComponent(name, isDirectory: true)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        try write(project.files, in: root)
        try git(["init", "-q", "-b", "main"], in: root)
        try git(["add", "-A"], in: root)
        try git(["commit", "-qm", "Start"], in: root)
        if project.branch != "main" { try git(["checkout", "-qb", project.branch], in: root) }
        try write(project.changes, in: root)
        return root.resolvingSymlinksInPath()
    }

    private func write(_ files: [String: String], in root: URL) throws {
        for (path, text) in files {
            let url = root.appendingPathComponent(path)
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            try Data(text.utf8).write(to: url)
        }
    }

    @discardableResult
    private func git(_ args: [String], in folder: URL) throws -> String {
        try Self.git(args, in: folder)
    }

    nonisolated static func git(_ args: [String], in folder: URL) throws -> String {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/git")
        process.arguments = ["-c", "user.name=Storefront", "-c", "user.email=dev@example.com", "-c", "commit.gpgsign=false",
                             "-c", "core.hooksPath=/dev/null", "-c", "core.quotepath=false"] + args
        process.currentDirectoryURL = folder
        let pipe = Pipe()
        process.standardOutput = pipe
        process.standardError = FileHandle.nullDevice
        try process.run()
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        return String(decoding: data, as: UTF8.self)
    }

    // MARK: - The fake am serve

    /// Answers the requests the windows make, from the fixtures: sessions,
    /// threads, each folder's changes and diffs, search, Agentman, usage.
    struct FakeDaemon: Sendable {
        let sessions: DaemonEvent
        let pages: [String: DaemonEvent]
        let changes: [String: DaemonEvent]
        let diffs: [String: DaemonEvent]
        let git: [String: DaemonEvent]
        let search: DaemonEvent
        let threadSearch: [String: DaemonEvent]
        let history: DaemonEvent
        let settings: DaemonEvent
        let limits: DaemonEvent
        let titles: [String: String]
        let now: Int64

        /// Answers for any other request, by "type|session|path", "type|session"
        /// or "type".
        var more: [String: DaemonEvent] = [:]

        /// A folder as git has it: what changed, each change's diff (a new
        /// file's against nothing, as the daemon shows it), and the branch.
        struct FolderGit {
            var changes: [WorkspaceChange]
            var diffs: [String: String]
            var branch: String
        }

        /// Read once for each folder: the folders don't change while the
        /// pictures are drawn.
        nonisolated(unsafe) static var folderCache: [String: FolderGit] = [:]

        static func read(_ path: String) throws -> FolderGit {
            if let known = folderCache[path] { return known }
            let folder = URL(fileURLWithPath: path)
            var read = FolderGit(changes: [], diffs: [:], branch: "")
            let status = try SiteShotsTests.git(["status", "--porcelain", "--untracked-files=all"], in: folder)
            for line in status.split(separator: "\n") where line.count > 3 {
                let file = String(line.dropFirst(3))
                let untracked = line.hasPrefix("??")
                let range = untracked ? ["--no-index", "--", "/dev/null", file] : ["--", file]
                let counts = try SiteShotsTests.git(["diff", "--numstat"] + range, in: folder).split(separator: "\t")
                guard counts.count >= 2 else { continue }
                read.changes.append(WorkspaceChange(path: file, status: untracked ? "untracked" : "modified",
                                                    added: Int(counts[0]), removed: Int(counts[1])))
                read.diffs[file] = try SiteShotsTests.git(["diff"] + range, in: folder)
            }
            read.branch = try SiteShotsTests.git(["branch", "--show-current"], in: folder).trimmingCharacters(in: .whitespacesAndNewlines)
            folderCache[path] = read
            return read
        }

        init(fixtures: Fixtures, now: Int64) throws {
            self.now = now
            var sessions = DaemonEvent(type: "sessions")
            sessions.sessions = fixtures.sessions
            self.sessions = sessions
            var pages: [String: DaemonEvent] = [:]
            for session in fixtures.sessions {
                var event = DaemonEvent(type: "page")
                event.page = Page(sessionId: session.id, messages: fixtures.messages[session.id] ?? [], nextCursor: nil, hasMore: false)
                pages[session.id] = event
            }
            self.pages = pages

            // Each folder's changes and diffs, from git itself.
            var changes: [String: DaemonEvent] = [:], diffs: [String: DaemonEvent] = [:], git: [String: DaemonEvent] = [:]
            for session in fixtures.sessions {
                let read = try Self.read(session.cwd)
                let listed = read.changes
                for (path, text) in read.diffs {
                    var diff = DaemonEvent(type: "workspace")
                    diff.workspace = WorkspaceResult(kind: "diff", sessionId: session.id, path: path, diff: text)
                    diffs[session.id + "\u{0}" + path] = diff
                }
                var event = DaemonEvent(type: "workspace")
                event.workspace = WorkspaceResult(kind: "changes", sessionId: session.id, changes: listed)
                changes[session.id] = event
                var state = DaemonEvent(type: "git")
                let branch = read.branch
                state.git = GitState(sessionId: session.id, branch: branch, defaultBranch: "main", upstream: "origin/" + branch,
                                     ahead: 0, behind: 0, aheadOfDefault: 0, remote: "origin", changes: listed.count)
                git[session.id] = state
            }
            self.changes = changes
            self.diffs = diffs
            self.git = git

            var search = DaemonEvent(type: "search")
            search.search = fixtures.searchHits
            self.search = search
            var inThread = DaemonEvent(type: "search")
            inThread.search = fixtures.threadHits
            threadSearch = ["a": inThread]

            var history = DaemonEvent(type: "manager_history")
            history.entries = fixtures.managerEntries
            history.more = false
            self.history = history
            var settings = DaemonEvent(type: "manager_settings")
            settings.managerSettings = fixtures.managerSettings
            settings.managerStatus = ManagerStatus(state: "idle", keySet: fixtures.managerSettings.keySet)
            self.settings = settings
            var limits = DaemonEvent(type: "limits")
            limits.limits = fixtures.limits
            self.limits = limits
            titles = fixtures.runTitles
        }

        func answer(_ request: DaemonRequest) -> DaemonEvent? {
            let session = request.sessionId ?? ""
            switch request.type {
            case .listSessions: return sessions
            case .fetchMessages: return request.before == nil ? pages[session] : nil
            case .listChanges: return changes[session]
            case .fileDiff: return diffs[session + "\u{0}" + (request.path ?? "")]
            case .gitStatus: return git[session]
            case .search:
                // Only words the sample conversations have are found.
                let words = (request.text ?? "").lowercased()
                guard words.contains("apple") || words.contains("pay") else {
                    var none = DaemonEvent(type: "search")
                    none.search = MessageSearchResults(query: request.text ?? "", sessionId: request.sessionId, hits: [])
                    return none
                }
                return request.sessionId == nil ? search : threadSearch[session]
            case .managerHistory:
                if request.before == nil { return history }
                var none = DaemonEvent(type: "manager_history")
                none.entries = []
                none.more = false
                return none
            case .managerSettings: return settings
            case .usage: return Self.usage(days: request.days ?? 30, now: now)
            case .limits: return limits
            case .toolsTitle:
                guard let title = request.steps?.first.flatMap({ titles[$0.summary ?? ""] }) else { return nil }
                var event = DaemonEvent(type: "tools_title")
                event.preview = title
                return event
            default:
                let type = request.type.rawValue
                return more["\(type)|\(session)|\(request.path ?? "")"] ?? more["\(type)|\(session)"] ?? more[type]
            }
        }

        /// What three agents spent over the past days: busier on weekdays,
        /// steady enough to read as a month of real work.
        static func usage(days: Int, now: Int64) -> DaemonEvent {
            let count = days == 0 ? 90 : min(max(days, 1), 365)
            let calendar = Calendar.current
            let today = calendar.startOfDay(for: Date(timeIntervalSince1970: Double(now) / 1000))
            let kinds: [(kind: String, base: Double, sessions: Int, tokensPerDollar: Double)] = [
                ("claude", 13.5, 64, 1_050_000), ("codex", 5.2, 31, 900_000), ("opencode", 1.9, 12, 1_200_000),
            ]
            var buckets: [Usage.Bucket] = []
            var spent: [String: Double] = [:], tokens: [String: Int64] = [:]
            for back in stride(from: count - 1, through: 0, by: -1) {
                guard let day = calendar.date(byAdding: .day, value: -back, to: today) else { continue }
                let weekday = calendar.component(.weekday, from: day)
                let weekend = weekday == 1 || weekday == 7
                var cost: [String: Double] = [:], used: [String: Int64] = [:]
                for (index, agent) in kinds.enumerated() {
                    // A steady wobble rather than noise, so the chart looks
                    // the same each time it is drawn.
                    let wobble = 0.72 + 0.28 * sin(Double(back) * 1.7 + Double(index) * 2.1) + 0.18 * cos(Double(back) * 0.6)
                    let value = (agent.base * wobble * (weekend ? 0.28 : 1) * 100).rounded() / 100
                    cost[agent.kind] = value
                    used[agent.kind] = Int64(value * agent.tokensPerDollar)
                    spent[agent.kind, default: 0] += value
                    tokens[agent.kind, default: 0] += Int64(value * agent.tokensPerDollar)
                }
                buckets.append(Usage.Bucket(start: Int64(day.timeIntervalSince1970 * 1000), cost: cost, tokens: used))
            }
            func split(_ total: Int64) -> Usage.Tokens {
                Usage.Tokens(input: total * 12 / 1000, cacheRead: total * 921 / 1000, cacheWrite: total * 49 / 1000, output: total * 18 / 1000)
            }
            let agents = kinds.map { agent in
                Usage.Agent(kind: agent.kind, sessions: agent.sessions, tokens: split(tokens[agent.kind] ?? 0), cost: spent[agent.kind] ?? 0)
            }
            let total = spent.values.reduce(0, +)
            let allTokens = split(tokens.values.reduce(0, +))
            var event = DaemonEvent(type: "usage")
            event.usage = Usage(days: days, unit: "day", sessions: kinds.reduce(0) { $0 + $1.sessions }, tokens: allTokens,
                                cost: Usage.Cost(input: total * 0.06, cacheRead: total * 0.38, cacheWrite: total * 0.31, output: total * 0.25),
                                cacheSavings: total * 1.31, agents: agents, buckets: buckets, models: [])
            return event
        }
    }

    // MARK: - Fixtures

    struct Project {
        let branch: String
        let files: [String: String]
        let changes: [String: String]
    }

    /// A terminal's run of the tests, as vitest prints it.
    static let testRun: String = {
        let esc = "\u{1b}["
        let prompt = "\(esc)1;34mstorefront\(esc)0m \(esc)35m❯\(esc)0m "
        let files = [
            ("tests/checkout/ApplePayButton.test.tsx", 4, 38), ("tests/checkout/CheckoutSheet.test.tsx", 7, 52),
            ("tests/checkout/useCheckout.test.ts", 8, 21), ("tests/cart.test.ts", 12, 17), ("tests/format.test.ts", 21, 9),
        ]
        var text = "\(esc)2J\(esc)3J\(esc)H"
        text += prompt + "npm test\r\n\r\n"
        for (file, tests, ms) in files {
            text += " \(esc)32m✓\(esc)0m \(file) \(esc)2m(\(tests) tests)\(esc)0m \(esc)33m\(ms)ms\(esc)0m\r\n"
        }
        text += "\r\n \(esc)2mTest Files\(esc)0m  \(esc)1;32m5 passed\(esc)0m \(esc)2m(5)\(esc)0m\r\n"
        text += "      \(esc)2mTests\(esc)0m  \(esc)1;32m52 passed\(esc)0m \(esc)2m(52)\(esc)0m\r\n"
        text += "   \(esc)2mDuration\(esc)0m  1.42s\r\n\r\n"
        text += prompt
        return text
    }()

    static let projects: [String: Project] = [
        "storefront": Project(branch: "apple-pay", files: Storefront.files, changes: Storefront.changes),
        "api": Project(branch: "main", files: [
            "README.md": "# API\n\nOrders, payments and webhooks for the storefront.\n",
            "go.mod": "module example.com/api\n\ngo 1.25\n",
            "cmd/api/main.go": "package main\n\nfunc main() {}\n",
            "orders/orders.go": "package orders\n\ntype Order struct {\n\tID    int64\n\tTotal int64\n}\n",
            "db/schema.sql": "create table orders (\n  id bigserial primary key,\n  total_cents bigint not null\n);\n",
            "migrations/0041_refunds.sql": "alter table orders add column refunded_at timestamptz;\n",
        ], changes: [
            "migrations/0042_orders_pg17.sql": "-- Orders on Postgres 17: identity columns and a covering index.\nalter table orders alter column id add generated always as identity;\ncreate index concurrently orders_customer_created on orders (customer_id, created_at) include (total_cents);\n",
            "webhooks/limit.go": "package webhooks\n\n// Limit allows each sender this many deliveries a minute.\nconst Limit = 120\n",
        ]),
        "docs": Project(branch: "payments-guide", files: [
            "README.md": "# Docs\n",
            "reference/orders.md": "# Orders\n\nList, create and refund orders.\n",
            "reference/payments.md": "# Payments\n\nCards and wallets.\n",
        ], changes: [
            "reference/payments.md": "# Payments\n\nCards, Apple Pay and Google Pay.\n\n## Apple Pay\n\nUse the test cards below.\n",
            "reference/webhooks.md": "# Webhooks\n\nEvents we send when an order changes.\n",
        ]),
        "ios": Project(branch: "onboarding", files: [
            "README.md": "# Storefront for iOS\n",
            "App/StorefrontApp.swift": "import SwiftUI\n\n@main\nstruct StorefrontApp: App {\n    var body: some Scene { WindowGroup { Text(\"Storefront\") } }\n}\n",
            "App/Onboarding/Welcome.swift": "import SwiftUI\n\nstruct Welcome: View {\n    var body: some View { Text(\"Welcome\") }\n}\n",
        ], changes: [
            "App/Onboarding/Welcome.swift": "import SwiftUI\n\nstruct Welcome: View {\n    var body: some View {\n        VStack { Text(\"Welcome to Storefront\") }\n    }\n}\n",
        ]),
    ]

    /// The sessions, threads, Agentman's chat and search, for the folders
    /// as made.
    struct Fixtures {
        var sessions: [Session]
        var messages: [String: [Message]]
        var managerEntries: [ManagerEntry]
        var searchHits: MessageSearchResults
        var threadHits: MessageSearchResults
        var limits: Limits
        var runTitles: [String: String]
        var managerSettings = ManagerSettings(chatModel: "default", briefModel: "default", effort: "medium", briefs: true,
                                              autoApprove: true, style: "", instructions: "", keySet: true)

        init(folders: [String: URL], now: Int64) {
            let storefront = folders["storefront"]!.path, api = folders["api"]!.path
            let docs = folders["docs"]!.path, ios = folders["ios"]!.path
            func ago(_ minutes: Double) -> Int64 { now - Int64(minutes * 60_000) }

            let push = [
                QuestionOption(key: "1", label: "Yes"),
                QuestionOption(key: "2", label: "Yes, and don't ask again for git push"),
                QuestionOption(key: "3", label: "No, and tell Codex what to do differently", withText: true),
            ]
            let list = [
                Session(id: "a", kind: "claude", name: "Add Apple Pay to checkout", cwd: storefront, state: .idle, inject: "tmux",
                        startedAt: ago(16), lastActivityAt: ago(2.4)),
                Session(id: "b", kind: "cursor", name: "Fix cart badges", cwd: storefront, state: .busy, inject: "tmux",
                        startedAt: ago(9), lastActivityAt: ago(0.2),
                        servers: [Server(port: 5173, command: "node", title: "Storefront")]),
                Session(id: "c", kind: "codex", name: "Migrate orders to Postgres 17", cwd: api, state: .waitingInput, inject: "tmux",
                        startedAt: ago(26), lastActivityAt: ago(2),
                        question: Question(id: "q-push", prompt: "Run git push origin main?", title: "Wants to push to main",
                                           detail: "git push origin main", options: push)),
                Session(id: "d", kind: "claude", name: "Throttle webhooks", cwd: api, state: .busy, inject: "tmux",
                        startedAt: ago(12), lastActivityAt: ago(0.1)),
                Session(id: "e", kind: "opencode", name: "Refresh the API reference", cwd: docs, state: .busy, inject: "api",
                        startedAt: ago(31), lastActivityAt: ago(0.6)),
                Session(id: "f", kind: "claude", name: "Onboarding screens for v2", cwd: ios, state: .idle, inject: "tmux",
                        startedAt: ago(140), lastActivityAt: ago(52)),
                Session(id: "g", kind: "antigravity", name: "Onboarding push prompt", cwd: ios, state: .idle, inject: "api",
                        startedAt: ago(300), lastActivityAt: ago(185)),
            ]
            sessions = list

            func tool(_ id: String, _ session: String, _ minutes: Double, _ name: String, _ summary: String, _ title: String,
                      status: String = "ok") -> Message {
                Message(id: id, sessionId: session, role: .tool, ts: ago(minutes),
                        tool: ToolCall(name: name, summary: summary, status: status, title: title))
            }
            func said(_ id: String, _ session: String, _ minutes: Double, _ role: Role, _ text: String) -> Message {
                Message(id: id, sessionId: session, role: role, ts: ago(minutes), text: text)
            }
            messages = [
                "a": [
                    said("a1", "a", 15, .user, "Add Apple Pay to the checkout sheet. Only show it on devices that can pay with it, and keep the card form as the fallback."),
                    tool("a2", "a", 14.6, "Read", "src/checkout/CheckoutSheet.tsx", "Read CheckoutSheet.tsx"),
                    tool("a3", "a", 14.4, "Read", "src/checkout/useCheckout.ts", "Read useCheckout.ts"),
                    tool("a4", "a", 13.2, "Write", "src/checkout/ApplePayButton.tsx", "Wrote ApplePayButton.tsx"),
                    tool("a5", "a", 12.8, "Edit", "src/checkout/CheckoutSheet.tsx", "Added Apple Pay above the card form"),
                    tool("a6", "a", 12.1, "Write", "tests/checkout/ApplePayButton.test.tsx", "Wrote tests for the button"),
                    tool("a7", "a", 11.4, "Bash", "npm test", "Ran the tests"),
                    said("a8", "a", 10.9, .assistant, """
                    Apple Pay is in the checkout sheet.

                    - **Shows** only where `ApplePaySession.canMakePayments()` says the device can pay
                    - **Falls back** to the card form everywhere else
                    - **Charges** the cart's total, in the cart's currency

                    I added tests for all three. **52 tests pass**, 4 of them new.
                    """),
                    said("a9", "a", 6.5, .user, "Nice. Does it work in Safari on iPad too?"),
                    tool("a10", "a", 5.8, "Bash", "npx playwright test checkout --project=ipad", "Ran the checkout flow in Safari on iPad"),
                    said("a11", "a", 5.2, .assistant, "Yes. The checkout flow passes in Safari on iPad as well: the button shows, the payment sheet opens, and the card form is still there below it."),
                    said("a12", "a", 3.6, .user, "Great. Make sure the receipt page still works for card payments."),
                    tool("a13", "a", 3.2, "Read", "src/checkout/useCheckout.ts", "Read useCheckout.ts"),
                    tool("a14", "a", 2.9, "Bash", "npm test -- receipt", "Ran the receipt tests"),
                    said("a15", "a", 2.4, .assistant, "It does. Card payments and Apple Pay both go through the same `paid()` step now, so both land on the receipt page, and the receipt tests pass. The changes are ready to review."),
                ],
                "c": [
                    said("c1", "c", 25, .user, "Move the orders tables to Postgres 17: write the migration, run it on the staging copy, then push it."),
                    tool("c2", "c", 24.5, "Read", "db/schema.sql", "Read schema.sql"),
                    tool("c3", "c", 22, "Write", "migrations/0042_orders_pg17.sql", "Wrote the migration"),
                    tool("c4", "c", 19, "Bash", "make migrate DB=staging", "Ran it on the staging copy"),
                    tool("c5", "c", 6, "Bash", "go test ./orders/...", "Ran the orders tests"),
                    said("c6", "c", 3, .assistant, """
                    The migration ran cleanly on the staging copy: **2.4M orders** moved in 41 seconds, with no rows lost, and the orders tests pass.

                    Next I'll push it so the deploy picks it up.
                    """),
                ],
            ]
            runTitles = [
                "src/checkout/CheckoutSheet.tsx": "Read the checkout, added the Apple Pay button and ran the tests",
                "db/schema.sql": "Wrote the migration, ran it on staging and tested the orders",
                "src/checkout/useCheckout.ts": "Checked the receipt page still works for card payments",
            ]

            let card = ManagerCard(id: "card-c", kind: "question", state: "open", sessionId: "c", agentName: "Migrate orders to Postgres 17",
                                   title: "wants to run `git push origin main`", detail: "git push origin main",
                                   reason: "Your rule: ask before git push", questionId: "q-push", options: push)
            managerEntries = [
                ManagerEntry(id: "m1", role: "user", text: "Is the Apple Pay work ready to ship? And does anything need me?", ts: ago(7)),
                ManagerEntry(id: "m2", role: "activity", text: "Read the brief on Add Apple Pay to checkout", ts: ago(6.9), sessionId: "a", tool: "read_brief"),
                ManagerEntry(id: "m3", role: "activity", text: "Looked over the 5 files it changed in storefront", ts: ago(6.8), sessionId: "a", tool: "read_changes"),
                ManagerEntry(id: "m4", role: "activity", text: "Read the brief on Migrate orders to Postgres 17", ts: ago(6.7), sessionId: "c", tool: "read_brief"),
                ManagerEntry(id: "m5", role: "assistant", text: """
                Apple Pay is ready for your review. **Add Apple Pay to checkout** shows the button only on devices that can pay, keeps the card form as the fallback, and all 52 tests pass. Its 5 changed files are waiting on `apple-pay`, not yet committed.

                One thing needs you: **Migrate orders to Postgres 17** wants to push straight to `main`. Your rules ask you first before any push, so it's below.
                """, ts: ago(6.4)),
                ManagerEntry(id: "m6", role: "activity", text: "Approved npm test for Fix cart badges", ts: ago(4), sessionId: "b", tool: "approve"),
                ManagerEntry(id: "m7", role: "card", text: "Migrate orders to Postgres 17 wants to run git push origin main", ts: ago(2),
                             sessionId: "c", card: card),
            ]

            let ended = { (id: String, name: String, kind: String, cwd: String, days: Double) in
                Session(id: id, kind: kind, name: name, cwd: cwd, state: .ended, lastActivityAt: ago(days * 1440))
            }
            searchHits = MessageSearchResults(query: "apple pay", hits: [
                SearchHit(session: list[0], messageId: "a8", role: .assistant, ts: ago(10.9),
                          snippet: "Apple Pay is in the checkout sheet. Shows only where ApplePaySession.canMakePayments() says the device can pay", matches: 4),
                SearchHit(session: list[4], messageId: "e7", role: .assistant, ts: ago(18),
                          snippet: "…added a section on Apple Pay to the payments guide, with the test cards to try it.", matches: 2),
                SearchHit(session: ended("x1", "Wallet passes for orders", "claude", ios, 6), messageId: "x1-4", role: .user, ts: ago(6 * 1440),
                          snippet: "…add the order to Wallet once Apple Pay confirms it, the same as the web checkout does.", matches: 1),
                SearchHit(session: ended("x2", "Checkout spike", "codex", storefront, 21), messageId: "x2-9", role: .assistant, ts: ago(21 * 1440),
                          snippet: "…Apple Pay needs the domain verification file under /.well-known before the button shows.", matches: 1),
            ])
            threadHits = MessageSearchResults(query: "apple pay", sessionId: "a", hits: [
                SearchHit(messageId: "a1", role: .user, ts: ago(15), snippet: "Add Apple Pay to the checkout sheet."),
                SearchHit(messageId: "a8", role: .assistant, ts: ago(10.9), snippet: "Apple Pay is in the checkout sheet."),
            ])

            func at(_ hours: Double) -> Int64 { now + Int64(hours * 3_600_000) }
            limits = Limits(agents: [
                Limits.Agent(kind: "claude", plan: "Max", checkedAt: now - 90_000, windows: [
                    Limits.Window(id: "five_hour", kind: "session", label: "Session", used: 38, resetsAt: at(2.5), minutes: 300),
                    Limits.Window(id: "seven_day", kind: "weekly", label: "Weekly", used: 56, resetsAt: at(5 * 24 + 16), minutes: 10080),
                ]),
                Limits.Agent(kind: "codex", plan: "Plus", checkedAt: now - 4 * 60_000, windows: [
                    Limits.Window(id: "primary", kind: "session", label: "Session", used: 64, resetsAt: at(1.2), minutes: 300),
                    Limits.Window(id: "secondary", kind: "weekly", label: "Weekly", used: 41, resetsAt: at(2 * 24 + 2), minutes: 10080),
                ]),
            ])
        }
    }

    /// The storefront's web app, before and after the agent added Apple Pay.
    enum Storefront {
        static let files: [String: String] = [
            "README.md": """
            # Storefront

            The shop's web front: product pages, the cart and checkout.

                npm install
                npm run dev

            """,
            "package.json": """
            {
              "name": "storefront",
              "version": "0.9.2",
              "private": true,
              "type": "module",
              "scripts": {
                "dev": "vite",
                "build": "vite build",
                "test": "vitest run"
              },
              "dependencies": {
                "react": "^19.1.0",
                "react-dom": "^19.1.0"
              },
              "devDependencies": {
                "typescript": "^5.9.0",
                "vite": "^7.1.0",
                "vitest": "^3.2.0"
              }
            }

            """,
            "tsconfig.json": "{\n  \"compilerOptions\": {\n    \"target\": \"ES2022\",\n    \"jsx\": \"react-jsx\",\n    \"strict\": true\n  }\n}\n",
            "vite.config.ts": "import { defineConfig } from \"vite\";\nimport react from \"@vitejs/plugin-react\";\n\nexport default defineConfig({ plugins: [react()] });\n",
            "src/main.tsx": "import { createRoot } from \"react-dom/client\";\nimport { App } from \"./App\";\n\ncreateRoot(document.getElementById(\"root\")!).render(<App />);\n",
            "src/App.tsx": "export function App() {\n  return <main />;\n}\n",
            "src/cart/Cart.tsx": "export function Cart() {\n  return null;\n}\n",
            "src/cart/OrderSummary.tsx": "export function OrderSummary() {\n  return null;\n}\n",
            "tests/cart.test.ts": "import { it } from \"vitest\";\n\nit(\"totals the cart\", () => {});\n",
            "src/cart/types.ts": "export type Cart = { items: Item[]; total: number; currency: string; country: string };\n",
            "src/lib/format.ts": "export function formatPrice(amount: number, currency: string) {\n  return new Intl.NumberFormat(undefined, { style: \"currency\", currency }).format(amount);\n}\n",
            "tests/format.test.ts": "import { it } from \"vitest\";\n\nit(\"formats prices\", () => {});\n",
            "src/lib/payments.ts": "export async function charge() {}\n",
            "src/ui/Sheet.tsx": "export function Sheet() {\n  return null;\n}\n",
            "src/checkout/CardForm.tsx": "export function CardForm() {\n  return null;\n}\n",
            "tests/checkout/CheckoutSheet.test.tsx": "import { it } from \"vitest\";\n\nit(\"shows the card form\", () => {});\n",
            "tests/checkout/useCheckout.test.ts": "import { it } from \"vitest\";\n\nit(\"charges the card\", () => {});\n",
            "src/checkout/ApplePayButton.tsx": """
            import type { Cart } from "../cart/types";

            type Props = {
              cart: Cart;
            };

            /** Apple Pay in the checkout sheet. */
            export function ApplePayButton({ cart }: Props) {
              // TODO: show Apple Pay where the device supports it.
              return null;
            }

            """,
            "src/checkout/CheckoutSheet.tsx": """
            import { CardForm } from "./CardForm";
            import { useCheckout } from "./useCheckout";
            import { Sheet } from "../ui/Sheet";
            import { OrderSummary } from "../cart/OrderSummary";
            import type { Cart } from "../cart/types";

            type Props = {
              cart: Cart;
              onClose: () => void;
            };

            /** The sheet that takes a customer from their cart to a paid order. */
            export function CheckoutSheet({ cart, onClose }: Props) {
              const { total, pay, paying, error } = useCheckout(cart);

              return (
                <Sheet title="Checkout" onClose={onClose}>
                  <OrderSummary cart={cart} total={total} />
                  <CardForm onSubmit={pay} disabled={paying} />
                  {error && <p className="checkout-error">{error}</p>}
                </Sheet>
              );
            }

            """,
            "src/checkout/useCheckout.ts": """
            import { useState } from "react";
            import { charge, type CardDetails } from "../lib/payments";
            import type { Cart } from "../cart/types";

            export function useCheckout(cart: Cart) {
              const [paying, setPaying] = useState(false);
              const [error, setError] = useState<string>();
              const total = cart.items.reduce((sum, item) => sum + item.price * item.quantity, 0);

              async function pay(card: CardDetails) {
                setPaying(true);
                try {
                  await charge(cart, { card });
                } catch {
                  setError("The card was declined. Try another card.");
                } finally {
                  setPaying(false);
                }
              }

              return { total, pay, paying, error };
            }

            """,
        ]

        static let changes: [String: String] = [
            "src/checkout/CheckoutSheet.tsx": """
            import { ApplePayButton } from "./ApplePayButton";
            import { CardForm } from "./CardForm";
            import { useCheckout } from "./useCheckout";
            import { Divider } from "../ui/Divider";
            import { Sheet } from "../ui/Sheet";
            import { OrderSummary } from "../cart/OrderSummary";
            import type { Cart } from "../cart/types";

            type Props = {
              cart: Cart;
              onClose: () => void;
            };

            /** The sheet that takes a customer from their cart to a paid order. */
            export function CheckoutSheet({ cart, onClose }: Props) {
              const { total, pay, paid, paying, error } = useCheckout(cart);

              return (
                <Sheet title="Checkout" onClose={onClose}>
                  <OrderSummary cart={cart} total={total} />
                  <ApplePayButton cart={cart} onPaid={paid} />
                  <Divider label="or pay by card" />
                  <CardForm onSubmit={pay} disabled={paying} />
                  {error && <p className="checkout-error">{error}</p>}
                </Sheet>
              );
            }

            """,
            "src/checkout/useCheckout.ts": """
            import { useState } from "react";
            import { useNavigate } from "react-router";
            import { charge, type CardDetails } from "../lib/payments";
            import type { Cart } from "../cart/types";

            export function useCheckout(cart: Cart) {
              const navigate = useNavigate();
              const [paying, setPaying] = useState(false);
              const [error, setError] = useState<string>();
              const total = cart.items.reduce((sum, item) => sum + item.price * item.quantity, 0);

              async function pay(card: CardDetails) {
                setPaying(true);
                try {
                  const receipt = await charge(cart, { card });
                  paid(receipt);
                } catch {
                  setError("The card was declined. Try another card.");
                } finally {
                  setPaying(false);
                }
              }

              /** Paid, by card or by Apple Pay: on to the receipt. */
              function paid(receipt: string) {
                navigate(`/orders/${receipt}`);
              }

              return { total, pay, paid, paying, error };
            }

            """,
            "src/checkout/ApplePayButton.tsx": """
            import { useEffect, useState } from "react";
            import { charge } from "../lib/payments";
            import { formatPrice } from "../lib/format";
            import type { Cart } from "../cart/types";

            type Props = {
              cart: Cart;
              onPaid: (receipt: string) => void;
            };

            /** Apple Pay, where the device can pay with it; nothing elsewhere. */
            export function ApplePayButton({ cart, onPaid }: Props) {
              const [supported, setSupported] = useState(false);

              useEffect(() => {
                setSupported(Boolean(window.ApplePaySession?.canMakePayments()));
              }, []);

              if (!supported) return null;

              function pay() {
                const session = new ApplePaySession(3, {
                  countryCode: cart.country,
                  currencyCode: cart.currency,
                  supportedNetworks: ["visa", "masterCard", "amex"],
                  merchantCapabilities: ["supports3DS"],
                  total: { label: "Storefront", amount: cart.total.toFixed(2) },
                });
                session.onpaymentauthorized = async ({ payment }) => {
                  const receipt = await charge(cart, { applePay: payment.token });
                  session.completePayment(ApplePaySession.STATUS_SUCCESS);
                  onPaid(receipt);
                };
                session.begin();
              }

              return (
                <button className="apple-pay" onClick={pay} aria-label="Pay with Apple Pay">
                  Pay {formatPrice(cart.total, cart.currency)}
                </button>
              );
            }

            """,
            "tests/checkout/ApplePayButton.test.tsx": """
            import { render, screen } from "@testing-library/react";
            import { describe, expect, it, vi } from "vitest";
            import { ApplePayButton } from "../../src/checkout/ApplePayButton";
            import { cart } from "../fixtures";

            describe("ApplePayButton", () => {
              it("shows on devices that can pay with Apple Pay", async () => {
                vi.stubGlobal("ApplePaySession", { canMakePayments: () => true });
                render(<ApplePayButton cart={cart} onPaid={() => {}} />);
                expect(await screen.findByLabelText("Pay with Apple Pay")).toBeVisible();
              });

              it("stays hidden everywhere else", () => {
                vi.stubGlobal("ApplePaySession", undefined);
                render(<ApplePayButton cart={cart} onPaid={() => {}} />);
                expect(screen.queryByLabelText("Pay with Apple Pay")).toBeNull();
              });
            });

            """,
            "src/ui/Divider.tsx": """
            /** A line across the sheet with a few words in it. */
            export function Divider({ label }: { label: string }) {
              return (
                <div className="divider" role="separator">
                  <span>{label}</span>
                </div>
              );
            }

            """,
        ]
    }
}
