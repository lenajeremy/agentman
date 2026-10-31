import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// Blocks drawn at their own height wherever they sit in a reply: a code
/// block above a quote once shared its height with the quote, so the code was
/// cut off and the quote's bar ran on far below its line.
@MainActor
final class MarkdownLayoutTests: XCTestCase {
    static let code = """
    ```go
    for attempt := 0; attempt < maxAttempts; attempt++ {
    \terr := upload(ctx, part)
    \tif err == nil || !retryable(err) {
    \t\treturn err
    \t}
    \tselect {
    \tcase <-time.After(backoff(attempt)):
    \tcase <-ctx.Done():
    \t\treturn ctx.Err()
    \t}
    }
    ```
    """
    static let quote = "> The timeout test uses a fake clock, so it runs in a few milliseconds instead of 30 seconds."

    /// The reply drawn as a thread draws it, and the heights of the shapes
    /// down its left edge: the code block's fill, then the quote's bar.
    private func edgeRuns(_ reply: String, name: String) throws -> [CGFloat] {
        let width: CGFloat = 720, height: CGFloat = 900, gutter: CGFloat = 28
        let page = ScrollView {
            LazyVStack(alignment: .leading, spacing: 24) {
                MessageRow(message: Message(id: "r", sessionId: "s", role: .assistant, ts: 1, text: reply))
            }
            .padding(.horizontal, gutter)
            .padding(.top, 24)
        }
        .environment(AgentsStore())
        .frame(width: width, height: height)
        .background(Palette.paper)
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: width, height: height),
                              styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.appearance = NSAppearance(named: .aqua)
        window.contentView = NSHostingView(rootView: page)
        window.orderFrontRegardless()
        defer { window.close() }
        RunLoop.main.run(until: Date().addingTimeInterval(0.6))
        let view = try XCTUnwrap(window.contentView)
        let bitmap = try XCTUnwrap(view.bitmapImageRepForCachingDisplay(in: view.bounds))
        view.cacheDisplay(in: view.bounds, to: bitmap)
        if let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] {
            try bitmap.representation(using: .png, properties: [:])?
                .write(to: URL(fileURLWithPath: out).appendingPathComponent("markdown-\(name).png"))
        }
        // Down a column just inside the left edge, where only the code
        // block's fill and the quote's bar are drawn.
        let scale = CGFloat(bitmap.pixelsWide) / width
        let x = Int((gutter + 1.5) * scale)
        let paper = try XCTUnwrap(bitmap.colorAt(x: x, y: 0)?.usingColorSpace(.sRGB))
        func isPaper(_ y: Int) -> Bool {
            guard let colour = bitmap.colorAt(x: x, y: y)?.usingColorSpace(.sRGB) else { return true }
            return abs(colour.redComponent - paper.redComponent) < 0.01
                && abs(colour.greenComponent - paper.greenComponent) < 0.01
                && abs(colour.blueComponent - paper.blueComponent) < 0.01
        }
        var runs: [CGFloat] = []
        var start: Int?
        for y in 0...bitmap.pixelsHigh {
            let inside = y < bitmap.pixelsHigh && !isPaper(y)
            if inside, start == nil { start = y }
            if !inside, let top = start {
                let run = CGFloat(y - top) / scale
                if run >= 8 { runs.append(run) }
                start = nil
            }
        }
        return runs
    }

    func testACodeBlockAboveAQuoteKeepsItsHeight() throws {
        let code = try XCTUnwrap(edgeRuns(Self.code, name: "code-alone").first)
        let bar = try XCTUnwrap(edgeRuns(Self.quote + "\n\nAll 214 tests pass.", name: "quote-alone").first)
        let both = try edgeRuns(Self.code + "\n\n" + Self.quote + "\n\nAll 214 tests pass.", name: "code-then-quote")
        XCTAssertEqual(both.count, 2, "\(both)")
        XCTAssertEqual(both.first ?? 0, code, accuracy: 1, "the code block is cut off")
        XCTAssertEqual(both.last ?? 0, bar, accuracy: 1, "the quote's bar runs on")
    }
}
