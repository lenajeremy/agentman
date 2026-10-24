import Foundation

// The daemon's protocol, as far as this app uses it. These mirror the Go types
// in internal/protocol (and the phone's mobile/lib/protocol.ts), with the same
// JSON names. Fields the app does not use yet are left out: decoding ignores
// them, so a newer daemon that adds fields still decodes.

/// The wire protocol version this app speaks (internal/protocol.Version).
let protocolVersion = 2

/// One frame on the socket, in either direction.
struct Envelope<Payload: Codable>: Codable {
    var v: Int
    var id: String
    var replyTo: String?
    var to: String?
    var payload: Payload
}

/// The parts of an envelope that say where it goes, read before the payload.
struct EnvelopeHeader: Decodable {
    var v: Int
    var id: String
    var replyTo: String?
}

// MARK: - Requests

enum RequestType: String, Codable, Sendable {
    case listSessions = "list_sessions"
    case subscribe
    case unsubscribe
    case fetchMessages = "fetch_messages"
    case sendMessage = "send_message"
    case interrupt
    case answerQuestion = "answer_question"
}

struct DaemonRequest: Codable, Sendable {
    var type: RequestType
    var sessionId: String?
    var before: String?
    var limit: Int?
    var text: String?
    var questionId: String?
    var optionKey: String?
    var optionKeys: [String]?
    var answerText: String?
    var clientId: String?

    init(_ type: RequestType, sessionId: String? = nil) {
        self.type = type
        self.sessionId = sessionId
    }
}

// MARK: - Events

enum SendStatus: String, Codable, Sendable {
    case delivered, queued, failed
}

struct DaemonEvent: Codable, Sendable {
    var type: String
    var sessionId: String?
    var sessions: [Session]?
    var session: Session?
    var messages: [Message]?
    var page: Page?
    var sessionName: String?
    var preview: String?
    var clientId: String?
    var status: SendStatus?
    var error: String?
    var daemon: DaemonInfo?
}

// MARK: - Domain

enum SessionState: String, Codable, Sendable {
    case busy
    case idle
    case waitingInput = "waiting_input"
    case ended
    case unknown

    init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = SessionState(rawValue: raw) ?? .unknown
    }

    /// Needs you first, then working, then idle: the order the board reads in.
    var rank: Int {
        switch self {
        case .waitingInput: 0
        case .busy: 1
        case .idle: 2
        case .ended, .unknown: 3
        }
    }
}

struct Session: Codable, Sendable, Identifiable, Hashable {
    var id: String
    var kind: String
    var name: String
    var cwd: String
    var state: SessionState
    /// How the daemon can deliver a message: "tmux", "api", "hook" or "none".
    var inject: String?
    var startedAt: Int64?
    var lastActivityAt: Int64?
    var model: String?
    var mode: String?
    var contextPercent: Int?
    var question: Question?
    var queued: [String]?

    static func == (lhs: Session, rhs: Session) -> Bool {
        lhs.id == rhs.id && lhs.state == rhs.state && lhs.name == rhs.name &&
            lhs.lastActivityAt == rhs.lastActivityAt && lhs.question?.id == rhs.question?.id &&
            lhs.model == rhs.model && lhs.queued == rhs.queued
    }

    func hash(into hasher: inout Hasher) { hasher.combine(id) }

    /// Whether this app can send to it. A session the daemon cannot reach
    /// shows its conversation, read-only.
    var canReceive: Bool { inject != nil && inject != "none" }

    var lastActivity: Date? {
        lastActivityAt.map { Date(timeIntervalSince1970: TimeInterval($0) / 1000) }
    }
}

struct Question: Codable, Sendable, Hashable {
    var id: String?
    var prompt: String
    var title: String?
    var detail: String?
    var options: [QuestionOption]
    var multiple: Bool?
    var custom: Bool?
}

struct QuestionOption: Codable, Sendable, Hashable, Identifiable {
    var key: String
    var label: String
    var description: String?
    var selected: Bool?
    var checked: Bool?
    /// The option takes a note, like "No, and tell Claude what to do".
    var withText: Bool?

    var id: String { key }
}

enum Role: String, Codable, Sendable {
    case user, assistant, tool, system
}

struct ToolCall: Codable, Sendable, Hashable {
    var name: String
    var summary: String?
    /// "running", "ok" or "error".
    var status: String?
}

struct Message: Codable, Sendable, Identifiable, Hashable {
    var id: String
    var sessionId: String
    var role: Role
    var ts: Int64
    var text: String?
    var tool: ToolCall?
    var isSidechain: Bool?

    var date: Date { Date(timeIntervalSince1970: TimeInterval(ts) / 1000) }
}

struct Page: Codable, Sendable {
    var sessionId: String
    var messages: [Message]
    var nextCursor: String?
    var hasMore: Bool
}

/// The agentman running on this Mac, and whether a newer one is out.
struct DaemonInfo: Codable, Sendable, Hashable {
    var version: String
    var latest: String?
    var behind: Int?
    var upgrade: String?
}
