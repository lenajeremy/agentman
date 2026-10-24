import SwiftUI

/// Agentman's place in the window. Until the manager can talk (the next
/// phase), it is the overview the chat will grow out of: who needs you,
/// who is working, and the way to each of them.
struct ManagerHomeView: View {
    @Environment(AgentsStore.self) private var store

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 28) {
                VStack(alignment: .leading, spacing: 8) {
                    AgentmanBadge(size: 40)
                    Text(greeting)
                        .font(TextStyle.display)
                        .foregroundStyle(Palette.text)
                        .padding(.top, 6)
                    Text(summary)
                        .font(TextStyle.body)
                        .foregroundStyle(Palette.muted)
                }

                HStack(spacing: 12) {
                    CountTile(count: store.needsYou.count, label: "Need you", tint: Palette.needsYou)
                    CountTile(count: store.working.count, label: "Working", tint: Palette.working)
                    CountTile(count: store.idle.count, label: "Idle", tint: Palette.faint)
                }

                if !store.needsYou.isEmpty {
                    HomeSection(title: "Waiting on you") {
                        ForEach(store.needsYou) { session in
                            WaitingRow(session: session)
                        }
                    }
                }

                if !store.working.isEmpty {
                    HomeSection(title: "Working now") {
                        ForEach(store.working) { session in
                            AgentLinkRow(session: session)
                        }
                    }
                }

                ComingSoon()
            }
            .padding(.horizontal, 40)
            .padding(.vertical, 44)
            .frame(maxWidth: 760, alignment: .leading)
            .frame(maxWidth: .infinity)
        }
    }

    private var greeting: String {
        let hour = Calendar.current.component(.hour, from: .now)
        switch hour {
        case 5..<12: return "Good morning"
        case 12..<18: return "Good afternoon"
        default: return "Good evening"
        }
    }

    private var summary: String {
        switch store.status {
        case .unavailable(let reason): return "\(reason). Start it with am serve, and your agents appear here."
        case .connecting: return "Connecting to your Mac…"
        case .connected:
            let total = store.sessions.count
            if total == 0 { return "No agents running. Start one with am claude or am codex." }
            let waiting = store.needsYou.count
            let agents = total == 1 ? "1 agent" : "\(total) agents"
            if waiting == 0 { return "\(agents) running. Nothing needs you." }
            return "\(agents) running. \(waiting == 1 ? "One needs" : "\(waiting) need") you."
        }
    }
}

private struct HomeSection<Content: View>: View {
    let title: String
    @ViewBuilder var content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(title)
                .font(TextStyle.label)
                .foregroundStyle(Palette.muted)
            VStack(spacing: 0) { content }
                .background(Palette.surface, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 16, style: .continuous).strokeBorder(Palette.line))
        }
    }
}

private struct CountTile: View {
    let count: Int
    let label: String
    let tint: Color

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("\(count)")
                .font(Fonts.sans(26, .semibold))
                .foregroundStyle(count > 0 ? tint : Palette.faint)
                .contentTransition(.numericText())
            Text(label)
                .font(TextStyle.caption)
                .foregroundStyle(Palette.muted)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(16)
        .background(Palette.surface, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 16, style: .continuous).strokeBorder(Palette.line))
    }
}

/// An agent blocked on you, with what it is asking and the way to it.
private struct WaitingRow: View {
    @Environment(AgentsStore.self) private var store
    let session: Session

    var body: some View {
        Button {
            store.selection = .agent(session.id)
        } label: {
            HStack(spacing: 12) {
                AgentMark(kind: session.kind, size: 20)
                VStack(alignment: .leading, spacing: 2) {
                    Text(session.name)
                        .font(TextStyle.bodyMedium)
                        .foregroundStyle(Palette.text)
                    Text(session.question?.detail ?? session.question?.prompt ?? "Waiting on your answer")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.needsYouText)
                        .lineLimit(2)
                }
                Spacer()
                Text("Answer")
                    .font(TextStyle.label)
                    .foregroundStyle(Palette.onInverse)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 5)
                    .background(Palette.inverse, in: Capsule())
            }
            .padding(14)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

private struct AgentLinkRow: View {
    @Environment(AgentsStore.self) private var store
    let session: Session

    var body: some View {
        Button {
            store.selection = .agent(session.id)
        } label: {
            HStack(spacing: 12) {
                AgentMark(kind: session.kind, size: 18)
                Text(session.name)
                    .font(TextStyle.body)
                    .foregroundStyle(Palette.text)
                Text(AgentsStore.folderName(session.cwd))
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
                Spacer()
                if let when = session.lastActivity {
                    Text(Relative.short(when))
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.faint)
                }
                Image(systemName: "chevron.right")
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(Palette.faint)
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 11)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

/// Where the chat goes, said plainly rather than shown as a box that does
/// nothing.
private struct ComingSoon: View {
    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "bubble.left.and.text.bubble.right")
                .foregroundStyle(Palette.working)
            VStack(alignment: .leading, spacing: 3) {
                Text("Chat with Agentman is next")
                    .font(TextStyle.bodyMedium)
                    .foregroundStyle(Palette.text)
                Text("One conversation that reads every agent for you, briefs you on what happened, and answers the easy questions itself.")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Palette.workingWash, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
    }
}
