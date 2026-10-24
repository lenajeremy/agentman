import Foundation

/// Reading an agent's folder: what changed, its files, their diffs, and its
/// git history. Each call is one request to the daemon, which keeps every
/// read inside the agent's own folder.
extension AgentsStore {
    struct WorkspaceError: LocalizedError {
        let message: String
        var errorDescription: String? { message }
    }

    func workspace(_ type: RequestType, session: String, path: String = "",
                   offset: Int? = nil, commit: String? = nil, ref: String? = nil) async throws -> WorkspaceResult {
        var request = DaemonRequest(type, sessionId: session)
        request.path = path
        request.offset = offset
        request.commit = commit
        request.ref = ref
        let reply = try await client.request(request)
        if reply.type == "error" {
            let message = (reply.error ?? "That couldn't be read.")
                .replacingOccurrences(of: "^(daemon|source|workspace): ", with: "", options: .regularExpression)
            throw WorkspaceError(message: message.prefix(1).uppercased() + message.dropFirst())
        }
        guard let result = reply.workspace else { throw WorkspaceError(message: "That couldn't be read.") }
        return result
    }

    /// Shares a dev server through the relay and returns its public link.
    func shareServer(_ session: String, port: Int) async throws -> String {
        var request = DaemonRequest(.openServer, sessionId: session)
        request.port = port
        let reply = try await client.request(request, timeout: 45)
        guard reply.type != "error", let link = reply.link else {
            throw WorkspaceError(message: (reply.error ?? "It couldn't be shared.").replacingOccurrences(of: "^daemon: ", with: "", options: .regularExpression))
        }
        return link
    }

    /// Stops sharing a dev server; the link stops working at once.
    func unshareServer(_ session: String, port: Int) {
        var request = DaemonRequest(.closeServer, sessionId: session)
        request.port = port
        client.send(request)
    }

    /// Ends the process listening on a port. Returns the problem, if any.
    func stopServer(_ session: String, port: Int) async -> String? {
        var request = DaemonRequest(.stopServer, sessionId: session)
        request.port = port
        guard let reply = try? await client.request(request) else { return "Couldn't reach am serve." }
        return reply.type == "error" ? (reply.error ?? "It couldn't be stopped.").replacingOccurrences(of: "^daemon: ", with: "", options: .regularExpression) : nil
    }

    /// The documents a session's agent wrote, newest first.
    func artifacts(_ session: String) async throws -> [Artifact] {
        let reply = try await client.request(DaemonRequest(.listArtifacts, sessionId: session))
        if reply.type == "error" { throw WorkspaceError(message: reply.error ?? "They couldn't be listed.") }
        return (reply.artifacts ?? []).sorted { $0.updatedAt > $1.updatedAt }
    }

    /// Approves a document, or asks for changes to it, with a comment.
    /// Returns the problem, if any.
    func review(_ session: String, artifact: String, approve: Bool, comment: String) async -> String? {
        var request = DaemonRequest(.reviewArtifact, sessionId: session)
        request.path = artifact
        request.approve = approve
        request.text = comment
        request.clientId = "mac-review-\(UUID().uuidString)"
        do {
            let reply = try await client.request(request)
            if reply.type == "error" || reply.status == .failed {
                return (reply.error ?? "It couldn't be sent.").replacingOccurrences(of: "^(daemon|source): ", with: "", options: .regularExpression)
            }
            return nil
        } catch {
            return error.localizedDescription
        }
    }
}
