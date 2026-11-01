import Foundation

extension AgentsStore {
    /// What is left of each agent's plan. The daemon keeps its answer for a
    /// couple of minutes; force reads the limits again.
    func limits(force: Bool = false) async throws -> Limits {
        var request = DaemonRequest(.limits)
        if force { request.force = true }
        let reply = try await client.request(request, timeout: 60)
        if reply.type == "error" {
            let message = reply.error ?? ""
            if message.contains("unsupported request") {
                throw WorkspaceError(message: "Update agentman on your Mac to see plan limits.")
            }
            throw WorkspaceError(message: message.replacingOccurrences(of: "^daemon: ", with: "", options: .regularExpression))
        }
        guard let limits = reply.limits else { throw WorkspaceError(message: "The daemon didn't say what is left of the plans.") }
        return limits
    }
}
