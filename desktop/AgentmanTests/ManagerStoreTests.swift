import XCTest
@testable import Agentman

@MainActor
final class ManagerStoreTests: XCTestCase {
    private func entry(_ id: String, _ role: String, _ text: String = "", streaming: Bool? = nil, path: String? = nil) -> ManagerEntry {
        ManagerEntry(id: id, role: role, text: text, ts: 1, streaming: streaming, path: path)
    }

    private func event(_ type: String, entry: ManagerEntry? = nil, id: String? = nil, delta: String? = nil) -> DaemonEvent {
        var event = DaemonEvent(type: type)
        event.entry = entry
        event.entryId = id
        event.delta = delta
        return event
    }

    // A reply arrives as a placeholder that fills with deltas; its finished
    // entry replaces it whole, and a late placeholder never wipes the text.
    func testAReplyStreamsInAndIsReplacedWhenDone() {
        let store = ManagerStore(client: DaemonClient(socketPath: "/nonexistent"))
        store.apply(event("manager_entry", entry: entry("u", "user", "hi")))
        store.apply(event("manager_entry", entry: entry("r", "assistant", streaming: true)))
        store.apply(event("manager_delta", id: "r", delta: "Both agents "))
        store.apply(event("manager_delta", id: "r", delta: "are fine."))
        XCTAssertEqual(store.entries.map(\.text), ["hi", "Both agents are fine."])

        store.apply(event("manager_entry", entry: entry("r", "assistant", streaming: true)))
        XCTAssertEqual(store.entries.last?.text, "Both agents are fine.")

        store.apply(event("manager_entry", entry: entry("r", "assistant", "Both agents are fine.")))
        XCTAssertEqual(store.entries.count, 2)
        XCTAssertNil(store.entries.last?.streaming)
    }

    func testActivitiesFoldIntoOneLineAndImagesStandAlone() {
        let items = ChatItem.items(from: [
            entry("1", "user", "check"),
            entry("2", "activity", "Checked 2 agents"),
            entry("3", "activity", "Read site"),
            entry("4", "activity", "Looked at shot.png from site", path: "/tmp/shot.png"),
            entry("5", "assistant", "Done."),
        ])
        let shapes = items.map { item -> String in
            switch item {
            case .entry(let e): e.role
            case .activities(let run): "activities \(run.count)"
            case .image: "image"
            }
        }
        XCTAssertEqual(shapes, ["user", "activities 2", "image", "assistant"])
    }
}
