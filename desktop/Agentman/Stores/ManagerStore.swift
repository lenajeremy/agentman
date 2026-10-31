import AppKit
import Foundation
import Observation

/// The conversation with Agentman, as the daemon tells it: entries in order,
/// replies streaming in as they are written, what Agentman is doing, its
/// settings, and its brief on each agent.
@MainActor
@Observable
final class ManagerStore {
    private(set) var entries: [ManagerEntry] = []
    private(set) var hasOlder = false
    private(set) var status = ManagerStatus(state: "idle", keySet: false)
    private(set) var settings: ManagerSettings?
    private(set) var briefs: [String: Brief] = [:]
    /// The user's notes for their agents, without bodies, newest first.
    private(set) var notes: [Note] = []
    /// The keys Agentman keeps, by name.
    private(set) var keys: [Key] = []
    /// What Agentman has scheduled for itself, soonest first.
    private(set) var schedules: [ManagerSchedule] = []
    /// The last thing that went wrong talking to Agentman.
    var problem: String?
    private(set) var loaded = false
    /// Whether earlier messages are on their way, so the reader reaching the
    /// top asks for them once.
    private(set) var loadingOlder = false
    /// When the user last saw the conversation, in milliseconds. Kept across
    /// launches, so what arrived while the app was closed shows as new.
    private(set) var lastRead: Int64

    /// Something worth telling the user arrived: Agentman finished a reply,
    /// or put up a card or a brief.
    var onNews: ((ManagerEntry) -> Void)?
    /// A card no longer needs the user, wherever it was answered.
    var onCardClosed: ((ManagerCard) -> Void)?

    private let client: DaemonClient
    private let defaults: UserDefaults
    /// Internal rather than private so screenshot tests can fill it.
    var images: [String: NSImage] = [:]
    private static let lastReadKey = "managerLastRead"

    init(client: DaemonClient, defaults: UserDefaults = .standard) {
        self.client = client
        self.defaults = defaults
        let stored = defaults.object(forKey: Self.lastReadKey) as? Int64
        // The first launch starts with nothing unread, not the whole history.
        lastRead = stored ?? Int64(Date().timeIntervalSince1970 * 1000)
        if stored == nil { defaults.set(lastRead, forKey: Self.lastReadKey) }
    }

    /// What arrived since the user last looked: Agentman's replies, briefs,
    /// and cards still waiting on them.
    var unread: [ManagerEntry] {
        entries.filter { $0.ts > lastRead && $0.closed != true && Self.isNews($0) }
    }

    /// Cards still waiting on the user.
    var openCards: [ManagerCard] {
        entries.compactMap(\.card).filter(\.isOpen)
    }

    /// Notes Agentman suggests, waiting for the user to approve, edit or
    /// dismiss them.
    var noteSuggestions: [ManagerCard] {
        openCards.filter { $0.kind == "note" }
    }

    static func isNews(_ entry: ManagerEntry) -> Bool {
        switch entry.role {
        case "assistant": entry.streaming != true && !entry.text.isEmpty
        case "brief", "agent": true
        case "card": entry.card?.isOpen == true
        default: false
        }
    }

    /// The user has seen everything so far.
    func markRead() {
        guard let latest = entries.last?.ts, latest > lastRead else { return }
        lastRead = latest
        defaults.set(latest, forKey: Self.lastReadKey)
    }

    var thinking: Bool { status.state == "thinking" }
    /// Agentman can run, having its key: it speaks for the agents.
    var on: Bool { settings?.keySet ?? status.keySet }
    var needsKey: Bool { settings.map { !$0.keySet } ?? (status.state == "needs_key") }
    /// The engine Agentman's conversation runs on.
    var provider: String {
        settings?.provider ?? settings.map { ManagerSettings.provider(of: $0.chatModel) } ?? "anthropic"
    }

    /// Loads everything afresh: on connecting, and after a reconnect.
    func reload() async {
        async let settingsReply = try? client.request(DaemonRequest(.managerSettings))
        async let briefsReply = try? client.request(DaemonRequest(.managerBriefs))
        async let schedulesReply = try? client.request(DaemonRequest(.managerSchedules))
        var historyRequest = DaemonRequest(.managerHistory)
        historyRequest.limit = 80
        async let historyReply = try? client.request(historyRequest)

        if let reply = await settingsReply {
            apply(reply)
        }
        if let reply = await briefsReply, let all = reply.briefs {
            briefs = Dictionary(all.map { ($0.sessionId, $0) }, uniquingKeysWith: { a, _ in a })
        }
        if let reply = await historyReply {
            entries = reply.entries ?? []
            hasOlder = reply.more ?? false
        }
        if let reply = await schedulesReply {
            schedules = reply.schedules ?? []
        }
        loaded = true
    }

    // MARK: - Talking to Agentman

    func send(_ text: String, images: [Attachment] = []) async -> Bool {
        var request = DaemonRequest(.managerSend)
        request.text = text
        request.images = images.isEmpty ? nil : Attachments.encoded(images)
        problem = nil
        do {
            let reply = try await client.request(request)
            if reply.type == "error" {
                problem = reply.error
                return false
            }
            apply(reply)
            return true
        } catch {
            problem = error.localizedDescription
            return false
        }
    }

    func stop() {
        client.send(DaemonRequest(.managerStop))
    }

    /// Starts a new conversation. The one before is archived on the Mac, the
    /// cards still waiting carry over, and Agentman keeps what lasts of it in
    /// its private notes. Returns the problem, if any.
    func reset() async -> String? {
        guard let reply = try? await client.request(DaemonRequest(.managerReset)) else { return "Couldn't reach am serve." }
        if reply.type == "error" { return reply.error }
        await reload()
        return nil
    }

    func loadOlder() async {
        guard hasOlder, !loadingOlder, let first = entries.first else { return }
        loadingOlder = true
        defer { loadingOlder = false }
        var request = DaemonRequest(.managerHistory)
        request.before = first.id
        request.limit = 80
        guard let reply = try? await client.request(request), let older = reply.entries else { return }
        let known = Set(entries.map(\.id))
        entries.insert(contentsOf: older.filter { !known.contains($0.id) }, at: 0)
        hasOlder = reply.more ?? false
    }

    /// Saves settings. Returns the problem, if any.
    func save(_ next: ManagerSettings) async -> String? {
        var request = DaemonRequest(.managerSetSettings)
        request.manager = next
        guard let reply = try? await client.request(request) else { return "Couldn't reach am serve." }
        if reply.type == "error" { return reply.error }
        apply(reply)
        return nil
    }

    /// What Agentman would do with a request, under the settings as saved.
    func checkPermission(kind: String, target: String, folder: String) async -> PermissionCheck? {
        var request = DaemonRequest(.managerCheckPermission)
        request.kind = kind
        request.text = target
        request.path = folder
        return try? await client.request(request).permission
    }

    /// Stores an engine's API key in the Mac's Keychain, through the daemon,
    /// or removes it when key is empty. The key is never sent back.
    func setKey(_ key: String, provider: String = "anthropic") async -> String? {
        var request = DaemonRequest(.managerSetKey)
        request.text = key
        request.kind = provider == "gemini" ? "gemini" : nil
        guard let reply = try? await client.request(request) else { return "Couldn't reach am serve." }
        if reply.type == "error" { return reply.error }
        apply(reply)
        return nil
    }

    // MARK: - Cards

    /// Answers a card: the agent's question, or a reply to an agent waiting
    /// on the user. Returns the problem, if any.
    func reply(to card: ManagerCard, with reply: QuestionReply) async -> String? {
        var request = DaemonRequest(.managerCardReply)
        request.cardId = card.id
        if card.kind == "question" || card.kind == "key" {
            request.optionKey = reply.optionKey
            request.optionKeys = reply.optionKeys
            request.answerText = reply.text
        } else {
            request.text = reply.text
        }
        return await perform(request)
    }

    /// Saves a suggested note, as Agentman wrote it or as the user edited
    /// it. Returns the problem, if any.
    func saveNote(_ card: ManagerCard, as edited: Note? = nil) async -> String? {
        var request = DaemonRequest(.managerCardReply)
        request.cardId = card.id
        request.note = edited
        let problem = await perform(request)
        if problem == nil { await loadNotes() }
        return problem
    }

    /// Puts a card away unanswered.
    /// Takes a brief, an agent's word or a card out of the conversation, in
    /// every app. A card still asking is put away unanswered first.
    func close(_ entry: ManagerEntry) async -> String? {
        var request = DaemonRequest(.managerEntryClose)
        request.entryId = entry.id
        return await perform(request)
    }

    func dismiss(_ card: ManagerCard) async -> String? {
        var request = DaemonRequest(.managerCardDismiss)
        request.cardId = card.id
        return await perform(request)
    }

    private func perform(_ request: DaemonRequest) async -> String? {
        guard let reply = try? await client.request(request) else { return "Couldn't reach am serve." }
        if reply.type == "error" { return reply.error ?? "That didn't work." }
        apply(reply)
        return nil
    }

    // MARK: - Knowledge and keys

    func loadNotes() async {
        if let reply = try? await client.request(DaemonRequest(.knowledgeList)), let notes = reply.notes {
            self.notes = notes
        }
    }

    /// A note in full.
    func note(_ name: String, tier: String) async -> Note? {
        var request = DaemonRequest(.knowledgeRead)
        request.path = name
        request.kind = tier
        return try? await client.request(request).note
    }

    /// Saves a note. Returns it as saved, or the problem.
    func save(_ note: Note) async -> Result<Note, ManagerProblem> {
        var request = DaemonRequest(.knowledgeWrite)
        request.note = note
        guard let reply = try? await client.request(request) else { return .failure(ManagerProblem("Couldn't reach am serve.")) }
        guard reply.type != "error", let saved = reply.note else {
            return .failure(ManagerProblem(reply.error ?? "Couldn't save the note."))
        }
        await loadNotes()
        return .success(saved)
    }

    func delete(_ note: Note) async -> String? {
        var request = DaemonRequest(.knowledgeDelete)
        request.path = note.name
        request.kind = note.tier
        guard let reply = try? await client.request(request) else { return "Couldn't reach am serve." }
        if reply.type == "error" { return reply.error }
        if let notes = reply.notes { self.notes = notes }
        return nil
    }

    /// Cancels one of Agentman's reminders or watches.
    func cancel(_ schedule: ManagerSchedule) async -> String? {
        var request = DaemonRequest(.managerScheduleCancel)
        request.scheduleId = schedule.id
        guard let reply = try? await client.request(request) else { return "Couldn't reach am serve." }
        if reply.type == "error" { return reply.error }
        schedules = reply.schedules ?? []
        return nil
    }

    func loadKeys() async {
        if let reply = try? await client.request(DaemonRequest(.keysList)), let keys = reply.keys {
            self.keys = keys
        }
    }

    /// Saves a key's details, and its value when one is given. The value
    /// goes to the daemon, which keeps it in the Keychain; it never comes
    /// back.
    func save(_ key: Key, value: String) async -> String? {
        var request = DaemonRequest(.keySet)
        request.key = key
        request.text = value.isEmpty ? nil : value
        return await perform(request)
    }

    func delete(_ key: Key) async -> String? {
        var request = DaemonRequest(.keyDelete)
        request.path = key.name
        return await perform(request)
    }

    /// An image the user sent Agentman, kept with the conversation.
    func keptImage(_ name: String) async -> NSImage? {
        let key = "kept\u{0}" + name
        if let cached = images[key] { return cached }
        var request = DaemonRequest(.managerImage)
        request.path = name
        guard let reply = try? await client.request(request),
              let base64 = reply.workspace?.image,
              let data = Data(base64Encoded: base64),
              let image = NSImage(data: data) else { return nil }
        images[key] = image
        return image
    }

    /// An image an agent opened, for showing what Agentman looked at.
    func image(sessionId: String, path: String) async -> NSImage? {
        let key = sessionId + "\u{0}" + path
        if let cached = images[key] { return cached }
        var request = DaemonRequest(.readSeenFile, sessionId: sessionId)
        request.path = path
        guard let reply = try? await client.request(request),
              let base64 = reply.workspace?.image,
              let data = Data(base64Encoded: base64),
              let image = NSImage(data: data) else { return nil }
        images[key] = image
        return image
    }

    // MARK: - Events

    /// The entry with this ID, as it is now.
    func entry(_ id: String) -> ManagerEntry? {
        entries.last { $0.id == id }
    }

    func apply(_ event: DaemonEvent) {
        switch event.type {
        case "manager_entry":
            if let entry = event.entry { upsert(entry) }
        case "manager_delta":
            guard let id = event.entryId, let delta = event.delta,
                  let index = entries.firstIndex(where: { $0.id == id }) else { return }
            entries[index].text += delta
        case "manager_status":
            if let status = event.managerStatus {
                let finished = self.status.state == "thinking" && status.state != "thinking"
                self.status = status
                // One notice per reply, when it is done, with its last words.
                if finished, let reply = entries.last(where: { $0.role == "assistant" && !$0.text.isEmpty }),
                   reply.ts > lastRead {
                    onNews?(reply)
                }
            }
        case "manager_settings":
            if let settings = event.managerSettings { self.settings = settings }
            if let status = event.managerStatus { self.status = status }
        case "keys":
            if let keys = event.keys { self.keys = keys }
        case "manager_reset":
            // A new conversation, started here or on the phone.
            Task { await reload() }
        case "manager_schedules":
            schedules = event.schedules ?? []
        case "manager_brief":
            if let brief = event.brief { briefs[brief.sessionId] = brief }
            if let all = event.briefs {
                for brief in all { briefs[brief.sessionId] = brief }
            }
        default:
            break
        }
    }

    /// Adds an entry, or replaces the one with its ID: a reply streams in as
    /// a placeholder that fills with deltas, and its finished entry replaces
    /// it with the whole text.
    private func upsert(_ entry: ManagerEntry) {
        if let index = entries.firstIndex(where: { $0.id == entry.id }) {
            if entry.streaming == true, !entries[index].text.isEmpty { return }
            let before = entries[index]
            entries[index] = entry
            if let card = entry.card, before.card?.isOpen == true, !card.isOpen {
                onCardClosed?(card)
            } else if let card = entry.card, card.isOpen, before.card?.suggested != card.suggested {
                // Agentman marked its pick: the user has something to confirm.
                onNews?(entry)
            }
        } else {
            entries.append(entry)
            if entry.role == "brief" || entry.role == "agent" || entry.card?.isOpen == true {
                onNews?(entry)
            }
        }
    }
}

/// Something that went wrong, in words for the user.
struct ManagerProblem: Error, Sendable {
    let message: String
    init(_ message: String) { self.message = message }
}
