import AppKit
import SwiftUI

/// Starting an agent: what it should work on, which agent, and where, in one
/// box, the way Codex and Cursor start a task. Return starts it. Where can be
/// a worktree of the folder, one already made or a new one, so several
/// agents can work on one repository at once.
struct NewAgentView: View {
    @Environment(AgentsStore.self) private var store
    @AppStorage("newAgentKind") private var kind = "claude"
    @AppStorage("newAgentFolder") private var folder = ""
    @State private var message = ""
    @State private var focused = true
    @State private var starting = false
    @State private var problem: String?
    /// Where in the folder it works.
    @State private var place: Place = .folder
    /// The folder's worktrees, its own first; none when it isn't in a repository.
    @State private var worktrees: [Worktree] = []
    /// Naming a new worktree, in a popover from the where menu.
    @State private var naming = false
    /// Where it starts out, for screenshots of each choice.
    var startingPlace: Place?

    enum Place: Equatable {
        case folder
        case worktree(Worktree)
        /// Made, with this name, when the agent starts.
        case newWorktree(String)
    }

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
                    whereMenu
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
            .background {
                RoundedRectangle(cornerRadius: 24, style: .continuous)
                    .fill(Palette.surface)
                    .onTapGesture { focused = true }
            }
            .overlay(RoundedRectangle(cornerRadius: 24, style: .continuous).strokeBorder(Palette.line))
            .shadow(color: .black.opacity(0.06), radius: 18, y: 6)
            .frame(maxWidth: 640)

            if let problem {
                Text(problem)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.errorText)
            } else if !folder.isEmpty {
                Text(starting ? "Starting \(AgentName.of(kind)) \(whereItStarts)…" : "Starts \(whereItStarts)")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
                    .multilineTextAlignment(.center)
            }
            Spacer(minLength: 80)
        }
        .padding(.horizontal, 32)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Palette.paper)
        .navigationTitle("New agent")
        .task {
            applyPreset()
            await store.loadFolders()
            if folder.isEmpty || !FileManager.default.fileExists(atPath: folder) {
                folder = store.folders.first?.path ?? ""
            }
        }
        .task(id: folder) {
            await loadWorktrees()
            if let startingPlace { place = startingPlace }
        }
        .onChange(of: store.newAgentPreset) { _, _ in applyPreset() }
    }

    private var canStart: Bool { !starting && !folder.isEmpty }

    /// The repository's name, as its worktrees know it.
    private var repo: String { worktrees.first?.repo ?? (folder as NSString).lastPathComponent }

    /// Where the agent will start, in words.
    private var whereItStarts: String {
        switch place {
        case .folder:
            return "in \(shortened(folder))"
        case .worktree(let worktree):
            return "in the \(worktree.name) worktree, on \(worktree.branch ?? "a detached HEAD")"
        case .newWorktree(let name):
            let from = worktrees.first { $0.path == folder }?.branch.map { "from \($0)" } ?? "from what \(shortened(folder)) has checked out"
            return "in a new worktree, ~/Agentman/Worktrees/\(repo)/\(name.replacingOccurrences(of: "/", with: "-")), on a new branch \(name) \(from)"
        }
    }

    /// A worktree's name as its branch, as the daemon makes it.
    static func branchName(_ name: String) -> String {
        name.trimmingCharacters(in: .whitespaces)
            .replacingOccurrences(of: "\\s+", with: "-", options: .regularExpression)
            .trimmingCharacters(in: CharacterSet(charactersIn: "-/"))
    }

    private func applyPreset() {
        guard let preset = store.newAgentPreset else { return }
        store.newAgentPreset = nil
        folder = preset.folder
        place = .folder
        naming = preset.newWorktree
    }

    private func loadWorktrees() async {
        guard !folder.isEmpty, let list = try? await store.listWorktrees(in: folder) else {
            worktrees = []
            if place != .folder { place = .folder }
            naming = false
            return
        }
        worktrees = list
        if case .worktree(let chosen) = place, !list.contains(where: { $0.path == chosen.path }) { place = .folder }
    }

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

    /// Where it works: a recent folder, and in a repository, one of its
    /// worktrees or a new one, named in a popover.
    private var whereMenu: some View {
        Menu {
            Section("Recent") {
                ForEach(store.folders.prefix(10)) { recent in
                    Button {
                        folder = recent.path
                        place = .folder
                    } label: {
                        if recent.path == folder && place == .folder {
                            Label(shortened(recent.path), systemImage: "checkmark")
                        } else {
                            Text(shortened(recent.path))
                        }
                    }
                }
            }
            if !worktrees.isEmpty {
                Section("Worktrees of \(repo)") {
                    ForEach(worktrees.filter { $0.path != folder }) { worktree in
                        Button { place = .worktree(worktree) } label: {
                            let title = worktree.main == true ? "\(worktree.name) (the repository)" : worktree.name
                            if place == .worktree(worktree) { Label(title, systemImage: "checkmark") } else { Text(title) }
                        }
                    }
                    Button("New Worktree…") { naming = true }
                }
            }
            Divider()
            Button("Choose a folder…", action: choose)
        } label: {
            Pill {
                switch place {
                case .folder:
                    Image(systemName: "folder").font(.system(size: 11, weight: .medium))
                    Text(folder.isEmpty ? "Choose a folder" : (folder as NSString).lastPathComponent)
                case .worktree(let worktree):
                    Image(systemName: "arrow.triangle.branch").font(.system(size: 11, weight: .medium))
                    Text("\(worktree.repo) · \(worktree.name)")
                case .newWorktree(let name):
                    Image(systemName: "arrow.triangle.branch").font(.system(size: 11, weight: .medium))
                    Text("\(repo) · \(name)")
                }
            }
        }
        .menuStyle(.button)
        .buttonStyle(.plain)
        .menuIndicator(.hidden)
        .fixedSize()
        .help(folder.isEmpty ? "Where it works" : folder)
        .popover(isPresented: $naming, arrowEdge: .bottom) {
            WorktreeNamer(repo: repo, from: worktrees.first { $0.path == folder }?.branch,
                          suggestion: Self.suggestedName(message)) { name in
                naming = false
                if let name { place = .newWorktree(name) }
            }
        }
    }

    /// A worktree's name from the task: its first few words.
    static func suggestedName(_ task: String) -> String {
        let words = task.lowercased().split { !$0.isLetter && !$0.isNumber }.prefix(4)
        return words.joined(separator: "-")
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
            var target = folder
            switch place {
            case .folder:
                break
            case .worktree(let worktree):
                target = worktree.path
            case .newWorktree(let name):
                do {
                    let made = try await store.createWorktree(in: folder, name: name)
                    target = made.path
                    // Chosen from now on, so trying again starts the agent
                    // there rather than making the worktree twice.
                    await loadWorktrees()
                    place = .worktree(worktrees.first { $0.path == made.path } ?? made)
                } catch {
                    problem = error.localizedDescription
                    starting = false
                    return
                }
            }
            problem = await store.startAgent(kind: kind, folder: target, message: message)
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

/// Naming a new worktree: one field, the name it gets for its folder and
/// its branch, suggested from the task.
struct WorktreeNamer: View {
    let repo: String
    /// The branch it starts from.
    let from: String?
    let suggestion: String
    let done: (String?) -> Void
    @State private var name = ""
    @FocusState private var focused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("New worktree of \(repo)")
                .font(TextStyle.bodyMedium)
                .foregroundStyle(Palette.text)
            TextField("Name, such as rate-limits", text: $name)
                .textFieldStyle(.roundedBorder)
                .focused($focused)
                .onSubmit(use)
            Text("A folder and a branch of its own\(from.map { ", from \($0)" } ?? ""), so this agent works beside the others without getting in their way.")
                .font(TextStyle.caption)
                .foregroundStyle(Palette.muted)
                .fixedSize(horizontal: false, vertical: true)
            HStack {
                Spacer()
                Button("Cancel") { done(nil) }
                    .keyboardShortcut(.cancelAction)
                Button("Use", action: use)
                    .keyboardShortcut(.defaultAction)
                    .disabled(NewAgentView.branchName(name).isEmpty)
            }
        }
        .padding(14)
        .frame(width: 300)
        .onAppear {
            name = suggestion
            focused = true
        }
    }

    private func use() {
        let branch = NewAgentView.branchName(name)
        guard !branch.isEmpty else { return }
        done(branch)
    }
}
