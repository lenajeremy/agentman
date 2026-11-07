import SwiftUI

/// Across the top of the window while the app can't reach am serve: what
/// that means, and the way back. Before, the only sign was a line cut off
/// in the sidebar's footer, while the message box still took messages that
/// couldn't go anywhere.
struct ConnectionBanner: View {
    @Environment(AgentsStore.self) private var store
    @State private var restarting = false
    /// Why a restart didn't bring it back.
    @State private var problem: String?

    var body: some View {
        if case .unavailable(let reason) = store.status {
            HStack(alignment: .top, spacing: 12) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .font(.system(size: 13))
                    .foregroundStyle(Palette.error)
                    .padding(.top, 2)
                VStack(alignment: .leading, spacing: 3) {
                    Text("Agentman isn't connected")
                        .font(Fonts.heading(13))
                        .foregroundStyle(Palette.text)
                    Text(problem ?? Self.explanation(canRestart: store.setup.allowed))
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                        .fixedSize(horizontal: false, vertical: true)
                    Text(reason)
                        .font(TextStyle.label)
                        .foregroundStyle(Palette.faint)
                        .lineLimit(1)
                        .truncationMode(.tail)
                        .help(reason)
                }
                Spacer(minLength: 8)
                if store.setup.allowed {
                    if problem != nil {
                        SmallButton(title: "Set Up Again") { store.setup.runOnScreen() }
                    }
                    SmallButton(title: restarting ? "Restarting…" : "Restart", primary: true) { restart() }
                        .disabled(restarting)
                }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 11)
            .frame(maxWidth: 620)
            .background(Palette.surface, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(Palette.error.opacity(0.35)))
            .shadow(color: .black.opacity(0.08), radius: 10, y: 3)
            .padding(.horizontal, 20)
            .transition(.move(edge: .top).combined(with: .opacity))
        }
    }

    /// What being disconnected means, and what to do where the app can't
    /// restart it itself (am serve started from a terminal).
    nonisolated static func explanation(canRestart: Bool) -> String {
        canRestart
            ? "Your agents keep running, but you can't see or message them from here until it's back."
            : "Your agents keep running, but you can't see or message them from here. Start it again with am serve in a terminal."
    }

    private func restart() {
        restarting = true
        problem = nil
        Task {
            problem = await DaemonRestart.restart(canSetUp: store.setup.allowed)
            restarting = false
        }
    }
}
