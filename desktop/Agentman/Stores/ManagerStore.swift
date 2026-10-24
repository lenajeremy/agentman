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
    /// The last thing that went wrong talking to Agentman.
    var problem: String?
    private(set) var loaded = false

    private let client: DaemonClient
    private var images: [String: NSImage] = [:]

    init(client: DaemonClient) {
        self.client = client
    }

    var thinking: Bool { status.state == "thinking" }
    var needsKey: Bool { settings.map { !$0.keySet } ?? (status.state == "needs_key") }

    /// Loads everything afresh: on connecting, and after a reconnect.
    func reload() async {
        async let settingsReply = try? client.request(DaemonRequest(.managerSettings))
        async let briefsReply = try? client.request(DaemonRequest(.managerBriefs))
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
        loaded = true
    }

    // MARK: - Talking to Agentman

    func send(_ text: String) async -> Bool {
        var request = DaemonRequest(.managerSend)
        request.text = text
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

    func loadOlder() async {
        guard hasOlder, let first = entries.first else { return }
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

    /// Stores the API key in the Mac's Keychain, through the daemon, or
    /// removes it when key is empty. The key is never sent back.
    func setKey(_ key: String) async -> String? {
        var request = DaemonRequest(.managerSetKey)
        request.text = key
        guard let reply = try? await client.request(request) else { return "Couldn't reach am serve." }
        if reply.type == "error" { return reply.error }
        apply(reply)
        return nil
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

    func apply(_ event: DaemonEvent) {
        switch event.type {
        case "manager_entry":
            if let entry = event.entry { upsert(entry) }
        case "manager_delta":
            guard let id = event.entryId, let delta = event.delta,
                  let index = entries.firstIndex(where: { $0.id == id }) else { return }
            entries[index].text += delta
        case "manager_status":
            if let status = event.managerStatus { self.status = status }
        case "manager_settings":
            if let settings = event.managerSettings { self.settings = settings }
            if let status = event.managerStatus { self.status = status }
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
            entries[index] = entry
        } else {
            entries.append(entry)
        }
    }
}
