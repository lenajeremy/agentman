import XCTest
@testable import Agentman

@MainActor
final class ManagerChatViewTests: XCTestCase {
    // Agentman's name and big box are the page only while there is nothing
    // to read; with a conversation, its box sits below the last message,
    // whatever state the app opened or came back in.
    func testTheEntryPageIsOnlyForAnEmptyChat() {
        let manager = ManagerStore(client: DaemonClient(socketPath: "/nonexistent"))
        XCTAssertTrue(ManagerChatView.showsEntry(manager))

        var event = DaemonEvent(type: "manager_entry")
        event.entry = ManagerEntry(id: "u1", role: "user", text: "Is everything fine?", ts: 1)
        manager.apply(event)
        XCTAssertFalse(ManagerChatView.showsEntry(manager))
    }
}
