import XCTest
@testable import Agentman

@MainActor
final class AwayTests: XCTestCase {
    private func session(_ id: String, _ state: SessionState, _ at: Date) -> Session {
        Session(id: id, kind: "claude", name: id, cwd: "/work/" + id, state: state,
                lastActivityAt: Int64(at.timeIntervalSince1970 * 1000))
    }

    func testTheSummaryHasWhatHappenedAndWhatWaits() {
        let left = Date().addingTimeInterval(-3600)
        let back = Date()
        let log = AwayLog()
        log.left(at: left)
        log.record(.finished("Fixed the login bug.\n\nAll tests pass."), sessionId: "a", at: left.addingTimeInterval(600))
        log.record(.finished("Added a test."), sessionId: "a", at: left.addingTimeInterval(1200))
        log.record(.needsYou("Allow npm install?"), sessionId: "b", at: left.addingTimeInterval(1800))
        let sessions = [
            session("a", .idle, left.addingTimeInterval(1200)),
            session("b", .waitingInput, left.addingTimeInterval(1800)),
            session("c", .idle, left.addingTimeInterval(-7200)),
        ]
        let approval = ManagerEntry(id: "e1", role: "activity", text: "Approved for a: run `npm test`",
                                    ts: Int64(left.addingTimeInterval(900).timeIntervalSince1970 * 1000), sessionId: "a", tool: "approve")
        log.returned(at: back, minimum: 15 * 60, sessions: sessions, briefs: [:], entries: [approval])

        let digest = try! XCTUnwrap(log.digest)
        XCTAssertEqual(digest.agents.map(\.id), ["b", "a"], "the one waiting first; the quiet one left out")
        XCTAssertEqual(digest.agents[1].finished, 2)
        XCTAssertEqual(digest.agents[1].approved, 1)
        XCTAssertEqual(digest.agents[1].summary, "Added a test.")
        XCTAssertEqual(digest.waitingCount, 1)
        XCTAssertNil(log.leftAt)
    }

    func testAFreshBriefIsTheSummary() {
        let left = Date().addingTimeInterval(-3600)
        let log = AwayLog()
        log.left(at: left)
        let brief = Brief(sessionId: "a", name: "a", summary: "Shipped the settings page.",
                          updatedAt: Int64(Date().timeIntervalSince1970 * 1000))
        log.returned(minimum: 60, sessions: [session("a", .idle, Date())], briefs: ["a": brief], entries: [])
        XCTAssertEqual(log.digest?.agents.first?.summary, "Shipped the settings page.")
    }

    func testAShortAbsenceOrAQuietOneShowsNothing() {
        let log = AwayLog()
        log.left(at: Date().addingTimeInterval(-120))
        log.record(.finished("Done."), sessionId: "a")
        log.returned(minimum: 15 * 60, sessions: [session("a", .idle, Date())], briefs: [:], entries: [])
        XCTAssertNil(log.digest, "two minutes is not away")

        log.left(at: Date().addingTimeInterval(-7200))
        log.returned(minimum: 15 * 60, sessions: [session("z", .idle, Date().addingTimeInterval(-9000))], briefs: [:], entries: [])
        XCTAssertNil(log.digest, "nothing happened")
    }
}
