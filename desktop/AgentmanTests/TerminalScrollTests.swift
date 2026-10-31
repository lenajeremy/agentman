import AppKit
import XCTest
@testable import Agentman

/// Scrolling in a terminal, with a real program on its pty: tmux takes the
/// mouse, so the wheel goes to it as a report it scrolls its history by; a
/// full-screen program without the mouse gets arrow keys; a plain shell
/// moves the terminal's own scrollback. `cat` stands in for the program:
/// the pty echoes whatever reaches it, so the screen shows what was sent.
@MainActor
final class TerminalScrollTests: XCTestCase {
    private var session: TerminalSession!
    private var window: NSWindow!

    override func setUp() async throws {
        session = TerminalSession(folder: FileManager.default.temporaryDirectory, number: 1, command: ["/bin/cat"])
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 640, height: 320), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.contentView = session.view
        window.orderFrontRegardless()
        settle(1.5)
    }

    override func tearDown() async throws {
        session.terminate()
        window.orderOut(nil)
        session = nil
        window = nil
    }

    private func settle(_ seconds: TimeInterval = 0.4) {
        RunLoop.main.run(until: Date().addingTimeInterval(seconds))
    }

    private var screen: String {
        let terminal = session.view.getTerminal()
        return (0..<terminal.rows).compactMap { terminal.getLine(row: $0)?.translateToString(trimRight: true) }.joined(separator: "\n")
    }

    /// A wheel of a mouse, a few lines at a time; positive is up.
    private func wheel(_ lines: Int32) throws -> NSEvent {
        let event = try XCTUnwrap(CGEvent(scrollWheelEvent2Source: nil, units: .line, wheelCount: 1, wheel1: lines, wheel2: 0, wheel3: 0))
        return try XCTUnwrap(NSEvent(cgEvent: event))
    }

    func testTheWheelReachesAProgramThatTakesTheMouse() throws {
        // What tmux sends with `mouse on`: report presses, in SGR form.
        session.view.feed(text: "\u{1b}[?1000h\u{1b}[?1006h")
        session.view.scroll(with: try wheel(3))
        settle()
        XCTAssertTrue(screen.contains("[<64;"), "A wheel-up report reached the program:\n\(screen)")
        session.view.scroll(with: try wheel(-3))
        settle()
        XCTAssertTrue(screen.contains("[<65;"), "A wheel-down report reached the program:\n\(screen)")
    }

    // The case it was for: an agent's terminal is tmux attached in the
    // panel. On a private tmux server of its own, with the mouse on as the
    // user's config has it, scrolling up puts tmux into copy mode, whose
    // position marker shows at the top.
    func testScrollingUpInTmuxShowsItsHistory() throws {
        guard let tmux = ["/opt/homebrew/bin/tmux", "/usr/local/bin/tmux"].first(where: FileManager.default.isExecutableFile) else {
            throw XCTSkip("tmux isn't installed")
        }
        let socket = FileManager.default.temporaryDirectory.appendingPathComponent("am-scroll-\(UUID().uuidString.prefix(8)).sock").path
        func run(_ arguments: [String]) throws {
            let process = Process()
            process.executableURL = URL(fileURLWithPath: tmux)
            process.arguments = ["-S", socket, "-f", "/dev/null"] + arguments
            var environment = ProcessInfo.processInfo.environment
            environment["TMUX"] = nil
            process.environment = environment
            try process.run()
            process.waitUntilExit()
        }
        try run(["new-session", "-d", "-s", "scroll", "-x", "80", "-y", "20", "seq 1 300; sleep 600"])
        defer {
            try? run(["kill-server"])
            try? FileManager.default.removeItem(atPath: socket)
        }
        try run(["set-option", "-g", "mouse", "on"])
        session.terminate()
        session = TerminalSession(folder: FileManager.default.temporaryDirectory, number: 1, command: [tmux, "-S", socket, "attach-session", "-t", "scroll"])
        window.contentView = session.view
        settle(2)
        XCTAssertTrue(screen.contains("300"), "Attached, at the end of its output:\n\(screen)")
        for _ in 0..<4 { session.view.scroll(with: try wheel(3)) }
        settle(1)
        XCTAssertNotNil(screen.range(of: #"\[\d+/\d+\]"#, options: .regularExpression), "tmux is in copy mode, scrolled back:\n\(screen)")
    }

    func testAFullScreenProgramWithoutTheMouseGetsArrowKeys() throws {
        session.view.feed(text: "\u{1b}[?1049h")
        session.view.scroll(with: try wheel(-3))
        settle()
        XCTAssertTrue(screen.contains("^[[B"), "Down arrows reached the program:\n\(screen)")
    }

    func testAPlainShellScrollsItsOwnHistory() throws {
        session.view.feed(text: (1...200).map { "line \($0)" }.joined(separator: "\r\n"))
        settle()
        let terminal = session.view.getTerminal()
        let bottom = terminal.buffer.yDisp
        session.view.scroll(with: try wheel(3))
        XCTAssertLessThan(terminal.buffer.yDisp, bottom, "The view moved back through its scrollback")
        XCTAssertFalse(screen.contains("^["), "Nothing was sent to the program")
    }
}
