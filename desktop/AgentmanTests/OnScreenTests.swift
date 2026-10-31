import XCTest
@testable import Agentman

/// What the Mac tells Agentman is on screen, so an agent the user is looking
/// at gets no card and no push.
@MainActor
final class OnScreenTests: XCTestCase {
    final class Told: @unchecked Sendable {
        private let lock = NSLock()
        private var requests: [DaemonRequest] = []
        func add(_ request: DaemonRequest) { lock.lock(); requests.append(request); lock.unlock() }
        var viewing: [DaemonRequest] { lock.lock(); defer { lock.unlock() }; return requests.filter { $0.type == .managerViewing } }
    }

    // The selected agent and those in editors' panels are on screen; a change
    // is told at once, the same app each time, and nothing is told twice.
    func testTheAgentsOnScreenAreToldToAgentman() {
        let told = Told()
        let client = DaemonClient(socketPath: "/nonexistent/agentman-onscreen.sock")
        client.answer = { request in told.add(request); return nil }
        let store = AgentsStore(client: client)

        store.selection = .agent("claude:a")
        store.showInPanel("codex:b")
        store.selection = .manager
        store.selection = .manager
        store.hideFromPanel("codex:b")

        let sent = told.viewing
        XCTAssertEqual(sent.map { $0.viewing ?? [] }, [["claude:a"], ["claude:a", "codex:b"], ["codex:b"], []])
        XCTAssertEqual(Set(sent.compactMap(\.clientId)).count, 1, "one app, one id")
        XCTAssertTrue(sent.allSatisfy { $0.clientId?.hasPrefix("mac-") == true })
    }
}
