import AppKit
import Foundation
import Observation

/// What the main window shows.
enum SidebarItem: Hashable {
    case manager
    case newAgent
    case usage
    case agent(String)
}

/// Everything the app knows about the agents on this Mac, kept current from
/// the daemon's events.
@MainActor
@Observable
final class AgentsStore {
    private(set) var status: DaemonClient.Status = .connecting
    private(set) var sessions: [Session] = []
    /// Whether the daemon has listed its sessions yet: until then, an empty
    /// list says nothing about which agents are running.
    private(set) var sessionsLoaded = false
    private(set) var daemonInfo: DaemonInfo?
    /// The phones and tablets that have reached this Mac; nil until the
    /// daemon has said.
    var devices: [Device]?
    /// Set to open the Devices page straight into connecting one.
    var pairingRequested = false
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
    /// What the New Agent page opens on, when something sends the user there
    /// to start one in a particular place.
    var newAgentPreset: NewAgentPreset?
    /// The command palette, over the main window.
    var showPalette = false
    /// Unsent messages, by session, kept across launches.
    private(set) var drafts: [String: String] = (UserDefaults.standard.dictionary(forKey: "drafts") as? [String: String]) ?? [:]
    @ObservationIgnored private var draftWrite: Task<Void, Never>?

    /// Keeps a session's unsent message, or forgets it once empty.
    func saveDraft(_ text: String, for sessionId: String) {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty { drafts[sessionId] = nil } else { drafts[sessionId] = text }
        // A bounded few: drafts for sessions long gone are let go.
        if drafts.count > 50 {
            let live = Set(sessions.map(\.id))
            drafts = drafts.filter { live.contains($0.key) }
        }
        // Written once typing pauses, and at quit: a write wakes every view
        // that keeps a setting, and at each key that redrew the panels
        // beside the box.
        draftWrite?.cancel()
        draftWrite = Task { [weak self] in
            try? await Task.sleep(for: .seconds(0.8))
            guard !Task.isCancelled else { return }
            self?.writeDrafts()
        }
    }

    func writeDrafts() {
        UserDefaults.standard.set(drafts, forKey: "drafts")
    }

    /// Agentman's titles for runs of tool calls, by the run's first and last
    /// call: each run's its own, so a title arriving redraws that run alone.
    /// One dictionary every run read redrew them all at each title, and a
    /// run scrolled into view asks for its title.
    @ObservationIgnored private var toolTitles: [String: ToolRunTitle] = [:]
    /// Runs whose title has been asked for and not answered yet, and when a
    /// run's asking last failed: asked again a minute later, not at every
    /// scroll past it.
    @ObservationIgnored private var titling: Set<String> = []
    @ObservationIgnored private var titleFailed: [String: Date] = [:]
    /// Each conversation's rows, made again only when its messages change:
    /// not at every redraw while it scrolls.
    @ObservationIgnored private var itemCache: [String: (messages: [Message], items: [ThreadItem])] = [:]

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
        center.addObserver(forName: NSApplication.willTerminateNotification, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.writeDrafts() }
        }
        Task {
            for await _ in center.notifications(named: NSApplication.didBecomeActiveNotification) {
                appActive = true
                cameBack()
                seeManagerIfShown()
                tellOnScreen()
            }
        }
        Task {
            for await _ in center.notifications(named: NSApplication.didResignActiveNotification) {
                appActive = false
                away.left()
                tellOnScreen()
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

    /// The conversation that took over from this one in its terminal, if one
    /// did (/clear, /resume).
    func successor(of id: String) -> String? {
        sessions.first { $0.previous == id }?.id
    }

    /// The window follows a conversation that went on as another.
    private func followTakeovers() {
        if case .agent(let id) = selection, let next = successor(of: id) {
            selection = .agent(next)
        }
    }

    var needsYou: [Session] { sessions.filter { $0.state == .waitingInput } }
    var working: [Session] { sessions.filter { $0.state == .busy } }
    var idle: [Session] { sessions.filter { $0.state == .idle } }

    /// The sidebar's agents in sections: pinned, active, working, archived.
    /// See SidebarSections. Search and the filters apply first.
    var sidebar: SidebarSections {
        let query = search.trimmingCharacters(in: .whitespaces)
        let shown = sessions.filter { session in
            (query.isEmpty || session.name.localizedCaseInsensitiveContains(query) || session.cwd.localizedCaseInsensitiveContains(query)
                || (branches[session.cwd]?.localizedCaseInsensitiveContains(query) ?? false))
                && Self.inFolder(session.cwd, folderFilter)
                && (stateFilter == nil || Self.filterState(session) == stateFilter)
        }
        return SidebarSections.make(shown, choices: sidebarChoices)
    }

    /// What the user chose for the sidebar: pins, and agents archived or
    /// brought back by hand. Kept across launches.
    private(set) var sidebarChoices = AgentsStore.loadSidebarChoices() {
        didSet { saveSidebarChoices() }
    }

    func pin(_ id: String) {
        sidebarChoices.pinned.removeAll { $0 == id }
        sidebarChoices.pinned.append(id)
        // A pinned agent is live work again.
        sidebarChoices.archived[id] = nil
        sidebarChoices.unarchived[id] = Date()
    }

    func unpin(_ id: String) {
        sidebarChoices.pinned.removeAll { $0 == id }
    }

    /// Done with: out of the active list, its pin with it. The conversation
    /// stays, and comes back if the agent works or asks again.
    func archive(_ id: String) {
        sidebarChoices.pinned.removeAll { $0 == id }
        sidebarChoices.unarchived[id] = nil
        sidebarChoices.archived[id] = Date()
    }

    /// Back to active work, and kept there until it next does something.
    func unarchive(_ id: String) {
        sidebarChoices.archived[id] = nil
        sidebarChoices.unarchived[id] = Date()
    }

    func isPinned(_ id: String) -> Bool { sidebarChoices.pinned.contains(id) }

    func isArchived(_ session: Session) -> Bool {
        SidebarSections.isArchived(session, choices: sidebarChoices, now: .now)
    }

    private static let choicesKey = "sidebarChoices"

    private static func loadSidebarChoices() -> SidebarSections.Choices {
        let saved = UserDefaults.standard.dictionary(forKey: choicesKey) ?? [:]
        func dates(_ key: String) -> [String: Date] {
            ((saved[key] as? [String: Double]) ?? [:]).mapValues { Date(timeIntervalSince1970: $0) }
        }
        return SidebarSections.Choices(pinned: saved["pinned"] as? [String] ?? [], archived: dates("archived"), unarchived: dates("unarchived"))
    }

    private func saveSidebarChoices() {
        // A bounded few: choices for agents long gone are let go.
        let live = Set(sessions.map(\.id))
        func trim(_ dates: [String: Date]) -> [String: Double] {
            let kept = dates.count > 200 ? dates.filter { live.contains($0.key) } : dates
            return kept.mapValues(\.timeIntervalSince1970)
        }
        UserDefaults.standard.set([
            "pinned": sidebarChoices.pinned,
            "archived": trim(sidebarChoices.archived),
            "unarchived": trim(sidebarChoices.unarchived),
        ], forKey: Self.choicesKey)
    }

    /// The branch checked out in each folder an agent works in, as T3 Code
    /// shows it under a thread's title. Read every few seconds while the
    /// sidebar shows; folders that aren't repositories have none.
    private(set) var branches: [String: String] = [:]

    func readBranches() async {
        let folders = Set(sessions.filter { $0.state != .ended }.map(\.cwd))
        var read: [String: String] = [:]
        for folder in folders {
            // Empty on a detached HEAD; a new repository's branch, before
            // its first commit, is named all the same.
            guard let branch = await Git.run(["branch", "--show-current"], in: URL(fileURLWithPath: folder))?
                .trimmingCharacters(in: .whitespacesAndNewlines), !branch.isEmpty else { continue }
            read[folder] = branch
        }
        if read != branches { branches = read }
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
        case "usage":
            selection = .usage
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

    /// A run of tool calls' title, which its row alone observes.
    func toolTitle(_ calls: [Message]) -> ToolRunTitle {
        let key = Self.toolRunKey(calls)
        if let title = toolTitles[key] { return title }
        let title = ToolRunTitle()
        toolTitles[key] = title
        return title
    }

    /// Asks Agentman for a plain line saying what a finished run of tool
    /// calls did and why, once per run.
    func titleTools(_ calls: [Message], said: String) async {
        guard calls.count > 1, let first = calls.first else { return }
        let key = Self.toolRunKey(calls)
        let title = toolTitle(calls)
        guard title.text == nil, !titling.contains(key) else { return }
        if let failed = titleFailed[key], Date().timeIntervalSince(failed) < 60 { return }
        titling.insert(key)
        defer { titling.remove(key) }
        var request = DaemonRequest(.toolsTitle, sessionId: first.sessionId)
        request.text = said
        request.steps = calls.compactMap(\.tool)
        if let reply = try? await client.request(request), reply.type == "tools_title",
           let line = reply.preview, !line.isEmpty {
            title.text = line
            titleFailed[key] = nil
        } else {
            titleFailed[key] = Date()
        }
    }

    /// A conversation's rows, from its messages.
    func threadItems(_ sessionId: String) -> [ThreadItem] {
        let messages = self.messages[sessionId] ?? []
        // Equal at once while the messages are the very same array.
        if let cached = itemCache[sessionId], cached.messages == messages { return cached.items }
        let items = ThreadItem.items(from: messages)
        itemCache[sessionId] = (messages, items)
        return items
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
    // MARK: - Voice

    /// What a recording says, written out by Celery through the daemon. The
    /// recording goes in slices the relay would carry from a phone too.
    func transcribe(_ wav: Data) async -> Result<String, VoiceProblem> {
        let encoded = wav.base64EncodedString()
        let size = 96_000
        let parts = max(1, (encoded.count + size - 1) / size)
        let id = UUID().uuidString
        var start = encoded.startIndex
        for part in 0..<parts {
            let end = encoded.index(start, offsetBy: size, limitedBy: encoded.endIndex) ?? encoded.endIndex
            var request = DaemonRequest(.transcribe)
            request.voice = VoiceClip(id: id, part: part, parts: parts, mime: "audio/wav", data: String(encoded[start..<end]))
            start = end
            do {
                // The last part waits for Celery.
                let reply = try await client.request(request, timeout: part == parts - 1 ? 150 : 20)
                if reply.type == "error" {
                    return .failure(VoiceProblem(message: Self.daemonMessage(reply.error ?? "That couldn't be written out.")))
                }
                if let transcript = reply.transcript, transcript.done {
                    return .success(transcript.text ?? "")
                }
            } catch {
                return .failure(VoiceProblem(message: error.localizedDescription))
            }
        }
        return .failure(VoiceProblem(message: "The recording didn't come back written out. Try again."))
    }

    /// Agentman's Celery account, or nil when the daemon can't say.
    func voiceAccount(_ type: RequestType = .voiceAccount, _ step: VoiceSignIn? = nil) async -> Result<VoiceAccount, VoiceProblem> {
        var request = DaemonRequest(type)
        request.voiceSignIn = step
        do {
            let reply = try await client.request(request, timeout: 40)
            if let account = reply.voiceAccount { return .success(account) }
            return .failure(VoiceProblem(message: Self.daemonMessage(reply.error ?? "Celery didn't answer.")))
        } catch {
            return .failure(VoiceProblem(message: error.localizedDescription))
        }
    }

    private static func daemonMessage(_ text: String) -> String {
        text.replacingOccurrences(of: "^daemon: ", with: "", options: .regularExpression)
    }

    /// How to show an agent's live terminal, or why it can't be.
    func terminal(for sessionId: String) async -> Result<SessionTerminal, TerminalProblem> {
        do {
            let reply = try await client.request(DaemonRequest(.sessionTerminal, sessionId: sessionId))
            if let terminal = reply.terminal { return .success(terminal) }
            let problem = reply.error ?? "Its terminal can't be shown."
            return .failure(TerminalProblem(message: problem.replacingOccurrences(of: "^daemon: ", with: "", options: .regularExpression)))
        } catch {
            return .failure(TerminalProblem(message: error.localizedDescription))
        }
    }

    /// A session's "/" menu: what the agent would offer at its own prompt.
    func commands(for sessionId: String) async -> Result<[SlashCommand], TerminalProblem> {
        do {
            let reply = try await client.request(DaemonRequest(.listCommands, sessionId: sessionId))
            if let commands = reply.commands { return .success(commands) }
            return .failure(TerminalProblem(message: Self.plainProblem(reply.error, or: "The commands couldn't be listed.")))
        } catch {
            return .failure(TerminalProblem(message: error.localizedDescription))
        }
    }

    /// Runs a panel command (/usage, /status) in the agent's terminal and
    /// answers with what the panel showed. /usage takes a few seconds to
    /// settle.
    func commandPanel(_ name: String, in sessionId: String) async -> Result<CommandPanel, TerminalProblem> {
        var request = DaemonRequest(.commandPanel, sessionId: sessionId)
        request.text = "/" + name
        do {
            let reply = try await client.request(request, timeout: 40)
            if let panel = reply.panel { return .success(panel) }
            return .failure(TerminalProblem(message: Self.plainProblem(reply.error, or: "The panel couldn't be read.")))
        } catch {
            return .failure(TerminalProblem(message: error.localizedDescription))
        }
    }

    /// Has the daemon keep a file for an agent to read, under a name it can
    /// be given by, and answers with where: for a file whose own path can't
    /// follow an "@" as one word.
    func keepFile(_ url: URL, for sessionId: String) async -> Result<String, TerminalProblem> {
        guard let data = try? Data(contentsOf: url) else {
            return .failure(TerminalProblem(message: "\(url.lastPathComponent) couldn't be read."))
        }
        let base64 = data.base64EncodedString()
        let size = 96_000
        let parts = max(1, (base64.count + size - 1) / size)
        let id = "mac-" + UUID().uuidString.prefix(8)
        let characters = Array(base64.utf8)
        for part in 0..<parts {
            let slice = characters[min(part * size, characters.count)..<min((part + 1) * size, characters.count)]
            var request = DaemonRequest(.attachFile, sessionId: sessionId)
            request.file = FilePart(id: String(id), part: part, parts: parts, name: url.lastPathComponent,
                                    data: String(decoding: slice, as: UTF8.self))
            do {
                let reply = try await client.request(request, timeout: 60)
                if let attached = reply.attached {
                    if attached.done, let path = attached.path { return .success(path) }
                } else {
                    return .failure(TerminalProblem(message: Self.plainProblem(reply.error, or: "\(url.lastPathComponent) couldn't be sent.")))
                }
            } catch {
                return .failure(TerminalProblem(message: error.localizedDescription))
            }
        }
        return .failure(TerminalProblem(message: "\(url.lastPathComponent) didn't reach the daemon."))
    }

    /// The daemon's words without the package that raised them.
    static func plainProblem(_ error: String?, or fallback: String) -> String {
        guard let error, !error.isEmpty else { return fallback }
        return error.replacingOccurrences(of: "^(daemon|source|tmux): ", with: "", options: .regularExpression)
    }

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
            // A reconnect starts the daemon's side afresh: follow again,
            // and say again what is on screen.
            for id in following.keys {
                follow(id)
            }
            tellOnScreen(again: true)
        }
    }

    /// Internal rather than private so tests can feed it events.
    func apply(_ event: DaemonEvent) {
        if event.type.hasPrefix("manager_") {
            manager.apply(event)
            return
        }
        switch event.type {
        case "sessions":
            sessions = (event.sessions ?? []).sorted(by: Self.boardOrder)
            sessionsLoaded = true
            if let info = event.daemon {
                daemonInfo = info
            }
            followTakeovers()
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
            followTakeovers()
        case "session_gone":
            sessions.removeAll { $0.id == event.sessionId }
        case "devices":
            devices = event.devices ?? []
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
        } else if let id = entry.sessionId, looking(at: id) {
            // About the agent in front of the user: nothing to tell.
        } else if appActive, Self.toasts(entry) {
            showToast(entry)
        } else {
            let agent = entry.sessionId.flatMap { session($0) }
            notifier.announce(entry, agentName: agent?.name, folder: agent?.cwd)
        }
        refreshBadge()
    }

    // MARK: - Toasts

    /// One piece of Agentman's news in the window's corner; see
    /// AgentmanToasts. Its ID is the entry's.
    struct Toast: Identifiable, Equatable {
        let id: String
        let sessionId: String?
        let card: Bool
    }

    /// Newest last, three at most.
    private(set) var toasts: [Toast] = []
    static let maxToasts = 3

    /// What becomes a toast while the app is in front: an agent waiting on
    /// the user, its news, word from it, and Agentman's reply. A note or a
    /// key Agentman asks about stays a notification.
    nonisolated static func toasts(_ entry: ManagerEntry) -> Bool {
        if let card = entry.card {
            return card.isOpen && (card.kind == "question" || card.kind == "needs_you")
        }
        return entry.role == "brief" || entry.role == "agent" || entry.role == "assistant"
    }

    func showToast(_ entry: ManagerEntry) {
        toasts.removeAll { $0.id == entry.id }
        toasts.append(Toast(id: entry.id, sessionId: entry.sessionId, card: entry.card != nil))
        if toasts.count > Self.maxToasts {
            // News goes before a card that still waits on the user.
            if let index = toasts.firstIndex(where: { !$0.card }) {
                toasts.remove(at: index)
            } else {
                toasts.removeFirst()
            }
        }
    }

    func dismissToast(_ id: String) {
        toasts.removeAll { $0.id == id }
    }

    private func seeManagerIfShown() {
        guard viewingManager else { return }
        // Agentman's conversation shows all of it.
        toasts.removeAll()
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
        tellOnScreen()
        // Opened, an agent's own thread says what its toasts did.
        if case .agent(let id) = selection {
            toasts.removeAll { $0.sessionId == id }
        }
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
        tellOnScreen()
    }

    func hideFromPanel(_ sessionId: String?) {
        let key = sessionId ?? Self.agentmanPanel
        if let count = inPanels[key] { inPanels[key] = count > 1 ? count - 1 : nil }
        if let sessionId { unwatch(sessionId) }
        tellOnScreen()
    }

    private func follow(_ sessionId: String) {
        client.send(DaemonRequest(.subscribe, sessionId: sessionId))
        // Followed again after a reconnect, the thread already shows what came
        // before it: the newest page has to reach back to that, or what came
        // in between would be missing until it was scrolled to.
        let since = messages[sessionId]?.map(\.ts).max()
        Task {
            var request = DaemonRequest(.fetchMessages, sessionId: sessionId)
            request.limit = 40
            guard var page = try? await client.request(request).page else { return }
            merge(page.messages, into: sessionId)
            var pages = 1
            while let since, Self.needsAnotherPage(page.messages, hasMore: page.hasMore, since: since, pages: pages),
                  let cursor = page.nextCursor {
                var older = DaemonRequest(.fetchMessages, sessionId: sessionId)
                older.before = cursor
                older.limit = 40
                guard let next = try? await client.request(older).page else { break }
                merge(next.messages, into: sessionId)
                page = next
                pages += 1
            }
            cursors[sessionId] = page.nextCursor
            hasMore[sessionId] = page.hasMore
        }
    }

    /// Pages a catch-up reads at most: a long absence stops there, and the
    /// rest is still there to scroll to. As the phone's (mobile/lib/catchup.ts).
    nonisolated static let catchUpPages = 4

    /// Whether a catch-up needs the page before this one: its oldest message
    /// is still newer than the newest that was on screen, so the two do not
    /// meet yet.
    nonisolated static func needsAnotherPage(_ page: [Message], hasMore: Bool, since: Int64, pages: Int) -> Bool {
        guard hasMore, pages < catchUpPages, let oldest = page.map(\.ts).min() else { return false }
        return oldest > since
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

    // MARK: - What is on screen

    /// The agents whose threads are in front of the user: the one selected
    /// and those in editors' panels, while the app is in front.
    var onScreen: [String] {
        guard appActive else { return [] }
        var shown = Set(inPanels.keys.filter { $0 != Self.agentmanPanel })
        if case .agent(let id) = selection { shown.insert(id) }
        return shown.sorted()
    }

    /// This app, to the daemon, for as long as it runs.
    private let viewerId = "mac-" + UUID().uuidString
    private var toldOnScreen: [String] = []
    private var onScreenRenewal: Task<Void, Never>?

    /// Tells Agentman what is on screen when it changes, so an agent the
    /// user is looking at gets no card and no push. Agentman forgets after
    /// two minutes unless told again, so it is said again every minute while
    /// anything is shown.
    func tellOnScreen(again: Bool = false) {
        let shown = onScreen
        guard again || shown != toldOnScreen else { return }
        toldOnScreen = shown
        var request = DaemonRequest(.managerViewing)
        request.clientId = viewerId
        request.viewing = shown
        client.send(request)
        onScreenRenewal?.cancel()
        onScreenRenewal = nil
        guard !shown.isEmpty else { return }
        onScreenRenewal = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(60))
                guard !Task.isCancelled else { return }
                self?.tellOnScreen(again: true)
            }
        }
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

/// Why an agent's terminal can't be shown.
struct TerminalProblem: Error {
    let message: String
}

/// Why a voice prompt or a Celery sign-in didn't work, in words.
struct VoiceProblem: Error {
    let message: String
}

/// Restarting am serve, the login service the agents' daemon runs as, as
/// `am service restart` does. Agents keep running, in their own tmux server;
/// the app reconnects on its own.
@MainActor
enum DaemonRestart {
    static let label = "dev.agentman.serve"

    static func confirm() {
        let alert = NSAlert()
        alert.messageText = "Restart the daemon?"
        alert.informativeText = "Agentman's daemon starts again in a few seconds. Your agents keep running; a message still being sent may need sending again."
        alert.addButton(withTitle: "Restart")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        Task {
            if let problem = await restart() {
                let failed = NSAlert()
                failed.messageText = "The daemon couldn't be restarted"
                failed.informativeText = problem
                failed.runModal()
            }
        }
    }

    /// Kicks the service; answers why it couldn't.
    static func restart(service: String = label) async -> String? {
        await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                let process = Process()
                process.executableURL = URL(fileURLWithPath: "/bin/launchctl")
                process.arguments = ["kickstart", "-k", "gui/\(getuid())/\(service)"]
                process.standardOutput = FileHandle.nullDevice
                process.standardError = FileHandle.nullDevice
                do {
                    try process.run()
                    process.waitUntilExit()
                } catch {
                    continuation.resume(returning: error.localizedDescription)
                    return
                }
                continuation.resume(returning: process.terminationStatus == 0 ? nil
                    : "It isn't running as a service on this Mac. Run `am service restart` in a terminal, or stop am serve where it runs and start it again.")
            }
        }
    }
}

/// Agentman's line for one run of tool calls, once it has one.
@MainActor @Observable
final class ToolRunTitle {
    var text: String?
}

