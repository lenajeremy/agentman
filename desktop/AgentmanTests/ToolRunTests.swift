import XCTest
@testable import Agentman

final class ToolRunTests: XCTestCase {
    private func message(_ id: String, _ role: Role, tool: String? = nil, summary: String? = nil, status: String? = nil) -> Message {
        Message(id: id, sessionId: "s", role: role, ts: 1, text: role == .tool ? nil : "text",
                tool: tool.map { ToolCall(name: $0, summary: summary, status: status) })
    }

    // Tool calls between messages fold into one run each.
    func testToolCallsFoldIntoRuns() {
        let items = ThreadItem.items(from: [
            message("1", .user), message("2", .tool, tool: "Bash"), message("3", .tool, tool: "Read"),
            message("4", .assistant), message("5", .tool, tool: "Edit"),
        ])
        let shapes = items.map { item -> String in
            switch item {
            case .message(let m): "message \(m.id)"
            case .tools(let calls, _): "tools \(calls.map(\.id).joined())"
            case .image(let m, _): "image \(m.id)"
            }
        }
        XCTAssertEqual(shapes, ["message 1", "tools 23", "message 4", "tools 5"])
        XCTAssertEqual(items[1].id, "3", "a run is found by its newest call")
    }

    func testToolKindsReadAsVerbs() {
        XCTAssertEqual(ToolKind("Bash").phrase(count: 3), "Ran 3 commands")
        XCTAssertEqual(ToolKind("exec_command").verb, "Ran")
        XCTAssertEqual(ToolKind("Read").phrase(count: 1), "Read a file")
        XCTAssertEqual(ToolKind("apply_patch").verb, "Edited")
        XCTAssertEqual(ToolKind("mcp__agentman__search_notes").verb, "Used notes")
    }

    // Long scratchpad paths and home directories shrink to something
    // readable, on one line.
    func testTargetsAreTidied() {
        let long = "cat /private/tmp/claude-501/-Users-mac-Documents-portfolio/abf6f708-68eb-4ee6-9bdd-242be3e872e9/scratchpad/walltest/a.txt"
        XCTAssertEqual(ToolKind.tidy(long), "cat scratchpad/walltest/a.txt")
        XCTAssertEqual(ToolKind.tidy("tail /private/tmp/claude-501/-Users-mac-x/abf6f708-68eb-4ee6-9bdd-242be3e872e9/tasks/b.output"),
                       "tail tasks/b.output")
        XCTAssertEqual(ToolKind.tidy(NSHomeDirectory() + "/Documents/x.swift"), "~/Documents/x.swift")
        XCTAssertEqual(ToolKind.tidy("git status\n  && git diff"), "git status   && git diff")
    }

    // An image the agent read shows as a thumbnail, not inside a folded run.
    func testImageReadsStandApart() {
        let items = ThreadItem.items(from: [
            message("1", .tool, tool: "Bash", summary: "ls"),
            message("2", .tool, tool: "Read", summary: "/tmp/shots/home.PNG"),
            message("3", .tool, tool: "Read", summary: "/tmp/notes.md"),
        ])
        guard items.count == 3, case .image(_, let path) = items[1] else {
            return XCTFail("items = \(items)")
        }
        XCTAssertEqual(path, "/tmp/shots/home.PNG")
        XCTAssertNil(ImageThumbnail.imagePath(in: "/tmp/notes.md"))
        XCTAssertEqual(ImageThumbnail.imagePath(in: "Looked at /a/b c.jpeg"), nil, "a path with a space is not guessed at")
        XCTAssertEqual(ImageThumbnail.imagePath(in: "open /a/b/c.jpeg now"), "/a/b/c.jpeg")
    }
}
