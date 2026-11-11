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

    // However a stream is cut into reads, the same lines come out: one byte
    // at a time, or a big frame in the socket's 64 KB reads.
    func testLinesComeOutTheSameHoweverTheStreamIsCut() {
        let stream = Data("{\"a\":1}\n\n{\"b\":\"two\"}\n{\"c\":3}\n{\"d\"".utf8)
        var whole = LineFramer(), bytes = LineFramer()
        let atOnce = whole.append(stream)
        let oneByOne = stream.flatMap { bytes.append(Data([$0])) }
        XCTAssertEqual(oneByOne, atOnce)
        XCTAssertEqual(bytes.append(Data(":4}\n".utf8)), [Data("{\"d\":4}".utf8)])

        let big = Data(repeating: UInt8(ascii: "x"), count: 6 << 20) + Data("\nnext\n".utf8)
        var framer = LineFramer()
        var lines: [Data] = []
        let started = Date()
        for offset in stride(from: 0, to: big.count, by: 64 << 10) {
            lines += framer.append(big.subdata(in: offset..<min(offset + (64 << 10), big.count)))
        }
        print("LINEFRAMER 6 MB in 64 KB reads: \(Int(Date().timeIntervalSince(started) * 1000)) ms")
        XCTAssertEqual(lines.map(\.count), [6 << 20, 4])
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
        // A daemon from before API levels says neither.
        XCTAssertNil(event.daemon?.apiLevel)
        XCTAssertNil(event.daemon?.install)
    }

    // What a daemon since API level 1 says of itself (internal/protocol's
    // DaemonInfo), on every session list.
    func testTheDaemonSaysItsAPILevelAndHowItWasInstalled() throws {
        let event = try JSONDecoder().decode(DaemonEvent.self, from: Data("""
        {"type":"sessions","sessions":[],"daemon":{"version":"0.39.0","apiLevel":1,"install":"managed","latest":"0.39.0"}}
        """.utf8))
        XCTAssertEqual(event.daemon?.apiLevel, 1)
        XCTAssertEqual(event.daemon?.install, "managed")
    }

    // Claude's form of several questions, as internal/protocol's Question
    // carries it: its questions, the one on screen, and that it closes.
    func testAFormOfSeveralQuestionsDecodes() throws {
        let question = try JSONDecoder().decode(Question.self, from: Data("""
        {"id":"terminal-1","prompt":"Which size?","options":[{"key":"1","label":"Small"}],
         "steps":[{"title":"Colour","answered":true},{"title":"Size"},{"title":"Shape"}],"step":2,"reviewStep":true,"closable":true}
        """.utf8))
        XCTAssertTrue(question.isForm)
        XCTAssertFalse(question.isReview)
        XCTAssertEqual(question.steps?.map(\.title), ["Colour", "Size", "Shape"])
        XCTAssertEqual(question.steps?.first?.answered, true)
        XCTAssertNil(question.steps?[1].answered)
        XCTAssertEqual(question.closable, true)

        XCTAssertEqual(question.lastStep, 4)
        XCTAssertTrue(question.namesSteps)
        var review = question
        review.step = 4
        XCTAssertTrue(review.isReview)
        // Antigravity's: no review, and its questions named in full.
        var agy = question
        agy.reviewStep = nil
        agy.steps = [QuestionStep(title: "Which size would suit a small kitchen best?"), QuestionStep(title: "Which colour?")]
        agy.step = 2
        XCTAssertEqual(agy.lastStep, 2)
        XCTAssertFalse(agy.isReview)
        XCTAssertFalse(agy.namesSteps)
        // The question on screen not known: no moving through it.
        var unknown = question
        unknown.step = nil
        XCTAssertFalse(unknown.isForm)
        // A daemon from before forms sends none of it.
        let plain = try JSONDecoder().decode(Question.self, from: Data(#"{"prompt":"Proceed?","options":[]}"#.utf8))
        XCTAssertFalse(plain.isForm)
        XCTAssertNil(plain.closable)
    }

    func testMovingAndClosingSayWhichQuestion() throws {
        var move = DaemonRequest(.moveQuestion, sessionId: "claude:s1")
        move.questionId = "terminal-1"
        move.step = 3
        let moved = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(move)) as? [String: Any])
        XCTAssertEqual(moved["type"] as? String, "move_question")
        XCTAssertEqual(moved["questionId"] as? String, "terminal-1")
        XCTAssertEqual(moved["step"] as? Int, 3)

        var close = DaemonRequest(.closeQuestion, sessionId: "claude:s1")
        close.questionId = "terminal-1"
        let closed = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(close)) as? [String: Any])
        XCTAssertEqual(closed["type"] as? String, "close_question")
        XCTAssertNil(closed["step"])
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

    func testPermissionSettingsRoundTrip() throws {
        let json = #"{"chatModel":"claude-opus-5-5","briefModel":"claude-opus-5-5","effort":"medium","briefs":true,"autoApprove":true,"approve":{"reads":true,"edits":false,"commands":true,"continue":true},"rules":[{"action":"allow","kind":"command","pattern":"git push*","folder":"/work/app"}],"style":"","instructions":"","keySet":true}"#
        let settings = try JSONDecoder().decode(ManagerSettings.self, from: Data(json.utf8))
        XCTAssertEqual(settings.approve?.edits, false)
        XCTAssertEqual(settings.rules?.first?.pattern, "git push*")
        let again = try JSONDecoder().decode(ManagerSettings.self, from: JSONEncoder().encode(settings))
        XCTAssertEqual(again, settings)
        let older = #"{"chatModel":"claude-opus-5-5","briefModel":"claude-opus-5-5","effort":"medium","briefs":true,"autoApprove":true,"style":"","instructions":"","keySet":false}"#
        XCTAssertNil(try JSONDecoder().decode(ManagerSettings.self, from: Data(older.utf8)).rules)
    }
}
