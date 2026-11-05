import XCTest
@testable import Agentman

/// What a thread says about an agent you can't talk to.
@MainActor
final class ThreadLabelTests: XCTestCase {
    private func session(_ state: SessionState, inject: String?, handoff: String? = nil) -> Session {
        var session = Session(id: "s", kind: "claude", name: "Speed up the grid", cwd: "/work/s", state: state)
        session.inject = inject
        session.handoff = handoff
        return session
    }

    func testTheTitleSaysEndedOrWatching() {
        XCTAssertEqual(AgentThreadView.stateLabel(session(.ended, inject: "tmux"))?.text, "Ended")
        XCTAssertEqual(AgentThreadView.stateLabel(session(.idle, inject: "none"))?.text, "Watching")
        XCTAssertNil(AgentThreadView.stateLabel(session(.idle, inject: "tmux")))
    }

    func testTakeOverIsOfferedWhereItCanBeDone() {
        XCTAssertTrue(AgentThreadView.offersTakeOver(session(.idle, inject: "none", handoff: "agentman")))
        XCTAssertFalse(AgentThreadView.offersTakeOver(session(.idle, inject: "none")))
        XCTAssertFalse(AgentThreadView.offersTakeOver(session(.ended, inject: "none", handoff: "agentman")))
        XCTAssertFalse(AgentThreadView.offersTakeOver(session(.idle, inject: "tmux", handoff: "agentman")))
    }
}
