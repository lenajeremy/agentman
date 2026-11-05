import AppKit
import Foundation

/// The user's shell, as far as the app needs it: the PATH a terminal gets,
/// to find am and tmux on it, and a link put in one of its folders so am
/// works in Terminal. The app's is the real Mac's; tests give one whose PATH
/// is folders of their own and whose links go there, so no test reads the
/// user's shell profile, writes /usr/local/bin or asks for a password.
protocol ShellSystem: Sendable {
    /// The folders a login shell's PATH names, in order; nil when the shell
    /// didn't say.
    func loginPath() async -> [String]?
    /// A symbolic link at link to target, in place of a link already there;
    /// with admin, through macOS's administrator prompt, the folder made
    /// first if it isn't there.
    func link(_ link: String, to target: String, admin: Bool) async -> LinkResult
}

enum LinkResult: Equatable, Sendable {
    case linked
    /// The user said no to the administrator prompt.
    case cancelled
    case failed(String)
}

/// The real shell. Nothing of it runs under tests.
struct MacShell: ShellSystem {
    /// The user's shell, started as a terminal starts it, as am serve reads
    /// its environment (internal/shellenv): PATH is set up in its profile.
    func loginPath() async -> [String]? {
        guard !AgentmanApp.underTests else { return nil }
        let shell = ProcessInfo.processInfo.environment["SHELL"].flatMap { $0.isEmpty ? nil : $0 } ?? "/bin/zsh"
        let marker = "AGENTMAN_PATH_FOLLOWS:"
        let output = Subprocess.Collected()
        // What a chatty profile prints comes before the marker.
        _ = await Subprocess.run(shell, ["-l", "-i", "-c", "printf '\\n\(marker)%s\\n' \"$PATH\""], timeout: 10) { output.add($0) }
        guard let line = output.text.split(separator: "\n").last(where: { $0.hasPrefix(marker) }) else { return nil }
        return line.dropFirst(marker.count).split(separator: ":").map(String.init).filter { !$0.isEmpty }
    }

    func link(_ link: String, to target: String, admin: Bool) async -> LinkResult {
        guard !AgentmanApp.underTests else { return .failed("Nothing is linked under tests.") }
        let folder = (link as NSString).deletingLastPathComponent
        guard admin else {
            let files = FileManager.default
            do {
                // Only a link, such as one left dangling, is replaced.
                if (try? files.destinationOfSymbolicLink(atPath: link)) != nil { try files.removeItem(atPath: link) }
                try files.createSymbolicLink(atPath: link, withDestinationPath: target)
                return .linked
            } catch {
                return .failed(error.localizedDescription)
            }
        }
        // Run in the app, rather than by osascript, so the prompt names
        // Agentman.
        let script = """
        do shell script "/bin/mkdir -p " & quoted form of \(Self.quote(folder)) & " && /bin/ln -sfn " & \
        quoted form of \(Self.quote(target)) & " " & quoted form of \(Self.quote(link)) \
        with prompt \(Self.quote("Agentman wants to put the am command in \(folder), so it works in Terminal.")) \
        with administrator privileges
        """
        return await MainActor.run {
            var error: NSDictionary?
            NSAppleScript(source: script)?.executeAndReturnError(&error)
            guard let error else { return .linked }
            if (error[NSAppleScript.errorNumber] as? Int) == -128 { return .cancelled }
            return .failed(error[NSAppleScript.errorMessage] as? String ?? "The link couldn't be made.")
        }
    }

    /// text as an AppleScript string.
    static func quote(_ text: String) -> String {
        "\"" + text.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"") + "\""
    }
}
