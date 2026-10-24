import SwiftUI

/// Agentman first, then every agent, grouped by the folder it works in.
struct Sidebar: View {
    @Environment(AgentsStore.self) private var store

    var body: some View {
        @Bindable var store = store
        List(selection: $store.selection) {
            AgentmanRow()
                .tag(SidebarItem.manager)
                .padding(.vertical, 4)
            Label {
                Text("New agent")
                    .font(Fonts.sans(13.5))
                    .foregroundStyle(Palette.text)
            } icon: {
                Image(systemName: "square.and.pencil")
                    .foregroundStyle(Palette.muted)
            }
            .tag(SidebarItem.newAgent)
            .padding(.vertical, 2)

            SidebarFilters()
                .listRowSeparator(.hidden)

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
                    Text(group.name)
                        .font(TextStyle.label)
                        .foregroundStyle(Palette.faint)
                        .textCase(nil)
                        .help(group.id)
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
                }
            }
        }
        .listStyle(.sidebar)
        .searchable(text: $store.search, placement: .sidebar, prompt: "Search agents")
        .safeAreaInset(edge: .bottom) { ConnectionFooter() }
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

/// The pinned row: the manager itself.
private struct AgentmanRow: View {
    @Environment(AgentsStore.self) private var store

    var body: some View {
        HStack(spacing: 10) {
            AgentmanBadge(size: 28)
            VStack(alignment: .leading, spacing: 1) {
                Text("Agentman")
                    .font(unread > 0 ? Fonts.sans(14, .bold) : TextStyle.bodyMedium)
                    .foregroundStyle(Palette.text)
                Text(summary)
                    .font(TextStyle.caption)
                    .foregroundStyle(store.needsYou.isEmpty ? Palette.muted : Palette.needsYouText)
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
    }

    /// What arrived since the user last looked at the conversation.
    private var unread: Int {
        store.viewingManager ? 0 : store.manager.unread.count
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
        HStack(spacing: 9) {
            AgentMark(kind: session.kind, size: 15)
            VStack(alignment: .leading, spacing: 1) {
                if store.renaming == session.id {
                    RenameField(session: session)
                } else {
                    Text(session.name)
                        .font(Fonts.sans(13.5))
                        .foregroundStyle(Palette.text)
                        .lineLimit(1)
                        .help(session.originalName.map { "Renamed from “\($0)”" } ?? session.name)
                }
                Text(detail)
                    .font(Fonts.sans(11.5))
                    .foregroundStyle(session.state == .waitingInput ? Palette.needsYouText : Palette.muted)
                    .lineLimit(1)
            }
            Spacer(minLength: 4)
            Circle()
                .fill(Palette.dot(session.state))
                .frame(width: 6, height: 6)
                .accessibilityLabel(stateName)
        }
        .padding(.vertical, 3)
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
        guard let when = session.lastActivity else { return stateName }
        return "\(stateName) · \(Relative.short(when))"
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

/// The folder the list shows, and its statuses to narrow it to: Needs you,
/// Working, Idle, each only when something is in it.
private struct SidebarFilters: View {
    @Environment(AgentsStore.self) private var store

    var body: some View {
        let counts = store.stateCounts
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                Menu {
                    Button {
                        store.folderFilter = nil
                    } label: {
                        if store.folderFilter == nil { Label("All folders", systemImage: "checkmark") } else { Text("All folders") }
                    }
                    Divider()
                    ForEach(store.folders.prefix(15)) { folder in
                        Button {
                            store.folderFilter = folder.path
                        } label: {
                            let title = "\((folder.path as NSString).lastPathComponent)  ·  \(folder.agents)"
                            if store.folderFilter == folder.path { Label(title, systemImage: "checkmark") } else { Text(title) }
                        }
                    }
                } label: {
                    chip(store.folderFilter.map { ($0 as NSString).lastPathComponent } ?? "All folders",
                         icon: "folder", on: store.folderFilter != nil)
                }
                .menuStyle(.button)
                .buttonStyle(.plain)
                .menuIndicator(.hidden)
                .fixedSize()
                .help("Show one folder, with the sessions that ended there")

                stateChip(.waitingInput, "needs you", counts.needsYou, tint: Palette.needsYouText)
                stateChip(.busy, "working", counts.working, tint: Palette.workingText)
                stateChip(.idle, "idle", counts.idle, tint: Palette.muted)
            }
            .padding(.vertical, 2)
        }
        .task { await store.loadFolders() }
    }

    @ViewBuilder private func stateChip(_ state: SessionState, _ label: String, _ count: Int, tint: Color) -> some View {
        if count > 0 || store.stateFilter == state {
            Button {
                store.stateFilter = store.stateFilter == state ? nil : state
            } label: {
                HStack(spacing: 4) {
                    Text("\(count)").foregroundStyle(store.stateFilter == state ? Palette.onInverse : tint)
                    Text(label)
                }
                .font(TextStyle.caption)
                .foregroundStyle(store.stateFilter == state ? Palette.onInverse : Palette.textSecondary)
                .padding(.horizontal, 9)
                .frame(height: 24)
                .background(store.stateFilter == state ? Palette.inverse : Palette.fill, in: Capsule())
            }
            .buttonStyle(.plain)
            .help(store.stateFilter == state ? "Show every agent" : "Show only these")
        }
    }

    private func chip(_ title: String, icon: String, on: Bool) -> some View {
        HStack(spacing: 5) {
            Image(systemName: icon).font(.system(size: 10, weight: .medium))
            Text(title).lineLimit(1)
            Image(systemName: "chevron.down").font(.system(size: 7, weight: .bold)).foregroundStyle(Palette.faint)
        }
        .font(TextStyle.caption)
        .foregroundStyle(on ? Palette.workingText : Palette.textSecondary)
        .padding(.horizontal, 9)
        .frame(height: 24)
        .background(on ? Palette.workingWash : Palette.fill, in: Capsule())
    }
}
