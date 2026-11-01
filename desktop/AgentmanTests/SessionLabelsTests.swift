import XCTest
@testable import Agentman

/// The same names the phone gives: see mobile/lib/session-view.ts.
final class SessionLabelsTests: XCTestCase {
    func testModelsAreNamedAsPeopleSayThem() {
        XCTAssertEqual(SessionLabels.model("claude-opus-5-5"), "Opus 5.5")
        XCTAssertEqual(SessionLabels.model("claude-sonnet-4-5-20250929"), "Sonnet 4.5")
        XCTAssertEqual(SessionLabels.model("claude-3.5-sonnet"), "Sonnet 3.5")
        XCTAssertEqual(SessionLabels.model("gpt-6.1-sol"), "GPT-6.1 Sol")
        XCTAssertEqual(SessionLabels.model("auto"), "Auto")
        XCTAssertEqual(SessionLabels.model("Gemini 3.8 Pro (High)"), "Gemini 3.8 Pro (High)")
        XCTAssertEqual(SessionLabels.model("muse-spark-1.3-contributor"), "Muse Spark 1.3")
        XCTAssertEqual(SessionLabels.model("muse-wonder-1.0"), "Muse Wonder 1.0")
    }

    func testModesAreNamedAsPeopleSayThem() {
        XCTAssertEqual(SessionLabels.mode("accept-edits"), "Accept edits")
        XCTAssertEqual(SessionLabels.mode("kiro_planner"), "Planner")
        XCTAssertEqual(SessionLabels.mode("plan"), "Plan")
        XCTAssertEqual(SessionLabels.mode("bypassPermissions"), "Bypass permissions")
    }
}
