import Foundation

/// The sidebar's agents in sections: the ones pinned on top, then those
/// still working, then the active ones (those that came back to you,
/// finished or asking something), then the archived: work the user is done
/// with, out of the way but not gone.
nonisolated struct SidebarSections: Equatable, Sendable {
    var pinned: [Session] = []
    var active: [Session] = []
    var working: [Session] = []
    var archived: [Session] = []

    var isEmpty: Bool { pinned.isEmpty && active.isEmpty && working.isEmpty && archived.isEmpty }

    /// An agent idle this long is archived by itself.
    static let archiveAfter: TimeInterval = 3 * 24 * 60 * 60

    /// What the user decided: the agents pinned, in their order; those
    /// archived by hand, and when; those brought back by hand, and when.
    struct Choices: Equatable, Sendable {
        var pinned: [String] = []
        var archived: [String: Date] = [:]
        var unarchived: [String: Date] = [:]
    }

    static func make(_ sessions: [Session], choices: Choices, now: Date = .now) -> SidebarSections {
        var sections = SidebarSections()
        var pinned: [String: Session] = [:]
        for session in sessions {
            if isArchived(session, choices: choices, now: now) {
                sections.archived.append(session)
            } else if choices.pinned.contains(session.id) {
                pinned[session.id] = session
            } else if AgentsStore.filterState(session) == .busy {
                sections.working.append(session)
            } else {
                sections.active.append(session)
            }
        }
        sections.pinned = choices.pinned.compactMap { pinned[$0] }
        let recent: (Session, Session) -> Bool = { ($0.lastActivityAt ?? 0) > ($1.lastActivityAt ?? 0) }
        // What asks for you leads; then whatever came back to you last.
        sections.active.sort { a, b in
            let waitingA = AgentsStore.filterState(a) == .waitingInput
            let waitingB = AgentsStore.filterState(b) == .waitingInput
            if waitingA != waitingB { return waitingA }
            return recent(a, b)
        }
        sections.working.sort(by: recent)
        sections.archived.sort(by: recent)
        return sections
    }

    /// Archived: by hand, until the agent works or asks again; or by itself
    /// once it has ended or sat idle for three days, unless the user brought
    /// it back since it last did anything. An agent working or waiting on
    /// the user never is.
    static func isArchived(_ session: Session, choices: Choices, now: Date) -> Bool {
        let state = AgentsStore.filterState(session)
        if state == .busy || state == .waitingInput { return false }
        let active = session.lastActivity ?? .distantPast
        if let at = choices.archived[session.id] { return active <= at }
        if let at = choices.unarchived[session.id], active <= at { return false }
        if session.state == .ended { return true }
        return now.timeIntervalSince(active) > archiveAfter
    }

    /// Every agent shown, in the order the sidebar lists them, with the
    /// collapsed sections left out: what the arrow keys step through.
    func shown(working workingOpen: Bool, archived archivedOpen: Bool) -> [Session] {
        pinned + (workingOpen ? working : []) + active + (archivedOpen ? archived : [])
    }
}
