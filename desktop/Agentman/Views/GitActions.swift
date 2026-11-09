import AppKit
import SwiftUI

/// What the workspace card's git row does, decided as T3 Code decides it:
/// the action that fits where the branch stands on the row, every action
/// in its menu.
enum GitAction: Equatable {
    /// Opens the commit sheet; with `push`, pushes after.
    case commit(push: Bool)
    case push
    case createPR
    /// Nothing to do, and why.
    case idle(String, hint: String)

    var label: String {
        switch self {
        case .commit(let push): push ? "Commit & push" : "Commit"
        case .push: "Push"
        case .createPR: "Create PR"
        case .idle(let label, _): label
        }
    }

    var symbol: String {
        switch self {
        case .commit: "checkmark.circle"
        case .push: "arrow.up.circle"
        case .createPR: "arrow.triangle.pull"
        case .idle: "checkmark.circle"
        }
    }

    static func quick(_ state: GitState?) -> GitAction {
        guard let state else { return .idle("Commit", hint: "Git status is unavailable.") }
        guard state.branch != nil else { return .idle("Commit", hint: "Check out a branch to commit and push.") }
        let canPush = state.upstream != nil || state.remote != nil
        if state.changes > 0 { return .commit(push: canPush) }
        if state.upstream != nil {
            if state.ahead > 0, state.behind > 0 {
                return .idle("Diverged", hint: "The branch has diverged from its upstream. Rebase or merge first.")
            }
            if state.behind > 0 { return .idle("Behind", hint: "The upstream has commits this branch doesn't. Pull first.") }
        }
        if canPush, state.ahead > 0 { return .push }
        if canCreatePR(state) { return .createPR }
        if !canPush { return .idle("Commit", hint: "This repository has no remote to push to.") }
        return .idle("Up to date", hint: "Nothing to commit or push.")
    }

    static func canPush(_ state: GitState?) -> Bool {
        guard let state, state.branch != nil else { return false }
        return state.behind == 0 && state.ahead > 0 && (state.upstream != nil || state.remote != nil)
    }

    static func canCreatePR(_ state: GitState?) -> Bool {
        guard let state, state.branch != nil, !state.onDefaultBranch else { return false }
        return state.behind == 0 && state.aheadOfDefault > 0 && (state.upstream != nil || state.remote != nil)
    }
}

extension GitAction {
    var isIdle: Bool {
        if case .idle = self { true } else { false }
    }

    /// Why there is nothing to do, when there isn't.
    var hint: String? {
        if case .idle(_, let hint) = self { hint } else { nil }
    }

    /// Whether it sends work to a branch that is the user's to push, which
    /// a button asks about once before doing.
    func pushesToProtected(_ state: GitState?) -> Bool {
        guard self == .push, let state else { return false }
        return ShipCheck.pushesToProtected(state)
    }
}

/// One thing git is asked to do, as part of a run of them.
enum GitStep: Equatable {
    /// With no message Agentman writes one.
    case commit(paths: [String], message: String)
    case push
    case createPR

    var running: String {
        switch self {
        case .commit(_, let message): message.isEmpty ? "Writing the message…" : "Committing…"
        case .push: "Pushing…"
        case .createPR: "Opening the PR…"
        }
    }

    var failed: String {
        switch self {
        case .commit: "Commit failed"
        case .push: "Push failed"
        case .createPR: "PR failed"
        }
    }
}

/// What a run of git steps came to, in a sentence.
struct GitOutcome: Equatable {
    let text: String
    var url: String?
    var failed = false
}

extension AgentsStore {
    /// Takes the steps in order, saying which it is on, and stops at the
    /// first that fails, saying what got done before it.
    func run(_ steps: [GitStep], session: String, progress: (String) -> Void) async -> GitOutcome {
        var said: [String] = []
        var url: String?
        for step in steps {
            progress(step.running)
            do {
                let after = try await take(step, session: session)
                switch after.done {
                case "committed": said.append("Committed \(after.commit ?? "")")
                case "pushed": said.append("pushed to \(after.upstream ?? "the remote")")
                case "pr_created":
                    said.append("opened the pull request")
                    url = after.url
                default: break
                }
            } catch {
                let before = said.isEmpty ? "" : said.joined(separator: ", ") + ", but "
                return GitOutcome(text: before + error.localizedDescription, failed: true)
            }
        }
        let text = said.joined(separator: ", ")
        return GitOutcome(text: text.prefix(1).uppercased() + text.dropFirst() + ".", url: url)
    }

    private func take(_ step: GitStep, session: String) async throws -> GitState {
        switch step {
        case .commit(let paths, let message):
            return try await git(.gitCommit, session: session, message: message, paths: paths)
        case .push:
            return try await git(.gitPush, session: session)
        case .createPR:
            return try await git(.gitCreatePR, session: session)
        }
    }
}

/// The card's git row: its action, a menu of the rest, then what the last
/// one did or why it failed.
struct GitActionsRow: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    let state: GitState?
    let changes: [WorkspaceChange]
    let hidden: Int
    /// Reads the branch and the changes again.
    let changed: () -> Void
    @State private var committing: CommitSheet.Then?
    @State private var confirming: GitAction?
    @State private var running: String?
    @State private var outcome: GitOutcome?

    var body: some View {
        let action = GitAction.quick(state)
        let idle: Bool = { if case .idle = action { true } else { false } }()
        VStack(alignment: .leading, spacing: 2) {
            CardRow(running ?? action.label, action: idle || running != nil ? nil : { start(action) }) {
                Image(systemName: action.symbol)
            } trailing: {
                if running != nil { ProgressView().controlSize(.mini) }
                // Room for the menu laid over the row's end.
                Color.clear.frame(width: 20, height: 20)
            }
            .help({ if case .idle(_, let hint) = action { hint } else { action.label } }())
            .overlay(alignment: .trailing) {
                Menu {
                    Button { committing = .commit } label: { Label("Commit…", systemImage: "checkmark.circle") }
                        .disabled((state?.changes ?? 0) == 0 || state?.branch == nil)
                    Button { confirming = .push } label: { Label("Push", systemImage: "arrow.up.circle") }
                        .disabled(!GitAction.canPush(state))
                    Button { confirming = .createPR } label: { Label("Create PR", systemImage: "arrow.triangle.pull") }
                        .disabled(!GitAction.canCreatePR(state))
                } label: {
                    Image(systemName: "chevron.down")
                        .font(.system(size: 9, weight: .semibold))
                        .foregroundStyle(Palette.faint)
                        .frame(width: 20, height: 20)
                        .contentShape(Rectangle())
                }
                .menuStyle(.button)
                .buttonStyle(.plain)
                .menuIndicator(.hidden)
                .fixedSize()
                .disabled(running != nil || state == nil)
                .padding(.trailing, CardRowMetrics.inset)
                .help("Commit, push or open a pull request")
            }
            if let outcome {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(outcome.text)
                        .foregroundStyle(outcome.failed ? Palette.errorText : Palette.textSecondary)
                        .fixedSize(horizontal: false, vertical: true)
                        .textSelection(.enabled)
                    Spacer(minLength: 4)
                    if let url = outcome.url, let link = URL(string: url) {
                        Button("View PR") { NSWorkspace.shared.open(link) }
                            .buttonStyle(.link)
                    }
                    Button { self.outcome = nil } label: {
                        Image(systemName: "xmark").font(.system(size: 8, weight: .bold))
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(Palette.faint)
                    .help("Dismiss")
                }
                .font(TextStyle.caption)
                // Under the row's words, not its icon.
                .padding(.leading, CardRowMetrics.inset + CardRowMetrics.icon + CardRowMetrics.gap)
                .padding(.trailing, CardRowMetrics.inset)
                .padding(.bottom, 6)
                .task(id: outcome) {
                    guard !outcome.failed else { return }
                    try? await Task.sleep(for: .seconds(10))
                    if self.outcome == outcome { self.outcome = nil }
                }
            }
        }
        // "Commit & push" on the Board's card, once there is something to
        // show in the sheet.
        .task(id: requestReady) {
            guard requestReady else { return }
            store.gitActionRequest = nil
            start(GitAction.quick(state))
        }
        .sheet(item: $committing) { then in
            CommitSheet(state: state, changes: changes, hidden: hidden, then: then) { paths, message, push in
                committing = nil
                run([.commit(paths: paths, message: message)] + (push ? [.push] : []))
            } cancel: {
                committing = nil
            }
        }
        .alert(confirmTitle, isPresented: Binding(get: { confirming != nil }, set: { if !$0 { confirming = nil } })) {
            Button(confirming == .createPR ? "Create PR" : "Push") {
                let action = confirming
                confirming = nil
                run(action == .createPR ? [.createPR] : [.push])
            }
            Button("Cancel", role: .cancel) { confirming = nil }
        } message: {
            Text(confirmMessage)
        }
    }

    private var requestReady: Bool {
        guard store.gitActionRequest == session.id, let state else { return false }
        return state.changes == 0 || !changes.isEmpty
    }

    private func start(_ action: GitAction) {
        switch action {
        case .commit(let push): committing = push ? .commitAndPush : .commit
        case .push, .createPR: confirming = action
        case .idle: break
        }
    }

    private var confirmTitle: String {
        guard let state, let branch = state.branch else { return "" }
        if confirming == .createPR { return "Open a pull request from \(branch)?" }
        return state.onDefaultBranch ? "Push to \(branch), the default branch?" : "Push \(branch)?"
    }

    private var confirmMessage: String {
        guard let state, let branch = state.branch else { return "" }
        let commits = "\(state.ahead) commit\(state.ahead == 1 ? "" : "s")"
        let target = state.upstream ?? "\(state.remote ?? "origin")/\(branch)"
        if confirming == .createPR {
            let pushed = state.upstream == nil || state.ahead > 0 ? " Its \(commits) not pushed yet go first." : ""
            return "\(branch) goes to \(state.defaultBranch ?? "the default branch") on GitHub, titled and described from its commits.\(pushed)"
        }
        return "\(commits) go to \(target)."
    }

    private func run(_ steps: [GitStep]) {
        outcome = nil
        Task {
            outcome = await store.run(steps, session: session.id) { running = $0 }
            running = nil
            changed()
        }
    }
}

/// T3 Code's "Commit changes": the branch, the files going in, which can be
/// left out, and a message, which Agentman writes when it is left empty.
struct CommitSheet: View {
    enum Then: String, Identifiable {
        case commit, commitAndPush
        var id: String { rawValue }
    }

    let state: GitState?
    let changes: [WorkspaceChange]
    let hidden: Int
    let then: Then
    /// What it is doing, while the commit runs here in the sheet.
    var busy: String? = nil
    /// Why the commit failed, with what was typed still here to try again.
    var problem: String? = nil
    let confirm: (_ paths: [String], _ message: String, _ push: Bool) -> Void
    let cancel: () -> Void
    @State private var message = ""
    @State private var excluded: Set<String> = []
    @State private var editing = false

    private var included: [WorkspaceChange] { changes.filter { !excluded.contains($0.path) } }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            VStack(alignment: .leading, spacing: 4) {
                Text("Commit changes").font(TextStyle.title).foregroundStyle(Palette.text)
                Text("Review and confirm your commit. Leave the message blank to auto-generate one.")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
            }
            VStack(alignment: .leading, spacing: 12) {
                HStack(spacing: 8) {
                    Text("Branch").foregroundStyle(Palette.muted)
                    Text(state?.branch ?? "(detached HEAD)").fontWeight(.medium).foregroundStyle(Palette.text)
                    Spacer()
                    if state?.onDefaultBranch == true {
                        Text("Default branch").foregroundStyle(Palette.needsYou)
                    }
                }
                VStack(alignment: .leading, spacing: 6) {
                    HStack(spacing: 8) {
                        if editing {
                            Toggle("", isOn: Binding(get: { excluded.isEmpty }, set: { excluded = $0 ? [] : Set(changes.map(\.path)) }))
                                .toggleStyle(.checkbox)
                                .labelsHidden()
                        }
                        Text("Files").foregroundStyle(Palette.muted)
                        if !excluded.isEmpty, !editing {
                            Text("(\(included.count) of \(changes.count))").foregroundStyle(Palette.muted)
                        }
                        Spacer()
                        Button(editing ? "Done" : "Edit") { editing.toggle() }
                            .buttonStyle(.plain)
                            .foregroundStyle(Palette.textSecondary)
                    }
                    ScrollView {
                        VStack(spacing: 2) {
                            ForEach(changes) { change in fileRow(change) }
                        }
                        .padding(4)
                    }
                    .scrollIndicators(.never)
                    .frame(height: 176)
                    .background(Palette.surface, in: RoundedRectangle(cornerRadius: 8))
                    .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Palette.edge, lineWidth: Palette.edgeWidth))
                    HStack(spacing: 0) {
                        if hidden > 0 {
                            Text("\(hidden) file\(hidden == 1 ? "" : "s") holding secrets left out")
                                .foregroundStyle(Palette.faint)
                        }
                        Spacer()
                        Text("+\(included.reduce(0) { $0 + ($1.added ?? 0) })").foregroundStyle(Palette.ok)
                        Text(" / ").foregroundStyle(Palette.muted)
                        Text("−\(included.reduce(0) { $0 + ($1.removed ?? 0) })").foregroundStyle(Palette.errorText)
                    }
                    .font(Fonts.mono(11.5))
                }
            }
            .font(TextStyle.caption)
            .padding(12)
            .background(Palette.fill.opacity(0.4), in: RoundedRectangle(cornerRadius: 12))
            .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(Palette.edge, lineWidth: Palette.edgeWidth))
            VStack(alignment: .leading, spacing: 6) {
                Text("Commit message (optional)").font(TextStyle.captionMedium).foregroundStyle(Palette.text)
                TextEditor(text: $message)
                    .font(TextStyle.body)
                    .scrollContentBackground(.hidden)
                    .padding(6)
                    .frame(height: 76)
                    .background(Palette.surface, in: RoundedRectangle(cornerRadius: 8))
                    .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Palette.edge, lineWidth: Palette.edgeWidth))
                    .overlay(alignment: .topLeading) {
                        if message.isEmpty {
                            Text("Leave empty to auto-generate")
                                .font(TextStyle.body)
                                .foregroundStyle(Palette.faint)
                                .padding(.horizontal, 11)
                                .padding(.vertical, 6)
                                .allowsHitTesting(false)
                        }
                    }
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
                Button("Cancel", action: cancel)
                    .buttonStyle(.brandSecondary)
                    .keyboardShortcut(.cancelAction)
                    .disabled(busy != nil)
                if then == .commitAndPush {
                    Button("Commit") { confirm(included.map(\.path), message, false) }
                        .buttonStyle(.brandSecondary)
                        .disabled(included.isEmpty || busy != nil)
                }
                Button(then == .commitAndPush ? "Commit & push" : "Commit") {
                    confirm(included.map(\.path), message, then == .commitAndPush)
                }
                .buttonStyle(.brand)
                .keyboardShortcut(.defaultAction)
                .disabled(included.isEmpty || busy != nil)
            }
            .controlSize(.large)
        }
        .padding(22)
        .frame(width: 520)
        .background(Palette.paper)
    }

    private func fileRow(_ change: WorkspaceChange) -> some View {
        let out = excluded.contains(change.path)
        return HStack(spacing: 8) {
            if editing {
                Toggle("", isOn: Binding(get: { !out }, set: { if $0 { excluded.remove(change.path) } else { excluded.insert(change.path) } }))
                    .toggleStyle(.checkbox)
                    .labelsHidden()
            }
            Text(change.path)
                .foregroundStyle(out ? Palette.faint : Palette.text)
                .lineLimit(1)
                .truncationMode(.head)
                .help(change.path)
            Spacer(minLength: 8)
            if out {
                Text("Excluded").foregroundStyle(Palette.faint)
            } else {
                Text("+\(change.added ?? 0)").foregroundStyle(Palette.ok)
                Text("/").foregroundStyle(Palette.faint)
                Text("−\(change.removed ?? 0)").foregroundStyle(Palette.errorText)
            }
        }
        .font(Fonts.mono(11.5))
        .padding(.horizontal, 8)
        .padding(.vertical, 4)
    }
}
