import AppKit
import Foundation
import Observation

/// What the main window shows.
enum SidebarItem: Hashable {
    case manager
    case newAgent
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
    /// The status the sidebar shows, when it is filtered to one.
    var stateFilter: SessionState?
    /// The folder the sidebar shows, with its subfolders, when filtered.
    var folderFilter: String? {
        didSet { if folderFilter != oldValue { Task { await loadPastSessions() } } }
    }
    /// Sessions recorded in the folder filtered to, running ones included.
    var pastSessions: [Session] = []

    /// The ones of them that have ended: those not running now.
    var earlierHere: [Session] {
        let running = Set(sessions.map(\.id))
        return pastSessions.filter { !running.contains($0.id) }
    }
    /// Folders agents have run in, newest first.
    var folders: [Folder] = []
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
    /// What happened while the user was away, for the summary on their
    /// return.
    let away = AwayLog()
    /// An image open full window, over everything.
    var lightbox: Lightbox?
    /// The session being renamed in the sidebar, if one is.
    var renaming: String?
    /// The command palette, over the main window.
    var showPalette = false
    /// Unsent messages, by session, kept across launches.
    private(set) var drafts: [String: String] = (UserDefaults.standard.dictionary(forKey: "drafts") as? [String: String]) ?? [:]

    /// Keeps a session's unsent message, or forgets it once empty.
    func saveDraft(_ text: String, for sessionId: String) {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty { drafts[sessionId] = nil } else { drafts[sessionId] = text }
        // A bounded few: drafts for sessions long gone are let go.
        if drafts.count > 50 {
            let live = Set(sessions.map(\.id))
            drafts = drafts.filter { live.contains($0.key) }
        }
        UserDefaults.standard.set(drafts, forKey: "drafts")
    }

    /// Agentman's titles for runs of tool calls, by the run's first and last
    /// call; empty when there is none to be had.
    var toolTitles: [String: String] = [:]

    let client: DaemonClient
    private var cursors: [String: String] = [:]
    /// The agents whose threads are showing, in the main window or an
    /// editor's panel: each one subscribed to once, however many show it.
    private var following: [String: Int] = [:]
    /// What the editors' agent panels show: agents by id, and Agentman.
    private var inPanels: [String: Int] = [:]
    private static let agentmanPanel = "agentman"
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
                cameBack()
                seeManagerIfShown()
            }
        }
        Task {
            for await _ in center.notifications(named: NSApplication.didResignActiveNotification) {
                appActive = false
                away.left()
            }
        }
        // The screen locked, or the Mac asleep, is away too, with the app in
        // front or not; unlocked with the app in front is back.
        let distributed = DistributedNotificationCenter.default()
        Task {
            for await _ in distributed.notifications(named: Notification.Name("com.apple.screenIsLocked")) { away.left() }
        }
        Task {
            for await _ in distributed.notifications(named: Notification.Name("com.apple.screenIsUnlocked")) {
                if NSApplication.shared.isActive { cameBack() }
            }
        }
        Task {
            for await _ in NSWorkspace.shared.notificationCenter.notifications(named: NSWorkspace.willSleepNotification) { away.left() }
        }
        // For screenshots and tests: the summary since some minutes ago.
        if let minutes = ProcessInfo.processInfo.environment["AGENTMAN_AWAY"].flatMap(Double.init) {
            Task {
                try? await Task.sleep(for: .seconds(4))
                showAway(since: Date().addingTimeInterval(-minutes * 60))
            }
        }
        client.start()
        Task { for await status in client.statuses { statusChanged(status) } }
        Task { for await event in client.events { apply(event) } }
    }

    // MARK: - Reading

    func session(_ id: String) -> Session? {
        sessions.first { $0.id == id } ?? pastSessions.first { $0.id == id }
    }

    var needsYou: [Session] { sessions.filter { $0.state == .waitingInput } }
    var working: [Session] { sessions.filter { $0.state == .busy } }
    var idle: [Session] { sessions.filter { $0.state == .idle } }

    /// Agents grouped by the folder they work in: the group with someone
    /// waiting on you first, then the most recently active.
    var groups: [AgentGroup] {
        let query = search.trimmingCharacters(in: .whitespaces)
        let shown = sessions.filter { session in
            (query.isEmpty || session.name.localizedCaseInsensitiveContains(query) || session.cwd.localizedCaseInsensitiveContains(query))
                && Self.inFolder(session.cwd, folderFilter)
                && (stateFilter == nil || Self.filterState(session) == stateFilter)
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
    /// or agentman://home (and agentman://home/conversation, as notifications
    /// about a reply or a card have it) for Agentman itself.
    func open(_ url: URL) {
        guard url.scheme == "agentman" else { return }
        switch url.host() {
        case "agent":
            let id = url.path().trimmingCharacters(in: CharacterSet(charactersIn: "/"))
            if let id = id.removingPercentEncoding, !id.isEmpty {
                selection = .agent(id)
            }
        case "new":
            selection = .newAgent
        case "home":
            selection = .manager
        default:
            break
        }
    }

    // MARK: - Acting

    /// Sends a message to an agent. Returns whether the daemon took it.
    @discardableResult
    func send(_ text: String, images: [InlineImage] = [], to sessionId: String) async -> Bool {
        var request = DaemonRequest(.sendMessage, sessionId: sessionId)
        request.text = text
        request.images = images.isEmpty ? nil : images
        request.clientId = "mac-send-\(UUID().uuidString)"
        let status = await performWithStatus(request, for: sessionId)
        if status == .queued {
            let note = images.count == 1 ? "1 image" : "\(images.count) images"
            queued[sessionId, default: []].append(images.isEmpty ? text : text.isEmpty ? note : "\(note) · \(text)")
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

    /// Asks Agentman for a plain line saying what a finished run of tool
    /// calls did and why, once per run.
    func titleTools(_ calls: [Message], said: String) async {
        guard calls.count > 1, let first = calls.first else { return }
        let key = Self.toolRunKey(calls)
        guard toolTitles[key] == nil else { return }
        toolTitles[key] = ""
        var request = DaemonRequest(.toolsTitle, sessionId: first.sessionId)
        request.text = said
        request.steps = calls.compactMap(\.tool)
        if let reply = try? await client.request(request), reply.type == "tools_title",
           let title = reply.preview, !title.isEmpty {
            toolTitles[key] = title
        } else {
            // Asked again when the run is next shown or the Mac reconnects.
            toolTitles[key] = nil
        }
    }

    nonisolated static func toolRunKey(_ calls: [Message]) -> String {
        "\(calls.first?.id ?? "")|\(calls.last?.id ?? "")|\(calls.count)"
    }

    // MARK: - Folders, filters and starting agents

    /// How many agents need the user, are working, and are idle.
    var stateCounts: (needsYou: Int, working: Int, idle: Int) {
        var counts = (needsYou: 0, working: 0, idle: 0)
        for session in sessions where Self.inFolder(session.cwd, folderFilter) {
            switch Self.filterState(session) {
            case .waitingInput: counts.needsYou += 1
            case .busy: counts.working += 1
            default: counts.idle += 1
            }
        }
        return counts
    }

    /// A session's status as the filters count it: waiting on the user
    /// whenever a question is up.
    nonisolated static func filterState(_ session: Session) -> SessionState {
        if session.question != nil || session.state == .waitingInput { return .waitingInput }
        return session.state == .busy ? .busy : .idle
    }

    nonisolated static func inFolder(_ cwd: String, _ folder: String?) -> Bool {
        guard let folder else { return true }
        return cwd == folder || cwd.hasPrefix(folder.hasSuffix("/") ? folder : folder + "/")
    }

    func loadFolders() async {
        if let reply = try? await client.request(DaemonRequest(.listFolders)), let folders = reply.folders {
            self.folders = folders.sorted { ($0.lastActivityAt ?? 0) > ($1.lastActivityAt ?? 0) }
        }
    }

    /// The ended sessions in the folder filtered to, read-only until resumed.
    func loadPastSessions() async {
        guard let folder = folderFilter else {
            pastSessions = []
            return
        }
        var request = DaemonRequest(.directorySessions)
        request.path = folder
        guard let reply = try? await client.request(request), reply.type != "error" else {
            pastSessions = []
            return
        }
        pastSessions = (reply.sessions ?? [])
            .sorted { ($0.lastActivityAt ?? 0) > ($1.lastActivityAt ?? 0) }
    }

    /// Starts an agent in a folder in the user's home, with a first message
    /// if there is one, and opens it once it shows. Returns the problem, if
    /// any.
    func startAgent(kind: String, folder: String, message: String) async -> String? {
        let home = NSHomeDirectory()
        guard folder == home || folder.hasPrefix(home + "/") else {
            return "Choose a folder inside your home folder."
        }
        var request = DaemonRequest(.startSession)
        request.kind = kind
        request.path = String(folder.dropFirst(home.count)).trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        request.text = message.trimmingCharacters(in: .whitespacesAndNewlines)
        request.clientId = "mac-start-\(UUID().uuidString)"
        do {
            let reply = try await client.request(request)
            guard reply.type != "error", let id = reply.sessionId else {
                return (reply.error ?? "It didn't start.").replacingOccurrences(of: "^daemon: ", with: "", options: .regularExpression)
            }
            await open(started: id)
            return nil
        } catch {
            return error.localizedDescription
        }
    }

    /// Opens a session that has just started, once the daemon lists it.
    private func open(started id: String) async {
        for _ in 0..<40 where sessions.first(where: { $0.id == id }) == nil {
            try? await Task.sleep(for: .milliseconds(250))
        }
        folderFilter = nil
        stateFilter = nil
        selection = .agent(id)
    }

    /// Reopens an ended session where it can be typed into. Returns the
    /// problem, if any.
    func resume(_ sessionId: String) async -> String? {
        var request = DaemonRequest(.resumeSession, sessionId: sessionId)
        request.clientId = "mac-resume-\(UUID().uuidString)"
        do {
            let reply = try await client.request(request)
            guard reply.type != "error", let id = reply.sessionId else {
                return (reply.error ?? "It couldn't be resumed.").replacingOccurrences(of: "^daemon: ", with: "", options: .regularExpression)
            }
            await open(started: id)
            return nil
        } catch {
            return error.localizedDescription
        }
    }

    /// Closes the pane a session runs in. Its conversation is kept, and it
    /// can be resumed. Returns the problem, if any.
    func end(_ sessionId: String) async -> String? {
        do {
            let reply = try await client.request(DaemonRequest(.endSession, sessionId: sessionId))
            if reply.type == "error" {
                return (reply.error ?? "It couldn't be ended.").replacingOccurrences(of: "^daemon: ", with: "", options: .regularExpression)
            }
            return nil
        } catch {
            return error.localizedDescription
        }
    }

    /// Switches a session's mode or model to one it offers. Returns the
    /// problem, if any, in words about the switch.
    func switchSession(_ sessionId: String, model: Bool, to value: String) async -> String? {
        var request = DaemonRequest(model ? .setModel : .setMode, sessionId: sessionId)
        request.text = value
        request.clientId = "mac-switch-\(UUID().uuidString)"
        let kind = model ? "model" : "mode"
        let label = model ? SessionLabels.model(value) : SessionLabels.mode(value)
        do {
            let reply = try await client.request(request)
            if reply.type == "error" || reply.status == .failed {
                let reason = (reply.error ?? "").replacingOccurrences(of: "^(daemon|source): ", with: "", options: .regularExpression)
                return "Couldn't switch the \(kind) to \(label)\(reason.isEmpty ? "." : ": " + reason)"
            }
            return nil
        } catch {
            return "Couldn't switch the \(kind) to \(label): \(error.localizedDescription)"
        }
    }

    /// Gives a session a name of the user's, or its own back when name is
    /// empty. Returns the problem, if any. Every app hears the new name.
    @discardableResult
    /// Whether an agent notifies for itself, rather than through Agentman.
    private func speaks(_ sessionId: String, as session: Session? = nil) -> Bool {
        let session = session ?? self.session(sessionId)
        return NotificationPolicy.agentSpeaks(agentmanOn: manager.on, asked: session?.alerts == true)
    }

    /// Turns an agent's own notifications on or off, on every device:
    /// while Agentman is on, only the agents asked for speak for themselves.
    func setAlerts(_ sessionId: String, _ on: Bool) async -> String? {
        var request = DaemonRequest(.setAgentAlerts, sessionId: sessionId)
        request.alerts = on
        do {
            let reply = try await client.request(request)
            if reply.type == "error" { return reply.error ?? "Couldn't change that." }
            apply(reply)
            return nil
        } catch {
            return error.localizedDescription
        }
    }

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
            for id in following.keys {
                follow(id)
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
        case "turn_complete":
            if let id = event.sessionId { away.record(.finished(event.preview), sessionId: id) }
            guard let id = event.sessionId, !looking(at: id), speaks(id),
                  NotificationPolicy.allows(.finished, folder: self.session(id)?.cwd) else { return }
            let name = self.session(id)?.name ?? event.sessionName ?? "An agent"
            notifier.agent(title: "\(name) finished", body: event.preview ?? "Its turn is done.",
                           sessionId: id, id: "finished-\(id)-\(Date().timeIntervalSince1970)")
        case "session_update":
            guard let session = event.session else { return }
            if let index = sessions.firstIndex(where: { $0.id == session.id }) {
                // Waiting on the user now, and not before: say so, unless
                // Agentman speaks for it with a card.
                let before = sessions[index]
                if session.question != nil, before.question?.id != session.question?.id {
                    away.record(.needsYou(session.question?.title), sessionId: session.id)
                }
                if session.question != nil, before.question?.id != session.question?.id,
                   speaks(session.id, as: session), !looking(at: session.id),
                   NotificationPolicy.allows(.needsYou, folder: session.cwd) {
                    notifier.agent(title: "\(session.name) needs you", body: session.question?.title ?? "It's waiting on you.",
                                   sessionId: session.id, id: "needs-\(session.id)-\(session.question?.id ?? "")")
                }
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
    var viewingManager: Bool { (selection == .manager || inPanels[Self.agentmanPanel] != nil) && appActive }

    /// News from Agentman: seen at once if the user is looking, a
    /// notification if not.
    private func news(_ entry: ManagerEntry) {
        if viewingManager {
            manager.markRead()
        } else {
            let agent = entry.sessionId.flatMap { session($0) }
            notifier.announce(entry, agentName: agent?.name, folder: agent?.cwd)
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
            unwatch(id)
        }
        if case .agent(let id) = selection {
            watch(id)
        }
    }

    /// Starts following an agent's conversation live, the first time any
    /// view shows it.
    func watch(_ sessionId: String) {
        following[sessionId, default: 0] += 1
        if following[sessionId] == 1 { follow(sessionId) }
    }

    /// Stops following it once no view shows it any more.
    func unwatch(_ sessionId: String) {
        guard let count = following[sessionId] else { return }
        if count > 1 {
            following[sessionId] = count - 1
        } else {
            following[sessionId] = nil
            client.send(DaemonRequest(.unsubscribe, sessionId: sessionId))
        }
    }

    /// An editor's agent panel shows an agent's thread, or Agentman with
    /// nil: what it shows counts as looked at, as in the main window.
    func showInPanel(_ sessionId: String?) {
        let key = sessionId ?? Self.agentmanPanel
        inPanels[key, default: 0] += 1
        if let sessionId { watch(sessionId) } else { seeManagerIfShown() }
    }

    func hideFromPanel(_ sessionId: String?) {
        let key = sessionId ?? Self.agentmanPanel
        if let count = inPanels[key] { inPanels[key] = count > 1 ? count - 1 : nil }
        if let sessionId { unwatch(sessionId) }
    }

    private func follow(_ sessionId: String) {
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
    private func merge(_ new: [Message], into sessionId: String) {
        let (current, added) = Self.merged(messages[sessionId] ?? [], with: new)
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

    // MARK: - Away

    /// Back at the Mac: the summary of what happened, when it was long
    /// enough and the user wants one.
    private func cameBack() {
        let defaults = UserDefaults.standard
        guard defaults.object(forKey: "awaySummary") as? Bool ?? true else {
            away.reset()
            return
        }
        let minutes = defaults.object(forKey: "awayMinutes") as? Double ?? 15
        away.returned(minimum: minutes * 60, sessions: sessions, briefs: manager.briefs, entries: manager.entries)
    }

    /// The summary since a time, on request.
    func showAway(since: Date) {
        away.show(since: since, sessions: sessions, briefs: manager.briefs, entries: manager.entries)
    }

    // MARK: - Helpers

    /// Whether the user is looking at that agent's thread right now.
    private func looking(at sessionId: String) -> Bool {
        appActive && (selection == .agent(sessionId) || inPanels[sessionId] != nil)
    }


    /// A conversation with new messages folded in, and those that were new.
    /// Live messages and history pages overlap, and both carry stable IDs. A
    /// later copy of a message replaces the earlier one: that is how a tool
    /// call that was running arrives finished. Never the other way: a history
    /// page read before the result was written must not set a finished call
    /// running again.
    nonisolated static func merged(_ current: [Message], with new: [Message]) -> (all: [Message], added: [Message]) {
        var all = current
        var index: [String: Int] = [:]
        for (i, message) in all.enumerated() { index[message.id] = i }
        var added: [Message] = []
        for message in new {
            if let i = index[message.id] {
                let finished = all[i].tool.map { $0.status != "running" } ?? false
                if finished, message.tool?.status == "running" { continue }
                all[i] = message
                if let j = added.firstIndex(where: { $0.id == message.id }) { added[j] = message }
            } else {
                index[message.id] = all.count
                all.append(message)
                added.append(message)
            }
        }
        all.sort { $0.ts < $1.ts }
        return (all, added)
    }

    nonisolated static func boardOrder(_ a: Session, _ b: Session) -> Bool {
        if a.state.rank != b.state.rank { return a.state.rank < b.state.rank }
        return (a.lastActivityAt ?? 0) > (b.lastActivityAt ?? 0)
    }

    nonisolated static func folderName(_ cwd: String) -> String {
        let name = (cwd as NSString).lastPathComponent
        return name.isEmpty ? cwd : name
    }
}
