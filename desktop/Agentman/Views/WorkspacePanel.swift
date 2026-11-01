import AppKit
import SwiftUI

/// An agent's folder beside its conversation: what it changed, its files,
/// and its git history, each opened to the diff, the file or the commit.
struct WorkspacePanel: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    @Binding var tab: WorkspaceTab
    @Environment(\.openWindow) private var openWindow
    @State private var branch: String?
    /// What is open over the list, deepest last.
    @State private var stack: [WorkspaceRoute] = []

    @State private var expanded = false
    @State private var changes: [WorkspaceChange] = []
    /// Files holding secrets that changed, left out of the list.
    @State private var hidden = 0
    @State private var git: GitState?
    /// Bumped after a git action, to read everything again.
    @State private var reread = 0
    /// What the branch was last read for: it is read again when a turn
    /// begins or ends, or after a git action, not at every step.
    @State private var gitReadFor: GitRead?

    var body: some View {
        VStack(spacing: 0) {
            if let route = stack.last {
                header(route.title) { stack.removeLast() }
                Divider().overlay(Palette.line)
                destination(route).id(route)
            } else if expanded {
                header(tab.title) { expanded = false }
                Divider().overlay(Palette.line)
                switch tab {
                case .changes: ChangesList(session: session, open: push)
                case .files: FileList(session: session, path: "", open: push)
                case .history: HistoryPanel(sessionID: session.id, cwd: session.cwd, state: session.state, activity: session.lastActivityAt)
                case .plans: ArtifactList(session: session, open: push)
                }
            } else {
                VStack(alignment: .leading, spacing: 0) {
                    CardRow("This Mac", symbol: "desktopcomputer") {
                        Button {
                            NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: session.cwd)
                        } label: {
                            Image(systemName: "folder")
                                .frame(width: 20, height: 20)
                                .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .help("Show the folder in Finder")
                    }
                    if FileManager.default.fileExists(atPath: session.cwd) {
                        CardRow("Open in editor", symbol: "arrow.up.right.square", action: {
                            EditorOpener.open(folder: session.cwd, with: openWindow)
                        })
                        CardRow("Open terminal", symbol: "terminal", action: { RightPanel.showTerminal(in: session.cwd) })
                    }
                }
                .cardSection()
                Divider().overlay(Palette.line)
                VStack(alignment: .leading, spacing: 0) {
                    if let branch {
                        CardRow(branch, symbol: "arrow.triangle.branch")
                        // Not from a daemon too old to read where the branch stands.
                        if var git {
                            // The changes as last listed, read more often than the branch.
                            let _ = (git.changes = changes.count + hidden)
                            GitActionsRow(session: session, state: git, changes: changes, hidden: hidden) { reread += 1 }
                        }
                    }
                    CardRow("History", symbol: "clock.arrow.circlepath", action: { RightPanel.show(.history) })
                    let added = changes.reduce(0) { $0 + ($1.added ?? 0) }
                    let removed = changes.reduce(0) { $0 + ($1.removed ?? 0) }
                    CardRow("Changes", symbol: "plusminus", action: { RightPanel.show(.diff) }) {
                        // Nothing to count, nothing shown.
                        if added + removed > 0 {
                            HStack(spacing: 6) {
                                Text("+\(added)").foregroundStyle(Palette.ok)
                                Text("−\(removed)").foregroundStyle(Palette.errorText)
                            }
                            .font(Fonts.mono(11))
                        }
                    }
                    CardRow("Files", symbol: "folder", action: { RightPanel.show(.files) })
                    if (session.artifacts ?? 0) > 0 {
                        CardRow("Plans", symbol: "doc.text", action: { show(.plans) }) {
                            Image(systemName: "chevron.right").font(.system(size: 9, weight: .semibold)).foregroundStyle(Palette.faint)
                                .frame(width: 20)
                        }
                    }
                }
                .cardSection()
                ProjectSiblings(session: session)
            }
        }
        .font(Fonts.sans(12))
        .foregroundStyle(Palette.textSecondary)
        .frame(maxWidth: .infinity, maxHeight: expanded || !stack.isEmpty ? .infinity : nil, alignment: .top)
        .frame(width: expanded || !stack.isEmpty ? Self.openWidth : Self.summaryWidth)
        .animation(.easeOut(duration: 0.18), value: expanded || !stack.isEmpty)
        .background(Palette.surface, in: RoundedRectangle(cornerRadius: 14))
        .clipShape(RoundedRectangle(cornerRadius: 14))
        .overlay(RoundedRectangle(cornerRadius: 14).strokeBorder(Palette.line))
        // Again as the agent works: a turn changes files long before it ends.
        // A burst of activity asks once, a second after it settles.
        .task(id: Refresh(state: session.state, activity: session.lastActivityAt, reread: reread)) {
            if changes.isEmpty == false || branch != nil { try? await Task.sleep(for: .seconds(1)) }
            guard !Task.isCancelled else { return }
            let read = GitRead(state: session.state, reread: reread)
            if gitReadFor != read {
                gitReadFor = read
                let state = try? await store.git(.gitStatus, session: session.id)
                if state != git { git = state }
                // A daemon from before the git actions names the branch here.
                let name = state != nil ? state?.branch : try? await store.workspace(.listCommits, session: session.id).branch
                if name != branch { branch = name }
            }
            let listed = try? await store.workspace(.listChanges, session: session.id)
            if (listed?.changes ?? []) != changes { changes = listed?.changes ?? [] }
            if (listed?.hidden ?? 0) != hidden { hidden = listed?.hidden ?? 0 }
        }
        .task {
            let open = ProcessInfo.processInfo.environment["AGENTMAN_FILES_OPEN"] ?? ""
            if open.hasPrefix("diff:") { stack = [.diff(String(open.dropFirst(5)))] }
            if open.hasPrefix("file:") { stack = [.file(String(open.dropFirst(5)))] }
        }
        .onChange(of: tab) { _, _ in stack = []; expanded = true }
        .onChange(of: session.id) { _, _ in stack = []; expanded = false }
    }

    /// The other agents working in the same folder, at the card's foot. A
    /// view of its own, as it reads every agent's session: news of any agent
    /// redraws it alone, not the card.
    private struct ProjectSiblings: View {
        @Environment(AgentsStore.self) private var store
        let session: Session

        var body: some View {
            let siblings = store.sessions.filter { $0.cwd == session.cwd && $0.id != session.id && $0.state != .ended }
            if !siblings.isEmpty {
                Divider().overlay(Palette.line)
                VStack(alignment: .leading, spacing: 0) {
                    Text("In this project · \(siblings.count)")
                        .font(Fonts.sans(11))
                        .foregroundStyle(Palette.faint)
                        .padding(.horizontal, CardRowMetrics.inset)
                        .frame(height: 24, alignment: .leading)
                    ForEach(Array(siblings.prefix(6))) { sibling in
                        CardRow(sibling.name, action: { store.selection = .agent(sibling.id) }) {
                            AgentMark(kind: sibling.kind, size: 14)
                        } trailing: {
                            Circle().fill(Palette.dot(sibling.state)).frame(width: 6, height: 6).frame(width: 20)
                        }
                    }
                }
                .cardSection()
            }
        }
    }

    /// What brings the branch and changes up to date.
    private struct Refresh: Equatable {
        let state: SessionState
        let activity: Int64?
        let reread: Int
    }

    private struct GitRead: Equatable {
        let state: SessionState
        let reread: Int
    }

    /// The summary beside the thread; a list, a diff or a file, wider.
    static let summaryWidth: CGFloat = 220
    static let openWidth: CGFloat = 540

    private func show(_ selected: WorkspaceTab) {
        tab = selected
        expanded = true
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
    case diff(String), file(String), folder(String), artifact(Artifact)

    var title: String {
        switch self {
        case .diff(let path), .file(let path), .folder(let path): path
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

    // git's "XY" codes: these read every change as modified when they
    // matched words the daemon never sends.
    static func mark(_ status: String) -> String { ChangeStatus.letter(status) }

    static func tint(_ status: String) -> Color { ChangeStatus.tint(status) }
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
                Divider().overlay(Palette.line)
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

struct FileLoader: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    let path: String

    var body: some View {
        Loaded(load: { try await store.workspace(.readFile, session: session.id, path: path) }) { result in
            if let image = result.image, let data = Data(base64Encoded: image), let picture = NSImage(data: data) {
                // Fitted to the panel, never past its own size; a click opens
                // it full window, where it zooms.
                Image(nsImage: picture)
                    .resizable()
                    .interpolation(.high)
                    .scaledToFit()
                    .frame(maxWidth: picture.size.width, maxHeight: picture.size.height)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .padding(16)
                    .contentShape(Rectangle())
                    .onTapGesture { store.lightbox = Lightbox(image: picture, title: (path as NSString).lastPathComponent) }
                    .help("Open full window")
                    .contextMenu {
                        Button("Copy Image") { ImageClipboard.copy(picture) }
                    }
            } else if let text = result.text {
                VStack(spacing: 0) {
                    FileBar(session: session, path: path, lines: text.reduce(into: 1) { if $1 == "\n" { $0 += 1 } }, copy: text)
                    Divider().overlay(Palette.line)
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
                Divider().overlay(Palette.line)
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

/// How the workspace card's rows sit: each the same height, its icon in a
/// column of its own, its words at one inset, so the card reads as one list.
enum CardRowMetrics {
    static let height: CGFloat = 30
    /// From a section's edge to a row's icon, and to a heading.
    static let inset: CGFloat = 8
    static let icon: CGFloat = 16
    static let gap: CGFloat = 10
}

/// A row of the workspace card: an icon, its words, and what goes at its
/// end. A row that does something lights up under the pointer.
struct CardRow<Icon: View, Trailing: View>: View {
    let title: String
    var action: (() -> Void)?
    let icon: Icon
    let trailing: Trailing
    @State private var hovering = false

    init(_ title: String, action: (() -> Void)? = nil, @ViewBuilder icon: () -> Icon, @ViewBuilder trailing: () -> Trailing) {
        self.title = title
        self.action = action
        self.icon = icon()
        self.trailing = trailing()
    }

    var body: some View {
        if let action {
            Button(action: action) { content }
                .buttonStyle(.plain)
                .onHover { hovering = $0 }
        } else {
            content
        }
    }

    private var content: some View {
        HStack(spacing: CardRowMetrics.gap) {
            icon
                .foregroundStyle(Palette.muted)
                .frame(width: CardRowMetrics.icon)
            Text(title)
                .foregroundStyle(Palette.textSecondary)
                .lineLimit(1)
                .truncationMode(.middle)
            Spacer(minLength: 6)
            trailing.foregroundStyle(Palette.muted)
        }
        .font(Fonts.sans(12))
        .padding(.horizontal, CardRowMetrics.inset)
        .frame(height: CardRowMetrics.height)
        .background(hovering ? Palette.fill.opacity(0.6) : .clear, in: RoundedRectangle(cornerRadius: 7))
        .contentShape(Rectangle())
    }
}

extension CardRow where Icon == Image {
    init(_ title: String, symbol: String, action: (() -> Void)? = nil, @ViewBuilder trailing: () -> Trailing) {
        self.init(title, action: action, icon: { Image(systemName: symbol) }, trailing: trailing)
    }
}

extension CardRow where Icon == Image, Trailing == EmptyView {
    init(_ title: String, symbol: String, action: (() -> Void)? = nil) {
        self.init(title, symbol: symbol, action: action) { EmptyView() }
    }
}

extension View {
    /// A section of the workspace card: its rows, inset alike.
    func cardSection() -> some View {
        padding(.horizontal, 8)
            .padding(.vertical, 6)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}
