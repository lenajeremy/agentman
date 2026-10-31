import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// The image viewer: the pictures a conversation's arrows go through, ←
/// and → moving between them, a picture fitted and zoomed, and, with
/// TEST_RUNNER_AGENTMAN_SHOTS=<folder>, the viewer and an image file in the
/// editor.
@MainActor
final class ImageViewerTests: XCTestCase {
    func testAPictureFitsAndZoomsWithinBounds() {
        XCTAssertEqual(ZoomableImage.fitScale(CGSize(width: 2000, height: 1000), in: CGSize(width: 1000, height: 800)), 0.5)
        XCTAssertEqual(ZoomableImage.fitScale(CGSize(width: 200, height: 100), in: CGSize(width: 1000, height: 800)), 1,
                       "A small picture isn't blown up")
        XCTAssertEqual(ZoomableImage.actualSize(fit: 0.5), 2, "A double-click shows it at its own size")
        XCTAssertEqual(ZoomableImage.actualSize(fit: 1), 2)
        XCTAssertEqual(ZoomableImage.clamp(0.5, fit: 0.5), 1, "Never smaller than fitting")
        XCTAssertEqual(ZoomableImage.clamp(100, fit: 0.5), 16, "At most eight times its own size")
    }

    func testThePictureFitsTheViewAndStaysInItsMiddle() throws {
        let view = PictureScrollView()
        view.frame = NSRect(x: 0, y: 0, width: 400, height: 300)
        view.show(Self.swatch(.systemBlue, CGSize(width: 800, height: 600)))
        XCTAssertEqual(view.magnification, 0.5, accuracy: 0.001)
        XCTAssertEqual(view.zoom, 1)

        view.setZoom(2)
        XCTAssertEqual(view.magnification, 1, accuracy: 0.001, "Twice fitting is its own size")
        view.setZoom(1)
        XCTAssertEqual(view.magnification, 0.5, accuracy: 0.001)

        // A larger view fits it larger; it stays fitted.
        view.setFrameSize(NSSize(width: 1600, height: 1200))
        XCTAssertEqual(view.magnification, 1, accuracy: 0.001, "Not past its own size")

        // A small picture sits in the middle.
        view.show(Self.swatch(.systemPink, CGSize(width: 100, height: 100)))
        let bounds = view.contentView.constrainBoundsRect(NSRect(origin: .zero, size: view.contentView.bounds.size))
        XCTAssertEqual(bounds.midX, 50, accuracy: 0.5)
        XCTAssertEqual(bounds.midY, 50, accuracy: 0.5)
    }

    func testTheArrowsGoThroughTheConversationsPictures() {
        func read(_ id: String, _ path: String) -> Message {
            Message(id: id, sessionId: "s", role: .tool, ts: 1, text: nil, tool: ToolCall(name: "Read", summary: path, status: nil))
        }
        let items = ThreadItem.items(from: [
            Message(id: "1", sessionId: "s", role: .user, ts: 1, text: "look", tool: nil),
            read("2", "/tmp/a.png"),
            Message(id: "3", sessionId: "s", role: .assistant, ts: 1, text: "and these", tool: nil),
            read("4", "/tmp/b.png"), read("5", "/tmp/c.png"), read("6", "/tmp/a.png"),
        ])
        XCTAssertEqual(ThreadItem.pictures(in: items), ["/tmp/a.png", "/tmp/b.png", "/tmp/c.png"],
                       "In order, each once")

        let box = Lightbox.files(["/tmp/a.png", "/tmp/b.png"], from: "/tmp/b.png")
        XCTAssertEqual(box.pictures.map(\.title), ["a.png", "b.png"])
        XCTAssertEqual(box.start, 1)
        let alone = Lightbox.files(["/tmp/a.png"], from: "/tmp/z.png")
        XCTAssertEqual(alone.pictures.map(\.title), ["z.png"], "One not in the group opens alone")
        XCTAssertEqual(Lightbox(pictures: [], start: 4).start, 0)
    }

    // The scroll wheel zooms about the pointer: the spot under it stays put.
    // A real wheel's notch zooms a step; a trackpad pans a zoomed picture.
    func testTheScrollWheelZoomsAboutThePointer() throws {
        let view = PictureScrollView()
        view.frame = NSRect(x: 0, y: 0, width: 400, height: 300)
        let window = NSWindow(contentRect: view.frame, styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.contentView = view
        defer { window.orderOut(nil) }
        view.show(Self.swatch(.systemBlue, CGSize(width: 800, height: 600)))
        XCTAssertEqual(view.zoom, 1)

        // A notch away zooms in a step, about a spot off centre.
        let spot = NSPoint(x: 600, y: 150)
        let before = view.picture.convert(spot, to: view)
        view.wheelZoom(by: 1, precise: false, at: spot)
        XCTAssertEqual(view.zoom, ZoomableImage.step, accuracy: 0.001)
        let after = view.picture.convert(spot, to: view)
        XCTAssertEqual(after.x, before.x, accuracy: 1, "The spot under the pointer stays put")
        XCTAssertEqual(after.y, before.y, accuracy: 1)
        // A notch back, and no further out than fitting.
        view.wheelZoom(by: -1, precise: false, at: spot)
        view.wheelZoom(by: -1, precise: false, at: spot)
        XCTAssertEqual(view.zoom, 1, accuracy: 0.001)

        // A mouse's wheel, as AppKit hands it over.
        let notch = try XCTUnwrap(CGEvent(scrollWheelEvent2Source: nil, units: .line, wheelCount: 1, wheel1: 1, wheel2: 0, wheel3: 0))
        let wheel = try XCTUnwrap(NSEvent(cgEvent: notch))
        XCTAssertFalse(wheel.hasPreciseScrollingDeltas)
        view.scrollWheel(with: wheel)
        XCTAssertNotEqual(view.zoom, 1, accuracy: 0.001, "The wheel zoomed")

        // Zoomed in, a trackpad's scroll is left to the scroll view to pan
        // with, not zoomed. (AppKit doesn't move an offscreen scroll view
        // for a made-up event, so the pan itself isn't checked here.)
        view.setZoom(2)
        let swipe = try XCTUnwrap(CGEvent(scrollWheelEvent2Source: nil, units: .pixel, wheelCount: 1, wheel1: -40, wheel2: 0, wheel3: 0))
        let pan = try XCTUnwrap(NSEvent(cgEvent: swipe))
        XCTAssertTrue(pan.hasPreciseScrollingDeltas)
        view.scrollWheel(with: pan)
        XCTAssertEqual(view.zoom, 2, accuracy: 0.001, "It panned, not zoomed")
        // At fit, there is nowhere to pan: the trackpad zooms.
        view.setZoom(1)
        let up = try XCTUnwrap(NSEvent(cgEvent: try XCTUnwrap(CGEvent(scrollWheelEvent2Source: nil, units: .pixel, wheelCount: 1,
                                                                        wheel1: 40, wheel2: 0, wheel3: 0))))
        view.scrollWheel(with: up)
        XCTAssertNotEqual(view.zoom, 1, accuracy: 0.001, "A fitted picture zooms")
    }

    func testArrowKeysMoveBetweenPictures() throws {
        let asked = Asked()
        let colors: [NSColor] = [.systemBlue, .systemGreen, .systemOrange]
        let box = Lightbox(pictures: colors.enumerated().map { i, color in
            Lightbox.Picture(title: "shot-\(i + 1).png") {
                asked.add(i)
                return Self.swatch(color, CGSize(width: 1200, height: 800))
            }
        }, start: 1)
        let window = Self.window(LightboxView(box: box), size: CGSize(width: 900, height: 600), dark: true)
        defer { window.orderOut(nil) }
        RunLoop.main.run(until: Date().addingTimeInterval(0.5))
        XCTAssertEqual(asked.all, [1], "It opens on the one clicked")

        for (key, code) in [(NSRightArrowFunctionKey, 124), (NSRightArrowFunctionKey, 124), (NSLeftArrowFunctionKey, 123)] {
            let event = try XCTUnwrap(NSEvent.keyEvent(
                with: .keyDown, location: .zero, modifierFlags: [.function, .numericPad], timestamp: 0,
                windowNumber: window.windowNumber, context: nil,
                characters: String(Character(UnicodeScalar(key)!)), charactersIgnoringModifiers: String(Character(UnicodeScalar(key)!)),
                isARepeat: false, keyCode: UInt16(code)))
            _ = window.performKeyEquivalent(with: event)
            RunLoop.main.run(until: Date().addingTimeInterval(0.3))
        }
        XCTAssertEqual(asked.all, [1, 2, 1], "→ goes on, not past the last; ← goes back")
    }

    // MARK: - Copying

    /// A clipboard of the test's own, so the user's isn't touched.
    private func privateClipboard() -> NSPasteboard {
        let board = NSPasteboard(name: NSPasteboard.Name("agentman-test-" + UUID().uuidString))
        let was = ImageClipboard.pasteboard
        ImageClipboard.pasteboard = board
        addTeardownBlock { @MainActor in
            ImageClipboard.pasteboard = was
            board.releaseGlobally()
        }
        return board
    }

    /// The picture on the clipboard, as an app pasting PNG reads it.
    private func copied(_ board: NSPasteboard) -> CGSize? {
        guard let png = board.data(forType: .png), let rep = NSBitmapImageRep(data: png) else { return nil }
        return CGSize(width: rep.pixelsWide, height: rep.pixelsHigh)
    }

    func testCopyImagePutsThePictureOnTheClipboard() {
        let board = privateClipboard()
        ImageClipboard.copy(Self.swatch(.systemTeal, CGSize(width: 300, height: 200)))
        XCTAssertEqual(copied(board), CGSize(width: 300, height: 200))
        XCTAssertNotNil(board.data(forType: .tiff), "TIFF too, for apps that take only that")
    }

    // A right-click on the picture in the viewer or an editor tab offers
    // Copy Image; beside the picture, nothing.
    func testRightClickingTheViewersPictureCopiesIt() throws {
        let board = privateClipboard()
        let view = PictureScrollView()
        view.frame = NSRect(x: 0, y: 0, width: 400, height: 300)
        view.show(Self.swatch(.systemOrange, CGSize(width: 800, height: 600)))
        let click = try XCTUnwrap(NSEvent.mouseEvent(with: .rightMouseDown, location: .zero, modifierFlags: [], timestamp: 0,
                                                     windowNumber: 0, context: nil, eventNumber: 0, clickCount: 1, pressure: 1))
        let menu = try XCTUnwrap(view.picture.menu(for: click))
        XCTAssertEqual(menu.items.map(\.title), ["Copy Image"])
        menu.performActionForItem(at: 0)
        XCTAssertEqual(copied(board), CGSize(width: 800, height: 600), "The whole picture, not as fitted")
    }

    // A right-click on a picture in a conversation offers Copy Image too,
    // and copies the picture itself, not its thumbnail.
    func testRightClickingAPictureInAConversationCopiesIt() throws {
        let board = privateClipboard()
        let folder = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: folder) }
        let path = folder.appendingPathComponent("sent.png").path
        let rep = try XCTUnwrap(NSBitmapImageRep(data: try XCTUnwrap(Self.swatch(.systemPurple, CGSize(width: 1200, height: 900)).tiffRepresentation)))
        try XCTUnwrap(rep.representation(using: .png, properties: [:])).write(to: URL(fileURLWithPath: path))

        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-viewer.sock"))
        let window = Self.window(SentFiles(paths: [path]).padding(20).frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading),
                                 size: CGSize(width: 400, height: 200), dark: false, store: store)
        defer { window.orderOut(nil) }
        RunLoop.main.run(until: Date().addingTimeInterval(1))
        // In the middle of the thumbnail, 20 in and 110 high.
        let point = NSPoint(x: 60, y: 200 - 20 - 55)
        let click = try XCTUnwrap(NSEvent.mouseEvent(with: .rightMouseDown, location: point, modifierFlags: [], timestamp: 0,
                                                     windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 1))
        let content = try XCTUnwrap(window.contentView)
        let hit = try XCTUnwrap(content.hitTest(content.convert(point, from: nil)))
        let menu = try XCTUnwrap(hit.menu(for: click), "No menu under the thumbnail")
        let copy = try XCTUnwrap(menu.items.firstIndex { $0.title == "Copy Image" }, "\(menu.items.map(\.title))")
        menu.performActionForItem(at: copy)
        RunLoop.main.run(until: Date().addingTimeInterval(0.3))
        XCTAssertEqual(copied(board), CGSize(width: 1200, height: 900), "The picture itself")

        // A click still opens it full window.
        XCTAssertNil(store.lightbox)
        for type in [NSEvent.EventType.leftMouseDown, .leftMouseUp] {
            window.sendEvent(try XCTUnwrap(NSEvent.mouseEvent(with: type, location: point, modifierFlags: [], timestamp: 0,
                                                              windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 1)))
        }
        RunLoop.main.run(until: Date().addingTimeInterval(0.3))
        XCTAssertEqual(store.lightbox?.pictures.first?.title, "sent.png", "A click opens it full window")
    }

    // And a picture an agent looked at, read through the daemon.
    func testRightClickingAPictureAnAgentLookedAtCopiesIt() throws {
        let board = privateClipboard()
        let store = AgentsStore(client: Self.serving(Self.swatch(.systemGreen, CGSize(width: 1600, height: 1000))))
        let thumbnail = ImageThumbnail(sessionId: "a", path: "/tmp/agent-shot.png", label: "Read agent-shot.png", pictureOnly: true)
            .padding(20).frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        let window = Self.window(thumbnail, size: CGSize(width: 400, height: 200), dark: false, store: store)
        defer { window.orderOut(nil) }
        RunLoop.main.run(until: Date().addingTimeInterval(1))
        let point = NSPoint(x: 60, y: 200 - 20 - 60)
        let click = try XCTUnwrap(NSEvent.mouseEvent(with: .rightMouseDown, location: point, modifierFlags: [], timestamp: 0,
                                                     windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 1))
        let content = try XCTUnwrap(window.contentView)
        let menu = try XCTUnwrap(content.hitTest(content.convert(point, from: nil))?.menu(for: click), "No menu under the picture")
        menu.performActionForItem(at: try XCTUnwrap(menu.items.firstIndex { $0.title == "Copy Image" }))
        RunLoop.main.run(until: Date().addingTimeInterval(0.5))
        XCTAssertEqual(copied(board), CGSize(width: 1600, height: 1000))
    }

    // And an image file in the right panel's Files.
    func testRightClickingAnImageFileInThePanelCopiesIt() throws {
        let board = privateClipboard()
        let store = AgentsStore(client: Self.serving(Self.swatch(.systemRed, CGSize(width: 900, height: 600))))
        let session = try JSONDecoder().decode(Session.self, from: Data(#"{"id":"a","kind":"claude","name":"x","cwd":"/tmp","state":"idle"}"#.utf8))
        let window = Self.window(FileLoader(session: session, path: "shots/home.png"), size: CGSize(width: 400, height: 400), dark: false, store: store)
        defer { window.orderOut(nil) }
        RunLoop.main.run(until: Date().addingTimeInterval(1))
        let point = NSPoint(x: 200, y: 200)
        let click = try XCTUnwrap(NSEvent.mouseEvent(with: .rightMouseDown, location: point, modifierFlags: [], timestamp: 0,
                                                     windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 1))
        let content = try XCTUnwrap(window.contentView)
        let menu = try XCTUnwrap(content.hitTest(content.convert(point, from: nil))?.menu(for: click), "No menu over the picture")
        menu.performActionForItem(at: try XCTUnwrap(menu.items.firstIndex { $0.title == "Copy Image" }))
        XCTAssertEqual(copied(board), CGSize(width: 900, height: 600))
    }

    // MARK: - Screenshots

    func testRenderTheViewer() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"].map(URL.init(fileURLWithPath:)) else {
            throw XCTSkip("for screenshots")
        }
        let pictures = [Self.screen("Sign in", .systemIndigo), Self.screen("Settings", .systemTeal), Self.screen("Billing", .systemPink)]
        for dark in [false, true] {
            let look = dark ? "dark" : "light"
            let box = Lightbox(pictures: pictures.enumerated().map { i, image in
                Lightbox.Picture(title: "screen-\(i + 1).png") { image }
            }, start: 1)
            try Self.capture(LightboxView(box: box), size: CGSize(width: 1100, height: 720), dark: dark,
                             to: out.appendingPathComponent("viewer-\(look).png"))
            let alone = Lightbox(image: pictures[0], title: "screen-1.png")
            try Self.capture(LightboxView(box: alone), size: CGSize(width: 1100, height: 720), dark: dark,
                             to: out.appendingPathComponent("viewer-one-\(look).png"))

            // A large picture in an editor pane fits it.
            var zoom: CGFloat = 1
            let editor = ZoomableImage(image: Self.screen("A 2880 × 1800 screenshot", .systemIndigo, size: CGSize(width: 2880, height: 1800)),
                                       zoom: Binding(get: { zoom }, set: { zoom = $0 }))
                .padding(24)
                .background(Palette.paper)
            try Self.capture(editor, size: CGSize(width: 820, height: 560), dark: dark, to: out.appendingPathComponent("editor-image-\(look).png"))

            // And in the right panel's Files.
            let session = try JSONDecoder().decode(Session.self, from: Data(#"{"id":"a","kind":"claude","name":"x","cwd":"/tmp","state":"idle"}"#.utf8))
            try Self.capture(FileLoader(session: session, path: "shots/home.png"), size: CGSize(width: 420, height: 560), dark: dark,
                             store: AgentsStore(client: Self.serving(Self.screen("Home", .systemPink, size: CGSize(width: 2880, height: 1800)))),
                             to: out.appendingPathComponent("panel-image-\(look).png"))
        }
    }

    /// A stand-in daemon that reads every file as this picture.
    private static func serving(_ image: NSImage) -> DaemonClient {
        let client = DaemonClient(socketPath: "/nonexistent/agentman-viewer.sock")
        let png = NSBitmapImageRep(data: image.tiffRepresentation!)!.representation(using: .png, properties: [:])!.base64EncodedString()
        client.answer = { request in
            guard request.type == .readFile || request.type == .readSeenFile else { return nil }
            let event: [String: Any] = ["type": "workspace", "workspace": ["kind": "file", "sessionId": "a", "path": request.path ?? "", "image": png]]
            return try? JSONDecoder().decode(DaemonEvent.self, from: JSONSerialization.data(withJSONObject: event))
        }
        return client
    }

    // MARK: - Helpers

    final class Asked: @unchecked Sendable {
        private let lock = NSLock()
        private var loads: [Int] = []
        func add(_ index: Int) { lock.lock(); loads.append(index); lock.unlock() }
        var all: [Int] { lock.lock(); defer { lock.unlock() }; return loads }
    }

    static func swatch(_ color: NSColor, _ size: CGSize) -> NSImage {
        NSImage(size: size, flipped: false) { rect in
            color.setFill()
            rect.fill()
            return true
        }
    }

    /// A stand-in for a screenshot an agent took: a title bar, a heading and
    /// some rows.
    static func screen(_ title: String, _ accent: NSColor, size: CGSize = CGSize(width: 1440, height: 900)) -> NSImage {
        NSImage(size: size, flipped: true) { rect in
            NSColor.white.setFill()
            rect.fill()
            NSColor(white: 0.95, alpha: 1).setFill()
            NSRect(x: 0, y: 0, width: rect.width, height: rect.height * 0.06).fill()
            accent.setFill()
            NSBezierPath(roundedRect: NSRect(x: rect.width * 0.08, y: rect.height * 0.14, width: rect.width * 0.22, height: rect.height * 0.08),
                         xRadius: 12, yRadius: 12).fill()
            let attributes: [NSAttributedString.Key: Any] = [.font: NSFont.boldSystemFont(ofSize: rect.height * 0.06), .foregroundColor: NSColor.black]
            (title as NSString).draw(at: NSPoint(x: rect.width * 0.08, y: rect.height * 0.28), withAttributes: attributes)
            NSColor(white: 0.88, alpha: 1).setFill()
            for row in 0..<5 {
                NSBezierPath(roundedRect: NSRect(x: rect.width * 0.08, y: rect.height * (0.45 + Double(row) * 0.09),
                                                 width: rect.width * (0.84 - Double(row % 3) * 0.12), height: rect.height * 0.045),
                             xRadius: 8, yRadius: 8).fill()
            }
            return true
        }
    }

    private static func window<V: View>(_ view: V, size: CGSize, dark: Bool, store: AgentsStore? = nil) -> NSWindow {
        let store = store ?? AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-viewer.sock"))
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentView = NSHostingView(rootView: view.environment(store).frame(width: size.width, height: size.height).background(Palette.paper))
        window.orderFrontRegardless()
        return window
    }

    private static func capture<V: View>(_ view: V, size: CGSize, dark: Bool, store: AgentsStore? = nil, to url: URL) throws {
        let window = window(view, size: size, dark: dark, store: store)
        defer { window.orderOut(nil) }
        RunLoop.main.run(until: Date().addingTimeInterval(1))
        let content = try XCTUnwrap(window.contentView)
        let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
        content.cacheDisplay(in: content.bounds, to: bitmap)
        try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: url)
    }
}
