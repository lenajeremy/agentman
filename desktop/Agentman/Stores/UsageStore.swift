import Foundation

extension AgentsStore {
    /// What the agents spent over the last days days (1, 7, 30 or 90), as
    /// the daemon sums it from their own records.
    func usage(days: Int) async throws -> Usage {
        var request = DaemonRequest(.usage)
        request.days = days
        let reply = try await client.request(request, timeout: 120)
        if reply.type == "error" {
            let message = reply.error ?? ""
            if message.contains("unsupported request") {
                throw WorkspaceError(message: "Update agentman on your Mac to see usage.")
            }
            throw WorkspaceError(message: message.replacingOccurrences(of: "^daemon: ", with: "", options: .regularExpression))
        }
        guard let usage = reply.usage else { throw WorkspaceError(message: "The daemon didn't say what the agents spent.") }
        return usage
    }
}
