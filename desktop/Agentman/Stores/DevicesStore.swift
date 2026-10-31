import Foundation

extension AgentsStore {
    /// Asks the daemon for the devices; the answer lands in `devices`, as
    /// every change after it does.
    func loadDevices() async {
        guard let reply = try? await client.request(DaemonRequest(.devices), timeout: 10) else { return }
        if reply.type == "devices" { devices = reply.devices ?? [] }
    }

    /// A code for connecting a new device, from the relay through the daemon.
    func pairCode() async throws -> PairCode {
        let reply = try await client.request(DaemonRequest(.pairCode), timeout: 30)
        if reply.type == "error" {
            let message = reply.error ?? ""
            if message.contains("unsupported request") {
                throw WorkspaceError(message: "Update agentman on your Mac to connect devices here, or run am pair in a terminal.")
            }
            throw WorkspaceError(message: message.replacingOccurrences(of: "^daemon: ", with: "", options: .regularExpression))
        }
        guard let code = reply.pairCode else { throw WorkspaceError(message: "The daemon didn't give a code.") }
        return code
    }
}
