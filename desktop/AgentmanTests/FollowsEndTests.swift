import SwiftUI
import XCTest
@testable import Agentman

@MainActor
final class FollowsEndTests: XCTestCase {
    /// A conversation of numbered messages of different lengths, whose
    /// earlier pages wait until the test lets them in.
    @MainActor
    @Observable
    final class Feed {
        var rows: [Int] = []
        var more = false
        var loads = 0
        /// Where each message's top is, from the top of the scroll view.
        var tops: [Int: CGFloat] = [:]
        @ObservationIgnored var gate: CheckedContinuation<Void, Never>?

        /// Lets the page waiting at the gate in, once.
        func release() {
            gate?.resume()
            gate = nil
        }

        func loadEarlier() async {
            loads += 1
            await withCheckedContinuation { gate = $0 }
            let first = rows[0]
            rows.insert(contentsOf: (first - 40)..<first, at: 0)
            more = first - 40 > 80
        }
    }

    struct FeedView: View {
        let feed: Feed
        @State private var atEnd = true

        var body: some View {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 24) {
                    if feed.more { LoadingEarlier() }
                    ForEach(feed.rows, id: \.self) { row in
                        Text("Message \(row)" + String(repeating: " and a little more", count: row % 5 * 8))
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .onGeometryChange(for: CGFloat.self) { $0.frame(in: .named("feed")).minY } action: { feed.tops[row] = $0 }
                            .id(row)
                            .firstMessage(row, if: row == feed.rows.first)
                    }
                }
                .padding(24)
            }
            .coordinateSpace(.named("feed"))
            .modifier(FollowsEnd(atEnd: $atEnd, earlier: feed.more ? { await feed.loadEarlier() } : nil))
        }
    }

    private func settle(_ seconds: TimeInterval = 3, until done: () -> Bool = { false }) {
        let deadline = Date().addingTimeInterval(seconds)
        while !done(), Date() < deadline {
            RunLoop.main.run(until: Date().addingTimeInterval(0.02))
        }
    }

    private func scrollView(in view: NSView) -> NSScrollView? {
        if let scroll = view as? NSScrollView { return scroll }
        return view.subviews.lazy.compactMap { self.scrollView(in: $0) }.first
    }

    private func scrollToTop(_ scroll: NSScrollView) {
        let clip = scroll.contentView
        let top = scroll.documentView?.isFlipped == false
            ? (scroll.documentView?.frame.height ?? 0) - clip.bounds.height
            : -scroll.contentInsets.top
        clip.scroll(to: NSPoint(x: clip.bounds.minX, y: top))
        scroll.reflectScrolledClipView(clip)
    }

    /// The conversation in a window that is on screen, as SwiftUI lays out
    /// and scrolls only then, but unseen and out of the way of the mouse.
    private func show(_ feed: Feed) -> NSWindow {
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 600, height: 500), styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.contentView = NSHostingView(rootView: FeedView(feed: feed))
        window.orderFrontRegardless()
        return window
    }

    // Reaching the top loads the page before it, with no button to press,
    // and the message the reader was on stays exactly where it was.
    func testReachingTheTopLoadsEarlierMessagesWithoutMovingTheView() throws {
        continueAfterFailure = false
        let feed = Feed()
        let window = show(feed)
        defer { window.close() }

        // The first page arrives after the view is up, as the daemon's does.
        // It opens at the end, and nothing loads on the way there.
        settle(0.3)
        feed.rows = Array(200..<260)
        feed.more = true
        settle { feed.tops[259] != nil }
        settle(0.5)
        XCTAssertEqual(feed.loads, 0)
        let last = try XCTUnwrap(feed.tops[259])
        XCTAssertTrue((0..<500).contains(last), "the last message should be in view, at \(last)")

        let scroll = try XCTUnwrap(scrollView(in: window.contentView!))
        scrollToTop(scroll)
        settle { feed.loads == 1 && feed.tops[200] != nil }
        XCTAssertEqual(feed.loads, 1)
        settle(0.3)
        let before = try XCTUnwrap(feed.tops[200])

        feed.release()
        settle { feed.rows.first == 160 }
        settle(0.8)
        let after = try XCTUnwrap(feed.tops[200])
        XCTAssertEqual(after, before, accuracy: 1, "message 200 moved from \(before) to \(after)")
        XCTAssertNotNil(feed.tops[199], "the page before should be laid out just above it")
        XCTAssertEqual(feed.loads, 1, "one arrival at the top loads one page")

        // Back at the top, the next page comes, and the last one ends it.
        scrollToTop(scroll)
        settle { feed.loads == 2 }
        XCTAssertEqual(feed.loads, 2)
        feed.release()
        settle { feed.rows.first == 120 }
        settle(0.8)
        scrollToTop(scroll)
        settle { feed.loads == 3 }
        XCTAssertEqual(feed.loads, 3)
        feed.release()
        settle { feed.rows.first == 80 }
        XCTAssertFalse(feed.more)
        scrollToTop(scroll)
        settle(0.5)
        XCTAssertEqual(feed.loads, 3, "nothing more loads once the start is reached")
    }

    // Too few messages to scroll has no top to reach: earlier ones load
    // until there are enough.
    func testAShortConversationLoadsUntilItScrolls() {
        continueAfterFailure = false
        let feed = Feed()
        let window = show(feed)
        defer { window.close() }

        settle(0.3)
        feed.rows = [200, 205]
        feed.more = true
        settle { feed.loads == 1 }
        XCTAssertEqual(feed.loads, 1)
        feed.release()
        settle { feed.rows.first == 160 }
        settle(0.8)
        XCTAssertEqual(feed.loads, 1, "a page that fills the view is enough")
    }
}
