import SwiftUI

/// Agentman's news while the user is elsewhere in the app, small in the
/// window's bottom-right corner over whatever is open: an agent waiting on
/// them, one that finished something worth knowing, or Agentman's reply.
/// Newest lowest. News fades on its own unless the pointer rests on it; a
/// card stays until it is answered, answered anywhere else, or put away.
struct AgentmanToasts: View {
    @Environment(AgentsStore.self) private var store
    /// Room left at the window's foot for a conversation's message box.
    static let clearance: CGFloat = 132

    var body: some View {
        VStack(alignment: .trailing, spacing: 8) {
            ForEach(store.toasts) { toast in
                ToastView(toast: toast)
                    .transition(.move(edge: .trailing).combined(with: .opacity))
            }
        }
        .padding(.trailing, 16)
        // Above the message box, whose send button they would cover.
        .padding(.bottom, Self.clearance)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomTrailing)
        .animation(.spring(duration: 0.3, bounce: 0.15), value: store.toasts.map(\.id))
    }
}

private struct ToastView: View {
    @Environment(AgentsStore.self) private var store
    let toast: AgentsStore.Toast
    @State private var hovering = false

    /// How long news stays without the pointer on it.
    static let fades: Duration = .seconds(8)

    var body: some View {
        let entry = store.manager.entry(toast.id)
        Group {
            if let entry, let card = entry.card, card.isOpen {
                CompactCard(card: card, closeHelp: "Hide; it stays in Agentman's chat") {
                    store.dismissToast(toast.id)
                    return nil
                }
            } else if let entry, entry.card == nil {
                NewsToast(entry: entry) { store.dismissToast(toast.id) }
            }
        }
        .frame(width: 340)
        .background(Palette.surface, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 14, style: .continuous).strokeBorder(Palette.line))
        .shadow(color: .black.opacity(0.14), radius: 14, y: 6)
        .onHover { hovering = $0 }
        // News goes after a while, unless the pointer rests on it.
        .task(id: hovering) {
            guard !toast.card, !hovering else { return }
            try? await Task.sleep(for: Self.fades)
            guard !Task.isCancelled else { return }
            store.dismissToast(toast.id)
        }
        // A card answered or put away anywhere takes its toast with it.
        .onChange(of: entry?.card?.isOpen) { _, open in
            if open == false { store.dismissToast(toast.id) }
        }
    }
}

/// An agent's news, or Agentman's reply, in a few lines: a click opens it.
private struct NewsToast: View {
    @Environment(AgentsStore.self) private var store
    let entry: ManagerEntry
    let close: () -> Void

    var body: some View {
        let session = entry.sessionId.flatMap { store.session($0) }
        Button(action: open) {
            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 6) {
                    if let session {
                        AgentMark(kind: session.kind, size: 14)
                        Text(session.name)
                            .font(TextStyle.captionMedium)
                            .foregroundStyle(Palette.text)
                            .lineLimit(1)
                    } else {
                        AgentmanBadge(size: 14)
                        Text("Agentman")
                            .font(TextStyle.captionMedium)
                            .foregroundStyle(Palette.text)
                    }
                    if entry.role == "agent" {
                        Text("says")
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.muted)
                    }
                    Spacer(minLength: 6)
                    Button(action: close) {
                        Image(systemName: "xmark")
                            .font(.system(size: 9, weight: .semibold))
                            .foregroundStyle(Palette.muted)
                            .frame(width: 18, height: 18)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .help("Hide")
                }
                Text(Notifier.plain(entry.text))
                    .font(Fonts.sans(13))
                    .foregroundStyle(Palette.textSecondary)
                    .lineLimit(3)
                    .multilineTextAlignment(.leading)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .padding(12)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(entry.sessionId == nil ? "Open Agentman" : "Open \(session?.name ?? "the agent")")
    }

    private func open() {
        if let id = entry.sessionId, store.session(id) != nil {
            store.selection = .agent(id)
        } else {
            store.selection = .manager
        }
        close()
    }
}
