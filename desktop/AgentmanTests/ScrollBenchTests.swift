import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// How long a step of scrolling a long conversation takes: the main window,
/// offscreen, with the messages in AGENTMAN_BENCH (a JSON messages event),
/// scrolled from the end to the top a trackpad's step at a time. Each step
/// is laid out and drawn before the next. Nothing reaches a daemon.
/// Runs with TEST_RUNNER_AGENTMAN_BENCH=<file> set; with
/// TEST_RUNNER_AGENTMAN_BENCH_SECONDS, it keeps scrolling up and down that
/// long, for a profiler to watch.
@MainActor
final class ScrollBenchTests: XCTestCase {
    func testScrollingALongConversation() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let file = environment["AGENTMAN_BENCH"] else { throw XCTSkip("for measuring") }
        let seconds = Double(environment["AGENTMAN_BENCH_SECONDS"] ?? "") ?? 0

        let folder = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: folder) }
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        let sessions = """
        {"type":"sessions","sessions":[{"id":"bench","kind":"codex","name":"Redesign the Mac app","cwd":"\(folder.path)","state":"idle","inject":"tmux","lastActivityAt":\(now)}]}
        """
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-bench.sock"))
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(sessions.utf8)))
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(contentsOf: URL(fileURLWithPath: file))))
        store.selection = .agent("bench")

        let size = CGSize(width: 1560, height: 980)
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.titled, .closable, .resizable],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.contentView = NSHostingView(rootView: ContentView().environment(store).frame(width: size.width, height: size.height))
        window.setContentSize(size)
        window.orderFrontRegardless()
        defer { window.close() }
        RunLoop.main.run(until: Date().addingTimeInterval(2))

        let scroll = try XCTUnwrap(conversation(in: try XCTUnwrap(window.contentView)), "no conversation scroll view")
        let clip = scroll.contentView
        // The clip view moved as scrolling moves it, then laid out and drawn.
        func step(to y: CGFloat) -> Double {
            let start = CFAbsoluteTimeGetCurrent()
            clip.scroll(to: NSPoint(x: clip.bounds.origin.x, y: y))
            scroll.reflectScrolledClipView(clip)
            window.contentView?.layoutSubtreeIfNeeded()
            window.displayIfNeeded()
            RunLoop.main.run(mode: .default, before: Date())
            return (CFAbsoluteTimeGetCurrent() - start) * 1000
        }
        func bottom() -> CGFloat { max(0, (scroll.documentView?.frame.height ?? 0) - clip.bounds.height) }
        // A trackpad flick moves about this far a frame.
        let delta: CGFloat = 48
        var times: [Double] = []
        var snapped = 0
        while clip.bounds.origin.y > 0.5, times.count < 4000 {
            let target = max(0, clip.bounds.origin.y - delta)
            times.append(step(to: target))
            // Pulled back down by the view rather than left where it was put.
            if clip.bounds.origin.y > target + 1 {
                snapped += 1
                if snapped <= 12 || snapped % 25 == 0 {
                    print("SCROLLTRACE step \(times.count) asked \(Int(target)) got \(Int(clip.bounds.origin.y)) height \(Int(scroll.documentView?.frame.height ?? 0))")
                }
            }
        }
        print("SCROLLBENCH reached the top: \(clip.bounds.origin.y <= 0.5) · steps pulled back: \(snapped)")
        let sorted = times.sorted()
        func percentile(_ p: Double) -> Double { sorted.isEmpty ? 0 : sorted[min(sorted.count - 1, Int(Double(sorted.count) * p))] }
        let slow = times.filter { $0 > 16.7 }.count
        let report = String(format: "steps %d · median %.1f ms · p90 %.1f ms · p99 %.1f ms · max %.1f ms · over 16.7 ms: %d (%.0f%%) · document %.0f pt",
                            times.count, percentile(0.5), percentile(0.9), percentile(0.99), sorted.last ?? 0,
                            slow, times.isEmpty ? 0 : Double(slow) * 100 / Double(times.count), scroll.documentView?.frame.height ?? 0)
        print("SCROLLBENCH " + report)

        // For a profiler: up and down, again and again.
        let until = Date().addingTimeInterval(seconds)
        var down = true
        while Date() < until {
            var moves = 0
            while Date() < until, moves < 4000, down ? clip.bounds.origin.y < bottom() - 0.5 : clip.bounds.origin.y > 0.5 {
                _ = step(to: down ? min(bottom(), clip.bounds.origin.y + delta) : max(0, clip.bounds.origin.y - delta))
                moves += 1
            }
            down.toggle()
        }
    }

    /// The scroll view with the tallest content: the conversation.
    private func conversation(in view: NSView) -> NSScrollView? {
        var best: NSScrollView?
        func visit(_ view: NSView) {
            if let scroll = view as? NSScrollView,
               (scroll.documentView?.frame.height ?? 0) > (best?.documentView?.frame.height ?? 0) {
                best = scroll
            }
            view.subviews.forEach(visit)
        }
        visit(view)
        return best
    }
}
