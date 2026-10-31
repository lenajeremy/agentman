import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// The image viewer: the pictures a conversation's arrows go through, ←
/// and → moving between them, a picture fitted and zoomed, and, with
/// TEST_RUNNER_AGENTMAN_SHOTS=<folder>, the viewer.
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
        }
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
