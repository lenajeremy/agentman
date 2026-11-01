import SwiftUI

/// am in Terminal: whether a terminal finds it and where from, and Install
/// to put the app's copy on PATH when it doesn't.
struct CommandLineSection: View {
    @Environment(AgentsStore.self) private var store

    var body: some View {
        let setup = store.setup
        CommandLineRows(command: setup.commandStatus(), tmuxMissing: setup.tmuxMissing) {
            Task { await setup.installCommand() }
        }
        .task { await setup.lookAtShell() }
    }
}

/// The section's rows, from what they say: a view of its own so pictures can
/// show each state.
struct CommandLineRows: View {
    let command: DaemonSetup.CommandStatus
    /// tmux isn't installed: a row says so, and how to install it.
    var tmuxMissing = false
    var install: () -> Void = {}
    @State private var copied = false

    var body: some View {
        SettingsSection(title: "Command line") {
            if tmuxMissing {
                SettingsRow(title: "tmux isn't installed", detail: TmuxNote.text) {
                    Button(copied ? "Copied" : "Copy Command") {
                        TmuxNote.copy()
                        copied = true
                    }
                }
            }
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

/// Agents started in a terminal (am claude, am codex…) run inside tmux, so
/// the daemon can type into them. The app doesn't carry or install it; it
/// says when it is missing, and how to get it.
struct TmuxNote: View {
    static let command = "brew install tmux"
    static let text = "Agents you start in a terminal, such as with am claude, run inside tmux. Install it with \(command)."

    @State private var copied = false

    static func copy() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(command, forType: .string)
    }

    var body: some View {
        HStack(alignment: .center, spacing: 10) {
            Image(systemName: "terminal")
                .font(.system(size: 13, weight: .medium))
                .foregroundStyle(Palette.needsYouText)
            VStack(alignment: .leading, spacing: 2) {
                Text("tmux isn't installed")
                    .font(TextStyle.captionMedium)
                    .foregroundStyle(Palette.text)
                Text(Self.text)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.textSecondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 8)
            SmallButton(title: copied ? "Copied" : "Copy", symbol: copied ? "checkmark" : "doc.on.doc") {
                Self.copy()
                copied = true
            }
        }
        .padding(12)
        .background(Palette.needsYouWash, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Palette.needsYouEdge))
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
