import XCTest
@testable import Agentman

final class LineFramerTests: XCTestCase {
    // A read can end mid-line or carry several lines; each comes out whole.
    func testLinesSplitAcrossReadsComeOutWhole() {
        var framer = LineFramer()
        XCTAssertEqual(framer.append(Data("{\"a\":1}\n{\"b\"".utf8)), [Data("{\"a\":1}".utf8)])
        XCTAssertEqual(framer.append(Data(":2}\n\n{\"c\":3}\n".utf8)), [Data("{\"b\":2}".utf8), Data("{\"c\":3}".utf8)])
        XCTAssertEqual(framer.append(Data()), [])
    }

    func testALineLongerThanAFrameIsDropped() {
        var framer = LineFramer(maxLine: 8)
        XCTAssertEqual(framer.append(Data("0123456789\nok\n".utf8)), [Data("ok".utf8)])
        XCTAssertEqual(framer.append(Data(String(repeating: "x", count: 20).utf8)), [])
        XCTAssertEqual(framer.append(Data("\nfine\n".utf8)), [Data("fine".utf8)])
    }
}

final class ProtocolTests: XCTestCase {
    // A frame as am serve writes it on the socket (internal/daemon/local.go).
    let sessionsFrame = """
    {"v":2,"id":"1792-1","replyTo":"mac-1","to":"app","payload":{"type":"sessions","sessions":[\
    {"id":"claude:s1","kind":"claude","nativeId":"s1","name":"agentman-58","cwd":"/Users/me/agentman",\
    "state":"waiting_input","inject":"tmux","startedAt":1,"lastActivityAt":2,"model":"claude-opus-5-5",\
    "question":{"id":"terminal-ab","prompt":"Do you want to proceed?","title":"Bash command","detail":"npm test",\
    "options":[{"key":"1","label":"Yes"},{"key":"3","label":"No","withText":true}]},"futureField":true},\
    {"id":"codex:x","kind":"codex","nativeId":"x","name":"site","cwd":"/Users/me/site","state":"thinking_hard",\
    "inject":"none","startedAt":1,"lastActivityAt":3}],"daemon":{"version":"0.17.0","latest":"0.17.0"}}}
    """

    func testASessionsFrameFromTheDaemonDecodes() throws {
        let data = Data(sessionsFrame.utf8)
        let header = try JSONDecoder().decode(EnvelopeHeader.self, from: data)
        XCTAssertEqual(header.replyTo, "mac-1")
        let event = try JSONDecoder().decode(Envelope<DaemonEvent>.self, from: data).payload
        XCTAssertEqual(event.type, "sessions")
        XCTAssertEqual(event.daemon?.version, "0.17.0")
        let sessions = try XCTUnwrap(event.sessions)
        XCTAssertEqual(sessions.count, 2)
        XCTAssertEqual(sessions[0].state, .waitingInput)
        XCTAssertEqual(sessions[0].question?.options.last?.withText, true)
        XCTAssertTrue(sessions[0].canReceive)
        // A state this app does not know yet must not lose the whole board.
        XCTAssertEqual(sessions[1].state, .unknown)
        XCTAssertFalse(sessions[1].canReceive)
    }

    // The daemon decodes requests with Go's encoding/json: unset fields are
    // left out rather than sent as null.
    func testARequestLeavesOutWhatItDoesNotSet() throws {
        var request = DaemonRequest(.answerQuestion, sessionId: "claude:s1")
        request.questionId = "terminal-ab"
        request.optionKey = "1"
        let envelope = Envelope(v: protocolVersion, id: "mac-7", replyTo: nil, to: "daemon", payload: request)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(envelope)) as? [String: Any])
        XCTAssertEqual(object["v"] as? Int, 2)
        XCTAssertEqual(object["to"] as? String, "daemon")
        let payload = try XCTUnwrap(object["payload"] as? [String: Any])
        XCTAssertEqual(payload["type"] as? String, "answer_question")
        XCTAssertEqual(payload["optionKey"] as? String, "1")
        XCTAssertNil(payload["text"])
        XCTAssertNil(payload["answerText"])
    }

    func testTheBoardPutsWhoNeedsYouFirst() {
        func session(_ id: String, _ state: SessionState, _ at: Int64) -> Session {
            Session(id: id, kind: "claude", name: id, cwd: "/x", state: state, lastActivityAt: at)
        }
        let sorted = [session("idle", .idle, 9), session("busy", .busy, 1), session("ask", .waitingInput, 0),
                      session("busy-new", .busy, 5)].sorted(by: AgentsStore.boardOrder)
        XCTAssertEqual(sorted.map(\.id), ["ask", "busy-new", "busy", "idle"])
    }

    // A message's images go as the daemon reads them: a list of type and
    // base64 bytes.
    func testAMessageCarriesItsImages() throws {
        var request = DaemonRequest(.managerSend)
        request.text = "look"
        request.images = [InlineImage(mime: "image/png", data: "iVBORw0KGgo=")]
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(request)) as? [String: Any])
        let images = try XCTUnwrap(json["images"] as? [[String: String]])
        XCTAssertEqual(images.first?["mime"], "image/png")
        XCTAssertEqual(images.first?["data"], "iVBORw0KGgo=")
        XCTAssertEqual(json["type"] as? String, "manager_send")
    }

    func testANoteCardCarriesItsNote() throws {
        let json = #"{"id":"c1","kind":"note","state":"open","sessionId":"","agentName":"Agentman","title":"suggests a note","reason":"You said so twice.","note":{"name":"","tier":"shared","title":"Commits","body":"No attribution.","updatedAt":0}}"#
        let card = try JSONDecoder().decode(ManagerCard.self, from: Data(json.utf8))
        XCTAssertEqual(card.kind, "note")
        XCTAssertEqual(card.note?.title, "Commits")
        XCTAssertEqual(card.note?.tier, "shared")
        XCTAssertTrue(card.isOpen)
    }

    func testAnAgentsDocumentsDecode() throws {
        let json = #"{"type":"artifacts","sessionId":"antigravity:a","artifacts":[{"name":"implementation_plan.md","kind":"plan","title":"Plan","summary":"Add a login page","updatedAt":1792000000000,"size":2048,"review":true}]}"#
        let event = try JSONDecoder().decode(DaemonEvent.self, from: Data(json.utf8))
        XCTAssertEqual(event.artifacts?.first?.kind, "plan")
        XCTAssertEqual(event.artifacts?.first?.review, true)
    }
}
