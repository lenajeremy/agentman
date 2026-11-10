import AppKit
import SwiftUI
import XCTest
@testable import Agentman

final class ToolRunTests: XCTestCase {
    /// Steps as pills, and opened: a run's steps a row each, an edit's diff,
    /// a command's output, each hung under its pill; one left closed. Light
    /// and dark. Runs with TEST_RUNNER_AGENTMAN_SHOTS=<folder>.
    @MainActor
    func testRenderStepsOpened() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        func call(_ id: String, _ name: String, _ summary: String, title: String, text: String? = nil, status: String = "ok") -> Message {
            Message(id: id, sessionId: "s", role: .tool, ts: 1, text: text, tool: ToolCall(name: name, summary: summary, status: status, title: title))
        }
        let reads = [call("1", "Read", "web/checkout/CheckoutSheet.tsx", title: "Read the checkout sheet"),
                     call("2", "Read", "web/checkout/useCart.ts", title: "Read the cart hook"),
                     call("3", "Grep", "canMakePayments", title: "Searched for canMakePayments")]
        let edit = call("4", "Edit", "web/checkout/CheckoutSheet.tsx", title: "Added the Apple Pay button",
                        text: "@@ -41,4 +41,7 @@\n   <CartSummary cart={cart} />\n+  {canPay && (\n+    <ApplePayButton total={cart.total} />\n+  )}\n   <CardForm onSubmit={pay} />")
        let closed = call("5", "Bash", "npm test", title: "Ran the tests · 52 passed")
        let ipad = call("6", "Bash", "npx playwright test checkout --project=\"iPad Safari\"", title: "Ran the checkout flow in Safari on iPad",
                        text: "Running 3 tests using 1 worker\n  ✓ shows the Apple Pay button (1.2s)\n  ✓ opens the payment sheet (2.4s)\n3 passed (5.1s)")
        let running = call("7", "Bash", "go test ./...", title: "Fixed the two test mistakes and ran the full suite", status: "running")
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-steps.sock"))
        for (look, appearance) in [("light", NSAppearance.Name.aqua), ("dark", .darkAqua)] {
            let size = CGSize(width: 640, height: 700)
            let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless], backing: .buffered, defer: false)
            window.isReleasedWhenClosed = false
            window.alphaValue = 0
            window.appearance = NSAppearance(named: appearance)
            let host = NSHostingView(rootView: VStack(alignment: .leading, spacing: 0) {
                ToolRun(calls: reads, open: true)
                ToolRun(calls: [edit], open: true)
                ToolRun(calls: [closed])
                ToolRun(calls: [ipad], open: true)
                ToolRun(calls: [running])
            }
            .padding(24).frame(width: size.width, height: size.height, alignment: .top)
            .background(Palette.paper)
            .environment(store))
            window.contentView = host
            window.orderFrontRegardless()
            defer { window.orderOut(nil) }
            RunLoop.main.run(until: Date().addingTimeInterval(0.6))
            host.display()
            let bitmap = try XCTUnwrap(host.bitmapImageRepForCachingDisplay(in: host.bounds))
            host.cacheDisplay(in: host.bounds, to: bitmap)
            try XCTUnwrap(bitmap.representation(using: .png, properties: [:]))
                .write(to: URL(fileURLWithPath: out).appendingPathComponent("steps-opened-\(look).png"))
        }
    }

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
            case .images(let calls, _, _): "images \(calls.map(\.id).joined())"
            case .worked(_, let seconds, _): "worked \(seconds)"
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

    // Images read one after another are one run with the pictures side by
    // side, as Claude's own app shows them; one on its own stays as it was.
    func testImagesReadTogetherAreOneRun() {
        let items = ThreadItem.items(from: [
            message("1", .assistant),
            message("2", .tool, tool: "Read", summary: "/tmp/a.png"),
            message("3", .tool, tool: "Read", summary: "/tmp/b.jpg"),
            message("4", .tool, tool: "Bash", summary: "ls"),
            message("5", .tool, tool: "Read", summary: "/tmp/c.png"),
        ])
        guard items.count == 4, case .images(let calls, let paths, let said) = items[1] else {
            return XCTFail("items = \(items)")
        }
        XCTAssertEqual(calls.map(\.id), ["2", "3"])
        XCTAssertEqual(paths, ["/tmp/a.png", "/tmp/b.jpg"])
        XCTAssertEqual(said, "text")
        XCTAssertEqual(items[1].id, "3")
        guard case .tools(let run, _) = items[2], run.map(\.id) == ["4"] else { return XCTFail("items = \(items)") }
        guard case .image(let single, _) = items[3], single.id == "5" else { return XCTFail("items = \(items)") }
    }
}

final class DiffRowTests: XCTestCase {
    // An agent's edit of several files: each named over its hunks, and no
    // header read as a change.
    func testADiffOfSeveralFilesNamesEach() {
        let diff = [
            "--- /dev/null", "+++ b/repo/a.md", "@@ -0,0 +1,2 @@", "+# Title", "+body",
            "--- a/repo/b.go", "+++ b/repo/b.go", "@@ -1,2 +1,2 @@", " a", "-b", "+c",
            "--- a/repo/old.go", "+++ /dev/null", "@@ -1,1 +0,0 @@", "-package old",
        ].joined(separator: "\n")
        let rows = DiffRow.parse(diff)
        XCTAssertEqual(rows.map { "\($0.kind) \($0.text)" }, [
            "meta a.md", "hunk @@ -0,0 +1,2 @@", "add # Title", "add body",
            "meta b.go", "hunk @@ -1,2 +1,2 @@", "context a", "remove b", "add c",
            "meta old.go", "hunk @@ -1,1 +0,0 @@", "remove package old",
        ])
        XCTAssertEqual(rows.map(\.id), Array(0..<rows.count))
    }

    func testOneFileIsNotNamedAndAHeaderShapedLineInAHunkIsAChange() {
        let rows = DiffRow.parse("--- a/notes.md\n+++ b/notes.md\n@@ -1,2 +1,1 @@\n--- a rule\n keep")
        XCTAssertEqual(rows.map { "\($0.kind) \($0.text)" }, ["hunk @@ -1,2 +1,1 @@", "remove -- a rule", "context keep"])
        XCTAssertEqual(rows.map(\.id), [0, 1, 2])
    }
}
