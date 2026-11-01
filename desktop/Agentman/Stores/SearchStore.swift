import Foundation

/// Where a search sent the user: a message in a conversation, and the words
/// to find there.
struct ThreadJump: Equatable {
    var sessionId: String
    var messageId: String
    var query: String
    /// New for every pick, so picking the same message twice jumps twice.
    var serial = UUID()
}

/// What the sidebar shows of a search: a hit under each agent it lists by
/// name, and the conversations found only by what was said in them.
struct SidebarHits {
    var bySession: [String: SearchHit] = [:]
    /// Best first, each with its session as it is now when it is running.
    var others: [SearchHit] = []
    /// Asked, and not answered yet.
    var searching = false
    /// The daemon is still reading conversations for the first time.
    var indexing = false
}

/// Search, through the daemon's index of every conversation on this Mac.
extension AgentsStore {
    /// The sidebar's search, with the same folder and status filters as the
    /// agents it lists.
    var sidebarHits: SidebarHits {
        let query = search.trimmingCharacters(in: .whitespaces)
        var found = SidebarHits()
        guard !query.isEmpty else { return found }
        found.searching = Self.searchable(query) && messageHits?.query != query
        found.indexing = messageHits?.indexing == true
        guard let hits = messageHits?.hits else { return found }
        let sections = sidebar
        let named = Set((sections.pinned + sections.working + sections.active + sections.archived).map(\.id))
        for var hit in hits {
            guard let session = hit.session, Self.inFolder(session.cwd, folderFilter) else { continue }
            if named.contains(session.id) {
                if found.bySession[session.id] == nil { found.bySession[session.id] = hit }
                continue
            }
            let current = sessions.first { $0.id == session.id } ?? session
            if let stateFilter, current.state == .ended || Self.filterState(current) != stateFilter { continue }
            hit.session = current
            found.others.append(hit)
        }
        return found
    }

    /// Whether a query has a word the index can look up: three characters
    /// or more. Shorter words only narrow, so on their own they find nothing.
    nonisolated static func searchable(_ query: String) -> Bool {
        query.split(whereSeparator: \.isWhitespace).contains { $0.count >= 3 }
    }

    /// The best message of each conversation that has the query's words, the
    /// best conversations first; nil when nothing could be asked.
    func searchMessages(_ query: String, limit: Int = 50) async -> MessageSearchResults? {
        guard Self.searchable(query) else { return nil }
        var request = DaemonRequest(.search)
        request.text = String(query.prefix(200))
        request.limit = limit
        guard let reply = try? await client.request(request, timeout: 15),
              reply.type == "search", let results = reply.search else { return nil }
        return results
    }

    /// Every message in one conversation that has the query's words, oldest
    /// first, for its find bar.
    func searchThread(_ sessionId: String, query: String) async -> [SearchHit] {
        guard Self.searchable(query) else { return [] }
        var request = DaemonRequest(.search, sessionId: sessionId)
        request.text = String(query.prefix(200))
        guard let reply = try? await client.request(request, timeout: 15),
              reply.type == "search" else { return [] }
        return reply.search?.hits ?? []
    }

    /// Opens the conversation a hit is in, at the message, with its words
    /// found. One that has ended is kept so it can be shown, as the folder
    /// filter's "Earlier here" does.
    func open(_ hit: SearchHit, query: String) {
        guard let session = hit.session else { return }
        if !sessions.contains(where: { $0.id == session.id }), !pastSessions.contains(where: { $0.id == session.id }) {
            foundSessions[session.id] = session
        }
        jump = ThreadJump(sessionId: session.id, messageId: hit.messageId, query: query)
        selection = .agent(session.id)
    }

    /// Reads a conversation back until a message is in it, as scrolling up
    /// would, and reports whether it got there. The newest page has to have
    /// come first.
    func loadThrough(_ sessionId: String, messageId: String) async -> Bool {
        for _ in 0..<80 where hasMore[sessionId] == nil {
            try? await Task.sleep(for: .milliseconds(100))
        }
        // A thousand pages back is far past where anyone searches, and
        // keeps a vanished message from reading forever.
        for _ in 0..<1000 {
            if messages[sessionId]?.contains(where: { $0.id == messageId }) == true { return true }
            guard hasMore[sessionId] == true else { return false }
            if loadingOlder.contains(sessionId) {
                try? await Task.sleep(for: .milliseconds(50))
            } else {
                await loadOlder(sessionId)
            }
        }
        return false
    }
}
