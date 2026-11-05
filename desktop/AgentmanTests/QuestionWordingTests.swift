import XCTest
@testable import Agentman

/// How a question reads in a thread: the command said once, and the message
/// box saying what to do while the agent waits.
final class QuestionWordingTests: XCTestCase {
    private func question(prompt: String, detail: String?, options: [String], custom: Bool = false) -> Question {
        Question(prompt: prompt, options: options.enumerated().map { QuestionOption(key: String($0.offset + 1), label: $0.element) })
            .with { $0.detail = detail; $0.custom = custom }
    }

    func testAnApprovalSaysItsCommandOnce() {
        let push = question(prompt: "Run git push origin main?", detail: "git push origin main", options: ["Yes", "No"])
        XCTAssertTrue(push.promptRepeatsDetail)
        let explained = question(prompt: "This pushes the migration to main, where the deploy picks it up. Run it?",
                                 detail: "git push origin main", options: ["Yes", "No"])
        XCTAssertFalse(explained.promptRepeatsDetail, "a sentence that says more stays")
        XCTAssertFalse(question(prompt: "Which badge style?", detail: nil, options: ["A", "B"]).promptRepeatsDetail)
    }

    func testWhatTheBoxSaysWhileAQuestionWaits() {
        XCTAssertTrue(question(prompt: "Run it?", detail: "npm test", options: ["Yes, run it", "No"]).isApproval)
        XCTAssertFalse(question(prompt: "Which style?", detail: nil, options: ["A count", "A dot"]).isApproval)
        XCTAssertTrue(question(prompt: "Which style?", detail: nil, options: ["A count", "A dot"], custom: true).takesTypedAnswer)
        XCTAssertFalse(question(prompt: "Which style?", detail: nil, options: ["A count", "A dot"]).takesTypedAnswer)
    }
}

private extension Question {
    func with(_ change: (inout Question) -> Void) -> Question {
        var copy = self
        change(&copy)
        return copy
    }
}
