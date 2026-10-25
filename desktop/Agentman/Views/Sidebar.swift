import SwiftUI

/// Agentman first, pinned above every agent, which are grouped by the
/// folder they work in.
struct Sidebar: View {
    @Environment(AgentsStore.self) private var store

    var body: some View {
        @Bindable var store = store
        List(selection: $store.selection) {
            if store.folderFilter != nil || store.stateFilter != nil {
                ActiveFilters()
                    .listRowSeparator(.hidden)
            }

            ForEach(store.groups) { group in
                Section {
                    ForEach(group.sessions) { session in
                        AgentRow(session: session)
                            .tag(SidebarItem.agent(session.id))
                            .contextMenu {
                                Button("Rename…") { store.renaming = session.id }
                                if let own = session.originalName {
                                    Button("Use the agent's own name, “\(own)”") {
                                        Task { await store.rename(session.id, to: "") }
                                    }
                                }
                            }
                    }
                } header: {
                    FolderHeader(name: group.name, folder: group.id)
                }
            }

            // In a folder, the sessions that ran there and ended: read them,
            // or resume one.
            if store.folderFilter != nil, !store.earlierHere.isEmpty {
                Section {
                    ForEach(store.earlierHere) { session in
                        AgentRow(session: session)
                            .tag(SidebarItem.agent(session.id))
                    }
                } header: {
                    Text("Earlier here")
                        .font(TextStyle.label)
                        .foregroundStyle(Palette.faint)
                        .textCase(nil)
                        .padding(.leading, SidebarMetrics.header)
                }
            }
        }
        .listStyle(.sidebar)
        .searchable(text: $store.search, placement: .sidebar, prompt: "Search agents")
        // Agentman is where the user spends most of their time, so it stays
        // in view above the agents, set apart from them.
        .safeAreaInset(edge: .top, spacing: 0) { AgentmanCard() }
        .safeAreaInset(edge: .bottom) { ConnectionFooter() }
        .toolbar {
            ToolbarItemGroup {
                FilterMenu()
                Button {
                    store.selection = .newAgent
                } label: {
                    Label("New Agent", systemImage: "square.and.pencil")
                }
                .help("Start a new agent (⌘N)")
            }
        }
        .task { await store.loadFolders() }
        .overlay {
            if store.sessions.isEmpty, store.status == .connected {
                ContentUnavailableView(
                    "No agents yet",
                    systemImage: "sparkles",
                    description: Text("Start one with am claude or am codex in a terminal.")
                )
                .padding(.top, 120)
            }
        }
    }
}

/// Every row's mark sits in one column, so every name starts at the same
/// edge, and Agentman's above them.
private enum SidebarMetrics {
    static let mark: CGFloat = 22
    static let gap: CGFloat = 10
    /// Inside the selection's highlight, so a row's contents keep clear of
    /// its edges as they do of its top and bottom.
    static let inset: CGFloat = 5
    /// A section's title starts where the marks do.
    static let header: CGFloat = 7
    /// The list's highlight stops this far from the sidebar's sides, and
    /// Agentman's card does too.
    static let margin: CGFloat = 10
}

/// Agentman, pinned at the top: its mark, what it has to say, and anything
/// new since the user last looked.
private struct AgentmanCard: View {
    @Environment(AgentsStore.self) private var store
    @Environment(\.colorScheme) private var scheme
    @State private var hovering = false

    var body: some View {
        Button {
            store.selection = .manager
        } label: {
            HStack(spacing: 11) {
                AgentmanBadge(size: 32)
                VStack(alignment: .leading, spacing: 2) {
                    Text("Agentman")
                        .font(Fonts.sans(14.5, .medium))
                        .foregroundStyle(Palette.text)
                    Text(summary)
                        .font(Fonts.sans(12))
                        .foregroundStyle(attention ? Palette.needsYouText : Palette.muted)
                        .lineLimit(1)
                }
                Spacer(minLength: 4)
                if unread > 0 {
                    Text(unread > 99 ? "99+" : String(unread))
                        .font(TextStyle.label)
                        .monospacedDigit()
                        .foregroundStyle(.white)
                        .padding(.horizontal, 6)
                        .frame(minWidth: 18, minHeight: 18)
                        .background(Palette.working, in: Capsule())
                        .accessibilityLabel("\(unread) new")
                }
            }
            .padding(.horizontal, 11)
            .padding(.vertical, 10)
            .background(background, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: 12, style: .continuous)
                    .strokeBorder(Color.primary.opacity(selected ? 0.10 : 0.06))
            )
            .contentShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .padding(.horizontal, SidebarMetrics.margin)
        .padding(.top, 6)
        .padding(.bottom, 8)
        .help("Agentman (⌥⌘0)")
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    private var selected: Bool {
        store.selection == .manager || store.selection == nil
    }

    /// Grey like a selected row when it is open; otherwise a card of its
    /// own, white on a light sidebar, so it never passes for the selection.
    private var background: Color {
        if selected { return Color.primary.opacity(0.10) }
        if scheme == .light { return hovering ? Color.white.opacity(0.75) : .white }
        return Color.primary.opacity(hovering ? 0.07 : 0.045)
    }

    /// What arrived since the user last looked at the conversation.
    private var unread: Int {
        store.viewingManager ? 0 : store.manager.unread.count
    }

    private var attention: Bool {
        store.manager.openCards.count > 0 || !store.needsYou.isEmpty
    }

    private var summary: String {
        let cards = store.manager.openCards.count
        if cards > 0 { return cards == 1 ? "1 card waiting for you" : "\(cards) cards waiting for you" }
        let waiting = store.needsYou.count
        let working = store.working.count
        if waiting > 0 { return waiting == 1 ? "1 agent needs you" : "\(waiting) agents need you" }
        if working > 0 { return working == 1 ? "1 agent working" : "\(working) agents working" }
        return "All quiet"
    }
}

/// Agentman's mark: the app icon's prompt, on the brand's ink tile.
struct AgentmanBadge: View {
    var size: CGFloat

    var body: some View {
        RoundedRectangle(cornerRadius: size * 0.28, style: .continuous)
            .fill(Palette.inverse)
            .frame(width: size, height: size)
            .overlay {
                Image(systemName: "chevron.right")
                    .font(.system(size: size * 0.42, weight: .bold))
                    .foregroundStyle(Palette.onInverse)
                    .offset(x: -size * 0.04)
            }
            .overlay(alignment: .topTrailing) {
                Circle()
                    .fill(Palette.needsYou)
                    .frame(width: size * 0.26, height: size * 0.26)
                    .offset(x: -size * 0.12, y: size * 0.12)
            }
            .accessibilityHidden(true)
    }
}

struct AgentRow: View {
    @Environment(AgentsStore.self) private var store
    let session: Session

    var body: some View {
        HStack(alignment: .top, spacing: SidebarMetrics.gap) {
            // Level with the name, however many lines follow it.
            AgentMark(kind: session.kind, size: 16)
                .frame(width: SidebarMetrics.mark, height: 18)
            VStack(alignment: .leading, spacing: 2) {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    if store.renaming == session.id {
                        RenameField(session: session)
                    } else {
                        Text(session.name)
                            .font(Fonts.sans(13.5, session.state == .waitingInput ? .medium : .regular))
                            .foregroundStyle(session.state == .ended ? Palette.muted : Palette.text)
                            .lineLimit(1)
                            .help(session.originalName.map { "Renamed from “\($0)”" } ?? session.name)
                    }
                    Spacer(minLength: 4)
                    if let when = session.lastActivity {
                        Text(Relative.short(when))
                            .font(Fonts.sans(11))
                            .monospacedDigit()
                            .foregroundStyle(Palette.faint)
                    }
                }
                HStack(spacing: 5) {
                    Circle()
                        .fill(Palette.dot(session.state))
                        .frame(width: 6, height: 6)
                    Text(detail)
                        .font(Fonts.sans(11.5))
                        .foregroundStyle(detailColor)
                        .lineLimit(1)
                }
                if let servers = session.servers, !servers.isEmpty {
                    HStack(spacing: 4) {
                        Image(systemName: "globe")
                            .font(.system(size: 9))
                            .foregroundStyle(Palette.ok)
                        Text(servers.map { ":\($0.port)" }.joined(separator: "  "))
                            .font(Fonts.mono(10.5))
                            .foregroundStyle(Palette.muted)
                    }
                    .help("Dev servers it is running")
                }
            }
        }
        .padding(.vertical, 5)
        .padding(.horizontal, SidebarMetrics.inset)
        .accessibilityElement(children: .combine)
    }

    private var stateName: String {
        switch session.state {
        case .waitingInput: "Needs you"
        case .busy: "Working"
        case .idle: "Idle"
        case .ended: "Ended"
        case .unknown: "Unknown"
        }
    }

    private var detail: String {
        if session.state == .waitingInput {
            return session.question?.title ?? session.question?.prompt ?? "Needs you"
        }
        return stateName
    }

    private var detailColor: Color {
        switch session.state {
        case .waitingInput: Palette.needsYouText
        case .busy: Palette.workingText
        default: Palette.muted
        }
    }
}

/// Whether the app can reach `am serve`, and which version it is.
private struct ConnectionFooter: View {
    @Environment(AgentsStore.self) private var store

    var body: some View {
        HStack(spacing: 7) {
            Circle()
                .fill(color)
                .frame(width: 7, height: 7)
            Text(text)
                .font(TextStyle.caption)
                .foregroundStyle(Palette.muted)
                .lineLimit(1)
            Spacer()
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
        // Its own ground, so the list ends above it instead of running under.
        .background(SidebarMaterial())
        .overlay(alignment: .top) { Divider() }
        .help(store.socketPath)
    }

    private var color: Color {
        switch store.status {
        case .connected: Palette.ok
        case .connecting: Palette.faint
        case .unavailable: Palette.error
        }
    }

    private var text: String {
        switch store.status {
        case .connected:
            if let version = store.daemonInfo?.version { return "Mac connected · am \(version)" }
            return "Mac connected"
        case .connecting:
            return "Connecting to am serve…"
        case .unavailable(let reason):
            return "\(reason). Run am serve."
        }
    }
}

/// "now", "4m", "3h", "2d": how long ago, in the fewest characters.
enum Relative {
    static func short(_ date: Date, now: Date = .now) -> String {
        let seconds = max(0, Int(now.timeIntervalSince(date)))
        switch seconds {
        case ..<60: return "now"
        case ..<3600: return "\(seconds / 60)m"
        case ..<86_400: return "\(seconds / 3600)h"
        default: return "\(seconds / 86_400)d"
        }
    }
}

/// A session's name, being edited where it shows: Return keeps it, Escape
/// or clicking away leaves it as it was.
private struct RenameField: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    @State private var name = ""
    @FocusState private var focused: Bool

    var body: some View {
        TextField("Name", text: $name)
            .textFieldStyle(.plain)
            .font(Fonts.sans(13.5))
            .padding(.horizontal, 4)
            .padding(.vertical, 1)
            .background(Palette.surface, in: RoundedRectangle(cornerRadius: 5, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 5, style: .continuous).strokeBorder(Palette.working.opacity(0.6)))
            .focused($focused)
            .onSubmit(save)
            .onExitCommand { store.renaming = nil }
            .onChange(of: focused) { _, now in
                if !now, store.renaming == session.id { store.renaming = nil }
            }
            .onAppear {
                name = session.name
                focused = true
            }
    }

    private func save() {
        let new = name.trimmingCharacters(in: .whitespacesAndNewlines)
        store.renaming = nil
        guard new != session.name else { return }
        Task { await store.rename(session.id, to: new) }
    }
}

/// One button for what the list shows: agents in one state, and in one
/// folder, with how many are in each.
private struct FilterMenu: View {
    @Environment(AgentsStore.self) private var store

    var body: some View {
        let counts = store.stateCounts
        Menu {
            Section("Show") {
                option("All agents", on: store.stateFilter == nil) { store.stateFilter = nil }
                option("Needs you", count: counts.needsYou, on: store.stateFilter == .waitingInput) { store.stateFilter = .waitingInput }
                option("Working", count: counts.working, on: store.stateFilter == .busy) { store.stateFilter = .busy }
                option("Idle", count: counts.idle, on: store.stateFilter == .idle) { store.stateFilter = .idle }
            }
            Section("Folder") {
                option("All folders", on: store.folderFilter == nil) { store.folderFilter = nil }
                ForEach(store.folders.prefix(15)) { folder in
                    option((folder.path as NSString).lastPathComponent, count: folder.agents,
                           on: store.folderFilter == folder.path) { store.folderFilter = folder.path }
                }
            }
        } label: {
            Label("Filter", systemImage: "line.3.horizontal.decrease")
                .foregroundStyle(filtering ? Palette.working : Palette.textSecondary)
        }
        .menuIndicator(.hidden)
        .help("Show agents in one state, or one folder with the sessions that ended there")
    }

    private var filtering: Bool { store.stateFilter != nil || store.folderFilter != nil }

    @ViewBuilder
    private func option(_ title: String, count: Int? = nil, on: Bool, action: @escaping () -> Void) -> some View {
        let text = count.map { "\(title)  \($0)" } ?? title
        Button(action: action) {
            if on { Label(text, systemImage: "checkmark") } else { Text(text) }
        }
    }
}

/// What the list is narrowed to, above it, each undone with its ×.
private struct ActiveFilters: View {
    @Environment(AgentsStore.self) private var store

    var body: some View {
        HStack(spacing: 6) {
            if let folder = store.folderFilter {
                token((folder as NSString).lastPathComponent, icon: "folder") { store.folderFilter = nil }
            }
            if let state = store.stateFilter {
                token(name(state), icon: nil) { store.stateFilter = nil }
            }
            Spacer(minLength: 0)
        }
        .padding(.vertical, 2)
        .padding(.horizontal, SidebarMetrics.inset)
    }

    private func name(_ state: SessionState) -> String {
        switch state {
        case .waitingInput: "Needs you"
        case .busy: "Working"
        default: "Idle"
        }
    }

    private func token(_ title: String, icon: String?, clear: @escaping () -> Void) -> some View {
        Button(action: clear) {
            HStack(spacing: 5) {
                if let icon { Image(systemName: icon).font(.system(size: 10, weight: .medium)) }
                Text(title).lineLimit(1)
                Image(systemName: "xmark").font(.system(size: 8, weight: .bold)).foregroundStyle(Palette.workingText.opacity(0.7))
            }
            .font(TextStyle.caption)
            .foregroundStyle(Palette.workingText)
            .padding(.horizontal, 9)
            .frame(height: 24)
            .background(Palette.workingWash, in: Capsule())
        }
        .buttonStyle(.plain)
        .help("Show every agent again")
    }
}

/// A folder's name over its agents; its menu mutes the folder's
/// notifications.
private struct FolderHeader: View {
    let name: String
    let folder: String
    @State private var muted = false

    var body: some View {
        HStack(spacing: 4) {
            Text(name)
                .font(TextStyle.label)
                .foregroundStyle(Palette.faint)
                .textCase(nil)
            if muted {
                Image(systemName: "bell.slash")
                    .font(.system(size: 8.5))
                    .foregroundStyle(Palette.faint)
                    .help("Notifications from this folder are muted")
            }
        }
        .padding(.leading, SidebarMetrics.header)
        .help(folder)
        .contextMenu {
            Button(muted ? "Unmute Notifications" : "Mute Notifications") {
                NotificationPolicy.setMuted(folder, !muted)
                muted.toggle()
            }
        }
        .onAppear { muted = NotificationPolicy.isMuted(folder) }
    }
}
