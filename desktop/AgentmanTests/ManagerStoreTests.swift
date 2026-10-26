import XCTest
@testable import Agentman

@MainActor
final class ManagerStoreTests: XCTestCase {
    private func entry(_ id: String, _ role: String, _ text: String = "", streaming: Bool? = nil, path: String? = nil,
                       ts: Int64 = 1, card: ManagerCard? = nil) -> ManagerEntry {
        ManagerEntry(id: id, role: role, text: text, ts: ts, streaming: streaming, path: path, card: card)
    }

    private func card(_ id: String, _ state: String) -> ManagerCard {
        ManagerCard(id: id, kind: "question", state: state, sessionId: "codex:b", agentName: "api",
                    title: "wants to run a command", detail: "git push", questionId: "q1",
                    options: [QuestionOption(key: "1", label: "Yes"), QuestionOption(key: "2", label: "No")])
    }

    private func store() -> ManagerStore {
        let defaults = UserDefaults(suiteName: "test-\(UUID().uuidString)")!
        defaults.set(Int64(100), forKey: "managerLastRead")
        return ManagerStore(client: DaemonClient(socketPath: "/nonexistent"), defaults: defaults)
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

    // What the user closed is gone from the conversation, here and on the
    // phone, and isn't unread news.
    func testClosedEntriesAreLeftOut() {
        var closed = entry("2", "brief", "site finished.", ts: 200)
        closed.closed = true
        let items = ChatItem.items(from: [entry("1", "user", "hi"), closed, entry("3", "assistant", "Done.")])
        XCTAssertEqual(items.map(\.id), ["1", "3"])

        let store = store()
        var event = DaemonEvent(type: "manager_entry")
        event.entry = closed
        store.apply(event)
        XCTAssertTrue(store.unread.isEmpty)
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

    // What arrived since the user last looked counts as new: replies, briefs
    // and open cards, not their own messages or Agentman's activity.
    func testNewsCountsUntilRead() {
        let store = store()
        var told: [String] = []
        store.onNews = { told.append($0.id) }
        store.apply(event("manager_entry", entry: entry("old", "assistant", "Earlier.", ts: 50)))
        store.apply(event("manager_entry", entry: entry("u", "user", "hi", ts: 200)))
        store.apply(event("manager_entry", entry: entry("a", "activity", "Checked 2 agents", ts: 201)))
        store.apply(event("manager_entry", entry: entry("r", "assistant", "All fine.", ts: 202)))
        store.apply(event("manager_entry", entry: entry("c", "card", "api wants to run a command", ts: 203, card: card("c", "open"))))
        XCTAssertEqual(store.unread.map(\.id), ["r", "c"])
        XCTAssertEqual(told, ["c"], "a new card is news at once; a reply is news when the turn ends")

        store.markRead()
        XCTAssertTrue(store.unread.isEmpty)
    }

    // A reply is told once, when Agentman finishes, with its last words.
    func testAFinishedReplyIsToldOnce() {
        let store = store()
        var told: [String] = []
        store.onNews = { told.append($0.text) }
        var thinking = DaemonEvent(type: "manager_status")
        thinking.managerStatus = ManagerStatus(state: "thinking", keySet: true)
        store.apply(thinking)
        store.apply(event("manager_entry", entry: entry("r1", "assistant", "Looking.", ts: 300)))
        store.apply(event("manager_entry", entry: entry("r2", "assistant", "Both are fine.", ts: 301)))
        var idle = DaemonEvent(type: "manager_status")
        idle.managerStatus = ManagerStatus(state: "idle", keySet: true)
        store.apply(idle)
        store.apply(idle)
        XCTAssertEqual(told, ["Both are fine."])
    }

    // A card answered in the agent itself leaves the conversation, and its
    // notification is taken back.
    func testACardSettledElsewhereLeaves() {
        let store = store()
        var closed: [String] = []
        store.onCardClosed = { closed.append($0.id) }
        store.apply(event("manager_entry", entry: entry("c", "card", ts: 400, card: card("c", "open"))))
        XCTAssertEqual(store.openCards.count, 1)
        store.apply(event("manager_entry", entry: entry("c", "card", ts: 400, card: card("c", "settled"))))
        XCTAssertEqual(closed, ["c"])
        XCTAssertTrue(store.openCards.isEmpty)
        XCTAssertTrue(store.unread.isEmpty)
        XCTAssertTrue(ChatItem.items(from: store.entries).isEmpty)

        store.apply(event("manager_entry", entry: entry("d", "card", ts: 401, card: card("d", "answered"))))
        XCTAssertEqual(ChatItem.items(from: store.entries).count, 1, "a card answered here stays, as a receipt")
    }

    func testNotificationTextIsPlain() {
        XCTAssertEqual(Notifier.plain("**site** finished.\n\n- Fixed `header`\n- Tests pass"), "site finished. Fixed header Tests pass")
    }
}
