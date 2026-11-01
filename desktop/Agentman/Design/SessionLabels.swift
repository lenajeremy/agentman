import Foundation

/// Modes and models as a person would say them, not as the agent names them:
/// "Accept edits" for accept-edits, "Opus 5.5" for claude-opus-5-5. The
/// phone says them the same way.
enum SessionLabels {
    static func mode(_ mode: String) -> String {
        let raw = mode.trimmingCharacters(in: .whitespaces)
        let known = [
            "default": "Default", "kiro_default": "Default", "kiro_planner": "Planner", "kiro_guide": "Guide",
            "accept-edits": "Accept edits", "accept edits": "Accept edits", "acceptedits": "Accept edits",
            "bypasspermissions": "Bypass permissions",
        ]
        if let found = known[raw.lowercased()] { return found }
        var words = raw
        if words.lowercased().hasPrefix("kiro_") { words = String(words.dropFirst(5)) }
        words = words.replacingOccurrences(of: "[_-]+", with: " ", options: .regularExpression)
            .trimmingCharacters(in: .whitespaces)
        guard let first = words.first else { return raw }
        return first.uppercased() + words.dropFirst()
    }

    static func model(_ model: String) -> String {
        let raw = model.trimmingCharacters(in: .whitespaces)
        if let m = raw.wholeMatch(of: /(?i)claude-(opus|sonnet|haiku)-(\d+)(?:[-.](\d+))?(?:-\d{8})?/) {
            let family = m.1.prefix(1).uppercased() + m.1.dropFirst().lowercased()
            if let minor = m.3 { return "\(family) \(m.2).\(minor)" }
            return "\(family) \(m.2)"
        }
        if let m = raw.wholeMatch(of: /(?i)claude-(\d+(?:\.\d+)?)-(opus|sonnet|haiku)(?:-\d{8})?/) {
            return m.2.prefix(1).uppercased() + m.2.dropFirst().lowercased() + " \(m.1)"
        }
        if let m = raw.wholeMatch(of: /(?i)gpt-([\w.]+?)(?:-(.+))?/) {
            let rest = m.2.map { " " + $0.split(separator: "-").map { $0.prefix(1).uppercased() + $0.dropFirst() }.joined(separator: " ") } ?? ""
            return "GPT-\(m.1)\(rest)"
        }
        // Meta Muse: muse-spark-1.3-contributor is "Muse Spark 1.3", as Muse's
        // terminal says it (contributor is the account's mode, not a model).
        if let m = raw.wholeMatch(of: /(?i)muse-([a-z]+)-(\d+(?:\.\d+)*)(?:-contributor)?/) {
            return "Muse " + m.1.prefix(1).uppercased() + m.1.dropFirst().lowercased() + " \(m.2)"
        }
        if raw == "auto" { return "Auto" }
        return raw
    }
}

/// An agent's product name, by its kind: "Claude Code" for claude.
enum AgentName {
    static func of(_ kind: String) -> String {
        switch kind {
        case "claude": "Claude Code"
        case "codex": "Codex"
        case "opencode": "OpenCode"
        case "cursor": "Cursor"
        case "cursor-cli": "Cursor CLI"
        case "kiro": "Kiro"
        case "antigravity": "Antigravity"
        case "muse": "Muse"
        default: kind.capitalized
        }
    }
}
