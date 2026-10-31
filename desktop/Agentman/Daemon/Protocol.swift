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
    case setAgentAlerts = "set_agent_alerts"
    case setMode = "set_mode"
    case setModel = "set_model"
    case listFiles = "list_files"
    case readFile = "read_file"
    case listChanges = "list_changes"
    case fileDiff = "file_diff"
    case listCommits = "list_commits"
    case commitFiles = "commit_files"
    case commitDiff = "commit_diff"
    case listBranches = "list_branches"
    case gitStatus = "git_status"
    case gitCommit = "git_commit"
    case gitPush = "git_push"
    case gitCreatePR = "git_create_pr"
    case createWorktree = "create_worktree"
    case listWorktrees = "list_worktrees"
    case removeWorktree = "remove_worktree"
    case usage
    case startSession = "start_session"
    case listFolders = "list_folders"
    case directorySessions = "directory_sessions"
    case resumeSession = "resume_session"
    case endSession = "end_session"
    case listArtifacts = "list_artifacts"
    case readArtifact = "read_artifact"
    case reviewArtifact = "review_artifact"
    case openServer = "open_server"
    case closeServer = "close_server"
    case stopServer = "stop_server"
    case toolsTitle = "tools_title"
    case managerSend = "manager_send"
    case managerHistory = "manager_history"
    case managerStop = "manager_stop"
    case managerReset = "manager_reset"
    case managerSettings = "manager_settings"
    case managerSetSettings = "manager_set_settings"
    case managerSetKey = "manager_set_key"
    case managerBriefs = "manager_briefs"
    case managerCardReply = "manager_card_reply"
    case managerCardDismiss = "manager_card_dismiss"
    case managerEntryClose = "manager_entry_close"
    case knowledgeList = "knowledge_list"
    case knowledgeRead = "knowledge_read"
    case knowledgeWrite = "knowledge_write"
    case knowledgeDelete = "knowledge_delete"
    case managerImage = "manager_image"
    case managerSchedules = "manager_schedules"
    case managerScheduleCancel = "manager_schedule_cancel"
    case managerCheckPermission = "manager_check_permission"
    case managerViewing = "manager_viewing"
    case keysList = "keys_list"
    case keySet = "key_set"
    case keyDelete = "key_delete"
    case lspEndpoint = "lsp_endpoint"
    case lspStatus = "lsp_status"
    case lspSet = "lsp_set"
    case lspRemove = "lsp_remove"
    case sessionTerminal = "session_terminal"
    case transcribe
    case voiceAccount = "voice_account"
    case voiceSignInStart = "voice_signin_start"
    case voiceSignInVerify = "voice_signin_verify"
    case voiceSignOut = "voice_signout"
    case listCommands = "list_commands"
    case commandPanel = "command_panel"
    case attachFile = "attach_file"
}

struct DaemonRequest: Codable, Sendable {
    var type: RequestType
    var sessionId: String?
    var before: String?
    var limit: Int?
    /// How far back a usage request looks: 1, 7, 30 or 90 days.
    var days: Int?
    var text: String?
    var questionId: String?
    var optionKey: String?
    var optionKeys: [String]?
    var answerText: String?
    var clientId: String?
    var path: String?
    var manager: ManagerSettings?
    var cardId: String?
    /// The conversation entry a manager_entry_close takes out.
    var entryId: String?
    var note: Note?
    var key: Key?
    var scheduleId: String?
    var images: [InlineImage]?
    /// The tool calls a tools_title request is about.
    var steps: [ToolCall]?
    /// A note's tier, on the knowledge requests.
    var kind: String?
    /// Where a page of commits starts, or a chunk of a file.
    var offset: Int?
    /// The commit a commit_files or commit_diff request is about.
    var commit: String?
    /// The branch list_commits reads; absent for the one checked out.
    var ref: String?
    /// Leaves changes to whitespace alone out of a file_diff or commit_diff.
    var ignoreWhitespace: Bool?
    /// Asks a commit_diff for the file as the commit left it as well.
    var withFile: Bool?
    /// The files a git_commit commits.
    var paths: [String]?
    /// Removes a worktree with uncommitted changes or an agent in it.
    var force: Bool?
    /// Which way a review_artifact goes: approved, or changes asked for.
    var approve: Bool?
    /// The server an open_server, close_server or stop_server is about.
    var port: Int?
    /// A change to the editor's language servers, on lsp_set and lsp_remove.
    var language: LanguageChange?
    /// An agent's own alerts on or off, on set_agent_alerts.
    var alerts: Bool?
    /// The agents in front of the user, on manager_viewing.
    var viewing: [String]?
    /// One part of a recording to write out, on transcribe.
    var voice: VoiceClip?
    /// One part of a file sent to an agent, on "attach_file".
    var file: FilePart?
    /// A step of signing in to Celery, for voice prompts.
    var voiceSignIn: VoiceSignIn?

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
    /// Folders agents have run in, on "folders".
    var folders: [Folder]?
    /// The documents a session's agent wrote, on "artifacts".
    var artifacts: [Artifact]?
    /// The server shared, and its public link, on "server_opened".
    var port: Int?
    var link: String?
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
    var git: GitState?
    var worktree: Worktree?
    var worktrees: [Worktree]?
    var usage: Usage?

    // Agentman, the manager.
    var entry: ManagerEntry?
    var entries: [ManagerEntry]?
    var entryId: String?
    var delta: String?
    var managerStatus: ManagerStatus?
    var managerSettings: ManagerSettings?
    var brief: Brief?
    var briefs: [Brief]?
    /// What Agentman would do with a request, on "manager_permission".
    var permission: PermissionCheck?
    var more: Bool?

    var schedules: [ManagerSchedule]?

    // Knowledge and keys.
    var notes: [Note]?
    var note: Note?
    var keys: [Key]?

    /// Where the editor's language servers are, on "lsp_endpoint".
    var lsp: LSPEndpoint?
    /// How to show an agent's terminal, on "session_terminal".
    var terminal: SessionTerminal?
    /// What a recording said, on "transcript".
    var transcript: Transcript?
    /// Agentman's Celery account, on "voice_account".
    var voiceAccount: VoiceAccount?
    /// A session's "/" menu, on "commands".
    var commands: [SlashCommand]?
    /// What a panel command showed, on "command_panel".
    var panel: CommandPanel?
    /// A file part received, or where the file is kept, on "attached".
    var attached: AttachedFile?
    /// The editor's language servers, on "lsp_status".
    var languages: LanguagesState?
}

/// The editor's language servers and where each comes from.
struct LanguagesState: Codable, Sendable, Equatable {
    var noDownloads: Bool
    var servers: [LanguageServer]
    /// The Node.js am downloaded, in bytes.
    var nodeSize: Int64?
}

struct LanguageServer: Codable, Sendable, Equatable, Identifiable {
    var id: String
    var name: String
    var language: String
    /// yours, mac, downloaded, download, missing or off.
    var source: String
    var path: String?
    var size: Int64?
    var downloadSize: String?
    var missing: String?
    var running: [String]?
}

/// One change to the language servers' settings.
struct LanguageChange: Codable, Sendable, Equatable {
    var server: String?
    var path: String?
    var disabled: Bool?
    var noDownloads: Bool?
}

/// One part of a recording: 16 kHz mono WAV, as Celery takes it, sent in
/// slices of its base64 that the daemon joins.
struct VoiceClip: Codable, Sendable {
    var id: String
    var part: Int
    var parts: Int
    var mime: String
    var data: String
}

/// A part received, or, for the last, what was said.
struct Transcript: Codable, Sendable {
    var id: String
    var done: Bool
    var text: String?
}

/// A step of signing in to Celery.
struct VoiceSignIn: Codable, Sendable {
    var email: String?
    var requestId: String?
    var code: String?
}

/// One entry of an agent's "/" menu, as the agent itself would offer it.
struct SlashCommand: Codable, Sendable, Equatable, Hashable {
    /// What follows the "/": "compact", "vercel:deploy".
    var name: String
    /// What goes before the name when it is typed, "/" when absent. Codex's
    /// skills take "$".
    var prefix: String?
    var description: String?
    /// "[instructions]", "<model>": what it takes.
    var argumentHint: String?
    /// builtin, bundled, personal, project, plugin or managed.
    var source: String?
    /// Sent without arguments, it answers in a panel of the terminal.
    var panel: Bool?
    /// A skill rather than a command: a message can name it anywhere.
    var skill: Bool?
    /// A skill's SKILL.md on this Mac, for a glance at it.
    var path: String?
}

/// One part of a file sent to an agent, joined and kept by the daemon.
struct FilePart: Codable, Sendable, Equatable {
    var id: String
    var part: Int
    var parts: Int
    var name: String
    var data: String
}

/// A file part received, or, for the last, where the daemon keeps the file.
struct AttachedFile: Codable, Sendable, Equatable {
    var id: String
    var done: Bool
    var path: String?
}

/// What a panel command (/usage, /status) showed in the agent's terminal.
struct CommandPanel: Codable, Sendable, Equatable {
    var command: String
    var text: String
}

/// Agentman's Celery account, for voice prompts.
struct VoiceAccount: Codable, Sendable, Equatable {
    var signedIn: Bool
    var email: String?
    var plan: String?
    var requestId: String?
}

/// An agent's terminal: the tmux session it runs in, and the command that
/// attaches a terminal to it.
struct SessionTerminal: Codable, Sendable, Equatable {
    var name: String
    var argv: [String]
}

/// The language servers' WebSocket on loopback, and the token it wants.
struct LSPEndpoint: Codable, Sendable, Equatable {
    var url: String
    var token: String
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

/// What an agent's folder holds: a listing, its changes, a file, a diff,
/// or its history.
/// An agent's branch as the git actions need it, and, after one, what it did.
/// One of a repository's working folders.
/// What the agents on the Mac spent over a stretch of time, from their own
/// records: tokens by kind, and what the API would have charged for them.
struct Usage: Codable, Sendable, Equatable {
    struct Tokens: Codable, Sendable, Equatable {
        var input: Int64
        var cacheRead: Int64
        var cacheWrite: Int64
        var output: Int64
        var total: Int64 { input + cacheRead + cacheWrite + output }
    }
    struct Cost: Codable, Sendable, Equatable {
        var input: Double
        var cacheRead: Double
        var cacheWrite: Double
        var output: Double
        var total: Double { input + cacheRead + cacheWrite + output }
    }
    struct Agent: Codable, Sendable, Equatable {
        var kind: String
        var sessions: Int
        var tokens: Tokens
        var cost: Double
    }
    /// A day of the chart, an hour over the last day.
    struct Bucket: Codable, Sendable, Equatable {
        /// Milliseconds since 1970.
        var start: Int64
        var cost: [String: Double]?
        var tokens: [String: Int64]?
        var date: Date { Date(timeIntervalSince1970: Double(start) / 1000) }
    }
    struct Model: Codable, Sendable, Equatable {
        var model: String
        var kind: String
        var tokens: Tokens
        var cost: Double
        /// False for a model with no known price: its cost is left out.
        var priced: Bool
    }

    var days: Int
    var hourly: Bool?
    var sessions: Int
    var tokens: Tokens
    var cost: Cost
    var cacheSavings: Double
    var agents: [Agent]?
    var buckets: [Bucket]?
    var models: [Model]?
    /// Records that couldn't be read: usage may be short.
    var skipped: Int?
}

struct Worktree: Codable, Sendable, Hashable, Identifiable {
    /// The folder, absolute.
    var path: String
    var name: String
    /// The repository's own folder name.
    var repo: String
    /// Checked out there; absent when detached.
    var branch: String?
    /// The repository's own folder.
    var main: Bool?
    /// Made by Agentman, under ~/Agentman/Worktrees, which it can remove.
    var agentman: Bool?
    /// The .env files copied in when it was made.
    var copied: [String]?
    var id: String { path }
}

struct GitState: Codable, Sendable, Equatable {
    var sessionId: String
    /// Absent while HEAD is detached.
    var branch: String?
    var defaultBranch: String?
    /// Where the branch is pushed, as origin/name; absent before its first push.
    var upstream: String?
    /// Commits not pushed; the upstream's not here.
    var ahead: Int
    var behind: Int
    var aheadOfDefault: Int
    var remote: String?
    /// Files changed and not committed.
    var changes: Int
    /// "committed", "pushed" or "pr_created", after an action.
    var done: String?
    var commit: String?
    var message: String?
    /// The pull request created, or the one already open.
    var url: String?

    var onDefaultBranch: Bool { branch != nil && branch == defaultBranch }
}

struct WorkspaceResult: Codable, Sendable {
    var kind: String
    var sessionId: String
    var path: String?
    var entries: [WorkspaceEntry]?
    var changes: [WorkspaceChange]?
    var text: String?
    /// Base64, for a supported image small enough to send.
    var image: String?
    var mime: String?
    var diff: String?
    var truncated: Bool?
    /// Entries withheld because they hold secrets.
    var hidden: Int?
    var commits: [WorkspaceCommit]?
    /// The branch checked out; absent when detached.
    var branch: String?
    var commit: WorkspaceCommit?
    var branches: [WorkspaceBranch]?
}

/// A dev server an agent is running.
struct Server: Codable, Sendable, Hashable, Identifiable {
    var port: Int
    /// The listening program: "node", "python3".
    var command: String?
    /// The page's title, when it serves HTML.
    var title: String?
    /// The public preview link while it is shared.
    var link: String?
    var id: Int { port }
}

/// A document an agent wrote for the user: a plan, a task list, a
/// walkthrough, a screenshot.
struct Artifact: Codable, Sendable, Hashable, Identifiable {
    var name: String
    /// "plan", "task", "walkthrough", "spec", "image", "video" or "file".
    var kind: String
    var title: String?
    var summary: String?
    var updatedAt: Int64
    var size: Int64
    var mime: String?
    /// The agent asked the user to approve it.
    var review: Bool?
    var id: String { name }
}

/// A folder agents have run in, with how many there and how many running.
struct Folder: Codable, Sendable, Hashable, Identifiable {
    var path: String
    var agents: Int
    var running: Int?
    var lastActivityAt: Int64?
    var id: String { path }
}

struct WorkspaceEntry: Codable, Sendable, Hashable, Identifiable {
    var name: String
    var directory: Bool
    var size: Int64?
    var id: String { name }
}

struct WorkspaceChange: Codable, Sendable, Hashable, Identifiable {
    var path: String
    /// "modified", "added", "deleted", "renamed" or "untracked".
    var status: String
    var added: Int?
    var removed: Int?
    var id: String { path }
}

struct WorkspaceCommit: Codable, Sendable, Hashable, Identifiable {
    var hash: String
    var subject: String
    var body: String?
    var author: String
    var authoredAt: Int64
    var merge: Bool?
    var files: Int?
    var added: Int?
    var removed: Int?
    var id: String { hash }
}

struct WorkspaceBranch: Codable, Sendable, Hashable, Identifiable {
    var name: String
    var ref: String
    var remote: Bool?
    var current: Bool?
    var updatedAt: Int64
    var id: String { ref }
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
    /// The user closed it, here or on the phone: it is left out.
    var closed: Bool?
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
    /// Which low-risk kinds it answers; nil means all of them.
    var approve: ApproveKinds?
    /// The user's own rules, checked first; an "ask" rule wins over all.
    var rules: [PermissionRule]?
    var style: String
    var instructions: String
    /// Whether the chat model's engine has its key, so Agentman can run.
    var keySet: Bool
    /// The chat model's engine, "anthropic" or "gemini", and each engine's
    /// key. Read only; absent from a daemon before Gemini.
    var provider: String?
    var anthropicKeySet: Bool?
    var geminiKeySet: Bool?

    /// The settings the user edits, without what the daemon reports, so a
    /// key saved meanwhile is not taken for an unsaved change.
    var editable: ManagerSettings {
        var copy = self
        copy.keySet = false
        copy.provider = nil
        copy.anthropicKeySet = nil
        copy.geminiKeySet = nil
        return copy
    }

    /// The engine a model runs on.
    static func provider(of model: String) -> String {
        model.hasPrefix("gemini-") ? "gemini" : "anthropic"
    }
}

/// The kinds of low-risk request Agentman may answer by itself.
struct ApproveKinds: Codable, Sendable, Hashable {
    var reads: Bool
    var edits: Bool
    var commands: Bool
    var `continue`: Bool

    static let all = ApproveKinds(reads: true, edits: true, commands: true, continue: true)
}

/// One of the user's permission rules.
struct PermissionRule: Codable, Sendable, Hashable {
    /// "allow" or "ask".
    var action: String
    /// "command", "edit", "read", "network" or "any".
    var kind: String
    /// The command, path or URL, with * for anything and ? for one character.
    var pattern: String
    /// Only for agents working in this folder; nil for every agent.
    var folder: String?
}

/// What Agentman would do with one request.
struct PermissionCheck: Codable, Sendable, Hashable {
    var auto: Bool
    var summary: String
    var reason: String?
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
    /// The user asked to hear from this agent itself, even while Agentman
    /// speaks for the agents.
    var alerts: Bool?
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
    /// How many documents the agent wrote for the user, and how many of
    /// them wait for their review.
    var artifacts: Int?
    var artifactsToReview: Int?
    /// The dev servers it is running.
    var servers: [Server]?
    var contextPercent: Int?
    var question: Question?
    var queued: [String]?
    /// The conversation this one took over in the same terminal: Claude's
    /// /clear starts a new one there, and /resume switches to another. A
    /// thread showing that one follows to this.
    var previous: String?

    static func == (lhs: Session, rhs: Session) -> Bool {
        lhs.id == rhs.id && lhs.state == rhs.state && lhs.name == rhs.name &&
            lhs.lastActivityAt == rhs.lastActivityAt && lhs.question?.id == rhs.question?.id &&
            lhs.model == rhs.model && lhs.queued == rhs.queued && lhs.mode == rhs.mode &&
            lhs.modes == rhs.modes && lhs.models == rhs.models && lhs.originalName == rhs.originalName &&
            lhs.artifacts == rhs.artifacts && lhs.artifactsToReview == rhs.artifactsToReview &&
            lhs.servers == rhs.servers
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
    /// Pictures attached to a user's message, as paths: from how the agent
    /// recorded it, never from its words.
    var images: [String]?
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
    /// Agentman is on, and speaks for the agents.
    var agentman: Bool?
}
