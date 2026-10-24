import AppKit
import Foundation
import Observation

/// What the main window shows.
enum SidebarItem: Hashable {
    case manager
    case agent(String)
}

/// The agents working in one folder.
struct AgentGroup: Identifiable {
    let id: String
    let name: String
    let sessions: [Session]
}

/// Everything the app knows about the agents on this Mac, kept current from
/// the daemon's events.
@MainActor
@Observable
final class AgentsStore {
    private(set) var status: DaemonClient.Status = .connecting
    private(set) var sessions: [Session] = []
    private(set) var daemonInfo: DaemonInfo?
    private(set) var messages: [String: [Message]] = [:]
    private(set) var hasMore: [String: Bool] = [:]
    private(set) var loadingOlder: Set<String> = []
    /// The last thing that went wrong sending to or answering an agent.
    var actionError: [String: String] = [:]
    /// Messages the daemon queued for an agent it cannot type into: they
    /// reach it when its current turn ends. Shown until the agent's own copy
    /// of them arrives in its conversation.
    private(set) var queued: [String: [String]] = [:]
    var search = ""
    var selection: SidebarItem? = .manager {
        didSet { selectionChanged(from: oldValue) }
    }

    /// The conversation with Agentman, on the same connection.
    let manager: ManagerStore
    /// Agentman's notifications and the Dock badge.
    let notifier = Notifier()
    /// Whether the app is in front, so news can be shown rather than
    /// notified.
    private(set) var appActive = true
    private var resignedAt: Date?
    /// An image open full window, over everything.
    var lightbox: Lightbox?
    /// The session being renamed in the sidebar, if one is.
    var renaming: String?

    private let client: DaemonClient
    private var cursors: [String: String] = [:]
    private var followed: String?
    private var started = false

    init(client: DaemonClient = DaemonClient()) {
        self.client = client
        manager = ManagerStore(client: client)
        manager.onNews = { [weak self] entry in self?.news(entry) }
        manager.onCardClosed = { [weak self] card in
            self?.notifier.withdraw(card)
            self?.refreshBadge()
        }
        notifier.onOpen = { [weak self] url in self?.open(url) }
    }

    var socketPath: String { client.socketPath }

    func start() {
        guard !started else { return }
        started = true
        appActive = NSApplication.shared.isActive
        notifier.start()
        let center = NotificationCenter.default
        Task {
            for await _ in center.notifications(named: NSApplication.didBecomeActiveNotification) {
                appActive = true
                // Back after a while away: Agentman starts fresh on its entry
                // page, the conversation still above it.
                if let away = resignedAt, Date().timeIntervalSince(away) > 30 * 60, !manager.thinking {
                    manager.showEntry = true
                }
                seeManagerIfShown()
            }
        }
        Task {
            for await _ in center.notifications(named: NSApplication.didResignActiveNotification) {
                appActive = false
                resignedAt = Date()
            }
        }
        client.start()
        Task { for await status in client.statuses { statusChanged(status) } }
        Task { for await event in client.events { apply(event) } }
    }

    // MARK: - Reading

    func session(_ id: String) -> Session? {
        sessions.first { $0.id == id }
    }

    var needsYou: [Session] { sessions.filter { $0.state == .waitingInput } }
    var working: [Session] { sessions.filter { $0.state == .busy } }
    var idle: [Session] { sessions.filter { $0.state == .idle } }

    /// Agents grouped by the folder they work in: the group with someone
    /// waiting on you first, then the most recently active.
    var groups: [AgentGroup] {
        let query = search.trimmingCharacters(in: .whitespaces)
        let shown = query.isEmpty ? sessions : sessions.filter {
            $0.name.localizedCaseInsensitiveContains(query) || $0.cwd.localizedCaseInsensitiveContains(query)
        }
        return Dictionary(grouping: shown, by: \.cwd)
            .map { cwd, members in
                AgentGroup(id: cwd, name: Self.folderName(cwd), sessions: members.sorted(by: Self.boardOrder))
            }
            .sorted { a, b in
                let rankA = a.sessions.first?.state.rank ?? 9
                let rankB = b.sessions.first?.state.rank ?? 9
                if rankA != rankB { return rankA < rankB }
                return (a.sessions.first?.lastActivityAt ?? 0) > (b.sessions.first?.lastActivityAt ?? 0)
            }
    }

    /// Opens what an agentman:// link names: agentman://agent/<session id>,
    /// agentman://home for Agentman itself, or agentman://home/conversation
    /// for its conversation rather than its entry page, as a notification
    /// about a reply or a card does.
    func open(_ url: URL) {
        guard url.scheme == "agentman" else { return }
        switch url.host() {
        case "agent":
            let id = url.path().trimmingCharacters(in: CharacterSet(charactersIn: "/"))
            if let id = id.removingPercentEncoding, !id.isEmpty {
                selection = .agent(id)
            }
        case "home":
            selection = .manager
            if url.path().hasPrefix("/conversation") {
                manager.showEntry = false
            }
        default:
            break
        }
    }

    // MARK: - Acting

    /// Sends a message to an agent. Returns whether the daemon took it.
    @discardableResult
    func send(_ text: String, to sessionId: String) async -> Bool {
        var request = DaemonRequest(.sendMessage, sessionId: sessionId)
        request.text = text
        request.clientId = "mac-send-\(UUID().uuidString)"
        let status = await performWithStatus(request, for: sessionId)
        if status == .queued {
            queued[sessionId, default: []].append(text)
        }
        return status != .failed
    }

    /// Answers the question an agent is showing, with an option, a note, or both.
    @discardableResult
    func answer(_ sessionId: String, question: Question, optionKey: String?, text: String? = nil) async -> Bool {
        var request = DaemonRequest(.answerQuestion, sessionId: sessionId)
        request.questionId = question.id
        request.optionKey = optionKey
        request.answerText = text?.isEmpty == false ? text : nil
        request.clientId = "mac-answer-\(UUID().uuidString)"
        return await perform(request, for: sessionId)
    }

    /// Answers a question however the reply was given.
    @discardableResult
    func answer(_ sessionId: String, question: Question, reply: QuestionReply) async -> Bool {
        if let keys = reply.optionKeys, !keys.isEmpty {
            return await answerMultiple(sessionId, question: question, keys: keys)
        }
        return await answer(sessionId, question: question, optionKey: reply.optionKey, text: reply.text)
    }

    /// Answers a multiple-choice question with every option that is ticked.
    @discardableResult
    func answerMultiple(_ sessionId: String, question: Question, keys: [String]) async -> Bool {
        var request = DaemonRequest(.answerQuestion, sessionId: sessionId)
        request.questionId = question.id
        request.optionKeys = keys
        request.clientId = "mac-answer-\(UUID().uuidString)"
        return await perform(request, for: sessionId)
    }

    /// Gives a session a name of the user's, or its own back when name is
    /// empty. Returns the problem, if any. Every app hears the new name.
    @discardableResult
    func rename(_ sessionId: String, to name: String) async -> String? {
        var request = DaemonRequest(.renameSession, sessionId: sessionId)
        request.text = name.trimmingCharacters(in: .whitespacesAndNewlines)
        do {
            let reply = try await client.request(request)
            if reply.type == "error" { return reply.error ?? "Couldn't rename it." }
            apply(reply)
            return nil
        } catch {
            return error.localizedDescription
        }
    }

    /// Has the agent read the messages it is holding now, rather than when
    /// its current step ends: Ctrl-X Ctrl-S in Claude Code. Sends the whole
    /// queue, as the keys do.
    @discardableResult
    func sendQueuedNow(_ sessionId: String) async -> Bool {
        var request = DaemonRequest(.sendQueued, sessionId: sessionId)
        request.clientId = "mac-send-now-\(UUID().uuidString)"
        return await perform(request, for: sessionId)
    }

    func interrupt(_ sessionId: String) {
        client.send(DaemonRequest(.interrupt, sessionId: sessionId))
    }

    func loadOlder(_ sessionId: String) async {
        guard hasMore[sessionId] == true, let cursor = cursors[sessionId],
              !loadingOlder.contains(sessionId) else { return }
        loadingOlder.insert(sessionId)
        defer { loadingOlder.remove(sessionId) }
        var request = DaemonRequest(.fetchMessages, sessionId: sessionId)
        request.before = cursor
        request.limit = 40
        guard let page = try? await client.request(request).page else { return }
        merge(page.messages, into: sessionId)
        cursors[sessionId] = page.nextCursor
        hasMore[sessionId] = page.hasMore
    }

    // MARK: - Daemon events

    private func statusChanged(_ status: DaemonClient.Status) {
        self.status = status
        guard status == .connected else { return }
        Task {
            if let event = try? await client.request(DaemonRequest(.listSessions)) {
                apply(event)
            }
            await manager.reload()
            seeManagerIfShown()
            refreshBadge()
            // A reconnect starts the daemon's side afresh: follow again.
            if let followed {
                follow(followed)
            }
        }
    }

    private func apply(_ event: DaemonEvent) {
        if event.type.hasPrefix("manager_") {
            manager.apply(event)
            return
        }
        switch event.type {
        case "sessions":
            sessions = (event.sessions ?? []).sorted(by: Self.boardOrder)
            if let info = event.daemon {
                daemonInfo = info
            }
        case "session_update":
            guard let session = event.session else { return }
            if let index = sessions.firstIndex(where: { $0.id == session.id }) {
                sessions[index] = session
            } else {
                sessions.append(session)
            }
            sessions.sort(by: Self.boardOrder)
        case "session_gone":
            sessions.removeAll { $0.id == event.sessionId }
        case "messages":
            if let id = event.sessionId, let new = event.messages {
                merge(new, into: id)
            }
        default:
            break
        }
    }

    private func perform(_ request: DaemonRequest, for sessionId: String) async -> Bool {
        await performWithStatus(request, for: sessionId) != .failed
    }

    /// Sends an action and says how far it got: delivered, queued for the
    /// agent's next turn end, or failed, with the reason kept for display.
    private func performWithStatus(_ request: DaemonRequest, for sessionId: String) async -> SendStatus {
        actionError[sessionId] = nil
        do {
            let reply = try await client.request(request)
            if reply.type == "error" || reply.status == .failed {
                actionError[sessionId] = reply.error ?? "The agent didn't take that."
                return .failed
            }
            return reply.status ?? .delivered
        } catch {
            actionError[sessionId] = error.localizedDescription
            return .failed
        }
    }

    // MARK: - Following one agent

    // MARK: - Agentman's news

    /// Whether the user is looking at the conversation with Agentman.
    var viewingManager: Bool { selection == .manager && appActive }

    /// News from Agentman: seen at once if the user is looking, a
    /// notification if not.
    private func news(_ entry: ManagerEntry) {
        if viewingManager {
            manager.markRead()
        } else {
            notifier.announce(entry, agentName: entry.sessionId.flatMap { session($0)?.name })
        }
        refreshBadge()
    }

    private func seeManagerIfShown() {
        guard viewingManager else { return }
        manager.markRead()
        notifier.clearAll()
        refreshBadge()
    }

    private func refreshBadge() {
        notifier.setBadge(manager.unread.count)
    }

    private func selectionChanged(from old: SidebarItem?) {
        guard old != selection else { return }
        seeManagerIfShown()
        if case .agent(let id) = old {
            client.send(DaemonRequest(.unsubscribe, sessionId: id))
            followed = nil
        }
        if case .agent(let id) = selection {
            follow(id)
        }
    }

    private func follow(_ sessionId: String) {
        followed = sessionId
        client.send(DaemonRequest(.subscribe, sessionId: sessionId))
        Task {
            var request = DaemonRequest(.fetchMessages, sessionId: sessionId)
            request.limit = 40
            guard let page = try? await client.request(request).page else { return }
            merge(page.messages, into: sessionId)
            cursors[sessionId] = page.nextCursor
            hasMore[sessionId] = page.hasMore
        }
    }

    /// Adds messages to an agent's conversation, once each, in time order.
    /// Live messages and history pages overlap, and both carry stable IDs.
    private func merge(_ new: [Message], into sessionId: String) {
        var current = messages[sessionId] ?? []
        let known = Set(current.map(\.id))
        let added = new.filter { !known.contains($0.id) }
        current.append(contentsOf: added)
        current.sort { $0.ts < $1.ts }
        messages[sessionId] = current
        // A queued message that has reached the agent shows as its own.
        if var waiting = queued[sessionId], !waiting.isEmpty {
            for message in added where message.role == .user {
                let text = (message.text ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
                if let index = waiting.firstIndex(where: { text.contains($0.trimmingCharacters(in: .whitespacesAndNewlines)) }) {
                    waiting.remove(at: index)
                }
            }
            queued[sessionId] = waiting
        }
    }

    // MARK: - Helpers

    nonisolated static func boardOrder(_ a: Session, _ b: Session) -> Bool {
        if a.state.rank != b.state.rank { return a.state.rank < b.state.rank }
        return (a.lastActivityAt ?? 0) > (b.lastActivityAt ?? 0)
    }

    nonisolated static func folderName(_ cwd: String) -> String {
        let name = (cwd as NSString).lastPathComponent
        return name.isEmpty ? cwd : name
    }
}
