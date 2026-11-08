import Foundation

/// The Board's columns, the agents sorted by what they want from the user:
/// answering, waiting, or nothing. Agents move between them by themselves,
/// as their states change.
nonisolated struct BoardSections: Equatable, Sendable {
    var needsYou: [Session] = []
    var working: [Session] = []
    var idle: [Session] = []
    /// Put away by the user, and left out of the columns.
    var hidden: [Session] = []

    /// Every card on the Board, by column: what moving cards animate on.
    var layout: [[String]] { [needsYou.map(\.id), working.map(\.id), idle.map(\.id)] }
    var shown: Int { needsYou.count + working.count + idle.count }

    /// Sorts the agents. `waiting` names those Agentman has put a card up
    /// about, which need the user whatever their state says. Needing the
    /// user beats being hidden or archived; an agent that has ended, or
    /// that the sidebar has archived, is not on the Board at all.
    static func make(_ sessions: [Session], choices: SidebarSections.Choices, hidden: Set<String>, waiting: Set<String> = [],
                     folder: String? = nil, now: Date = .now) -> BoardSections {
        var sections = BoardSections()
        for session in sessions where session.state != .ended && AgentsStore.inFolder(session.cwd, folder) {
            let state = AgentsStore.filterState(session)
            if state == .waitingInput || waiting.contains(session.id) {
                sections.needsYou.append(session)
            } else if SidebarSections.isArchived(session, choices: choices, now: now) {
                continue
            } else if hidden.contains(session.id) {
                sections.hidden.append(session)
            } else if state == .busy {
                sections.working.append(session)
            } else {
                sections.idle.append(session)
            }
        }
        let recent: (Session, Session) -> Bool = { ($0.lastActivityAt ?? 0, $0.id) > ($1.lastActivityAt ?? 0, $1.id) }
        // A working agent's last activity moves every second: by when it
        // started, its card stays put.
        let started: (Session, Session) -> Bool = { ($0.startedAt ?? 0, $0.id) > ($1.startedAt ?? 0, $1.id) }
        sections.needsYou.sort(by: recent)
        sections.working.sort(by: started)
        sections.idle.sort(by: recent)
        sections.hidden.sort(by: recent)
        return sections
    }

    /// The line across the top of the Board.
    func headline(connected: Bool) -> String {
        if !connected { return "Waiting to reach your agents." }
        let others = working.count + idle.count
        if !needsYou.isEmpty {
            let waiting = needsYou.count == 1 ? "1 agent is waiting on you." : "\(needsYou.count) agents are waiting on you."
            return others > 0 ? waiting + " The rest are fine." : waiting
        }
        if !working.isEmpty {
            return "Nothing needs you. " + (working.count == 1 ? "1 agent is working." : "\(working.count) agents are working.")
        }
        return idle.isEmpty && hidden.isEmpty ? "No agents running." : "All quiet. Nothing needs you."
    }

    /// The line under it: who is working, by name, and how many are idle.
    var detail: String {
        var parts: [String] = []
        if !working.isEmpty {
            let names = working.prefix(2).map(\.name)
            let more = working.count - names.count
            let who = more > 0 ? names.joined(separator: ", ") + " and \(more) more" : names.joined(separator: " and ")
            parts.append(who + (working.count == 1 ? " is working" : " are working"))
        }
        if !idle.isEmpty, !working.isEmpty || !needsYou.isEmpty {
            parts.append(idle.count == 1 ? "1 is idle" : "\(idle.count) are idle")
        }
        return parts.isEmpty ? "" : parts.joined(separator: ", ") + "."
    }

    /// The agents Agentman's latest answer is about: those it looked into
    /// and those it names.
    static func mentioned(in entries: [ManagerEntry], sessions: [Session]) -> Set<String> {
        let start = entries.lastIndex { $0.role == "user" }.map { $0 + 1 } ?? entries.startIndex
        let turn = entries[start...]
        var ids = Set(turn.compactMap { $0.role == "activity" ? $0.sessionId : nil })
        let said = turn.filter { $0.role == "assistant" }.map(\.text).joined(separator: "\n")
        for session in sessions where session.name.count >= 4 && said.localizedCaseInsensitiveContains(session.name) {
            ids.insert(session.id)
        }
        return ids
    }
}

/// What a card says about an agent, read from the end of its conversation:
/// the steps of the turn it is on, the command it ran last, how far through
/// its checklist it is, and what it last said.
struct BoardDigest: Equatable {
    struct Step: Equatable, Identifiable {
        enum State: Equatable { case done, running, failed }
        let id: String
        let text: String
        let state: State
    }

    struct Progress: Equatable {
        var done: Int
        var total: Int
        /// The item it is on.
        var now: String?
    }

    var steps: [Step] = []
    var command: String?
    var progress: Progress?
    /// What it said last: where it began, which sums up finished work, and
    /// where it ended, which is where a question comes.
    var said: String?
    var asked: String?
    var turnStarted: Date?

    /// How much of the conversation is read: the end is all a card shows,
    /// and this runs each time any agent says something.
    private static let window = 80
    private static let stepsShown = 3

    static func make(_ messages: [Message]) -> BoardDigest {
        var digest = BoardDigest()
        let tail = messages.suffix(window).filter { $0.isSidechain != true }
        let start = tail.lastIndex { $0.role == .user }
        digest.turnStarted = start.map { tail[$0].date }
        let turn = start.map { tail[($0 + 1)...] } ?? tail[...]

        let tools = turn.filter { $0.tool != nil && ToolKind($0.tool?.name ?? "") != .plan }
        digest.steps = tools.suffix(stepsShown).compactMap(step)
        digest.command = tools.last { ToolKind($0.tool?.name ?? "") == .command }?.tool?.summary
            .map(ToolKind.tidy).flatMap { $0.isEmpty ? nil : $0 }

        for message in tail.reversed() {
            if digest.progress == nil, ToolKind(message.tool?.name ?? "") == .plan, let text = message.text {
                digest.progress = checklist(text)
            }
            if digest.said == nil, message.role == .assistant, let text = message.text {
                let paragraphs = paragraphs(of: text)
                digest.said = paragraphs.first
                digest.asked = paragraphs.last
            }
            if digest.progress != nil, digest.said != nil { break }
        }
        return digest
    }

    private static func step(_ message: Message) -> Step? {
        guard let tool = message.tool else { return nil }
        let text = tool.title ?? tool.summary.map(ToolKind.tidy) ?? tool.name
        guard !text.isEmpty else { return nil }
        let state: Step.State = switch tool.status {
        case "running": .running
        case "error": .failed
        default: .done
        }
        return Step(id: message.id, text: text, state: state)
    }

    /// How far through a checklist, written one item a line, each behind a
    /// mark as the parsers write them. Nil for anything else: no bar is
    /// better than an invented one.
    static func checklist(_ text: String) -> Progress? {
        var progress = Progress(done: 0, total: 0)
        for raw in text.split(separator: "\n") {
            let line = raw.trimmingCharacters(in: .whitespaces)
            guard let first = line.first else { continue }
            let item = line.dropFirst().trimmingCharacters(in: .whitespaces)
            switch first {
            case "✔", "✓":
                progress.done += 1
                progress.total += 1
            case "◐":
                progress.total += 1
                if progress.now == nil { progress.now = item }
            case "○":
                progress.total += 1
            case "✕":
                continue
            default:
                return nil
            }
        }
        return progress.total >= 2 ? progress : nil
    }

    /// What it said, paragraph by paragraph, without code, headings, tables
    /// or a list's bullets.
    static func paragraphs(of text: String) -> [String] {
        var paragraphs: [String] = []
        var current: [String] = []
        var fenced = false
        func close() {
            if !current.isEmpty { paragraphs.append(current.joined(separator: " ")) }
            current = []
        }
        for raw in text.split(separator: "\n", omittingEmptySubsequences: false) {
            var line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("```") {
                fenced.toggle()
                close()
                continue
            }
            if fenced { continue }
            if line.isEmpty || line.hasPrefix("#") || line.hasPrefix("|") {
                close()
                continue
            }
            for bullet in ["- ", "* ", "> "] where line.hasPrefix(bullet) {
                line = String(line.dropFirst(bullet.count))
            }
            current.append(line)
        }
        close()
        return paragraphs
    }

    /// The first line of what it said that reads as a sentence, without
    /// code, headings or a list's bullet.
    static func firstLine(_ text: String) -> String? {
        var fenced = false
        for raw in text.split(separator: "\n") {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("```") {
                fenced.toggle()
                continue
            }
            if fenced || line.isEmpty || line.hasPrefix("#") || line.hasPrefix("|") { continue }
            for bullet in ["- ", "* ", "> "] where line.hasPrefix(bullet) {
                return String(line.dropFirst(bullet.count))
            }
            return line
        }
        return nil
    }
}
