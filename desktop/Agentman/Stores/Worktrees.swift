import Foundation

/// Where the New Agent page starts an agent, when something sends the user
/// there: in a folder, perhaps in a new worktree of it.
struct NewAgentPreset: Equatable {
    var folder: String
    var newWorktree = false
}

/// Folders Agentman made as git worktrees: ~/Agentman/Worktrees/<repo>/<name>.
enum Worktrees {
    static var root: String { NSHomeDirectory() + "/Agentman/Worktrees" }

    /// The repository and the worktree a folder in one belongs to, or nil.
    static func parts(of path: String) -> (repo: String, name: String)? {
        guard path.hasPrefix(root + "/") else { return nil }
        let rest = path.dropFirst(root.count + 1).split(separator: "/")
        guard rest.count >= 2 else { return nil }
        return (String(rest[0]), String(rest[1]))
    }

    /// A folder's name as the sidebar and a thread's title show it: a
    /// worktree's as its repository's and its own, "agentman · rate-limits".
    static func label(_ path: String) -> String {
        if let parts = parts(of: path) { return "\(parts.repo) · \(parts.name)" }
        return (path as NSString).lastPathComponent
    }

    /// Whether a folder is in a git repository: it or a folder above it,
    /// up to home, holds .git (a folder, or a worktree's file).
    static func inRepository(_ path: String) -> Bool {
        var folder = path
        let home = NSHomeDirectory()
        while folder.hasPrefix(home + "/") {
            if FileManager.default.fileExists(atPath: folder + "/.git") { return true }
            folder = (folder as NSString).deletingLastPathComponent
        }
        return false
    }

    /// The folder's path relative to home, as the daemon takes one.
    static func relative(_ path: String) -> String? {
        let home = NSHomeDirectory()
        guard path.hasPrefix(home + "/") else { return nil }
        return String(path.dropFirst(home.count + 1))
    }
}

/// Making, listing and removing worktrees, through the daemon.
extension AgentsStore {
    func createWorktree(in folder: String, name: String) async throws -> Worktree {
        var request = DaemonRequest(.createWorktree)
        request.path = try Self.homeRelative(folder)
        request.text = name
        let reply = try await worktreeReply(request)
        guard let worktree = reply.worktree else { throw WorkspaceError(message: "Update am serve to make worktrees from here.") }
        return worktree
    }

    func listWorktrees(in folder: String) async throws -> [Worktree] {
        var request = DaemonRequest(.listWorktrees)
        request.path = try Self.homeRelative(folder)
        return try await worktreeReply(request).worktrees ?? []
    }

    func removeWorktree(_ path: String, force: Bool) async throws {
        var request = DaemonRequest(.removeWorktree)
        request.path = try Self.homeRelative(path)
        request.force = force ? true : nil
        _ = try await worktreeReply(request)
    }

    private func worktreeReply(_ request: DaemonRequest) async throws -> DaemonEvent {
        let reply = try await client.request(request, timeout: 120)
        if reply.type == "error" {
            let message = (reply.error ?? "Git couldn't do that.")
                .replacingOccurrences(of: "^(daemon|workspace): ", with: "", options: .regularExpression)
            throw WorkspaceError(message: message.prefix(1).uppercased() + message.dropFirst())
        }
        return reply
    }

    private static func homeRelative(_ folder: String) throws -> String {
        guard let relative = Worktrees.relative(folder) else {
            throw WorkspaceError(message: "Worktrees are made from a folder inside your home folder.")
        }
        return relative
    }
}
