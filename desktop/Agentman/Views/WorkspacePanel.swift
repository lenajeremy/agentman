import AppKit
import SwiftUI

/// An agent's folder beside its conversation: what it changed, its files,
/// and its git history, each opened to the diff, the file or the commit.
struct WorkspacePanel: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    @Binding var tab: WorkspaceTab
    /// What is open over the list, deepest last.
    @State private var stack: [WorkspaceRoute] = []

    var body: some View {
        VStack(spacing: 0) {
            if let route = stack.last {
                header(route.title) { stack.removeLast() }
                Divider()
                destination(route)
                    .id(route)
            } else {
                Picker("", selection: $tab) {
                    ForEach(WorkspaceTab.allCases.filter { $0 != .plans || (session.artifacts ?? 0) > 0 || tab == .plans }) {
                        Text($0.title).tag($0)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .padding(.horizontal, 14)
                .padding(.vertical, 12)
                Divider()
                switch tab {
                case .changes: ChangesList(session: session, open: push)
                case .files: FileList(session: session, path: "", open: push)
                case .history: HistoryList(session: session, open: push)
                case .plans: ArtifactList(session: session, open: push)
                }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Palette.paper)
        .task {
            // For screenshots and tests: "diff:<path>" or "file:<path>" open,
            // once the tab the thread asked for has been set.
            let open = ProcessInfo.processInfo.environment["AGENTMAN_FILES_OPEN"] ?? ""
            try? await Task.sleep(for: .milliseconds(300))
            if open.hasPrefix("diff:") { stack = [.diff(String(open.dropFirst(5)))] }
            if open.hasPrefix("file:") { stack = [.file(String(open.dropFirst(5)))] }
        }
        .onChange(of: tab) { _, _ in stack = [] }
        .onChange(of: session.id) { _, _ in stack = [] }
    }

    private func push(_ route: WorkspaceRoute) { stack.append(route) }

    private func header(_ title: String, back: @escaping () -> Void) -> some View {
        HStack(spacing: 8) {
            Button(action: back) {
                Image(systemName: "chevron.left")
                    .font(.system(size: 12, weight: .semibold))
                    .frame(width: 24, height: 24)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .keyboardShortcut("[", modifiers: .command)
            .help("Back")
            Text((title as NSString).lastPathComponent)
                .font(TextStyle.captionMedium)
                .foregroundStyle(Palette.text)
                .lineLimit(1)
                .truncationMode(.middle)
            Spacer()
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
    }

    @ViewBuilder private func destination(_ route: WorkspaceRoute) -> some View {
        switch route {
        case .diff(let path):
            DiffLoader(session: session, path: path, commit: nil)
        case .file(let path):
            FileLoader(session: session, path: path)
        case .folder(let path):
            FileList(session: session, path: path, open: push)
        case .commit(let commit):
            CommitFiles(session: session, commit: commit, open: push)
        case .commitDiff(let hash, let path):
            DiffLoader(session: session, path: path, commit: hash)
        case .artifact(let artifact):
            ArtifactView(session: session, artifact: artifact)
        }
    }
}

enum WorkspaceTab: String, CaseIterable, Identifiable {
    case changes, files, history, plans
    var id: String { rawValue }
    var title: String {
        switch self {
        case .changes: "Changes"
        case .files: "Files"
        case .history: "History"
        case .plans: "Plans"
        }
    }
}

enum WorkspaceRoute: Hashable {
    case diff(String), file(String), folder(String), commit(WorkspaceCommit), commitDiff(String, String), artifact(Artifact)

    var title: String {
        switch self {
        case .diff(let path), .file(let path), .folder(let path), .commitDiff(_, let path): path
        case .commit(let commit): commit.subject
        case .artifact(let artifact): artifact.title ?? artifact.name
        }
    }
}

// MARK: - Loading

/// Loads one thing from the daemon and shows it, a spinner, or what went
/// wrong, with a way to try again.
private struct Loaded<Value: Sendable, Content: View>: View {
    let load: () async throws -> Value
    @ViewBuilder var content: (Value) -> Content
    @State private var value: Value?
    @State private var problem: String?
    @State private var attempt = 0

    var body: some View {
        Group {
            if let value {
                content(value)
            } else if let problem {
                VStack(spacing: 10) {
                    Text(problem)
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                        .multilineTextAlignment(.center)
                    Button("Try again") { self.problem = nil; attempt += 1 }
                        .controlSize(.small)
                }
                .padding(24)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ProgressView()
                    .controlSize(.small)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .task(id: attempt) {
            do {
                value = try await load()
            } catch {
                problem = error.localizedDescription
            }
        }
    }
}

private struct EmptyNote: View {
    let text: String
    var body: some View {
        Text(text)
            .font(TextStyle.caption)
            .foregroundStyle(Palette.muted)
            .multilineTextAlignment(.center)
            .padding(28)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

// MARK: - Changes

private struct ChangesList: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    let open: (WorkspaceRoute) -> Void
    @State private var reload = 0

    var body: some View {
        Loaded(load: { try await store.workspace(.listChanges, session: session.id) }) { result in
            let changes = result.changes ?? []
            if changes.isEmpty {
                EmptyNote(text: (result.hidden ?? 0) > 0
                    ? "Only files holding secrets have changed; they aren't shown."
                    : "Nothing has changed since the last commit.")
            } else {
                List {
                    ChangesSummary(changes: changes)
                    ForEach(changes) { change in
                        Button { open(.diff(change.path)) } label: { ChangeRow(change: change) }
                            .buttonStyle(.plain)
                    }
                    if let hidden = result.hidden, hidden > 0 {
                        Text("\(hidden) file\(hidden == 1 ? "" : "s") holding secrets not shown")
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.faint)
                    }
                }
                .listStyle(.plain)
                .scrollContentBackground(.hidden)
            }
        }
        .id(reload)
        .onChange(of: session.state) { _, now in if now != .busy { reload += 1 } }
    }
}

private struct ChangeRow: View {
    let change: WorkspaceChange

    var body: some View {
        HStack(spacing: 10) {
            Text(Self.mark(change.status))
                .font(Fonts.mono(11, .medium))
                .foregroundStyle(Self.tint(change.status))
                .frame(width: 14)
            PathLabel(path: change.path)
            Spacer(minLength: 8)
            if let added = change.added, added > 0 {
                Text("+\(added)").foregroundStyle(Palette.ok)
            }
            if let removed = change.removed, removed > 0 {
                Text("−\(removed)").foregroundStyle(Palette.errorText)
            }
        }
        .font(Fonts.mono(11))
        .padding(.vertical, 5)
        .contentShape(Rectangle())
    }

    static func mark(_ status: String) -> String {
        switch status {
        case "added": "A"
        case "deleted": "D"
        case "renamed": "R"
        case "untracked": "?"
        default: "M"
        }
    }

    static func tint(_ status: String) -> Color {
        switch status {
        case "added", "untracked": Palette.ok
        case "deleted": Palette.errorText
        default: Palette.codeNumber
        }
    }
}

/// A path as its file's name, with the folder it is in dimmed before it.
private struct PathLabel: View {
    let path: String

    var body: some View {
        let name = (path as NSString).lastPathComponent
        let folder = (path as NSString).deletingLastPathComponent
        HStack(spacing: 0) {
            if !folder.isEmpty {
                Text(folder + "/")
                    .foregroundStyle(Palette.faint)
                    .lineLimit(1)
                    .truncationMode(.head)
                    .layoutPriority(0)
            }
            Text(name)
                .foregroundStyle(Palette.text)
                .lineLimit(1)
                .layoutPriority(1)
        }
        .font(TextStyle.caption)
        .help(path)
    }
}

// MARK: - Diffs

private struct DiffLoader: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    let path: String
    let commit: String?

    var body: some View {
        Loaded(load: {
            try await commit == nil
                ? store.workspace(.fileDiff, session: session.id, path: path)
                : store.workspace(.commitDiff, session: session.id, path: path, commit: commit)
        }) { result in
            let rows = DiffRow.parse(result.diff ?? "")
            VStack(spacing: 0) {
                FileBar(session: session, path: path,
                        added: rows.filter { $0.kind == .add }.count,
                        removed: rows.filter { $0.kind == .remove }.count,
                        copy: result.diff, local: commit == nil)
                Divider()
                if rows.isEmpty {
                    EmptyNote(text: result.truncated == true ? "This diff is too large to show." : "No line changes: a binary file, or only its mode changed.")
                } else {
                    DiffView(rows: rows, truncated: result.truncated == true)
                }
            }
        }
    }
}

/// A diff as lines numbered by what they were and became, added lines on
/// green and removed on red.
struct DiffView: View {
    let rows: [DiffRow]
    var truncated = false

    var body: some View {
        // Each row is at least as wide as the view, so its colour runs the
        // whole width rather than stopping where its text does.
        GeometryReader { geometry in
            lines(width: geometry.size.width)
        }
    }

    private func lines(width: CGFloat) -> some View {
        ScrollView([.vertical, .horizontal]) {
            LazyVStack(alignment: .leading, spacing: 0) {
                ForEach(rows) { row in
                    HStack(spacing: 0) {
                        Text(row.oldNumber.map(String.init) ?? "")
                            .frame(width: 38, alignment: .trailing)
                        Text(row.newNumber.map(String.init) ?? "")
                            .frame(width: 38, alignment: .trailing)
                        Text(marker(row.kind))
                            .frame(width: 18)
                        Text(row.text.isEmpty ? " " : row.text)
                            .foregroundStyle(row.kind == .hunk || row.kind == .meta ? Palette.muted : Palette.text)
                            .fixedSize()
                            .padding(.trailing, 16)
                    }
                    .foregroundStyle(Palette.faint)
                    .font(Fonts.mono(11.5))
                    .padding(.vertical, 1)
                    .frame(minWidth: width, alignment: .leading)
                    .background(background(row.kind))
                }
                if truncated {
                    Text("The rest of this diff is too large to show.")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                        .padding(14)
                }
            }
            .textSelection(.enabled)
            .padding(.vertical, 8)
        }
    }

    private func marker(_ kind: DiffRow.Kind) -> String {
        switch kind {
        case .add: "+"
        case .remove: "−"
        default: ""
        }
    }

    private func background(_ kind: DiffRow.Kind) -> Color {
        switch kind {
        case .add: Palette.diffAdd
        case .remove: Palette.diffRemove
        case .hunk: Palette.diffHunk
        default: .clear
        }
    }
}

// MARK: - Files

private struct FileList: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    let path: String
    let open: (WorkspaceRoute) -> Void

    var body: some View {
        Loaded(load: { try await store.workspace(.listFiles, session: session.id, path: path) }) { result in
            let entries = (result.entries ?? []).sorted { a, b in
                a.directory != b.directory ? a.directory : a.name.localizedStandardCompare(b.name) == .orderedAscending
            }
            if entries.isEmpty {
                EmptyNote(text: "This folder is empty.")
            } else {
                List(entries) { entry in
                    Button {
                        let child = path.isEmpty ? entry.name : path + "/" + entry.name
                        open(entry.directory ? .folder(child) : .file(child))
                    } label: {
                        HStack(spacing: 9) {
                            FileTypeIcon(name: entry.name, directory: entry.directory, size: 15)
                                .frame(width: 16)
                            Text(entry.name)
                                .font(TextStyle.caption)
                                .foregroundStyle(Palette.text)
                                .lineLimit(1)
                            Spacer()
                            if let size = entry.size, !entry.directory {
                                Text(ByteCountFormatter.string(fromByteCount: size, countStyle: .file))
                                    .font(TextStyle.caption)
                                    .foregroundStyle(Palette.faint)
                            }
                            if entry.directory {
                                Image(systemName: "chevron.right")
                                    .font(.system(size: 9, weight: .semibold))
                                    .foregroundStyle(Palette.faint)
                            }
                        }
                        .padding(.vertical, 4)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                }
                .listStyle(.plain)
                .scrollContentBackground(.hidden)
            }
        }
    }
}

enum FileIcon {
    /// The theme's icon for a file or folder, as an asset name: by its exact
    /// name first, then its longest extension ("d.ts" before "ts"), then the
    /// theme's plain file or folder. See desktop/scripts/file-icons.py.
    static func asset(_ name: String, directory: Bool = false, open: Bool = false) -> String {
        let name = (name as NSString).lastPathComponent.lowercased()
        if directory {
            let icon = open ? FileIconMap.openFolderNames[name] ?? "folder-open" : FileIconMap.folderNames[name] ?? "folder"
            return "FileIcon-" + icon
        }
        if let icon = FileIconMap.fileNames[name] { return "FileIcon-" + icon }
        var rest = Substring(name)
        while let dot = rest.firstIndex(of: ".") {
            rest = rest[rest.index(after: dot)...]
            if let icon = FileIconMap.extensions[String(rest)] { return "FileIcon-" + icon }
        }
        return "FileIcon-file"
    }

    static func tint(_ name: String) -> Color {
        switch (name as NSString).pathExtension.lowercased() {
        case "swift": Color(red: 0.94, green: 0.45, blue: 0.24)
        case "go": Color(red: 0.0, green: 0.68, blue: 0.85)
        case "ts", "tsx", "mts": Color(red: 0.19, green: 0.47, blue: 0.85)
        case "js", "jsx", "mjs", "cjs": Color(red: 0.86, green: 0.70, blue: 0.10)
        case "py": Color(red: 0.25, green: 0.48, blue: 0.75)
        case "rs": Color(red: 0.80, green: 0.42, blue: 0.27)
        case "json", "yaml", "yml", "toml", "plist": Color(red: 0.75, green: 0.58, blue: 0.13)
        case "md", "txt": Palette.muted
        case "png", "jpg", "jpeg", "gif", "webp", "heic", "svg": Color(red: 0.62, green: 0.38, blue: 0.82)
        case "css", "scss": Color(red: 0.36, green: 0.42, blue: 0.86)
        case "html", "xml": Color(red: 0.89, green: 0.37, blue: 0.24)
        case "sh", "bash", "zsh": Palette.ok
        default: Palette.muted
        }
    }

    static func symbol(_ name: String) -> String {
        switch (name as NSString).pathExtension.lowercased() {
        case "png", "jpg", "jpeg", "gif", "webp", "heic", "svg": "photo"
        case "md", "txt", "rtf": "doc.text"
        case "json", "yaml", "yml", "toml", "plist", "xml": "curlybraces"
        case "": "doc"
        default: "chevron.left.forwardslash.chevron.right"
        }
    }
}

/// A file's or folder's icon from the theme, as the editor's file trees
/// show them.
struct FileTypeIcon: View {
    let name: String
    var directory = false
    var open = false
    var size: CGFloat = 14

    var body: some View {
        Image(FileIcon.asset(name, directory: directory, open: open))
            .resizable()
            .interpolation(.high)
            .frame(width: size, height: size)
            .accessibilityHidden(true)
    }
}

private struct FileLoader: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    let path: String

    var body: some View {
        Loaded(load: { try await store.workspace(.readFile, session: session.id, path: path) }) { result in
            if let image = result.image, let data = Data(base64Encoded: image), let picture = NSImage(data: data) {
                ScrollView([.vertical, .horizontal]) {
                    Image(nsImage: picture)
                        .resizable()
                        .scaledToFit()
                        .frame(maxWidth: max(picture.size.width, 200))
                        .padding(16)
                        .onTapGesture { store.lightbox = Lightbox(image: picture, title: (path as NSString).lastPathComponent) }
                }
            } else if let text = result.text {
                VStack(spacing: 0) {
                    FileBar(session: session, path: path, lines: text.reduce(into: 1) { if $1 == "\n" { $0 += 1 } }, copy: text)
                    Divider()
                    CodeTextView(text: text, path: path)
                    if result.truncated == true {
                        Text("Showing the start of a long file.")
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.muted)
                            .padding(8)
                    }
                }
            } else {
                EmptyNote(text: "This file can't be shown here.")
            }
        }
    }
}

// MARK: - History

private struct HistoryList: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    let open: (WorkspaceRoute) -> Void
    @State private var ref: String?
    @State private var branches: [WorkspaceBranch] = []
    @State private var commits: [WorkspaceCommit] = []
    @State private var more = false
    @State private var problem: String?
    @State private var loading = false

    var body: some View {
        VStack(spacing: 0) {
            if branches.count > 1 {
                Picker("Branch", selection: Binding(get: { ref ?? "" }, set: { ref = $0.isEmpty ? nil : $0 })) {
                    ForEach(branches) { branch in
                        Text(branch.name + (branch.current == true ? " (checked out)" : "")).tag(branch.current == true ? "" : branch.ref)
                    }
                }
                .labelsHidden()
                .padding(.horizontal, 14)
                .padding(.vertical, 8)
                Divider()
            }
            if let problem, commits.isEmpty {
                EmptyNote(text: problem)
            } else if commits.isEmpty, loading {
                ProgressView().controlSize(.small).frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if commits.isEmpty {
                EmptyNote(text: "No commits here yet.")
            } else {
                List {
                    ForEach(commits) { commit in
                        Button { open(.commit(commit)) } label: { CommitRow(commit: commit) }
                            .buttonStyle(.plain)
                    }
                    if more {
                        Button(loading ? "Loading…" : "Load older commits") { Task { await page() } }
                            .buttonStyle(.link)
                            .font(TextStyle.caption)
                            .disabled(loading)
                    }
                }
                .listStyle(.plain)
                .scrollContentBackground(.hidden)
            }
        }
        .task(id: ref) {
            commits = []
            await page()
        }
        .task {
            if let result = try? await store.workspace(.listBranches, session: session.id) {
                branches = result.branches ?? []
            }
        }
    }

    private func page() async {
        loading = true
        defer { loading = false }
        do {
            let result = try await store.workspace(.listCommits, session: session.id, offset: commits.count, ref: ref)
            commits += result.commits ?? []
            more = result.truncated == true
            problem = nil
        } catch {
            problem = error.localizedDescription
        }
    }
}

private struct CommitRow: View {
    let commit: WorkspaceCommit

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(commit.subject)
                .font(TextStyle.caption)
                .foregroundStyle(Palette.text)
                .lineLimit(2)
            HStack(spacing: 6) {
                Text(String(commit.hash.prefix(7))).font(Fonts.mono(10.5))
                Text("·")
                Text(commit.author)
                Text("·")
                Text(Date(timeIntervalSince1970: TimeInterval(commit.authoredAt) / 1000), style: .relative)
                if let added = commit.added, added > 0 { Text("+\(added)").foregroundStyle(Palette.ok) }
                if let removed = commit.removed, removed > 0 { Text("−\(removed)").foregroundStyle(Palette.errorText) }
            }
            .font(TextStyle.caption)
            .foregroundStyle(Palette.faint)
            .lineLimit(1)
        }
        .padding(.vertical, 5)
        .contentShape(Rectangle())
    }
}

private struct CommitFiles: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    let commit: WorkspaceCommit
    let open: (WorkspaceRoute) -> Void

    var body: some View {
        Loaded(load: { try await store.workspace(.commitFiles, session: session.id, commit: commit.hash) }) { result in
            List {
                VStack(alignment: .leading, spacing: 6) {
                    Text(result.commit?.subject ?? commit.subject)
                        .font(TextStyle.bodyMedium)
                        .foregroundStyle(Palette.text)
                    if let body = result.commit?.body, !body.isEmpty {
                        Text(body)
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.textSecondary)
                            .textSelection(.enabled)
                    }
                    Text("\(commit.author) · \(String(commit.hash.prefix(10)))")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.faint)
                }
                .padding(.vertical, 6)
                ForEach(result.changes ?? []) { change in
                    Button { open(.commitDiff(commit.hash, change.path)) } label: { ChangeRow(change: change) }
                        .buttonStyle(.plain)
                }
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
        }
    }
}

/// "9 files changed · +1,317 −2", over the list of changes.
private struct ChangesSummary: View {
    let changes: [WorkspaceChange]

    var body: some View {
        let added = changes.reduce(0) { $0 + ($1.added ?? 0) }
        let removed = changes.reduce(0) { $0 + ($1.removed ?? 0) }
        HStack(spacing: 6) {
            Text("\(changes.count) file\(changes.count == 1 ? "" : "s") changed")
                .foregroundStyle(Palette.textSecondary)
            if added > 0 { Text("+\(added.formatted())").foregroundStyle(Palette.ok) }
            if removed > 0 { Text("−\(removed.formatted())").foregroundStyle(Palette.errorText) }
        }
        .font(TextStyle.captionMedium)
        .padding(.vertical, 4)
        .listRowSeparator(.hidden)
    }
}

/// The bar over a file or a diff: where it is, how big or how much changed,
/// and what can be done with it.
private struct FileBar: View {
    let session: Session
    let path: String
    var added: Int?
    var removed: Int?
    var lines: Int?
    var copy: String?
    /// Whether the file is the one on this Mac now, which Finder can show.
    var local = true
    @State private var copied = false
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        HStack(spacing: 10) {
            FileTypeIcon(name: path, size: 14)
            let folder = (path as NSString).deletingLastPathComponent
            Text(folder.isEmpty ? (session.cwd as NSString).lastPathComponent : folder)
                .font(TextStyle.caption)
                .foregroundStyle(Palette.faint)
                .lineLimit(1)
                .truncationMode(.head)
                .help(path)
            Spacer(minLength: 8)
            if let lines {
                Text("\(lines.formatted()) line\(lines == 1 ? "" : "s")")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.faint)
            }
            if let added, added > 0 {
                Text("+\(added)").font(Fonts.mono(11)).foregroundStyle(Palette.ok)
            }
            if let removed, removed > 0 {
                Text("−\(removed)").font(Fonts.mono(11)).foregroundStyle(Palette.errorText)
            }
            if let copy {
                Button {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(copy, forType: .string)
                    copied = true
                    Task {
                        try? await Task.sleep(for: .seconds(1.5))
                        copied = false
                    }
                } label: {
                    Image(systemName: copied ? "checkmark" : "doc.on.doc")
                        .font(.system(size: 11))
                        .frame(width: 22, height: 22)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .foregroundStyle(copied ? Palette.ok : Palette.muted)
                .help(copied ? "Copied" : "Copy")
            }
            if local {
                Button {
                    EditorOpener.open(folder: session.cwd, file: path, with: openWindow)
                } label: {
                    Image(systemName: "pencil")
                        .font(.system(size: 11))
                        .frame(width: 22, height: 22)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .foregroundStyle(Palette.muted)
                .help("Edit in the editor")
                Button {
                    let url = URL(fileURLWithPath: session.cwd).appendingPathComponent(path)
                    NSWorkspace.shared.activateFileViewerSelecting([url])
                } label: {
                    Image(systemName: "folder")
                        .font(.system(size: 11))
                        .frame(width: 22, height: 22)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .foregroundStyle(Palette.muted)
                .help("Show in Finder")
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 7)
    }
}

// MARK: - Plans

private struct ArtifactList: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    let open: (WorkspaceRoute) -> Void

    var body: some View {
        Loaded(load: { try await store.artifacts(session.id) }) { artifacts in
            if artifacts.isEmpty {
                EmptyNote(text: "This agent hasn't written any plans or documents.")
            } else {
                List(artifacts) { artifact in
                    Button { open(.artifact(artifact)) } label: {
                        HStack(alignment: .top, spacing: 10) {
                            Image(systemName: Self.symbol(artifact.kind))
                                .font(.system(size: 13))
                                .foregroundStyle(Palette.working)
                                .frame(width: 18)
                                .padding(.top, 1)
                            VStack(alignment: .leading, spacing: 3) {
                                HStack(spacing: 6) {
                                    Text(artifact.title ?? artifact.name)
                                        .font(TextStyle.captionMedium)
                                        .foregroundStyle(Palette.text)
                                        .lineLimit(1)
                                    if artifact.review == true {
                                        Text("Needs your review")
                                            .font(TextStyle.caption)
                                            .foregroundStyle(Palette.needsYouText)
                                            .padding(.horizontal, 6)
                                            .padding(.vertical, 1)
                                            .background(Palette.needsYouWash, in: Capsule())
                                    }
                                }
                                if let summary = artifact.summary, !summary.isEmpty {
                                    Text(summary)
                                        .font(TextStyle.caption)
                                        .foregroundStyle(Palette.muted)
                                        .lineLimit(2)
                                }
                                Text(Date(timeIntervalSince1970: TimeInterval(artifact.updatedAt) / 1000), style: .relative)
                                    .font(TextStyle.caption)
                                    .foregroundStyle(Palette.faint)
                            }
                        }
                        .padding(.vertical, 5)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                }
                .listStyle(.plain)
                .scrollContentBackground(.hidden)
            }
        }
    }

    static func symbol(_ kind: String) -> String {
        switch kind {
        case "plan": "list.bullet.clipboard"
        case "task": "checklist"
        case "walkthrough": "figure.walk"
        case "spec": "doc.richtext"
        case "image": "photo"
        case "video": "film"
        default: "doc"
        }
    }
}

/// A plan or document as it reads, with Approve and Ask for changes when
/// the agent asked for the user's review.
private struct ArtifactView: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    let artifact: Artifact
    @State private var comment = ""
    @State private var busy = false
    @State private var outcome: String?
    @State private var problem: String?

    var body: some View {
        VStack(spacing: 0) {
            Loaded(load: { try await store.workspace(.readArtifact, session: session.id, path: artifact.name) }) { result in
                if let image = result.image, let data = Data(base64Encoded: image), let picture = NSImage(data: data) {
                    ScrollView {
                        Image(nsImage: picture).resizable().scaledToFit().padding(16)
                    }
                } else {
                    ScrollView {
                        MarkdownView(result.text ?? "")
                            .textSelection(.enabled)
                            .padding(20)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
            }
            if artifact.review == true {
                Divider()
                review
            }
        }
    }

    private var review: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let outcome {
                Label(outcome, systemImage: "checkmark.circle")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.ok)
            } else {
                Text("\(AgentName.of(session.kind)) asked for your review")
                    .font(TextStyle.captionMedium)
                    .foregroundStyle(Palette.textSecondary)
                TextField("What should change? (for Ask for changes)", text: $comment, axis: .vertical)
                    .textFieldStyle(.roundedBorder)
                    .lineLimit(1...4)
                    .font(TextStyle.caption)
                HStack {
                    if let problem {
                        Text(problem).font(TextStyle.caption).foregroundStyle(Palette.errorText)
                    }
                    Spacer()
                    Button("Ask for changes") { send(approve: false) }
                        .disabled(busy || comment.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    Button("Approve") { send(approve: true) }
                        .buttonStyle(.borderedProminent)
                        .disabled(busy)
                }
            }
        }
        .padding(14)
    }

    private func send(approve: Bool) {
        busy = true
        problem = nil
        Task {
            problem = await store.review(session.id, artifact: artifact.name, approve: approve, comment: comment)
            busy = false
            if problem == nil { outcome = approve ? "Approved" : "Sent your changes" }
        }
    }
}
