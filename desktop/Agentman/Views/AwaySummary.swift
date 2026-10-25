import SwiftUI

/// "While you were away": a panel on the user's return with each agent that
/// did something, in a line or two, in the manner of Linear's Pulse and the
/// update panels in Turo and Sketch. Clicking an agent opens it.
struct AwaySummary: View {
    @Environment(AgentsStore.self) private var store
    let digest: AwayDigest

    var body: some View {
        ZStack {
            Color.black.opacity(0.18)
                .ignoresSafeArea()
                .onTapGesture(perform: close)
            VStack(alignment: .leading, spacing: 0) {
                HStack(alignment: .firstTextBaseline) {
                    VStack(alignment: .leading, spacing: 4) {
                        Text("While you were away")
                            .font(TextStyle.title)
                            .foregroundStyle(Palette.text)
                        Text(subtitle)
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.muted)
                    }
                    Spacer()
                    Button(action: close) {
                        Image(systemName: "xmark")
                            .font(.system(size: 10, weight: .bold))
                            .foregroundStyle(Palette.muted)
                            .frame(width: 24, height: 24)
                            .background(Palette.fill, in: Circle())
                    }
                    .buttonStyle(.plain)
                    .keyboardShortcut(.cancelAction)
                    .help("Close")
                }
                .padding(.horizontal, 20)
                .padding(.top, 18)
                .padding(.bottom, 14)
                Divider()
                ScrollView {
                    VStack(spacing: 2) {
                        ForEach(digest.agents) { agent in
                            AwayRow(agent: agent) {
                                store.selection = .agent(agent.id)
                                close()
                            }
                        }
                    }
                    .padding(8)
                }
                .frame(maxHeight: 420)
                .fixedSize(horizontal: false, vertical: true)
                Divider()
                HStack {
                    if store.manager.settings?.keySet == true {
                        Button {
                            let time = digest.since.formatted(date: .omitted, time: .shortened)
                            store.selection = .manager
                            Task { _ = await store.manager.send("Catch me up: what did my agents do since \(time), and what needs me?") }
                            close()
                        } label: {
                            Label("Ask Agentman for more", systemImage: "sparkles")
                        }
                        .buttonStyle(.link)
                        .help("Agentman reads the agents' work and writes you a summary")
                    }
                    Spacer()
                    Button("Done", action: close)
                        .keyboardShortcut(.defaultAction)
                }
                .font(TextStyle.caption)
                .padding(.horizontal, 20)
                .padding(.vertical, 12)
            }
            .frame(width: 560)
            .background(Palette.surface, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 18, style: .continuous).strokeBorder(Palette.line))
            .shadow(color: .black.opacity(0.2), radius: 40, y: 16)
        }
        .transition(.opacity)
    }

    private var subtitle: String {
        var parts = [Self.duration(digest.until.timeIntervalSince(digest.since))]
        let worked = digest.agents.count
        parts.append(worked == 1 ? "1 agent worked" : "\(worked) agents worked")
        if digest.approvedCount > 0 {
            parts.append("Agentman approved \(digest.approvedCount)")
        }
        if digest.waitingCount > 0 {
            parts.append(digest.waitingCount == 1 ? "1 needs you" : "\(digest.waitingCount) need you")
        }
        return parts.joined(separator: " · ")
    }

    /// "45 min", "1 h 20 min", "3 h".
    static func duration(_ seconds: TimeInterval) -> String {
        let minutes = max(1, Int(seconds / 60))
        if minutes < 60 { return "\(minutes) min away" }
        let hours = minutes / 60, rest = minutes % 60
        return rest == 0 ? "\(hours) h away" : "\(hours) h \(rest) min away"
    }

    private func close() {
        withAnimation(.easeOut(duration: 0.15)) { store.away.digest = nil }
    }
}

private struct AwayRow: View {
    let agent: AwayDigest.Agent
    let open: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: open) {
            HStack(alignment: .top, spacing: 12) {
                AgentMark(kind: agent.kind, size: 18)
                    .padding(.top, 1)
                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 6) {
                        Text(agent.name)
                            .font(TextStyle.bodyMedium)
                            .foregroundStyle(Palette.text)
                            .lineLimit(1)
                        Text((agent.folder as NSString).lastPathComponent)
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.faint)
                            .lineLimit(1)
                        Spacer(minLength: 6)
                        if agent.waiting != nil {
                            Text("Needs you")
                                .font(TextStyle.captionMedium)
                                .foregroundStyle(Palette.needsYouText)
                                .padding(.horizontal, 7)
                                .padding(.vertical, 2)
                                .background(Palette.needsYouWash, in: Capsule())
                        }
                    }
                    if let waiting = agent.waiting {
                        Text(waiting)
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.needsYouText)
                            .lineLimit(2)
                    }
                    if let summary = agent.summary {
                        Text(summary)
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.textSecondary)
                            .lineLimit(3)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    if !stats.isEmpty {
                        Text(stats)
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.faint)
                    }
                }
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            .background(hovering ? Palette.fill : .clear, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help("Open \(agent.name)")
    }

    private var stats: String {
        var parts: [String] = []
        if agent.finished > 0 { parts.append(agent.finished == 1 ? "1 turn finished" : "\(agent.finished) turns finished") }
        if agent.approved > 0 { parts.append(agent.approved == 1 ? "1 request approved by Agentman" : "\(agent.approved) requests approved by Agentman") }
        return parts.joined(separator: " · ")
    }
}
