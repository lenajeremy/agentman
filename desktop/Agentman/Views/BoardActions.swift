import AppKit
import SwiftUI

/// Whether an agent's work can be shipped, and if not why, the same way the
/// daemon decides it.
enum ShipCheck {
    static func reason(session: Session, git: GitState?, check: Verification?) -> String? {
        if session.state != .idle || session.question != nil { return "Still working" }
        if check?.state == "failed" { return "Its tests fail" }
        if check?.state == "checking" { return "Still being checked" }
        guard let git else { return "Not in a git repository" }
        guard let branch = git.branch else { return "No branch checked out" }
        if protected(branch, git) { return "On \(branch), which is yours to push" }
        if let upstream = git.upstream, let tracked = upstream.split(separator: "/", maxSplits: 1).last, protected(String(tracked), git) {
            return "Tracks \(upstream), which is yours to push"
        }
        if git.behind > 0 { return "Behind its upstream" }
        if git.upstream == nil, git.remote == nil { return "No remote to push to" }
        if git.changes == 0, git.ahead == 0, git.aheadOfDefault == 0 { return "Nothing to ship" }
        return nil
    }

    private static func protected(_ branch: String, _ git: GitState) -> Bool {
        branch == git.defaultBranch || ["main", "master", "trunk", "develop", "production"].contains(branch)
    }

    /// Whether a push from here lands on a default branch, or one that
    /// goes by a default branch's name.
    static func pushesToProtected(_ git: GitState) -> Bool {
        guard let branch = git.branch else { return false }
        if protected(branch, git) { return true }
        guard let upstream = git.upstream, let tracked = upstream.split(separator: "/", maxSplits: 1).last else { return false }
        return protected(String(tracked), git)
    }

    /// What will go out, in a few words.
    static func summary(_ git: GitState) -> String {
        var parts = [git.branch ?? ""]
        if git.changes > 0 { parts.append(git.changes == 1 ? "1 changed file" : "\(git.changes) changed files") }
        if git.ahead > 0 { parts.append(git.ahead == 1 ? "1 commit to push" : "\(git.ahead) commits to push") }
        return parts.joined(separator: " · ")
    }
}

/// Commit from the Board: the agent page's modal, then a way to push what
/// was just committed, without leaving it.
struct BoardCommitSheet: View {
    private enum Stage: Equatable {
        case loading
        case form
        /// `commit` is nil when there was nothing here to commit.
        case committed(commit: String?, message: String?)
    }

    @Environment(AgentsStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    let sessionId: String
    @State private var stage = Stage.loading
    @State private var git: GitState?
    @State private var changes: [WorkspaceChange] = []
    @State private var hidden = 0
    @State private var busy: String?
    @State private var problem: String?
    @State private var pushedTo: String?

    var body: some View {
        Group {
            switch stage {
            case .loading:
                HStack(spacing: 8) {
                    Loader(size: 11)
                    Text("Reading the changes")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                }
                .frame(width: 520, height: 120)
            case .form:
                CommitSheet(state: git, changes: changes, hidden: hidden, then: .commit, busy: busy, problem: problem) { paths, message, _ in
                    commit(paths, message: message)
                } cancel: {
                    dismiss()
                }
            case .committed(let commit, let message):
                committed(commit, message: message)
            }
        }
        .background(Palette.paper)
        .task { await load() }
    }

    private func committed(_ commit: String?, message: String?) -> some View {
        VStack(alignment: .leading, spacing: 16) {
            VStack(alignment: .leading, spacing: 4) {
                Text(pushedTo != nil ? "Pushed" : commit != nil ? "Committed" : "Nothing to commit")
                    .font(TextStyle.title)
                    .foregroundStyle(Palette.text)
                Text(summary(commit))
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let commit {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Image(systemName: "checkmark.circle.fill")
                        .font(.system(size: 11))
                        .foregroundStyle(Palette.ok)
                    Text(commit)
                        .font(Fonts.mono(11.5))
                        .foregroundStyle(Palette.muted)
                    Text(message ?? "")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.text)
                        .lineLimit(3)
                        .fixedSize(horizontal: false, vertical: true)
                        .textSelection(.enabled)
                    Spacer(minLength: 0)
                }
                .padding(12)
                .background(Palette.fill.opacity(0.4), in: RoundedRectangle(cornerRadius: 12))
                .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(Palette.edge, lineWidth: Palette.edgeWidth))
            }
            if let problem {
                Text(problem)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.errorText)
                    .fixedSize(horizontal: false, vertical: true)
                    .textSelection(.enabled)
            }
            HStack(spacing: 8) {
                if let busy {
                    Loader(size: 11)
                    Text(busy)
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                }
                Spacer()
                Button("Done") { dismiss() }
                    .buttonStyle(.brandSecondary)
                    .keyboardShortcut(.cancelAction)
                    .disabled(busy != nil)
                // Never on Return: a push is asked for, not fallen into.
                if canPush {
                    Button(pushTitle, action: push)
                        .buttonStyle(.brand)
                        .tint(.accentColor)
                        .disabled(busy != nil)
                }
            }
            .controlSize(.large)
        }
        .padding(22)
        .frame(width: 520)
    }

    private var canPush: Bool { pushedTo == nil && GitAction.canPush(git) }

    private var pushTitle: String {
        guard let git, ShipCheck.pushesToProtected(git), let branch = git.branch else { return "Push" }
        return "Push to \(branch)"
    }

    /// Where the branch stands now, in a line.
    private func summary(_ commit: String?) -> String {
        if let pushedTo { return "Everything on \(git?.branch ?? "this branch") is on \(pushedTo)." }
        guard let git else { return "" }
        if canPush {
            let commits = git.ahead == 1 ? "1 commit" : "\(git.ahead) commits"
            return "\(commits) on \(git.branch ?? "this branch") to push to \(git.upstream ?? "\(git.remote ?? "origin")/\(git.branch ?? "")")."
        }
        if commit == nil {
            return hidden > 0
                ? "Only files holding secrets are left, and those aren't committed from here."
                : "Everything here is already committed."
        }
        return GitAction.quick(git).hint ?? "Saved on \(git.branch ?? "this branch")."
    }

    private func load() async {
        guard stage == .loading else { return }
        do {
            let state = try await store.git(.gitStatus, session: sessionId)
            let listed = try await store.workspace(.listChanges, session: sessionId)
            git = state
            changes = listed.changes ?? []
            hidden = listed.hidden ?? 0
            stage = changes.isEmpty ? .committed(commit: nil, message: nil) : .form
        } catch {
            problem = error.localizedDescription
            stage = .form
        }
    }

    private func commit(_ paths: [String], message: String) {
        let step = GitStep.commit(paths: paths, message: message)
        problem = nil
        busy = step.running
        Task {
            do {
                let after = try await store.git(.gitCommit, session: sessionId, message: message, paths: paths)
                git = after
                stage = .committed(commit: after.commit, message: after.message)
                store.boardGitChanged[sessionId, default: 0] += 1
            } catch {
                problem = error.localizedDescription
            }
            busy = nil
        }
    }

    private func push() {
        problem = nil
        busy = GitStep.push.running
        Task {
            do {
                let after = try await store.git(.gitPush, session: sessionId)
                git = after
                pushedTo = after.upstream ?? "the remote"
                store.boardGitChanged[sessionId, default: 0] += 1
            } catch {
                problem = error.localizedDescription
            }
            busy = nil
        }
    }
}

/// Choosing finished agents to ship together, and watching it happen.
struct ShipSheet: View {
    @Environment(AgentsStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    let sessions: [Session]
    @State private var git: [String: GitState] = [:]
    @State private var loaded = false
    @State private var chosen: Set<String> = []
    @State private var runId: String?
    @State private var problem: String?

    private var run: ShipRun? {
        guard let runId, let run = store.manager.shipRun, run.id == runId else { return nil }
        return run
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            VStack(alignment: .leading, spacing: 3) {
                Text("Ship finished work")
                    .font(Fonts.sans(15, .semibold))
                    .foregroundStyle(Palette.text)
                Text("Each one is committed, pushed and opened as a pull request. Default branches are left to you.")
                    .font(Fonts.sans(12.5))
                    .foregroundStyle(Palette.muted)
                    .fixedSize(horizontal: false, vertical: true)
            }
            ScrollView {
                VStack(alignment: .leading, spacing: 2) {
                    if let run {
                        ForEach(run.items) { ShipProgressRow(item: $0) }
                    } else if !loaded {
                        HStack(spacing: 8) {
                            Loader(size: 11)
                            Text("Reading where each one stands")
                                .font(Fonts.sans(12.5))
                                .foregroundStyle(Palette.muted)
                        }
                        .padding(.vertical, 6)
                    } else {
                        ForEach(sessions) { row($0) }
                    }
                }
            }
            .frame(maxHeight: 320)
            if let problem {
                Text(problem)
                    .font(Fonts.sans(12))
                    .foregroundStyle(Palette.errorText)
            }
            HStack {
                Spacer()
                if run?.state == "done" {
                    SmallButton(title: "Done", primary: true) { dismiss() }
                } else if run == nil {
                    SmallButton(title: "Cancel") { dismiss() }
                    SmallButton(title: chosen.isEmpty ? "Ship" : "Ship \(chosen.count)", symbol: "arrow.up.circle", primary: true, action: ship)
                        .disabled(chosen.isEmpty)
                        .opacity(chosen.isEmpty ? 0.5 : 1)
                }
            }
        }
        .padding(20)
        .frame(width: 460)
        .task(load)
    }

    private func row(_ session: Session) -> some View {
        let state = git[session.id]
        let reason = ShipCheck.reason(session: session, git: state, check: store.manager.verifications[session.id])
        return HStack(alignment: .firstTextBaseline, spacing: 8) {
            Toggle("", isOn: Binding(
                get: { chosen.contains(session.id) },
                set: { if $0 { chosen.insert(session.id) } else { chosen.remove(session.id) } }
            ))
            .toggleStyle(.checkbox)
            .labelsHidden()
            .disabled(reason != nil)
            VStack(alignment: .leading, spacing: 1) {
                Text(session.name)
                    .font(Fonts.sans(13, .medium))
                    .foregroundStyle(reason == nil ? Palette.text : Palette.muted)
                    .lineLimit(1)
                Text(reason ?? state.map(ShipCheck.summary) ?? "")
                    .font(Fonts.sans(12))
                    .foregroundStyle(Palette.muted)
                    .lineLimit(1)
            }
            Spacer(minLength: 0)
            if let check = store.manager.verifications[session.id], check.state == "concerns" {
                Image(systemName: "exclamationmark.triangle.fill")
                    .font(.system(size: 10))
                    .foregroundStyle(Palette.needsYou)
                    .help(check.summary ?? "Agentman's check found something")
            }
        }
        .padding(.vertical, 5)
    }

    @Sendable private func load() async {
        for session in sessions {
            if let state = try? await store.git(.gitStatus, session: session.id) { git[session.id] = state }
        }
        // Work Agentman had doubts about stays unticked until the user says.
        chosen = Set(sessions.filter { session in
            ShipCheck.reason(session: session, git: git[session.id], check: store.manager.verifications[session.id]) == nil
                && store.manager.verifications[session.id]?.state != "concerns"
        }.map(\.id))
        loaded = true
    }

    private func ship() {
        let ids = sessions.map(\.id).filter(chosen.contains)
        Task {
            switch await store.manager.ship(ids) {
            case .success(let id): runId = id
            case .failure(let failure): problem = failure.message
            }
        }
    }
}

private struct ShipProgressRow: View {
    let item: ShipItem

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            icon.frame(width: 13)
            VStack(alignment: .leading, spacing: 1) {
                Text(item.name)
                    .font(Fonts.sans(13, .medium))
                    .foregroundStyle(Palette.text)
                    .lineLimit(1)
                if let note = item.note, !note.isEmpty {
                    Text(note)
                        .font(Fonts.sans(12))
                        .foregroundStyle(item.state == "failed" ? Palette.errorText : Palette.muted)
                        .lineLimit(2)
                }
            }
            Spacer(minLength: 0)
            if let link = item.url.flatMap(URL.init(string:)) {
                Button("View PR") { NSWorkspace.shared.open(link) }
                    .buttonStyle(.link)
                    .font(Fonts.sans(12))
            }
        }
        .padding(.vertical, 5)
    }

    @ViewBuilder private var icon: some View {
        switch item.state {
        case "working": Loader(size: 10)
        case "shipped": Image(systemName: "checkmark.circle.fill").font(.system(size: 11)).foregroundStyle(Palette.ok)
        case "failed": Image(systemName: "xmark.circle.fill").font(.system(size: 11)).foregroundStyle(Palette.error)
        case "skipped": Image(systemName: "minus.circle").font(.system(size: 11)).foregroundStyle(Palette.faint)
        default: Image(systemName: "clock").font(.system(size: 10)).foregroundStyle(Palette.faint)
        }
    }
}

/// Work lined up for tonight.
struct TonightButton: View {
    @Environment(AgentsStore.self) private var store
    @State private var open = false

    var body: some View {
        let waiting = store.manager.queue.filter { $0.state == "waiting" || $0.state == "running" }.count
        Button { open.toggle() } label: {
            HStack(spacing: 4) {
                Image(systemName: "moon")
                    .font(.system(size: 11))
                if waiting > 0 {
                    Text("\(waiting)")
                        .font(Fonts.sans(12))
                        .monospacedDigit()
                }
            }
            .foregroundStyle(Palette.muted)
            .frame(minWidth: 28, minHeight: 28)
            .padding(.horizontal, waiting > 0 ? 6 : 0)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help("Work lined up for tonight")
        .accessibilityLabel("Tonight's queue")
        .popover(isPresented: $open, arrowEdge: .bottom) { TonightPanel() }
    }
}

private struct TonightPanel: View {
    @Environment(AgentsStore.self) private var store
    @State private var task = ""
    @State private var folder = ""
    @State private var agent = "claude"
    @State private var problem: String?

    private static let agents = ["claude", "codex", "cursor", "opencode", "kiro", "antigravity"]

    private var overnightOn: Bool { store.manager.settings?.autopilot?.overnight == true }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Tonight")
                    .font(Fonts.sans(14, .semibold))
                    .foregroundStyle(Palette.text)
                Text(window)
                    .font(Fonts.sans(12))
                    .foregroundStyle(Palette.muted)
            }
            ForEach(store.manager.queue) { item in
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    VStack(alignment: .leading, spacing: 1) {
                        Text(item.task)
                            .font(Fonts.sans(12.5))
                            .foregroundStyle(Palette.text)
                            .lineLimit(2)
                        Text("\((item.folder as NSString).lastPathComponent) · \(item.agent) · \(item.state)")
                            .font(Fonts.sans(11.5))
                            .foregroundStyle(Palette.muted)
                    }
                    Spacer(minLength: 4)
                    if item.state == "waiting" {
                        Button {
                            Task { problem = await store.manager.removeFromQueue(item.id) }
                        } label: {
                            Image(systemName: "xmark")
                                .font(.system(size: 9, weight: .semibold))
                                .foregroundStyle(Palette.muted)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("Remove from tonight")
                    }
                }
            }
            if !store.manager.queue.isEmpty { Divider() }
            TextField("What should happen tonight?", text: $task, axis: .vertical)
                .textFieldStyle(.roundedBorder)
                .font(Fonts.sans(13))
                .lineLimit(2...5)
            HStack(spacing: 8) {
                Picker("", selection: $folder) {
                    Text("Choose a folder").tag("")
                    ForEach(store.folders) { Text(($0.path as NSString).lastPathComponent).tag($0.path) }
                }
                .labelsHidden()
                Picker("", selection: $agent) {
                    ForEach(Self.agents, id: \.self) { Text($0.capitalized).tag($0) }
                }
                .labelsHidden()
                .fixedSize()
                SmallButton(title: "Add", primary: true, action: add)
                    .disabled(!canAdd)
                    .opacity(canAdd ? 1 : 0.5)
            }
            if let problem {
                Text(problem)
                    .font(Fonts.sans(12))
                    .foregroundStyle(Palette.errorText)
            }
            if !overnightOn {
                Divider()
                HStack {
                    Text("The overnight queue is off")
                        .font(Fonts.sans(12))
                        .foregroundStyle(Palette.muted)
                    Spacer()
                    SmallButton(title: "Turn On") {
                        Task { problem = await store.manager.setOvernight(true) }
                    }
                }
            }
        }
        .padding(14)
        .frame(width: 380)
        .task { await store.loadFolders() }
    }

    private var canAdd: Bool { !task.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && !folder.isEmpty }

    private var window: String {
        let autopilot = store.manager.settings?.autopilot
        return "Runs \(autopilot?.nightStart ?? "23:00") to \(autopilot?.nightEnd ?? "07:00"), with a report in the morning."
    }

    private func add() {
        Task {
            problem = await store.manager.addToQueue(agent: agent, folder: folder, task: task)
            if problem == nil { task = "" }
        }
    }
}
