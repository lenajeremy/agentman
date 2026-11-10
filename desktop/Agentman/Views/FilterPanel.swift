import AppKit
import SwiftUI

/// What the sidebar's filter button opens: the agents to show, by state and
/// by folder (any folder at all), and what colours them.
struct FilterPanel: View {
    @Environment(AgentsStore.self) private var store
    @AppStorage(AgentColors.settingKey) private var colorBy: AgentColorBy = .none
    var close: () -> Void = {}

    private static let states: [SessionState?] = [nil, .waitingInput, .busy, .idle]

    var body: some View {
        @Bindable var store = store
        let counts = store.stateCounts
        VStack(alignment: .leading, spacing: 0) {
            heading("Show")
            Segments(options: Self.states, selection: $store.stateFilter) { state in
                switch state {
                case nil: "All"
                case .waitingInput?: Self.counted("Needs you", counts.needsYou)
                case .busy?: Self.counted("Working", counts.working)
                default: Self.counted("Idle", counts.idle)
                }
            }

            heading("Folder")
                .padding(.top, 18)
            VStack(spacing: 1) {
                FilterRow(name: "All folders", chosen: store.folderFilter == nil) { pick(nil) }
                ForEach(store.folderChoices, id: \.self) { path in
                    FilterRow(name: Worktrees.label(path), place: FolderChoices.place(of: path), count: running(in: path),
                              chosen: store.folderFilter == path) { pick(path) }
                }
                FilterRow(name: "Choose a folder…", symbol: "folder.badge.plus", chosen: false) {
                    close()
                    // After the popover has gone, so the panel isn't behind it.
                    DispatchQueue.main.async { FolderPicker.choose(for: store) }
                }
                .padding(.top, 4)
            }
            .padding(.horizontal, -6)

            heading("Color agents by")
                .padding(.top, 18)
            Segments(options: AgentColorBy.allCases, selection: $colorBy) { $0 == .none ? "None" : $0.title }
        }
        .padding(16)
        .frame(width: 300, alignment: .leading)
        .task { await store.loadFolders() }
    }

    private func heading(_ title: String) -> some View {
        Text(title)
            .font(TextStyle.label)
            .foregroundStyle(Palette.faint)
            .padding(.bottom, 7)
    }

    private func pick(_ folder: String?) {
        store.folderFilter = folder
        close()
    }

    /// The agents listed now that work in a folder.
    private func running(in path: String) -> Int {
        store.sessions.filter { $0.state != .ended && AgentsStore.inFolder($0.cwd, path) }.count
    }

    nonisolated static func counted(_ title: String, _ count: Int) -> String {
        count > 0 ? "\(title) \(count)" : title
    }
}

/// One choice in the panel's folder list: its name, where it is, and how
/// many agents work there now.
private struct FilterRow: View {
    let name: String
    var place: String?
    var count = 0
    var symbol: String?
    let chosen: Bool
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 7) {
                Image(systemName: symbol ?? "checkmark")
                    .font(.system(size: symbol == nil ? 10 : 11.5, weight: symbol == nil ? .semibold : .regular))
                    .foregroundStyle(symbol == nil ? Palette.text : Palette.muted)
                    .frame(width: 16)
                    .opacity(symbol != nil || chosen ? 1 : 0)
                Text(name)
                    .font(Fonts.sans(13, chosen ? .medium : .regular))
                    .foregroundStyle(symbol == nil ? Palette.text : Palette.textSecondary)
                    .lineLimit(1)
                    .layoutPriority(1)
                if let place {
                    // Whole or not at all: a cut place says nothing.
                    ViewThatFits(in: .horizontal) {
                        Text(place)
                            .font(Fonts.sans(11.5))
                            .foregroundStyle(Palette.faint)
                            .lineLimit(1)
                        Color.clear.frame(width: 0, height: 0)
                    }
                }
                Spacer(minLength: 8)
                if count > 0 {
                    Text("\(count)")
                        .font(Fonts.sans(11.5))
                        .monospacedDigit()
                        .foregroundStyle(Palette.muted)
                }
            }
            .padding(.horizontal, 6)
            .frame(height: 27)
            .background(hovering ? Palette.fill : .clear, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(place.map { "\($0)/\(name)" } ?? "")
        .accessibilityAddTraits(chosen ? .isSelected : [])
    }
}

/// Asks for any folder and shows its agents.
enum FolderPicker {
    @MainActor static func choose(for store: AgentsStore) {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.directoryURL = URL(fileURLWithPath: store.folderFilter ?? NSHomeDirectory())
        panel.message = "Choose a folder to show the agents that work in it"
        panel.prompt = "Show"
        if panel.runModal() == .OK, let url = panel.url {
            store.chooseFolder(url.path)
        }
    }
}
