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
}
