import XCTest
@testable import Agentman

final class TerminalSessionTests: XCTestCase {
    // An agent's terminal runs its attach command through the user's shell,
    // so each word must reach tmux exactly as the daemon gave it.
    func testCommandWordsSurviveTheShell() throws {
        let words = ["tmux", "-u", "-L", "agentman", "attach-session", "-t", "=agentman-claude-1-ab'c $HOME;x"]
        let script = "for word in " + words.map(TerminalSession.quoted).joined(separator: " ") + "; do printf '%s\\n' \"$word\"; done"
        let shell = Process()
        shell.executableURL = URL(fileURLWithPath: "/bin/sh")
        shell.arguments = ["-c", script]
        let pipe = Pipe()
        shell.standardOutput = pipe
        try shell.run()
        shell.waitUntilExit()
        let out = String(data: pipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
        XCTAssertEqual(out.split(separator: "\n").map(String.init), words)
    }
}
