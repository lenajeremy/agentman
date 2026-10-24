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
    case sendQueued = "send_queued"
    case answerQuestion = "answer_question"
    case readSeenFile = "read_seen_file"
    case renameSession = "rename_session"
    case setMode = "set_mode"
    case setModel = "set_model"
    case toolsTitle = "tools_title"
    case managerSend = "manager_send"
    case managerHistory = "manager_history"
    case managerStop = "manager_stop"
    case managerSettings = "manager_settings"
    case managerSetSettings = "manager_set_settings"
    case managerSetKey = "manager_set_key"
    case managerBriefs = "manager_briefs"
    case managerCardReply = "manager_card_reply"
    case managerCardDismiss = "manager_card_dismiss"
    case knowledgeList = "knowledge_list"
    case knowledgeRead = "knowledge_read"
    case knowledgeWrite = "knowledge_write"
    case knowledgeDelete = "knowledge_delete"
    case managerImage = "manager_image"
    case managerSchedules = "manager_schedules"
    case managerScheduleCancel = "manager_schedule_cancel"
    case keysList = "keys_list"
    case keySet = "key_set"
    case keyDelete = "key_delete"
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
    var path: String?
    var manager: ManagerSettings?
    var cardId: String?
    var note: Note?
    var key: Key?
    var scheduleId: String?
    var images: [InlineImage]?
    /// The tool calls a tools_title request is about.
    var steps: [ToolCall]?
    /// A note's tier, on the knowledge requests.
    var kind: String?

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
    init(type: String) {
        self.type = type
    }

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
    var workspace: WorkspaceResult?

    // Agentman, the manager.
    var entry: ManagerEntry?
    var entries: [ManagerEntry]?
    var entryId: String?
    var delta: String?
    var managerStatus: ManagerStatus?
    var managerSettings: ManagerSettings?
    var brief: Brief?
    var briefs: [Brief]?
    var more: Bool?

    var schedules: [ManagerSchedule]?

    // Knowledge and keys.
    var notes: [Note]?
    var note: Note?
    var keys: [Key]?
}

/// An image sent with a message: its type and its bytes, in base64.
struct InlineImage: Codable, Sendable, Hashable {
    var mime: String
    var data: String
}

/// Something Agentman scheduled for itself: a reminder, once or repeating,
/// or a watch on an agent whose result comes back to it.
struct ManagerSchedule: Codable, Sendable, Hashable, Identifiable {
    var id: String
    /// "reminder" or "watch".
    var kind: String
    var text: String
    /// When a reminder next fires, or a watch gives up, in milliseconds.
    var due: Int64
    var every: Int?
    var times: Int?
    var until: Int64?
    var fired: Int?
    var sessionId: String?
    var agentName: String?
    /// For a watch: "turn_end", "needs_user", "idle" or "ended".
    var condition: String?
    var createdAt: Int64

    var dueDate: Date { Date(timeIntervalSince1970: TimeInterval(due) / 1000) }
}

/// One of the user's notes for their agents. Body is empty in a list.
struct Note: Codable, Sendable, Hashable, Identifiable {
    var name: String
    /// "shared" (agents can read it) or "private" (only Agentman).
    var tier: String
    var title: String
    var always: Bool?
    var body: String?
    var updatedAt: Int64

    var id: String { tier + "/" + name }
}

/// An API key Agentman keeps for agents, without its value.
struct Key: Codable, Sendable, Hashable, Identifiable {
    /// The environment variable it becomes: ELEVENLABS_API_KEY.
    var name: String
    var description: String?
    /// "auto", "projects" or "ask".
    var rule: String
    var projects: [String]?
    var set: Bool
    var updatedAt: Int64

    var id: String { name }
}

/// A file read from an agent's folder, or an image it opened.
struct WorkspaceResult: Codable, Sendable {
    var kind: String
    var sessionId: String
    var path: String?
    /// Base64, for a supported image small enough to send.
    var image: String?
    var mime: String?
}

// MARK: - Agentman

/// One entry in the conversation with Agentman.
struct ManagerEntry: Codable, Sendable, Identifiable, Hashable {
    var id: String
    /// "user", "assistant", "activity", "brief" or "error".
    var role: String
    var text: String
    var ts: Int64
    var sessionId: String?
    var tool: String?
    var streaming: Bool?
    /// An image an activity looked at.
    var path: String?
    /// The images a user's message came with, by the names the daemon keeps
    /// them under.
    var images: [String]?
    /// On a "card" entry: a decision only the user can make.
    var card: ManagerCard?
}

/// A decision Agentman put in front of the user: an agent's question, with
/// its options, or its need in words, with a box to reply.
struct ManagerCard: Codable, Sendable, Hashable, Identifiable {
    var id: String
    /// "question", "needs_you", "key" or "note".
    var kind: String
    /// "open", "answered" (here, on the card), "settled" (no longer needs
    /// the user: answered in the agent, or the agent moved on) or
    /// "dismissed" (put away unanswered).
    var state: String
    var sessionId: String
    var agentName: String
    var title: String
    var detail: String?
    /// Why Agentman didn't answer it itself.
    var reason: String?
    /// Agentman's brief on the agent, for what it is working on.
    var context: String?
    var questionId: String?
    var options: [QuestionOption]?
    var multiple: Bool?
    var custom: Bool?
    /// The key a key card is about.
    var keyName: String?
    /// The option Agentman would pick, waiting on the user's OK.
    var suggested: String?
    var outcome: String?
    /// The note a note card suggests keeping.
    var note: Note?

    var isOpen: Bool { state == "open" }
    /// Settled or put away: the conversation no longer shows it.
    var isGone: Bool { state == "settled" || state == "dismissed" }

    /// The choices on the card: the agent's question, or whether to give a
    /// key.
    var question: Question? {
        switch kind {
        case "question":
            guard let questionId else { return nil }
            return Question(id: questionId, prompt: "", title: nil, detail: nil,
                            options: options ?? [], multiple: multiple, custom: custom)
        case "key":
            return Question(id: id, prompt: "", title: nil, detail: nil, options: options ?? [], multiple: false, custom: false)
        default:
            return nil
        }
    }
}

struct ManagerSettings: Codable, Sendable, Hashable {
    var chatModel: String
    var briefModel: String
    var effort: String
    var briefs: Bool
    /// Agentman answers low-risk requests itself.
    var autoApprove: Bool
    var style: String
    var instructions: String
    var keySet: Bool
}

struct ManagerStatus: Codable, Sendable, Hashable {
    /// "idle", "thinking", "needs_key" or "error".
    var state: String
    var error: String?
    var keySet: Bool
}

/// Agentman's living summary of one agent.
struct Brief: Codable, Sendable, Hashable, Identifiable {
    var sessionId: String
    var name: String
    var summary: String
    var goal: String?
    var done: [String]?
    var now: String?
    var blocked: String?
    var needsYou: String?
    var files: [String]?
    var notable: Bool?
    var updatedAt: Int64

    var id: String { sessionId }
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
    /// What it is called: the user's name for it when they gave one.
    var name: String
    /// The agent's own name, when the user renamed it.
    var originalName: String?
    var cwd: String
    var state: SessionState
    /// How the daemon can deliver a message: "tmux", "api", "hook" or "none".
    var inject: String?
    var startedAt: Int64?
    var lastActivityAt: Int64?
    var model: String?
    var mode: String?
    /// The modes and models it can be switched to, in the agent's own names.
    var modes: [String]?
    var models: [String]?
    /// What a model switch touches: "session" alone, or the "default" for
    /// new sessions too, which is confirmed first. Absent: no switching.
    var modelScope: String?
    var contextPercent: Int?
    var question: Question?
    var queued: [String]?

    static func == (lhs: Session, rhs: Session) -> Bool {
        lhs.id == rhs.id && lhs.state == rhs.state && lhs.name == rhs.name &&
            lhs.lastActivityAt == rhs.lastActivityAt && lhs.question?.id == rhs.question?.id &&
            lhs.model == rhs.model && lhs.queued == rhs.queued && lhs.mode == rhs.mode &&
            lhs.modes == rhs.modes && lhs.models == rhs.models && lhs.originalName == rhs.originalName
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
    /// What the step did, in plain words ("Ran the Go tests"); summary is
    /// the raw command or path behind it.
    var title: String? = nil
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
