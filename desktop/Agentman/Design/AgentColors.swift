import SwiftUI

/// What an agent's colour says, which the user picks.
enum AgentColorBy: String, CaseIterable, Identifiable {
    case none, state, project, folder

    var id: String { rawValue }
    var title: String {
        switch self {
        case .none: "No Color"
        case .state: "State"
        case .project: "Project"
        case .folder: "Folder"
        }
    }
}

/// The colour each agent is shown in, so which is which can be told at a
/// glance. A project or folder keeps its colour for good: each new one takes
/// whichever is used least so far, so few of them never share one.
enum AgentColors {
    static let settingKey = "agentColorBy"
    private static let assignedKey = "agentColorsAssigned"

    static let hues: [Color] = [
        Color(light: 0x3268F5, dark: 0x6A95FF),
        Color(light: 0x12A66A, dark: 0x2BC784),
        Color(light: 0xE5484D, dark: 0xFF6B70),
        Color(light: 0x8A3FB0, dark: 0xC78BEA),
        Color(light: 0xD99A2B, dark: 0xE7B04C),
        Color(light: 0x0E9F9A, dark: 0x3CC9C2),
        Color(light: 0xD6409F, dark: 0xF07BC4),
        Color(light: 0x5B6170, dark: 0xA8AEBB),
        Color(light: 0xB25A12, dark: 0xF2A867),
        Color(light: 0x4F46B8, dark: 0x9A93F2),
    ]

    static func color(for session: Session, by choice: AgentColorBy, defaults: UserDefaults = .standard) -> Color? {
        switch choice {
        case .none:
            return nil
        case .state:
            switch AgentsStore.filterState(session) {
            case .waitingInput: return Palette.needsYou
            case .busy: return Palette.working
            default: return Palette.ok
            }
        case .project:
            return hues[index(for: "project|" + projectName(session.cwd), defaults: defaults)]
        case .folder:
            return hues[index(for: "folder|" + session.cwd, defaults: defaults)]
        }
    }

    /// Worktrees of a repository are one project.
    static func projectName(_ path: String) -> String {
        Worktrees.parts(of: path)?.repo ?? (path as NSString).lastPathComponent
    }

    /// The colour a key has, giving it one the first time.
    static func index(for key: String, defaults: UserDefaults = .standard) -> Int {
        var assigned = defaults.dictionary(forKey: assignedKey) as? [String: Int] ?? [:]
        if let known = assigned[key], hues.indices.contains(known) { return known }
        var uses = [Int](repeating: 0, count: hues.count)
        for value in assigned.values where uses.indices.contains(value) { uses[value] += 1 }
        let next = uses.firstIndex(of: uses.min() ?? 0) ?? 0
        assigned[key] = next
        defaults.set(assigned, forKey: assignedKey)
        return next
    }
}

/// Choosing what agents are coloured by.
struct AgentColorMenuItems: View {
    @AppStorage(AgentColors.settingKey) private var colorBy: AgentColorBy = .none

    var body: some View {
        ForEach(AgentColorBy.allCases) { choice in
            Button {
                colorBy = choice
            } label: {
                if choice == colorBy { Label(choice.title, systemImage: "checkmark") } else { Text(choice.title) }
            }
        }
    }
}
