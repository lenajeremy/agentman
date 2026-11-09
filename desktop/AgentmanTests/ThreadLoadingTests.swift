import XCTest
@testable import Agentman

/// A conversation being opened gets its messages even if the first ask fails.
extension SiteShotsTests {
    func testAChatWhoseFirstLoadFailsLoadsAnyway() throws {
        final class Attempts: @unchecked Sendable {
            let lock = NSLock()
            var failuresLeft = 2
            var answered = 0
            func fails() -> Bool {
                lock.lock(); defer { lock.unlock() }
                guard failuresLeft > 0 else { answered += 1; return false }
                failuresLeft -= 1
                return true
            }
            var done: Bool { lock.lock(); defer { lock.unlock() }; return failuresLeft == 0 && answered > 0 }
        }
        let world = try world(fresh: true)
        // One that isn't open, so opening it asks for its messages.
        let id = try XCTUnwrap(world.store.sessions.first { world.store.selection != .agent($0.id) }?.id)
        let attempts = Attempts()
        let earlier = world.store.client.answer
        world.store.client.answer = { request in
            if request.type == .fetchMessages, request.sessionId == id, attempts.fails() {
                return try? JSONDecoder().decode(DaemonEvent.self, from: Data(#"{"type":"error","error":"daemon is handling too many requests"}"#.utf8))
            }
            return earlier?(request)
        }
        world.store.selection = .agent(id)
        let deadline = Date().addingTimeInterval(6)
        while !attempts.done, Date() < deadline {
            RunLoop.main.run(until: Date().addingTimeInterval(0.05))
        }
        XCTAssertTrue(attempts.done, "it gave up after \(2 - attempts.failuresLeft) failures")
    }
}
