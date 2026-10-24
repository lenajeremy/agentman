import Foundation
import Observation

/// What happened while the user was away from the Mac, for the summary
/// they get on their return: away is the app in the background, the screen
/// locked or the Mac asleep, for long enough.
@MainActor @Observable
final class AwayLog {
    struct Event {
        enum Kind { case finished(String?), needsYou(String?) }
        let sessionId: String
        let kind: Kind
        let at: Date
    }

    private(set) var events: [Event] = []
    private(set) var leftAt: Date?
    /// The summary showing now, if any.
    var digest: AwayDigest?
    /// The last one built, to see again later.
    private(set) var last: AwayDigest?

    func record(_ kind: Event.Kind, sessionId: String, at date: Date = Date()) {
        events.append(Event(sessionId: sessionId, kind: kind, at: date))
        // A day of it, at most, and never without end.
        let cutoff = date.addingTimeInterval(-24 * 3600)
        if events.count > 3000 || events.first.map({ $0.at < cutoff }) == true {
            events.removeAll { $0.at < cutoff }
            if events.count > 3000 { events.removeFirst(events.count - 3000) }
        }
    }

    func left(at date: Date = Date()) {
        if leftAt == nil { leftAt = date }
    }

    /// Back, without a summary.
    func reset() {
        leftAt = nil
    }

    /// Back: a summary, when the user was away for at least minimum and
    /// something happened meanwhile.
    func returned(at date: Date = Date(), minimum: TimeInterval, sessions: [Session],
                  briefs: [String: Brief], entries: [ManagerEntry]) {
        defer { leftAt = nil }
        guard let since = leftAt, date.timeIntervalSince(since) >= minimum else { return }
        let built = AwayDigest.make(since: since, until: date, events: events, sessions: sessions, briefs: briefs, entries: entries)
        guard !built.agents.isEmpty else { return }
        digest = built
        last = built
    }

    /// Shows the last summary again, or one since a given time.
    func show(since: Date, sessions: [Session], briefs: [String: Brief], entries: [ManagerEntry]) {
        let built = AwayDigest.make(since: since, until: Date(), events: events, sessions: sessions, briefs: briefs, entries: entries)
        digest = built
        last = built
    }
}

/// The summary itself: each agent that did something, in a line.
struct AwayDigest: Identifiable {
    struct Agent: Identifiable {
        let id: String
        let name: String
        let kind: String
        let folder: String
        /// Turns it finished while the user was away.
        let finished: Int
        /// Requests Agentman approved for it meanwhile.
        let approved: Int
        /// What it did: Agentman's brief when it has a fresh one, or what it
        /// said last.
        let summary: String?
        /// What it waits on the user for, now.
        let waiting: String?
    }

    let id = UUID()
    let since: Date
    let until: Date
    let agents: [Agent]

    var waitingCount: Int { agents.filter { $0.waiting != nil }.count }
    var approvedCount: Int { agents.reduce(0) { $0 + $1.approved } }

    static func make(since: Date, until: Date, events: [AwayLog.Event], sessions: [Session],
                     briefs: [String: Brief], entries: [ManagerEntry]) -> AwayDigest {
        let window = events.filter { $0.at >= since && $0.at <= until }
        let sinceMillis = Int64(since.timeIntervalSince1970 * 1000)
        let approvals = entries.filter { $0.role == "activity" && $0.tool == "approve" && $0.ts >= sinceMillis }
        var agents: [Agent] = []
        for session in sessions {
            let own = window.filter { $0.sessionId == session.id }
            let approved = approvals.filter { $0.sessionId == session.id }.count
            let active = (session.lastActivityAt ?? 0) >= sinceMillis
            let waitingSince = own.contains { if case .needsYou = $0.kind { true } else { false } }
            let waiting = session.state == .waitingInput && (active || waitingSince)
                ? (session.question?.title.flatMap { $0.isEmpty ? nil : $0 } ?? "Waiting on you")
                : nil
            guard active || !own.isEmpty || approved > 0 else { continue }
            let finished = own.filter { if case .finished = $0.kind { true } else { false } }.count
            var summary: String?
            if let brief = briefs[session.id], brief.updatedAt >= sinceMillis, !brief.summary.isEmpty {
                summary = brief.summary
            } else if let preview = own.reversed().lazy.compactMap({ event -> String? in
                if case .finished(let text) = event.kind { return text } else { return nil }
            }).first {
                summary = oneLine(preview)
            }
            agents.append(Agent(id: session.id, name: session.name, kind: session.kind, folder: session.cwd,
                                finished: finished, approved: approved, summary: summary, waiting: waiting))
        }
        // Waiting on the user first, then the busiest.
        agents.sort {
            if ($0.waiting != nil) != ($1.waiting != nil) { return $0.waiting != nil }
            return ($0.finished + $0.approved) > ($1.finished + $1.approved)
        }
        return AwayDigest(since: since, until: until, agents: agents)
    }

    /// A reply's first sentence or two, on one line.
    static func oneLine(_ text: String) -> String? {
        let flat = text
            .replacingOccurrences(of: #"[#*_`>]"#, with: "", options: .regularExpression)
            .split(whereSeparator: \.isNewline)
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
            .joined(separator: " ")
        guard !flat.isEmpty else { return nil }
        return flat.count > 180 ? String(flat.prefix(177)) + "…" : flat
    }
}
