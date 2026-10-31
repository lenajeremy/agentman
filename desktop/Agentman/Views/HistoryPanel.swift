import SwiftUI

/// A page belongs to the branch it was requested for, even if the user
/// switches branches while the daemon is answering.
@MainActor @Observable
final class HistoryPanelModel {
    private(set) var commits: [WorkspaceCommit] = []
    private(set) var branches: [WorkspaceBranch] = []
    private(set) var branch: String?
    private(set) var more = false
    private(set) var loading = false
    private(set) var problem: String?
    @ObservationIgnored private var generation = UUID()
    @ObservationIgnored private var ref: String?

    func readBranches(store: AgentsStore, session: String) async {
        if let result = try? await store.workspace(.listBranches, session: session) {
            branches = result.branches ?? []
        }
    }

    func reload(store: AgentsStore, session: String, ref: String?) async {
        generation = UUID()
        self.ref = ref
        commits = []
        more = false
        loading = false
        problem = nil
        await page(store: store, session: session)
    }

    /// The newest commits put on top of those already read, so a commit the
    /// agent makes shows without losing the pages scrolled through. History
    /// rewritten under the list (an amend, a rebase, a reset, another branch
    /// checked out) is read again from the start.
    func update(store: AgentsStore, session: String) async {
        guard !loading else { return }
        guard let top = commits.first else { return await reload(store: store, session: session, ref: ref) }
        let current = generation
        guard let result = try? await store.workspace(.listCommits, session: session, offset: 0, ref: ref),
              current == generation, !Task.isCancelled else { return }
        let fresh = result.commits ?? []
        if let index = fresh.firstIndex(where: { $0.hash == top.hash }) {
            if index > 0 { commits.insert(contentsOf: fresh[..<index], at: 0) }
        } else {
            commits = fresh
            more = result.truncated == true
        }
        branch = result.branch
        problem = nil
    }

    func page(store: AgentsStore, session: String) async {
        guard !loading else { return }
        let current = generation
        loading = true
        defer { if current == generation { loading = false } }
        do {
            let result = try await store.workspace(.listCommits, session: session, offset: commits.count, ref: ref)
            guard current == generation, !Task.isCancelled else { return }
            var known = Set(commits.map(\.hash))
            commits += (result.commits ?? []).filter { known.insert($0.hash).inserted }
            branch = result.branch
            more = result.truncated == true
            problem = nil
        } catch {
            guard current == generation, !Task.isCancelled else { return }
            problem = error.localizedDescription
        }
    }
}

/// History is a workspace tab, with the same chrome and diff renderer as
/// Diff and Files. Looking at another branch never checks it out. It keeps
/// up as the agent works: a commit it makes appears on top.
struct HistoryPanel: View, Equatable {
    @Environment(AgentsStore.self) private var store
    let sessionID: String
    let cwd: String
    let state: SessionState
    let activity: Int64?
    @State private var model = HistoryPanelModel()
    /// Whether the tab has read its list, after which the agent's work
    /// reads it again.
    @State private var watching = false

    nonisolated static func == (a: HistoryPanel, b: HistoryPanel) -> Bool {
        a.sessionID == b.sessionID && a.cwd == b.cwd && a.state == b.state && a.activity == b.activity
    }

    private struct Watch: Equatable {
        let state: SessionState
        let activity: Int64?
    }
    @State private var ref: String?
    @State private var selected: WorkspaceCommit?
    @State private var lastSelected: String?

    var body: some View {
        Group {
            if let selected {
                DiffPanel(sessionID: sessionID, cwd: cwd, state: .idle, activity: nil, commit: selected,
                          back: { self.selected = nil })
                    .equatable()
                    .id(selected.hash)
            } else {
                list
            }
        }
        .task(id: ref) { await model.reload(store: store, session: sessionID, ref: ref) }
        .task { await model.readBranches(store: store, session: sessionID) }
        // Once the agent has been quiet for two seconds, or its turn begins
        // or ends; not as the tab opens, which reads the list anyway.
        .task(id: Watch(state: state, activity: activity)) {
            guard watching else { watching = true; return }
            try? await Task.sleep(for: .seconds(2))
            guard !Task.isCancelled else { return }
            await model.update(store: store, session: sessionID)
        }
    }

    private var list: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                Menu {
                    Button { ref = nil } label: {
                        Label("Checked-out branch", systemImage: ref == nil ? "checkmark" : "point.3.connected.trianglepath.dotted")
                    }
                    ForEach(model.branches) { branch in
                        Button { ref = branch.current == true ? nil : branch.ref } label: {
                            Label(branch.name + (branch.current == true ? " (checked out)" : ""),
                                  systemImage: ref == branch.ref ? "checkmark" : "point.3.connected.trianglepath.dotted")
                        }
                    }
                } label: {
                    (Text(Image(systemName: "point.3.connected.trianglepath.dotted"))
                     + Text(" " + (model.branches.first { $0.ref == ref }?.name ?? model.branch ?? "Current branch") + " ")
                     + Text(Image(systemName: "chevron.down")).font(.system(size: 8, weight: .semibold)))
                        .lineLimit(1).truncationMode(.middle)
                    .font(TextStyle.captionMedium)
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .foregroundStyle(Palette.text)
                .help("Browse a branch’s history without checking it out")
                Spacer(minLength: 8)
                PanelButton(symbol: "arrow.clockwise", help: "Refresh history") {
                    Task {
                        await model.reload(store: store, session: sessionID, ref: ref)
                        await model.readBranches(store: store, session: sessionID)
                    }
                }
                .disabled(model.loading)
            }
            .padding(.horizontal, 10)
            .frame(height: 38)
            Divider().overlay(Palette.line)
            if model.commits.isEmpty, model.loading {
                ProgressView().controlSize(.small).frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if model.commits.isEmpty, model.problem == nil {
                Text("No commits here yet.").font(TextStyle.caption).foregroundStyle(Palette.muted)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollViewReader { proxy in
                    ScrollView {
                        LazyVStack(spacing: 0) {
                            ForEach(model.commits) { commit in
                                HistoryCommitRow(commit: commit) {
                                    lastSelected = commit.hash
                                    selected = commit
                                }
                                .id(commit.hash)
                                Rectangle().fill(Palette.line).frame(height: 1)
                            }
                            if let problem = model.problem {
                                Text(problem).font(TextStyle.caption).foregroundStyle(Palette.errorText).padding(12)
                            }
                            if model.more || model.problem != nil {
                                Button(model.loading ? "Loading…" : model.problem == nil ? "Load older commits" : "Try again") {
                                    Task { await model.page(store: store, session: sessionID) }
                                }
                                .buttonStyle(.plain).font(TextStyle.captionMedium).foregroundStyle(Palette.muted)
                                .padding(14).disabled(model.loading)
                            }
                        }
                    }
                    .scrollIndicators(.never)
                    .onAppear { if let lastSelected { proxy.scrollTo(lastSelected, anchor: .center) } }
                }
            }
        }
    }
}

private struct HistoryCommitRow: View {
    let commit: WorkspaceCommit
    let open: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: open) {
            HStack(alignment: .top, spacing: 10) {
                HStack(spacing: 0) {
                    Rectangle().frame(width: 4, height: 1)
                    Circle().strokeBorder(lineWidth: 1).frame(width: 7, height: 7)
                    Rectangle().frame(width: 4, height: 1)
                }
                .foregroundStyle(Palette.muted).frame(width: 16, height: 16)
                VStack(alignment: .leading, spacing: 6) {
                    Text(commit.subject).font(TextStyle.captionMedium).foregroundStyle(Palette.text)
                        .lineLimit(2).multilineTextAlignment(.leading)
                    HStack(spacing: 6) {
                        Text(String(commit.hash.prefix(7))).font(Fonts.mono(10.5))
                        Text("·").foregroundStyle(Palette.faint)
                        Text(commit.author).lineLimit(1).truncationMode(.tail).layoutPriority(-1)
                        Text("·").foregroundStyle(Palette.faint)
                        Text(Date(timeIntervalSince1970: Double(commit.authoredAt) / 1000),
                             format: .relative(presentation: .numeric, unitsStyle: .abbreviated))
                            .fixedSize()
                        Spacer(minLength: 4)
                        HStack(spacing: 5) {
                            if let added = commit.added, added > 0 { Text("+\(added.formatted())").foregroundStyle(Palette.ok) }
                            if let removed = commit.removed, removed > 0 { Text("−\(removed.formatted())").foregroundStyle(Palette.errorText) }
                        }
                        .font(Fonts.mono(10.5, .medium))
                        .fixedSize()
                    }
                    .font(TextStyle.caption).foregroundStyle(Palette.muted)
                }
                Image(systemName: "chevron.right").font(.system(size: 8, weight: .semibold))
                    .foregroundStyle(Palette.faint).padding(.top, 3)
            }
            .padding(.horizontal, 12).padding(.vertical, 11)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(hovering ? Palette.fill.opacity(0.5) : .clear)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(commit.subject + "\n" + Date(timeIntervalSince1970: Double(commit.authoredAt) / 1000).formatted())
    }
}
