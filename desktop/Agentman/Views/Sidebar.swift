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

            ForEach(store.groups) { group in
                Section {
                    ForEach(group.sessions) { session in
                        AgentRow(session: session)
                            .tag(SidebarItem.agent(session.id))
                    }
                } header: {
                    HStack(spacing: 6) {
                        Image(systemName: "folder")
                            .font(.system(size: 10, weight: .semibold))
                        Text(group.name)
                            .font(TextStyle.label)
                        Spacer()
                        Text("\(group.sessions.count)")
                            .font(TextStyle.label)
                            .foregroundStyle(Palette.faint)
                    }
                    .foregroundStyle(Palette.muted)
                    .help(group.id)
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
                    .font(unread > 0 ? Fonts.sans(14, .semibold) : TextStyle.bodyMedium)
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
    let session: Session

    var body: some View {
        HStack(spacing: 9) {
            AgentMark(kind: session.kind, size: 16)
            VStack(alignment: .leading, spacing: 1) {
                Text(session.name)
                    .font(TextStyle.body)
                    .foregroundStyle(Palette.text)
                    .lineLimit(1)
                Text(detail)
                    .font(TextStyle.caption)
                    .foregroundStyle(session.state == .waitingInput ? Palette.needsYouText : Palette.muted)
                    .lineLimit(1)
            }
            Spacer(minLength: 4)
            Circle()
                .fill(Palette.dot(session.state))
                .frame(width: 7, height: 7)
                .accessibilityLabel(stateName)
        }
        .padding(.vertical, 2)
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
