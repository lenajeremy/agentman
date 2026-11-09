import Foundation

/// What the Board shows of a search: the agents on it that match by name,
/// folder, branch or what was said in them, and the conversations that have
/// ended and said it.
struct BoardSearch: Equatable {
    var query: String
    var ids: Set<String> = []
    /// What was said, for the agents found that way or both ways.
    var bySession: [String: SearchHit] = [:]
    var past: [SearchHit] = []
    var searching = false
    var indexing = false
}

extension AgentsStore {
    /// The same search as the sidebar's, for the Board. Nil when nothing is
    /// being searched for.
    var boardSearch: BoardSearch? {
        let query = search.trimmingCharacters(in: .whitespaces)
        guard !query.isEmpty else { return nil }
        var found = BoardSearch(query: query)
        for session in sessions where session.state != .ended && Self.inFolder(session.cwd, folderFilter) {
            if session.name.localizedCaseInsensitiveContains(query) || session.cwd.localizedCaseInsensitiveContains(query)
                || (branches[session.cwd]?.localizedCaseInsensitiveContains(query) ?? false) {
                found.ids.insert(session.id)
            }
        }
        found.searching = Self.searchable(query) && messageHits?.query != query
        found.indexing = messageHits?.indexing == true
        for var hit in messageHits?.hits ?? [] {
            guard let session = hit.session, Self.inFolder(session.cwd, folderFilter) else { continue }
            let current = sessions.first { $0.id == session.id }
            if let current, current.state != .ended {
                found.ids.insert(current.id)
                if found.bySession[current.id] == nil { found.bySession[current.id] = hit }
            } else {
                hit.session = current ?? session
                found.past.append(hit)
            }
        }
        return found
    }

    /// Looks through what the conversations said for `search`, once typing
    /// has paused. For whichever of the sidebar and the Board is showing.
    func refreshMessageHits() async {
        let query = search.trimmingCharacters(in: .whitespaces)
        guard Self.searchable(query) else {
            messageHits = nil
            return
        }
        try? await Task.sleep(for: .milliseconds(150))
        guard !Task.isCancelled else { return }
        let results = await searchMessages(query)
        guard !Task.isCancelled else { return }
        messageHits = results
    }
}
