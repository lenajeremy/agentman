import AppKit
import SwiftUI

/// Starting an agent: what it should work on, which agent, and where, in one
/// box, the way Codex and Cursor start a task. Return starts it.
struct NewAgentView: View {
    @Environment(AgentsStore.self) private var store
    @AppStorage("newAgentKind") private var kind = "claude"
    @AppStorage("newAgentFolder") private var folder = ""
    @State private var message = ""
    @State private var focused = true
    @State private var starting = false
    @State private var problem: String?

    static let kinds = ["claude", "codex", "cursor-cli", "opencode", "kiro", "antigravity"]

    var body: some View {
        VStack(spacing: 22) {
            Spacer(minLength: 40)
            Text("What should we work on?")
                .font(TextStyle.display)
                .foregroundStyle(Palette.text)
            VStack(alignment: .leading, spacing: 8) {
                MessageField(
                    text: $message,
                    placeholder: "Describe the task, or leave it empty to start a session",
                    fontSize: 15,
                    enabled: !starting,
                    focused: $focused,
                    onSubmit: start
                )
                .padding(.horizontal, 8)
                .padding(.top, 6)
                HStack(spacing: 6) {
                    agentMenu
                    folderMenu
                    Spacer()
                    Button(action: start) {
                        Group {
                            if starting {
                                ProgressView().controlSize(.small)
                            } else {
                                Image(systemName: "arrow.up")
                                    .font(.system(size: 13, weight: .bold))
                                    .foregroundStyle(Palette.onInverse)
                            }
                        }
                        .frame(width: 32, height: 32)
                        .background(Palette.inverse.opacity(canStart ? 1 : 0.25), in: Circle())
                    }
                    .buttonStyle(.plain)
                    .disabled(!canStart)
                    .help("Start \(AgentName.of(kind))")
                }
            }
            .padding(10)
            .background(Palette.surface, in: RoundedRectangle(cornerRadius: 24, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 24, style: .continuous).strokeBorder(Palette.line))
            .shadow(color: .black.opacity(0.06), radius: 18, y: 6)
            .frame(maxWidth: 640)

            if let problem {
                Text(problem)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.errorText)
            } else if !folder.isEmpty {
                Text(starting ? "Starting \(AgentName.of(kind)) in \(shortened(folder))…" : "Starts in \(shortened(folder))")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
            }
            Spacer(minLength: 80)
        }
        .padding(.horizontal, 32)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Palette.paper)
        .navigationTitle("New agent")
        .task {
            await store.loadFolders()
            if folder.isEmpty || !FileManager.default.fileExists(atPath: folder) {
                folder = store.folders.first?.path ?? ""
            }
        }
    }

    private var canStart: Bool { !starting && !folder.isEmpty }

    private var agentMenu: some View {
        Menu {
            ForEach(Self.kinds, id: \.self) { option in
                Button {
                    kind = option
                } label: {
                    if option == kind {
                        Label(AgentName.of(option), systemImage: "checkmark")
                    } else {
                        Text(AgentName.of(option))
                    }
                }
            }
        } label: {
            Pill {
                AgentMark(kind: kind, size: 14)
                Text(AgentName.of(kind))
            }
        }
        .menuStyle(.button)
        .buttonStyle(.plain)
        .menuIndicator(.hidden)
        .fixedSize()
        .help("Which agent to start")
    }

    private var folderMenu: some View {
        Menu {
            Section("Recent") {
                ForEach(store.folders.prefix(10)) { recent in
                    Button {
                        folder = recent.path
                    } label: {
                        if recent.path == folder {
                            Label(shortened(recent.path), systemImage: "checkmark")
                        } else {
                            Text(shortened(recent.path))
                        }
                    }
                }
            }
            Divider()
            Button("Choose a folder…", action: choose)
        } label: {
            Pill {
                Image(systemName: "folder")
                    .font(.system(size: 11, weight: .medium))
                Text(folder.isEmpty ? "Choose a folder" : (folder as NSString).lastPathComponent)
            }
        }
        .menuStyle(.button)
        .buttonStyle(.plain)
        .menuIndicator(.hidden)
        .fixedSize()
        .help(folder.isEmpty ? "Where it works" : folder)
    }

    private func choose() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.directoryURL = URL(fileURLWithPath: folder.isEmpty ? NSHomeDirectory() : folder)
        panel.message = "Choose where the agent works"
        panel.prompt = "Choose"
        if panel.runModal() == .OK, let url = panel.url {
            folder = url.path
        }
    }

    private func start() {
        guard canStart else { return }
        starting = true
        problem = nil
        Task {
            problem = await store.startAgent(kind: kind, folder: folder, message: message)
            starting = false
            if problem == nil { message = "" }
        }
    }

    private func shortened(_ path: String) -> String {
        path.replacingOccurrences(of: NSHomeDirectory(), with: "~")
    }
}

/// A rounded choice in a message box's bottom row.
struct Pill<Content: View>: View {
    @ViewBuilder var content: Content

    var body: some View {
        HStack(spacing: 6) {
            content
            Image(systemName: "chevron.down")
                .font(.system(size: 8, weight: .bold))
                .foregroundStyle(Palette.faint)
        }
        .font(TextStyle.caption)
        .foregroundStyle(Palette.textSecondary)
        .padding(.horizontal, 10)
        .frame(height: 30)
        .background(Palette.fill, in: Capsule())
    }
}
