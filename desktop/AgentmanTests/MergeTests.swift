import XCTest
@testable import Agentman

final class MergeTests: XCTestCase {
    private func tool(_ id: String, _ status: String, ts: Int64 = 1) -> Message {
        Message(id: id, sessionId: "s", role: .tool, ts: ts, text: nil,
                tool: ToolCall(name: "Bash", summary: "ls", status: status), isSidechain: nil)
    }

    // A command that was running when it arrived settles when it finishes.
    func testAFinishedCallReplacesItsRunningRow() {
        let (all, added) = AgentsStore.merged([tool("t1", "running")], with: [tool("t1", "ok")])
        XCTAssertEqual(all.count, 1)
        XCTAssertEqual(all.first?.tool?.status, "ok")
        XCTAssertTrue(added.isEmpty)
    }

    // Both copies in one batch make one row, the later one.
    func testOneBatchWithBothCopiesMakesOneRow() {
        let (all, added) = AgentsStore.merged([], with: [tool("t1", "running"), tool("t1", "ok")])
        XCTAssertEqual(all.map(\.tool?.status), ["ok"])
        XCTAssertEqual(added.map(\.tool?.status), ["ok"])
    }

    // A history page read before the result was written never sets a
    // finished call running again.
    func testAnOlderCopyNeverRestartsAFinishedCall() {
        let (all, _) = AgentsStore.merged([tool("t1", "ok")], with: [tool("t1", "running")])
        XCTAssertEqual(all.first?.tool?.status, "ok")
    }

    func testNewMessagesKeepTimeOrder() {
        let (all, added) = AgentsStore.merged([tool("t2", "ok", ts: 2)], with: [tool("t1", "ok", ts: 1), tool("t3", "running", ts: 3)])
        XCTAssertEqual(all.map(\.id), ["t1", "t2", "t3"])
        XCTAssertEqual(added.count, 2)
    }
}
