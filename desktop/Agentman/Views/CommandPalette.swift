import AppKit
import SwiftUI

/// ⌘K: every agent and everything the app does, by a few letters, in the
/// manner of the command menus in Linear, Raycast and Slack; and whatever
/// was typed can go to Agentman as a question.
struct CommandPalette: View {
    @Environment(AgentsStore.self) private var store
    @Environment(\.openWindow) private var openWindow
    @Environment(\.openSettings) private var openSettings
    @AppStorage("settingsTab") private var settingsTab = "general"

    /// One thing the palette can do.
    struct Command {
        let item: PaletteItem
        /// Extra words it answers to, beyond its title.
        var keywords = ""
        let run: () -> Void
    }

    var body: some View {
        PaletteView(
            placeholder: "Go to an agent, run a command, or ask Agentman",
            results: results,
            choose: { item in
                store.showPalette = false
                if item.id.hasPrefix("ask:") {
                    ask(String(item.id.dropFirst(4)))
                } else {
                    commands(for: "").first { $0.item.id == item.id }?.run()
                }
            },
            dismiss: { store.showPalette = false }
        )
        .task { await store.loadFolders() }
    }

    private func results(_ query: String) -> [PaletteItem] {
        let all = commands(for: query)
        let trimmed = query.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty else { return all.prefix(40).map(\.item) }
        var ranked = all
            .filter { !$0.item.id.hasPrefix("ask:") }
            .compactMap { command in
                [Fuzzy.score(trimmed, command.item.title), Fuzzy.score(trimmed, command.keywords)].compactMap { $0 }.max()
                    .map { (command.item, $0) }
            }
            .sorted { $0.1 > $1.1 }
            .prefix(40)
            .map(\.0)
        // A question, or nothing that matches, goes to Agentman first.
        if let ask = all.first(where: { $0.item.id.hasPrefix("ask:") })?.item {
            if ranked.isEmpty || trimmed.hasSuffix("?") || trimmed.split(separator: " ").count >= 4 {
                ranked.insert(ask, at: 0)
            } else {
                ranked.append(ask)
            }
        }
        return ranked
    }

    private func ask(_ question: String) {
        store.selection = .manager
        Task { _ = await store.manager.send(question) }
    }

    private func commands(for query: String) -> [Command] {
        var commands: [Command] = []
        let trimmed = query.trimmingCharacters(in: .whitespaces)
        if !trimmed.isEmpty, store.manager.settings?.keySet == true {
            // The question rides in the row's id, to be asked when chosen.
            commands.append(Command(item: PaletteItem(id: "ask:" + trimmed, title: "Ask Agentman: “\(trimmed)”",
                                                      icon: "sparkles", tint: Palette.working)) { ask(trimmed) })
        }

        // The agent open now, first.
        if case .agent(let id) = store.selection, let session = store.session(id) {
            let folder = session.cwd
            commands.append(Command(item: PaletteItem(id: "here-editor", title: "Open \(session.name)'s Folder in the Editor",
                                                      icon: "chevron.left.forwardslash.chevron.right")) {
                EditorOpener.open(folder: folder, with: openWindow)
            })
            commands.append(Command(item: PaletteItem(id: "here-terminal", title: "Open a Terminal for \(session.name)", icon: "terminal")) {
                RightPanel.showTerminal(in: folder)
            })
            commands.append(Command(item: PaletteItem(id: "here-rename", title: "Rename \(session.name)…", icon: "pencil")) {
                store.renaming = id
            })
        }

        // Agents, those that need the user first.
        let agents = store.sessions.sorted {
            ($0.state == .waitingInput ? 0 : 1, $0.name) < ($1.state == .waitingInput ? 0 : 1, $1.name)
        }
        for session in agents {
            let state = session.state == .waitingInput ? "needs you" : session.state == .busy ? "working" : "idle"
            commands.append(Command(
                item: PaletteItem(id: "agent:" + session.id, title: session.name,
                                  detail: (session.cwd as NSString).lastPathComponent + " · " + state,
                                  icon: "circle.fill", tint: Palette.dot(session.state)),
                keywords: session.cwd + " " + AgentName.of(session.kind)
            ) {
                store.selection = .agent(session.id)
            })
        }

        commands.append(Command(item: PaletteItem(id: "new", title: "New Agent", icon: "square.and.pencil", shortcut: "⌘N")) {
            store.selection = .newAgent
        })
        commands.append(Command(item: PaletteItem(id: "agentman", title: "Agentman", detail: "the conversation", icon: "bubble.left.and.text.bubble.right")) {
            store.selection = .manager
        })
        commands.append(Command(item: PaletteItem(id: "away", title: "What Happened While I Was Away", icon: "clock.arrow.circlepath"),
                                keywords: "summary catch up") {
            store.showAway(since: store.away.last?.since ?? Date().addingTimeInterval(-3600))
        })
        for (state, title) in [(SessionState.waitingInput, "Show Agents That Need You"), (.busy, "Show Working Agents"), (.idle, "Show Idle Agents")] {
            commands.append(Command(item: PaletteItem(id: "filter:\(state)", title: title, icon: "line.3.horizontal.decrease"),
                                    keywords: "filter") {
                store.stateFilter = state
            })
        }
        commands.append(Command(item: PaletteItem(id: "filter:all", title: "Show All Agents", icon: "line.3.horizontal.decrease"),
                                keywords: "filter clear") {
            store.stateFilter = nil
            store.folderFilter = nil
        })

        commands.append(Command(item: PaletteItem(id: "open-folder", title: "Open a Folder in the Editor…",
                                                  icon: "chevron.left.forwardslash.chevron.right", shortcut: "⌘O")) {
            if let folder = EditorOpener.chooseFolder() { EditorOpener.open(folder: folder, with: openWindow) }
        })
        for folder in store.folders.prefix(8) {
            let name = (folder.path as NSString).lastPathComponent
            commands.append(Command(item: PaletteItem(id: "editor:" + folder.path, title: "Open \(name) in the Editor",
                                                      detail: (folder.path as NSString).abbreviatingWithTildeInPath,
                                                      icon: "chevron.left.forwardslash.chevron.right"), keywords: folder.path) {
                EditorOpener.open(folder: folder.path, with: openWindow)
            })
            commands.append(Command(item: PaletteItem(id: "terminal:" + folder.path, title: "Open a Terminal in \(name)",
                                                      detail: (folder.path as NSString).abbreviatingWithTildeInPath,
                                                      icon: "terminal"), keywords: folder.path + " shell") {
                EditorOpener.openTerminal(folder: folder.path, with: openWindow)
            })
        }

        commands.append(Command(item: PaletteItem(id: "settings", title: "Settings", icon: "gearshape", shortcut: "⌘,")) {
            openSettings()
        })
        for page in SettingsPage.allCases {
            commands.append(Command(item: PaletteItem(id: "settings:" + page.rawValue, title: "Settings: \(page.title)", icon: page.icon),
                                    keywords: "preferences") {
                settingsTab = page.rawValue
                openSettings()
            })
        }
        for (value, title) in [("light", "Use the Light Appearance"), ("dark", "Use the Dark Appearance"), ("system", "Match the Mac's Appearance")] {
            commands.append(Command(item: PaletteItem(id: "appearance:" + value, title: title, icon: "circle.lefthalf.filled"),
                                    keywords: "theme mode") {
                UserDefaults.standard.set(value, forKey: "appearance")
                Appearance.apply(value)
            })
        }
        return commands
    }
}
