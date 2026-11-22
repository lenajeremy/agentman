import AppKit
import QuartzCore
import SwiftUI
import XCTest
@testable import Agentman

@MainActor @Observable
private final class HighlightModel {
    var highlight: FindHighlight?
}

@MainActor
final class ThreadRenderAndPerfTests: XCTestCase {
    private var tempFolders: [URL] = []

    override func tearDown() {
        for folder in tempFolders { try? FileManager.default.removeItem(at: folder) }
        tempFolders = []
        super.tearDown()
    }

    private func makeFolder() throws -> String {
        let folder = FileManager.default.temporaryDirectory.appendingPathComponent("agentman-perf-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        tempFolders.append(folder)
        return folder.path
    }

    private func proseReply(turn: Int) -> String {
        """
        ### Analysis for turn \(turn): Rendering & Layout Profile

        I traced the frame drops across `AgentThreadView.swift`, `MarkdownView.swift`, `MarkdownText.swift`, `FollowsEnd.swift`, and `MessageField.swift`. Here is what happens on **every frame** and **every keystroke**:

        - `AgentThreadView` previously owned `@State private var draft = ""`, so typing a single letter invalidated the entire view body, re-running `store.threadItems(session.id)`, `ThreadItem.pictures(in: items)`, `TurnNotice.live(in:limit:)`, `ReplyLinks.finder(folder:)`, and `ForEach(items)`.
        - Inside `MarkdownView`, `MarkdownCache.blocks(for:)` hashed the full `source` string, then `MarkdownView.segments(blocks)` rebuilt the segment array and hashed `source` again for `key`.
        - Inside `MarkdownProseView`, `MarkdownProseText.attributed(blocks, highlight:)` rebuilt `NSAttributedString` from scratch using `NSFontManager.shared.convert` for every **bold** and *italic* run, and `NSFont(name: "Google Sans Code", ...)` for every `inlineCode` span.
        - Inside `MarkdownTextView.layoutManager(_:lineSpacingAfterGlyphAt:)`, `storage.string as NSString` bridged the full backing string on **every single line fragment**.
        - Inside `MarkdownTextView.size(fittingWidth:)`, when SwiftUI probed `proposal.width == nil` (`600`) and then `646`, it invalidated and re-ran full TextKit 1 layout repeatedly.
        - Inside `SlashCommands.dismiss()`, `query = nil`, `results = []`, `selected = 0` were assigned unconditionally on every keystroke and selection change.

        > Note: In a real session with **hundreds of messages** and long agent outputs, scrolling past 3–4 replies in a single trackpad flick used to re-rasterize every visible and offscreen `NSTextView` backing store on the main thread. See [the profiling notes](https://example.com/perf/turn-\(turn)) and `desktop/Agentman/Design/MarkdownText.swift`.

        #### Additional findings

        1. `MessageField.updateNSView` reset `textView.font`, `textView.textColor`, and `textView.defaultParagraphStyle` on every keystroke, causing AppKit to re-attribute the entire `NSTextStorage`.
        2. `MessageField.sizeThatFits` copied `textStorage` into a new `NSMutableAttributedString` and invoked `boundingRect(with:options:)` on every keystroke.
        3. `MessageTextView.makeChips` scanned with `skillWord` regex on every keystroke even when neither `/` nor `$` was present.
        4. `AgentsStore.drafts` was `@Observable` rather than `@ObservationIgnored`, notifying observers on every keystroke.
        """
    }

    private func findAll<T: NSView>(_ type: T.Type, in view: NSView) -> [T] {
        var out: [T] = []
        func visit(_ v: NSView) {
            if let match = v as? T { out.append(match) }
            for child in v.subviews { visit(child) }
        }
        visit(view)
        return out
    }

    private func conversationScroll(in root: NSView) -> NSScrollView? {
        let scrolls = findAll(NSScrollView.self, in: root).filter { !($0 is MessageScrollView) && $0.frame.width > 300 && $0.frame.height > 300 }
        return scrolls.max { ($0.frame.width * $0.frame.height) < ($1.frame.width * $1.frame.height) }
    }

    private func settle(_ seconds: TimeInterval, until done: () -> Bool = { false }) {
        let deadline = Date().addingTimeInterval(seconds)
        while !done(), Date() < deadline {
            RunLoop.main.run(until: Date().addingTimeInterval(0.015))
        }
    }

    private func makeWindow<V: View>(rootView: V, size: CGSize = CGSize(width: 1280, height: 820), appearance: NSAppearance.Name = .aqua) -> NSWindow {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size),
                              styleMask: [.titled, .closable, .resizable],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.appearance = NSAppearance(named: appearance)
        window.contentView = NSHostingView(rootView: rootView.frame(width: size.width, height: size.height))
        window.setContentSize(size)
        window.orderFrontRegardless()
        return window
    }

    /// Samples pixels inside `rect` (in `view` coords) and returns how many
    /// deviate noticeably from the top-left background color (i.e. rendered ink).
    private func inkPixelCount(in view: NSView, rect: NSRect) -> Int {
        view.layoutSubtreeIfNeeded()
        view.displayIfNeeded()
        CATransaction.flush()
        let bounds = rect.intersection(view.bounds).integral
        guard bounds.width > 4, bounds.height > 4,
              let rep = view.bitmapImageRepForCachingDisplay(in: bounds) else { return 0 }
        view.cacheDisplay(in: bounds, to: rep)
        guard let bg = rep.colorAt(x: 2, y: 2)?.usingColorSpace(.sRGB) else { return 0 }
        var ink = 0
        let stepX = max(1, rep.pixelsWide / 80)
        let stepY = max(1, rep.pixelsHigh / 80)
        for y in stride(from: 4, to: rep.pixelsHigh - 4, by: stepY) {
            for x in stride(from: 4, to: rep.pixelsWide - 4, by: stepX) {
                if let c = rep.colorAt(x: x, y: y)?.usingColorSpace(.sRGB) {
                    let diff = abs(c.redComponent - bg.redComponent)
                        + abs(c.greenComponent - bg.greenComponent)
                        + abs(c.blueComponent - bg.blueComponent)
                    if diff > 0.12 { ink += 1 }
                }
            }
        }
        return ink
    }

    // MARK: - Bug 1: Blank chat on cold async load

    /// Opening an agent chat before messages arrive (tool-heavy, very tall, or
    /// incremental arrival) must render immediately at the bottom with real
    /// painted content — never landing blank in phantom scroll space.
    func testColdAsyncLoadRendersImmediatelyWithoutLayoutShift() throws {
        let folder = try makeFolder()
        let now = Int64(Date().timeIntervalSince1970 * 1000)

        struct Scenario {
            let id: String
            let batches: [[Message]]
        }

        // Scenario 1: Tool-heavy (35 tool calls + 1 long final reply)
        var toolMsgs: [Message] = [Message(id: "t-u0", sessionId: "toolheavy", role: .user, ts: now - 500_000, text: "Inspect everything")]
        for i in 0..<35 {
            toolMsgs.append(Message(id: "t-c\(i)", sessionId: "toolheavy", role: .tool, ts: now - 490_000 + Int64(i * 1000),
                                    tool: ToolCall(name: "Read", summary: "file-\(i).swift", status: "done")))
        }
        toolMsgs.append(Message(id: "t-a0", sessionId: "toolheavy", role: .assistant, ts: now - 10_000, text: proseReply(turn: 1)))

        // Scenario 2: Very tall (30 turns = 60 heavy messages)
        var tallMsgs: [Message] = []
        for i in 0..<30 {
            let base = now - 600_000 + Int64(i * 10_000)
            tallMsgs.append(Message(id: "h-u\(i)", sessionId: "tall", role: .user, ts: base, text: "Tall prompt \(i)"))
            tallMsgs.append(Message(id: "h-a\(i)", sessionId: "tall", role: .assistant, ts: base + 5000, text: proseReply(turn: i)))
        }

        // Scenario 3: 1 message first, then 40 messages arrive shortly after
        var oneThenManyFirst = [Message(id: "m-u0", sessionId: "onethenmany", role: .user, ts: now - 500_000, text: "First message")]
        var oneThenManyFull = oneThenManyFirst
        for i in 1..<21 {
            let base = now - 400_000 + Int64(i * 10_000)
            oneThenManyFull.append(Message(id: "m-u\(i)", sessionId: "onethenmany", role: .user, ts: base, text: "Turn \(i)"))
            oneThenManyFull.append(Message(id: "m-a\(i)", sessionId: "onethenmany", role: .assistant, ts: base + 5000, text: proseReply(turn: i)))
        }

        let scenarios = [
            Scenario(id: "toolheavy", batches: [toolMsgs]),
            Scenario(id: "tall", batches: [tallMsgs]),
            Scenario(id: "onethenmany", batches: [oneThenManyFirst, oneThenManyFull]),
        ]

        let sessions = scenarios.map {
            Session(id: $0.id, kind: "claude", name: $0.id, cwd: folder, state: .idle, inject: "tmux", lastActivityAt: now)
        }
        let client = DaemonClient(socketPath: "/nonexistent/agentman-cold.sock")
        client.answer = { _ in DaemonEvent(type: "error") }
        let store = AgentsStore(client: client)
        var sEv = DaemonEvent(type: "sessions"); sEv.sessions = sessions; store.apply(sEv)
        store.selection = .manager

        let window = makeWindow(rootView: ContentView().environment(store))
        defer { window.orderOut(nil) }
        let root = try XCTUnwrap(window.contentView)
        settle(0.25)

        for scenario in scenarios {
            // Switch to thread BEFORE messages are loaded (cold async open).
            store.selection = .agent(scenario.id)
            settle(0.15)

            for batch in scenario.batches {
                var ev = DaemonEvent(type: "messages")
                ev.sessionId = scenario.id
                ev.messages = batch
                store.apply(ev)
                settle(0.35)
            }
            settle(0.35)

            let scroll = try XCTUnwrap(conversationScroll(in: root), "\(scenario.id): scroll view never mounted")
            let doc = try XCTUnwrap(scroll.documentView, "\(scenario.id): missing documentView")
            let clip = scroll.contentView
            let docH = doc.frame.height
            let clipH = clip.bounds.height
            let originY = clip.bounds.origin.y
            let maxY = max(-scroll.contentInsets.top, docH - clipH + scroll.contentInsets.bottom)
            let gapBottom = maxY - originY

            XCTAssertGreaterThan(docH, 200, "\(scenario.id): document height collapsed (\(docH))")
            XCTAssertLessThanOrEqual(originY, maxY + 4, "\(scenario.id): clipY=\(originY) stranded past maxY=\(maxY)")
            XCTAssertLessThanOrEqual(gapBottom, FollowsEnd.slack + 24, "\(scenario.id): stuck above bottom (docH=\(docH), clipY=\(originY), gapBottom=\(gapBottom))")

            let surfaces = findAll(MarkdownTextSurface.self, in: scroll)
            let visibleSurfaces = surfaces.filter { surface in
                let inScroll = surface.convert(surface.bounds, to: scroll)
                return !surface.bounds.isEmpty && inScroll.intersects(scroll.bounds)
            }
            XCTAssertFalse(visibleSurfaces.isEmpty, "\(scenario.id): no visible MarkdownTextSurface in viewport")

            let scrollInRoot = scroll.convert(scroll.bounds, to: root)
            let ink = inkPixelCount(in: root, rect: scrollInRoot)
            XCTAssertGreaterThan(ink, 25, "\(scenario.id): viewport rendered blank (ink=\(ink)) without sidebar resize")
        }
    }

    /// ManagerChatView transitioning from its empty welcome screen (EntryHero)
    /// to a populated conversation must mount at the bottom with painted replies.
    func testManagerChatTransitionFromEntryToConversationRendersAtBottom() throws {
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        let client = DaemonClient(socketPath: "/nonexistent/agentman-mgr.sock")
        client.answer = { _ in DaemonEvent(type: "error") }
        let store = AgentsStore(client: client)
        store.selection = .manager

        let window = makeWindow(rootView: ContentView().environment(store))
        defer { window.orderOut(nil) }
        let root = try XCTUnwrap(window.contentView)
        settle(0.25)

        // Deliver 20 entries asynchronously, transitioning entry -> conversation.
        for i in 0..<20 {
            var ev = DaemonEvent(type: "manager_entry")
            ev.entry = ManagerEntry(
                id: "m-\(i)",
                role: i % 2 == 0 ? "user" : "assistant",
                text: i % 2 == 0 ? "Question \(i)?" : proseReply(turn: i),
                ts: now + Int64(i * 1000)
            )
            store.apply(ev)
        }
        settle(0.6)

        let scroll = try XCTUnwrap(conversationScroll(in: root), "Manager scroll view did not mount")
        let doc = try XCTUnwrap(scroll.documentView)
        let clip = scroll.contentView
        let maxY = max(-scroll.contentInsets.top, doc.frame.height - clip.bounds.height + scroll.contentInsets.bottom)
        XCTAssertGreaterThan(doc.frame.height, 400)
        XCTAssertLessThanOrEqual(clip.bounds.origin.y, maxY + 4)
        XCTAssertLessThanOrEqual(maxY - clip.bounds.origin.y, FollowsEnd.slack + 24)

        let ink = inkPixelCount(in: root, rect: scroll.convert(scroll.bounds, to: root))
        XCTAssertGreaterThan(ink, 25, "ManagerChatView rendered blank after entry->conversation transition")
    }

    // MARK: - MarkdownTextSurface rendering, find highlight, and interactive promotion

    func testMarkdownSurfaceRendersLightDarkHighlightsAndActivatesOnInteraction() throws {
        struct Host: View {
            let source: String
            let model: HighlightModel
            let onLink: (URL) -> Void
            var body: some View {
                VStack(alignment: .leading) {
                    MarkdownView(source)
                        .equatable()
                        .quotable()
                        .environment(\.findHighlight, model.highlight)
                        .environment(\.openURL, OpenURLAction { url in
                            onLink(url)
                            return .handled
                        })
                }
                .padding(24)
                .frame(width: 640, height: 600, alignment: .top)
                .background(Palette.paper)
            }
        }

        let source = """
        Hello **world** with `inlineCode` and a [link](https://example.com/docs).<br>Second line in paragraph.

        > Quoted passage for testing.

        ```swift
        let answer = 42
        ```
        """
        let model = HighlightModel()
        var openedURL: URL?
        let window = makeWindow(rootView: Host(source: source, model: model, onLink: { openedURL = $0 }),
                                size: CGSize(width: 640, height: 600), appearance: .aqua)
        defer { window.orderOut(nil) }
        let root = try XCTUnwrap(window.contentView)
        settle(0.25)

        let surfaces = findAll(MarkdownTextSurface.self, in: root)
        XCTAssertEqual(surfaces.count, 2, "one prose surface and one code surface")
        XCTAssertTrue(surfaces.allSatisfy { $0.activeTextView == nil }, "passive mode creates zero NSTextViews")

        // Light mode renders ink pixels and exposes accessible text with plain newlines.
        let lightInk = inkPixelCount(in: root, rect: root.bounds)
        XCTAssertGreaterThan(lightInk, 25, "MarkdownTextSurface should draw visible glyphs in light mode")
        let accessible = try XCTUnwrap(surfaces[0].accessibilityValue() as? String)
        XCTAssertTrue(accessible.contains("Hello world"), "accessible text: \(accessible)")
        XCTAssertTrue(accessible.contains(".\nSecond line"), "line separator normalized to newline")

        // Dark mode updates cleanly without blanking.
        window.appearance = NSAppearance(named: .darkAqua)
        settle(0.15)
        let darkInk = inkPixelCount(in: root, rect: root.bounds)
        XCTAssertGreaterThan(darkInk, 25, "MarkdownTextSurface should draw visible glyphs in dark mode")

        // FindHighlight propagates through MarkdownView.equatable().
        model.highlight = FindHighlight(words: ["Quoted"], current: true)
        settle(0.15)
        let tv = surfaces[0].activateTextView()
        let plain = tv.string as NSString
        let qRange = plain.range(of: "Quoted")
        XCTAssertNotEqual(qRange.location, NSNotFound)
        XCTAssertNotNil(tv.textStorage?.attribute(.backgroundColor, at: qRange.location, effectiveRange: nil),
                        "FindHighlight must propagate through MarkdownView.equatable()")

        // Selection, copy (\u{2028} -> \n), link click, and Reply-quote watcher all work on activated view.
        window.makeFirstResponder(tv)
        tv.setSelectedRange(NSRange(location: 0, length: plain.length))
        let pasteboard = NSPasteboard(name: NSPasteboard.Name("surface-copy-test-\(UUID().uuidString)"))
        pasteboard.clearContents()
        XCTAssertTrue(tv.writeSelection(to: pasteboard, type: .string))
        XCTAssertTrue((pasteboard.string(forType: .string) ?? "").contains(".\nSecond line"))

        let selected = SelectionWatcherView.selection(in: window)
        XCTAssertNotNil(selected, "SelectionWatcherView should read activated MarkdownTextView selection")
        XCTAssertTrue(selected?.text.contains("Hello world") == true)

        // Link click routes through coordinator's openURL action.
        let linkURL = URL(string: "https://example.com/docs")!
        let handled = tv.delegate?.textView?(tv, clickedOnLink: linkURL, at: 0) ?? false
        XCTAssertTrue(handled)
        XCTAssertEqual(openedURL, linkURL)

        // Resigning focus with empty selection returns the surface to zero-cost static mode.
        tv.setSelectedRange(NSRange(location: 0, length: 0))
        window.makeFirstResponder(nil)
        settle(0.1)
        XCTAssertNil(surfaces[0].activeTextView, "Idle surface should deactivate back to passive mode")
    }

    /// Opening or switching to a chat must never scroll the sidebar's scroll view.
    func testSwitchingChatsNeverScrollsSidebar() throws {
        let folder = try makeFolder()
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        // Enough sessions so the sidebar itself scrolls vertically.
        var sessions: [Session] = []
        for i in 0..<35 {
            sessions.append(Session(id: "s-\(i)", kind: "claude", name: "Agent \(i)", cwd: folder, state: .idle, inject: "tmux", lastActivityAt: now - Int64(i * 1000)))
        }
        let client = DaemonClient(socketPath: "/nonexistent/agentman-sidebar.sock")
        client.answer = { _ in DaemonEvent(type: "error") }
        let store = AgentsStore(client: client)
        var sEv = DaemonEvent(type: "sessions"); sEv.sessions = sessions; store.apply(sEv)
        store.selection = .manager

        let window = makeWindow(rootView: ContentView().environment(store), size: CGSize(width: 1280, height: 600))
        defer { window.orderOut(nil) }
        let root = try XCTUnwrap(window.contentView)
        settle(0.35)

        // Identify the sidebar scroll view (leftmost tall scroll view).
        let sidebarScroll = try XCTUnwrap(
            findAll(NSScrollView.self, in: root)
                .filter { !($0 is MessageScrollView) && $0.frame.height > 300 && $0.convert($0.bounds, to: nil).minX < 40 }
                .first,
            "sidebar scroll view not found"
        )
        let sidebarClip = sidebarScroll.contentView
        let initialSidebarY = sidebarClip.bounds.origin.y

        // Switch to a new chat and deliver 20 messages asynchronously.
        store.selection = .agent("s-0")
        settle(0.1)
        var msgs: [Message] = []
        for i in 0..<20 {
            msgs.append(Message(id: "sb-u\(i)", sessionId: "s-0", role: .user, ts: now + Int64(i * 2000), text: "Prompt \(i)"))
            msgs.append(Message(id: "sb-a\(i)", sessionId: "s-0", role: .assistant, ts: now + Int64(i * 2000 + 1000), text: proseReply(turn: i)))
        }
        var mEv = DaemonEvent(type: "messages"); mEv.sessionId = "s-0"; mEv.messages = msgs; store.apply(mEv)
        settle(0.5)

        XCTAssertEqual(sidebarClip.bounds.origin.y, initialSidebarY, accuracy: 2.0,
                       "Opening a chat must not scroll the sidebar (moved from \(initialSidebarY) to \(sidebarClip.bounds.origin.y))")
    }

    // MARK: - Bug 2: Scroll FPS & Layer Caching

    /// Scrolling a heavy prose thread must keep backing layers cached (0 dirty
    /// surface layers on passive scroll steps) and keep commit/frame times well
    /// within the 60-120Hz budget.
    func testScrollingHeavyProseThreadStaysFastAndZeroLayerInvalidation() throws {
        let folder = try makeFolder()
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        let session = Session(id: "scroll-bench", kind: "claude", name: "scroll-bench", cwd: folder, state: .idle, inject: "tmux", lastActivityAt: now)
        let client = DaemonClient(socketPath: "/nonexistent/agentman-scroll.sock")
        client.answer = { _ in DaemonEvent(type: "error") }
        let store = AgentsStore(client: client)
        var sEv = DaemonEvent(type: "sessions"); sEv.sessions = [session]; store.apply(sEv)
        var msgs: [Message] = []
        var ts = now - 3_600_000
        for turn in 0..<25 {
            ts += 5000; msgs.append(Message(id: "u\(turn)", sessionId: session.id, role: .user, ts: ts, text: "Turn \(turn) prompt"))
            ts += 5000; msgs.append(Message(id: "a\(turn)", sessionId: session.id, role: .assistant, ts: ts, text: proseReply(turn: turn)))
        }
        var mEv = DaemonEvent(type: "messages"); mEv.sessionId = session.id; mEv.messages = msgs; store.apply(mEv)
        store.selection = .agent(session.id)

        let window = makeWindow(rootView: ContentView().environment(store), size: CGSize(width: 1440, height: 900))
        defer { window.orderOut(nil) }
        let root = try XCTUnwrap(window.contentView)
        settle(0.6)

        let scroll = try XCTUnwrap(conversationScroll(in: root))
        let clip = scroll.contentView

        var commits: [Double] = []
        var totals: [Double] = []
        var existingSurfaceDirtyFrames = 0
        var seenSurfaces = Set<ObjectIdentifier>()

        for step in 0..<110 where clip.bounds.origin.y > 0.5 {
            let y = max(0, clip.bounds.origin.y - 48)
            let t0 = CFAbsoluteTimeGetCurrent()
            clip.scroll(to: NSPoint(x: clip.bounds.origin.x, y: y))
            scroll.reflectScrolledClipView(clip)
            root.layoutSubtreeIfNeeded()
            window.displayIfNeeded()

            // Existing surfaces that were already drawn must NOT have their backing layer dirtied by scroll!
            let surfaces = findAll(MarkdownTextSurface.self, in: scroll)
            let dirtyExisting = surfaces.filter { seenSurfaces.contains(ObjectIdentifier($0)) && $0.layer?.needsDisplay() == true }.count
            if dirtyExisting > 0 { existingSurfaceDirtyFrames += 1 }
            for s in surfaces { seenSurfaces.insert(ObjectIdentifier(s)) }

            let tc0 = CFAbsoluteTimeGetCurrent()
            CATransaction.flush()
            let tc1 = CFAbsoluteTimeGetCurrent()
            RunLoop.main.run(mode: .default, before: Date())
            let t1 = CFAbsoluteTimeGetCurrent()

            if step >= 20 {
                commits.append((tc1 - tc0) * 1000)
                totals.append((t1 - t0) * 1000)
            }
        }

        let sc = commits.sorted(), st = totals.sorted()
        let p50c = sc[sc.count / 2], p90c = sc[Int(Double(sc.count) * 0.9)], maxc = sc.last ?? 0
        let p50t = st[st.count / 2], p90t = st[Int(Double(st.count) * 0.9)], maxt = st.last ?? 0
        let over16 = totals.filter { $0 > 16.7 }.count
        print(String(format: "SCROLL BENCH (%d steps): commit p50=%.2fms p90=%.2fms max=%.2fms | total p50=%.2fms p90=%.2fms max=%.2fms | >16.7ms=%d | dirtyExistingFrames=%d",
                     commits.count, p50c, p90c, maxc, p50t, p90t, maxt, over16, existingSurfaceDirtyFrames))

        XCTAssertEqual(existingSurfaceDirtyFrames, 0, "Scroll must not dirty already-rasterized MarkdownTextSurface layers")
        XCTAssertLessThan(p50c, 4.0, "Median CATransaction commit should stay under 4ms (was 34.7ms)")
        XCTAssertLessThan(p50t, 8.0, "Median scroll frame should stay under 8ms")
    }

    // MARK: - Bug 2: Keyboard Typing Isolation & Speed

    /// Typing in a heavy agent thread must be isolated to ThreadComposerBar,
    /// without re-rendering Markdown views or dropping frames.
    func testTypingInHeavyThreadStaysFastAndSavesDraft() throws {
        let folder = try makeFolder()
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        let session = Session(id: "type-bench", kind: "claude", name: "type-bench", cwd: folder, state: .idle, inject: "tmux", lastActivityAt: now)
        let client = DaemonClient(socketPath: "/nonexistent/agentman-type.sock")
        client.answer = { [session] request in
            if request.type == .listSessions {
                var ev = DaemonEvent(type: "sessions")
                ev.sessions = [session]
                return ev
            }
            if request.type == .listCommands {
                var ev = DaemonEvent(type: "commands")
                ev.commands = [
                    SlashCommand(name: "compact", description: "Compact", source: "builtin"),
                    SlashCommand(name: "review", description: "Review code", source: "skill", skill: true),
                ]
                return ev
            }
            return DaemonEvent(type: "error")
        }
        let store = AgentsStore(client: client)
        store.statusChanged(.connected)
        var sEv = DaemonEvent(type: "sessions"); sEv.sessions = [session]; store.apply(sEv)
        var msgs: [Message] = []
        var ts = now - 3_600_000
        for turn in 0..<25 {
            ts += 5000; msgs.append(Message(id: "tu\(turn)", sessionId: session.id, role: .user, ts: ts, text: "Prompt \(turn)"))
            ts += 5000; msgs.append(Message(id: "ta\(turn)", sessionId: session.id, role: .assistant, ts: ts, text: proseReply(turn: turn)))
        }
        var mEv = DaemonEvent(type: "messages"); mEv.sessionId = session.id; mEv.messages = msgs; store.apply(mEv)
        store.selection = .agent(session.id)

        let window = makeWindow(rootView: ContentView().environment(store), size: CGSize(width: 1440, height: 900))
        defer { window.orderOut(nil) }
        let root = try XCTUnwrap(window.contentView)
        settle(0.6)

        let box = try XCTUnwrap(findAll(MessageTextView.self, in: root).first)
        window.makeFirstResponder(box)
        settle(0.1)

        // Warm up first keystroke
        box.insertText("H", replacementRange: box.selectedRange())
        root.layoutSubtreeIfNeeded()
        CATransaction.flush()
        RunLoop.main.run(mode: .default, before: Date())

        let phrase = "ello world, typing fast inside a very long thread!"
        var keyTimes: [Double] = []
        for ch in phrase {
            let t0 = CFAbsoluteTimeGetCurrent()
            box.insertText(String(ch), replacementRange: box.selectedRange())
            root.layoutSubtreeIfNeeded()
            window.displayIfNeeded()
            CATransaction.flush()
            RunLoop.main.run(mode: .default, before: Date())
            keyTimes.append((CFAbsoluteTimeGetCurrent() - t0) * 1000)
        }
        settle(0.05)

        let sorted = keyTimes.sorted()
        let p50 = sorted[sorted.count / 2]
        let p90 = sorted[Int(Double(sorted.count) * 0.9)]
        let maxMs = sorted.last ?? 0
        print(String(format: "TYPING BENCH (%d keys): p50=%.2fms p90=%.2fms max=%.2fms", keyTimes.count, p50, p90, maxMs))

        XCTAssertEqual(store.drafts[session.id], "H" + phrase)
        XCTAssertLessThan(p50, 5.0, "Median keystroke time in a heavy thread should stay under 5ms")
    }

    // MARK: - Markdown / CodeHighlighter / Size Cache Micro-Benchmarks

    func testMarkdownProseSizeAndHighlightCachesEliminateRepeatedWork() {
        MarkdownProseCache.clear()
        MarkdownSizeCache.clear()

        let text = proseReply(turn: 999)
        let entry = MarkdownCache.entry(for: text)
        guard case .prose(let run)? = entry.segments.first?.content else {
            return XCTFail("expected prose segment")
        }
        let key = "bench-\(entry.id)"

        let t0 = CFAbsoluteTimeGetCurrent()
        let coldAttr = MarkdownProseCache.attributed(key: key, blocks: run, highlight: nil)
        let coldAttrMs = (CFAbsoluteTimeGetCurrent() - t0) * 1000

        let t1 = CFAbsoluteTimeGetCurrent()
        for _ in 0..<20 {
            let warmAttr = MarkdownProseCache.attributed(key: key, blocks: run, highlight: nil)
            XCTAssertTrue(warmAttr === coldAttr, "Prose cache should return the exact cached NSAttributedString")
        }
        let warmAttrMs = (CFAbsoluteTimeGetCurrent() - t1) * 1000 / 20.0

        // Simulate SwiftUI's 8x sizeThatFits ping-pong (600 <-> 646) on cold & warm surfaces.
        let coldSurface = MarkdownTextSurface(wraps: true)
        coldSurface.configure(key: key, highlight: nil, attributed: coldAttr)
        let s0 = CFAbsoluteTimeGetCurrent()
        var expected646 = CGSize.zero
        for _ in 0..<4 {
            _ = coldSurface.size(fittingWidth: 600)
            expected646 = coldSurface.size(fittingWidth: 646)
        }
        let coldPingPongMs = (CFAbsoluteTimeGetCurrent() - s0) * 1000

        // A freshly created surface (as LazyVStack creates when scrolling back) hits MarkdownSizeCache.
        let warmSurface = MarkdownTextSurface(wraps: true)
        warmSurface.configure(key: key, highlight: nil, attributed: coldAttr)
        let s1 = CFAbsoluteTimeGetCurrent()
        for _ in 0..<4 {
            _ = warmSurface.size(fittingWidth: 600)
            let got = warmSurface.size(fittingWidth: 646)
            XCTAssertEqual(got, expected646)
        }
        let warmPingPongMs = (CFAbsoluteTimeGetCurrent() - s1) * 1000

        // CodeHighlighter caches compiled regex by language key.
        let lang = CodeHighlighter.language(for: "sample.swift")!
        let sampleCode = String(repeating: "func render(_ value: Int) -> String { return \"turn \\(value)\" } // comment\n", count: 40)
        _ = CodeHighlighter.tokens(in: sampleCode, language: lang)
        let c0 = CFAbsoluteTimeGetCurrent()
        for _ in 0..<10 {
            _ = CodeHighlighter.tokens(in: sampleCode, language: lang)
        }
        let codeMs = (CFAbsoluteTimeGetCurrent() - c0) * 1000 / 10.0

        print(String(format: "CACHE BENCH: attr cold=%.2fms warm=%.4fms | 8x sizeThatFits cold=%.2fms warm=%.4fms | code=%.2fms",
                     coldAttrMs, warmAttrMs, coldPingPongMs, warmPingPongMs, codeMs))

        XCTAssertLessThan(warmAttrMs, 0.1)
        XCTAssertLessThan(warmPingPongMs, 0.2)
    }
}
