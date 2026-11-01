import SwiftUI

/// am in Terminal: whether a terminal finds it and where from, and Install
/// to put the app's copy on PATH when it doesn't.
struct CommandLineSection: View {
    @Environment(AgentsStore.self) private var store

    var body: some View {
        let setup = store.setup
        CommandLineRows(command: setup.commandStatus()) { Task { await setup.installCommand() } }
            .task { await setup.lookAtShell() }
    }
}

/// The section's rows, from what they say: a view of its own so pictures can
/// show each state.
struct CommandLineRows: View {
    let command: DaemonSetup.CommandStatus
    var install: () -> Void = {}

    var body: some View {
        SettingsSection(title: "Command line") {
            SettingsRow(title: "am in Terminal", detail: command.detail, divider: command.problem != nil) {
                if command.busy {
                    Loader(size: 10)
                } else if command.installed {
                    Label("Installed", systemImage: "checkmark.circle.fill")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                        .labelStyle(InstalledLabel())
                } else if command.installTo != nil {
                    Button("Install", action: install)
                }
            }
            if let problem = command.problem {
                Text(problem)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.errorText)
                    .fixedSize(horizontal: false, vertical: true)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 18)
                    .padding(.vertical, 12)
            }
        }
    }
}

/// A check in Palette.ok before muted words.
private struct InstalledLabel: LabelStyle {
    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: 5) {
            configuration.icon.foregroundStyle(Palette.ok)
            configuration.title
        }
    }
}

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
