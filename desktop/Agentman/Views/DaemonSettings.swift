import SwiftUI

/// am serve, in Settings' About: its version and where it runs from, and an
/// Upgrade button when it is behind. A daemon the app runs is upgraded from
/// here; for one it doesn't, such as Homebrew's, the command to run in a
/// terminal is a click away, as before.
struct DaemonServeRow: View {
    @Environment(AgentsStore.self) private var store
    @State private var copied = false

    var body: some View {
        let setup = store.setup
        let status = setup.serveStatus(daemon: store.daemonInfo, connected: store.status == .connected)
        SettingsRow(title: "am serve", detail: status.detail, divider: false) {
            HStack(spacing: 8) {
                if let busy = status.busy {
                    Loader(size: 10)
                    Text(busy)
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                } else {
                    Text(status.version)
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                    switch status.action {
                    case .setUp(let label):
                        Button(label) { Task { await setup.run() } }
                    case .upgrade:
                        Button("Upgrade") { Task { await setup.upgrade() } }
                    case .copy(let command):
                        Button(copied ? "Copied" : "Copy upgrade command") {
                            NSPasteboard.general.clearContents()
                            NSPasteboard.general.setString(command, forType: .string)
                            copied = true
                        }
                    case nil:
                        EmptyView()
                    }
                }
            }
        }
        .task { await setup.readVersions() }
        if let problem = status.problem, status.busy == nil {
            Label {
                Text(problem)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.errorText)
                    .fixedSize(horizontal: false, vertical: true)
                    .textSelection(.enabled)
            } icon: {
                Image(systemName: "exclamationmark.triangle.fill")
                    .font(.system(size: 11))
                    .foregroundStyle(Palette.errorText)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 18)
            .padding(.bottom, 14)
        }
    }
}
