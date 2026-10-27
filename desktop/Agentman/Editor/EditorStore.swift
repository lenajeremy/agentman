import AppKit
import Observation
import SwiftUI

/// A file or folder in the editor's tree.
struct FileEntry: Identifiable, Hashable, Sendable {
    /// Its path inside the folder.
    let path: String
    let name: String
    let isDirectory: Bool
    var id: String { path }
}

/// What git says about a file.
enum GitMark: Sendable, Equatable {
    case modified, added, untracked, deleted, renamed, conflicted

    var letter: String {
        switch self {
        case .modified: "M"
        case .added: "A"
        case .untracked: "U"
        case .deleted: "D"
        case .renamed: "R"
        case .conflicted: "!"
        }
    }

    var tint: Color {
        switch self {
        case .modified, .renamed: Palette.gitModified
        case .added, .untracked: Palette.ok
        case .deleted, .conflicted: Palette.errorText
        }
    }
}

/// The editor for each folder, kept for the app's life, so closing an
/// editor window loses nothing: its tabs and unsaved edits are there when
/// it opens again.
@MainActor
final class EditorRegistry {
    static let shared = EditorRegistry()
    private var stores: [String: EditorStore] = [:]

    func store(for folder: String) -> EditorStore {
        let key = URL(fileURLWithPath: folder).standardizedFileURL.path
        if let store = stores[key] { return store }
        let store = EditorStore(root: URL(fileURLWithPath: key, isDirectory: true))
        stores[key] = store
        return store
    }

    /// Every file with unsaved edits, in any folder.
    var unsaved: [EditorDocument] {
        stores.values.flatMap { $0.documents.filter(\.isDirty) }
    }
}

/// One folder in the editor: its tree, its files open in tabs, which one is
/// in front, and what git says has changed.
@MainActor @Observable
final class EditorStore {
    let root: URL
    private(set) var documents: [EditorDocument] = []
    /// The splits, left to right, and the file each shows: one, unless the
    /// window is split (⌘\\). See Splits below.
    var panes: [UUID?] = [nil]
    /// The split with the keyboard.
    var focusedPane = 0
    /// Where the splits are across the editor, as the page measured them,
    /// for the views a split shows over its text (a Markdown preview).
    var paneFrames: [ClosedRange<CGFloat>] = []
    /// The file in the split with the keyboard: the one in front.
    var activeID: UUID? {
        get { panes.indices.contains(focusedPane) ? panes[focusedPane] : nil }
        set {
            if !panes.indices.contains(focusedPane) { focusedPane = max(panes.count - 1, 0) }
            if panes.isEmpty { panes = [nil] }
            panes[focusedPane] = newValue
        }
    }
    /// The tree's selected row.
    var selection: String?
    private(set) var expanded: Set<String> = []
    private(set) var children: [String: [FileEntry]] = [:]
    private(set) var marks: [String: GitMark] = [:]
    private(set) var untrackedFolders: [String] = []
    private(set) var changedFolders: Set<String> = []
    private(set) var branch: String?
    private(set) var isRepository = false
    var quickOpen = false
    /// ⇧⌘F's search palette, and the search it shows: kept between
    /// openings, as VS Code keeps it. See EditorSearch.swift.
    var searchOpen = false
    var searchQuery = ""
    var searchOptions = SearchOptions()
    var searchResults: SearchResults?
    var searchRunning = false
    @ObservationIgnored var searching: SearchKey?
    @ObservationIgnored var searchWork: Task<SearchResults, Never>?
    /// Vim's mode in the editor while Vim is on ("normal", "insert",
    /// "visual", "visual line", "visual block", "replace"), and the keys of
    /// a command not finished yet.
    var vimMode: String?
    var vimKeys = ""
    /// The agent panel at the window's right, open as it was last left.
    var showAgentPanel = UserDefaults.standard.bool(forKey: "agentPanelOpen") {
        didSet { UserDefaults.standard.set(showAgentPanel, forKey: "agentPanelOpen") }
    }
    /// What the panel shows: nil until it first opens, when it picks the
    /// agent working on the folder.
    var panelRoute: PanelRoute?
    /// Asked to show the agent working on the folder, as `am open` asks:
    /// done once the agents are known.
    var showsFolderAgent = false

    /// Opens the panel on the agent working on the folder, when asked and
    /// the agents are known; with none there, the panel stays as it was.
    func showFolderAgent(in sessions: [Session], loaded: Bool) {
        guard showsFolderAgent, loaded else { return }
        showsFolderAgent = false
        guard let session = AgentPanel.agent(for: root.path, in: sessions) else { return }
        panelRoute = .agent(session.id)
        showAgentPanel = true
    }
    /// The folder's files for `@` in the panel's message box.
    @ObservationIgnored private(set) var mentions: FileMentions!

    // The tree's Vim keys (TreeVim.swift).
    /// Files and folders marked with Space or v, for one action on all.
    var marked: Set<String> = []
    /// What y, c or x took, for p to put somewhere.
    var treeClipboard: TreeClipboard?
    /// The folder a new file or folder goes in, while a asks its name.
    var creatingIn: String?
    /// The row being renamed in place, after r.
    var renamingInTree: String?
    /// What / filters the tree to; nil when not filtering.
    var treeFilter: String?
    /// The tree's keys, after ?.
    var showTreeKeys = false
    /// What a key just did, said for a moment in the status bar.
    private(set) var notice: String?
    /// Files and folders waiting for the user to confirm the Trash.
    var trashing: [FileEntry] = []
    /// Keys typed toward a command of more than one: g, then g or y.
    @ObservationIgnored var treeKeys = ""
    /// Set while a key moves the tree's selection, so moving over a file
    /// doesn't open it.
    @ObservationIgnored var movingInTree = false
    /// The tree's file changes, newest last, for undo and redo.
    @ObservationIgnored var treeUndo: [TreeChange] = []
    @ObservationIgnored var treeRedo: [TreeChange] = []
    @ObservationIgnored private var noticeTask: Task<Void, Never>?
    /// The shells in the terminal panel; they run on while the panel is
    /// hidden or the window closed, until the app quits.
    private(set) var terminals: [TerminalSession] = []
    var activeTerminalID: UUID?
    var showTerminal = false {
        didSet { if showTerminal && terminals.isEmpty { newTerminal() } }
    }
    /// A tab asking whether to save its edits before it closes.
    var closing: EditorDocument?
    var problem: String?
    private(set) var allFiles: [String] = []
    @ObservationIgnored private var allFilesAt: Date?
    @ObservationIgnored weak var window: NSWindow?
    /// The tree's own view, for giving it the keyboard.
    @ObservationIgnored weak var treeView: NSOutlineView?
    /// The page the files are edited in, kept while the window is closed,
    /// for a while, so each tab's undo and scroll are there when it opens.
    @ObservationIgnored var monaco: MonacoController?
    /// am serve, where the language servers are.
    @ObservationIgnored var client: DaemonClient?

    init(root: URL) {
        self.root = root
        children[""] = Self.list("", in: root)
        mentions = FileMentions(
            root: root,
            files: { [weak self] in self?.allFiles ?? [] },
            suggested: { [weak self] in
                guard let self else { return [] }
                let open = documents.filter { !$0.readOnly && $0.kind == .text }
                let front = active.flatMap { $0.readOnly ? nil : $0.path }
                return (front.map { [$0] } ?? []) + open.map(\.path) + marks.filter { $0.value != .deleted }.keys.sorted()
            },
            load: { [weak self] in await self?.loadAllFiles() }
        )
    }

    var name: String { root.lastPathComponent }
    var active: EditorDocument? { documents.first { $0.id == activeID } }
    var hasUnsaved: Bool { documents.contains(where: \.isDirty) }

    func entries(in folder: String) -> [FileEntry] { children[folder] ?? [] }

    func isExpanded(_ folder: String) -> Bool { expanded.contains(folder) }

    func setExpanded(_ folder: String, _ open: Bool) {
        if open {
            if children[folder] == nil { children[folder] = Self.list(folder, in: root) }
            expanded.insert(folder)
        } else {
            expanded.remove(folder)
        }
    }

    /// Git's mark for a file, or for one inside a folder git hasn't seen.
    func mark(for path: String) -> GitMark? {
        if let mark = marks[path] { return mark }
        return untrackedFolders.contains(where: { path.hasPrefix($0 + "/") }) ? .untracked : nil
    }

    // MARK: - Tabs

    /// Opens a file in a tab, or brings its tab to the front.
    func open(_ path: String) {
        activeID = openInBackground(path).id
        reveal(path)
    }

    /// Opens a file in a tab behind the one in front, or finds its tab.
    @discardableResult
    func openInBackground(_ path: String) -> EditorDocument {
        if let document = documents.first(where: { $0.path == path && !$0.readOnly }) { return document }
        let document = EditorDocument(root: root, path: path)
        documents.append(document)
        if activeID == nil { activeID = document.id }
        return document
    }

    /// Opens any file, as Go to Definition does: one in the folder as usual,
    /// one from outside it (a dependency, the standard library) read-only.
    func open(fileAt url: URL) {
        let path = url.standardizedFileURL.path
        if let inside = relativePath(of: url) {
            open(inside)
        } else if let document = documents.first(where: { $0.readOnly && $0.url.standardizedFileURL.path == path }) {
            activeID = document.id
        } else {
            let document = EditorDocument(root: URL(fileURLWithPath: "/"), path: String(path.drop { $0 == "/" }), readOnly: true)
            documents.append(document)
            activeID = document.id
        }
    }

    /// A file's path inside the folder, or nil for one outside it.
    func relativePath(of url: URL) -> String? {
        let path = url.standardizedFileURL.path
        let base = root.standardizedFileURL.path
        guard path.hasPrefix(base + "/") else { return nil }
        return String(path.dropFirst(base.count + 1))
    }

    func activate(_ document: EditorDocument) {
        activeID = document.id
        if !document.readOnly { reveal(document.path) }
    }

    /// Shows a file in the tree: its folders open, its row selected.
    func reveal(_ path: String) {
        var folder = (path as NSString).deletingLastPathComponent
        while !folder.isEmpty {
            setExpanded(folder, true)
            folder = (folder as NSString).deletingLastPathComponent
        }
        selection = path
    }

    /// Closes a tab, asking first when it has unsaved edits.
    func requestClose(_ document: EditorDocument) {
        if document.isDirty {
            closing = document
        } else {
            close(document)
        }
    }

    func close(_ document: EditorDocument) {
        guard let index = documents.firstIndex(where: { $0.id == document.id }) else { return }
        documents.remove(at: index)
        // A split that showed it shows another file, one no split shows,
        // or, with none left to show, closes.
        for pane in panes.indices.reversed() where panes[pane] == document.id {
            let near = documents.indices.sorted { abs($0 - index) < abs($1 - index) }
                .map { documents[$0].id }.first { !panes.contains($0) }
            if let near {
                panes[pane] = near
            } else if panes.count > 1 {
                closePane(pane)
            } else {
                panes[pane] = documents.isEmpty ? nil : documents[min(index, documents.count - 1)].id
            }
        }
        if closing?.id == document.id { closing = nil }
    }

    // MARK: - Splits

    /// At most this many splits side by side.
    static let maxPanes = 4

    /// ⌘\\: a split to the right of the one in front, showing its file, as
    /// VS Code's Split Editor does; the same file in two splits is the same
    /// text, edited in either.
    func split() {
        guard panes.count < Self.maxPanes else { return }
        panes.insert(activeID, at: focusedPane + 1)
        focusedPane += 1
    }

    /// Closes a split; its files stay open in their tabs.
    func closePane(_ index: Int) {
        guard panes.count > 1, panes.indices.contains(index) else { return }
        panes.remove(at: index)
        // The one on its left takes the keyboard, as going back does.
        if focusedPane >= index { focusedPane = max(min(focusedPane - 1, panes.count - 1), 0) }
    }

    /// The split clicked into, or moved to with Vim's Ctrl-W.
    func focusPane(_ index: Int) {
        guard panes.indices.contains(index), index != focusedPane else { return }
        focusedPane = index
    }

    func closeOthers(than document: EditorDocument) {
        for other in documents where other.id != document.id && !other.isDirty { close(other) }
        activeID = document.id
    }

    func closeSaved() {
        for document in documents where !document.isDirty { close(document) }
    }

    /// Every tab: the saved ones at once, then each with unsaved edits asks.
    func closeAll() {
        closeSaved()
        if let dirty = documents.first(where: \.isDirty) {
            activeID = dirty.id
            requestClose(dirty)
        }
    }

    /// A file in a new split to the right of the one in front.
    func openToSide(_ document: EditorDocument) {
        guard panes.count < Self.maxPanes else {
            activate(document)
            return
        }
        split()
        activeID = document.id
    }

    func saveActive() {
        active?.save()
    }

    func saveAll() {
        for document in documents where document.isDirty { document.save() }
    }

    /// Vim's `:` commands that are the editor's: `:w`, `:q`, `:wq`, `:q!`,
    /// `:wa`, `:qa`, `:e path` and `:Ex`.
    func vimCommand(_ name: String, path: String? = nil) {
        switch name {
        case "save":
            saveActive()
        case "close":
            if let active { requestClose(active) }
        case "discard":
            if let active { close(active) }
        case "saveClose":
            if let active, !active.isDirty || active.save() { close(active) }
        case "saveAll":
            saveAll()
        case "closeAll":
            closeAll()
        case "split":
            split()
        case "saveCloseAll":
            saveAll()
            closeSaved()
        case "open":
            guard let path, !path.isEmpty else { quickOpen = true; return }
            let url = path.hasPrefix("/") ? URL(fileURLWithPath: path)
                : path.hasPrefix("~") ? URL(fileURLWithPath: (path as NSString).expandingTildeInPath)
                : root.appendingPathComponent(path)
            if FileManager.default.fileExists(atPath: url.path) {
                open(fileAt: url)
            } else {
                problem = "There's no file at \(path)."
            }
        case "goToFile":
            quickOpen = true
        case "tree":
            focusTree()
        default:
            break
        }
    }

    // MARK: - Focus

    /// Puts the keyboard in the file tree, at the file in front, as Vim's
    /// Ctrl-W h does.
    func focusTree() {
        guard let window, let tree = treeView, tree.window === window else { return }
        if selection == nil || !(entryExists(selection!)) {
            selection = active.flatMap { $0.readOnly ? nil : $0.path } ?? entries(in: "").first?.path
        }
        window.makeFirstResponder(tree)
    }

    /// Puts the keyboard back in the editor.
    func focusEditor() {
        monaco?.focus()
    }

    /// A link followed in a Markdown file's preview: one to a file in the
    /// folder opens it in its tab (a Markdown file rendered, as the link was
    /// read), one to a folder shows it in the tree, as GitHub links a
    /// README's files; a path from / starts at the folder, as on GitHub.
    /// Anything else, the web, mail, opens as it would anywhere.
    func followLink(_ url: URL, from document: EditorDocument) -> OpenURLAction.Result {
        if let scheme = url.scheme, scheme != "file" { return .systemAction }
        let path = url.path(percentEncoded: false)
        // An anchor in the same file: the preview has no headings to go to.
        guard !path.isEmpty else { return .handled }
        let target: URL
        if url.scheme == "file" {
            target = URL(fileURLWithPath: path)
        } else if path.hasPrefix("/") {
            target = root.appendingPathComponent(String(path.dropFirst()))
        } else {
            target = document.url.deletingLastPathComponent().appendingPathComponent(path)
        }
        var isDirectory: ObjCBool = false
        guard let relative = relativePath(of: target),
              FileManager.default.fileExists(atPath: target.standardizedFileURL.path, isDirectory: &isDirectory) else {
            if !AgentmanApp.underTests { NSSound.beep() }
            return .handled
        }
        if isDirectory.boolValue {
            reveal(relative)
            setExpanded(relative, true)
            focusTree()
            return .handled
        }
        open(relative)
        if let opened = active, opened.isMarkdown, !opened.previewing {
            togglePreview()
        }
        return .handled
    }

    /// Shows the Markdown file in front rendered, or its text again. The
    /// text hidden under the preview gives up the keyboard, so nothing typed
    /// edits what can't be seen.
    func togglePreview() {
        guard let document = active, document.isMarkdown else { return }
        document.previewing.toggle()
        if document.previewing {
            window?.makeFirstResponder(nil)
        } else {
            focusEditor()
        }
    }

    private func entryExists(_ path: String) -> Bool {
        entries(in: (path as NSString).deletingLastPathComponent).contains { $0.path == path }
    }

    // MARK: - Terminal

    var activeTerminal: TerminalSession? { terminals.first { $0.id == activeTerminalID } }

    /// A new shell in the folder, in front.
    func newTerminal() {
        let session = TerminalSession(folder: root, number: terminals.count + 1)
        terminals.append(session)
        activeTerminalID = session.id
        if !showTerminal { showTerminal = true }
    }

    /// Shows an agent's live terminal in the panel, in its own tab, or
    /// brings the tab back if it is already there.
    func showAgentTerminal(_ terminal: SessionTerminal, title: String) {
        if let open = terminals.first(where: { $0.command == terminal.argv && !$0.exited }) {
            activeTerminalID = open.id
        } else {
            let session = TerminalSession(folder: root, number: terminals.count + 1, command: terminal.argv, title: title)
            terminals.append(session)
            activeTerminalID = session.id
        }
        if !showTerminal { showTerminal = true }
    }

    func closeTerminal(_ session: TerminalSession) {
        session.terminate()
        guard let index = terminals.firstIndex(where: { $0.id == session.id }) else { return }
        terminals.remove(at: index)
        if activeTerminalID == session.id {
            activeTerminalID = terminals.isEmpty ? nil : terminals[min(index, terminals.count - 1)].id
        }
        if terminals.isEmpty { showTerminal = false }
    }

    // MARK: - Files

    /// A new empty file, opened in a tab. Returns the problem, if any.
    func newFile(in folder: String, named name: String) -> String? {
        guard let path = childPath(folder, name) else { return "That isn't a file name." }
        let url = root.appendingPathComponent(path)
        guard !FileManager.default.fileExists(atPath: url.path) else { return "\(name) is already there." }
        guard FileManager.default.createFile(atPath: url.path, contents: Data()) else { return "Couldn't create \(name)." }
        record(TreeChange(steps: [.made(url)], label: "new \(name)"))
        relist(folder)
        open(path)
        return nil
    }

    func newFolder(in folder: String, named name: String) -> String? {
        guard let path = childPath(folder, name) else { return "That isn't a folder name." }
        do {
            try FileManager.default.createDirectory(at: root.appendingPathComponent(path), withIntermediateDirectories: false)
        } catch {
            return error.localizedDescription
        }
        record(TreeChange(steps: [.made(root.appendingPathComponent(path))], label: "new \(name)"))
        relist(folder)
        setExpanded(path, true)
        selection = path
        return nil
    }

    func rename(_ entry: FileEntry, to name: String) -> String? {
        let folder = (entry.path as NSString).deletingLastPathComponent
        guard let path = childPath(folder, name) else { return "That isn't a name." }
        guard path != entry.path else { return nil }
        do {
            try FileManager.default.moveItem(at: root.appendingPathComponent(entry.path), to: root.appendingPathComponent(path))
        } catch {
            return error.localizedDescription
        }
        record(TreeChange(steps: [.moved(from: root.appendingPathComponent(entry.path), to: root.appendingPathComponent(path))],
                          label: "rename \(entry.name)"))
        for document in documents where document.path == entry.path || document.path.hasPrefix(entry.path + "/") {
            document.moved(to: path + document.path.dropFirst(entry.path.count), root: root)
        }
        if entry.isDirectory, expanded.remove(entry.path) != nil { expanded.insert(path) }
        relist(folder)
        selection = path
        return nil
    }

    /// Moves a file or folder to the Trash, closing what was open from it.
    func trash(_ entry: FileEntry) -> String? {
        do {
            let step = try moveToTrash(entry)
            record(TreeChange(steps: [step], label: "trash \(entry.name)"))
        } catch {
            return error.localizedDescription
        }
        return nil
    }

    /// Moves one to the Trash, and says where it went, so undo can bring
    /// it back.
    func moveToTrash(_ entry: FileEntry) throws -> TreeStep {
        let url = root.appendingPathComponent(entry.path)
        var trashed: NSURL?
        try FileManager.default.trashItem(at: url, resultingItemURL: &trashed)
        for document in documents where document.path == entry.path || document.path.hasPrefix(entry.path + "/") {
            close(document)
        }
        relist((entry.path as NSString).deletingLastPathComponent)
        return .moved(from: url, to: (trashed as URL?) ?? url)
    }

    /// Says what just happened in the status bar, for a moment.
    func flash(_ text: String) {
        notice = text
        noticeTask?.cancel()
        noticeTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(2.5))
            guard !Task.isCancelled else { return }
            self?.notice = nil
        }
    }

    func childPath(_ folder: String, _ name: String) -> String? {
        let name = name.trimmingCharacters(in: .whitespaces)
        guard !name.isEmpty, name != ".", name != "..", !name.contains("/"), !name.contains(":") else { return nil }
        return folder.isEmpty ? name : folder + "/" + name
    }

    func relist(_ folder: String) {
        children[folder] = Self.list(folder, in: root)
    }

    // MARK: - Keeping up with the disk

    /// Lists open folders again, asks git what changed, and checks each
    /// open file against the disk: agents change all three while the editor
    /// is open.
    func refresh() async {
        let folders = [""] + expanded.sorted()
        let root = self.root
        let listed = await Task.detached(priority: .utility) {
            folders.reduce(into: [String: [FileEntry]]()) { $0[$1] = EditorStore.list($1, in: root) }
        }.value
        for (folder, entries) in listed where children[folder] != entries {
            children[folder] = entries
        }

        if let prefix = await Git.run(["rev-parse", "--show-prefix"], in: root) {
            isRepository = true
            let status = await Git.run(["status", "--porcelain=v1", "-z", "--untracked-files=normal"], in: root) ?? ""
            let parsed = GitStatus.parse(status, prefix: prefix.trimmingCharacters(in: .whitespacesAndNewlines))
            if parsed.marks != marks { marks = parsed.marks }
            if parsed.untrackedFolders != untrackedFolders { untrackedFolders = parsed.untrackedFolders }
            let folders = parsed.changedFolders
            if folders != changedFolders { changedFolders = folders }
            let head = await Git.run(["rev-parse", "--abbrev-ref", "HEAD"], in: root)?
                .trimmingCharacters(in: .whitespacesAndNewlines)
            if head != branch { branch = head }
        } else if isRepository {
            isRepository = false
            marks = [:]
            branch = nil
        }

        for document in documents { document.checkDisk() }
        window?.isDocumentEdited = hasUnsaved
    }

    /// Every file in the folder, for Go to File: git's list when it is a
    /// repository, so ignored files stay out, or a walk of the folder.
    func loadAllFiles() async {
        if let at = allFilesAt, Date().timeIntervalSince(at) < 10, !allFiles.isEmpty { return }
        let root = self.root
        var files: [String] = []
        if let listed = await Git.run(["ls-files", "-co", "--exclude-standard", "-z"], in: root) {
            files = listed.split(separator: "\0").map(String.init)
        } else {
            files = await Task.detached(priority: .utility) { EditorStore.walk(root) }.value
        }
        allFiles = files
        allFilesAt = Date()
    }

    /// Folders that are heavy and rarely read, shown dimmed.
    nonisolated static let quietFolders: Set<String> = [
        "node_modules", ".build", "build", "dist", ".next", "DerivedData", "Pods", "target", "__pycache__", ".venv", "venv", ".expo",
    ]

    nonisolated static func list(_ folder: String, in root: URL) -> [FileEntry] {
        let url = folder.isEmpty ? root : root.appendingPathComponent(folder)
        guard let urls = try? FileManager.default.contentsOfDirectory(
            at: url, includingPropertiesForKeys: [.isDirectoryKey], options: []
        ) else { return [] }
        return urls.compactMap { item -> FileEntry? in
            let name = item.lastPathComponent
            guard name != ".git", name != ".DS_Store" else { return nil }
            let isDirectory = (try? item.resourceValues(forKeys: [.isDirectoryKey]))?.isDirectory ?? false
            return FileEntry(path: folder.isEmpty ? name : folder + "/" + name, name: name, isDirectory: isDirectory)
        }
        .sorted {
            if $0.isDirectory != $1.isDirectory { return $0.isDirectory }
            return $0.name.localizedStandardCompare($1.name) == .orderedAscending
        }
    }

    nonisolated static func walk(_ root: URL, limit: Int = 20000) -> [String] {
        guard let enumerator = FileManager.default.enumerator(
            at: root, includingPropertiesForKeys: [.isDirectoryKey], options: [.skipsHiddenFiles]
        ) else { return [] }
        var files: [String] = []
        let base = root.standardizedFileURL.path.count + 1
        for case let url as URL in enumerator {
            if quietFolders.contains(url.lastPathComponent) {
                enumerator.skipDescendants()
                continue
            }
            if (try? url.resourceValues(forKeys: [.isDirectoryKey]))?.isDirectory == true { continue }
            files.append(String(url.standardizedFileURL.path.dropFirst(base)))
            if files.count >= limit { break }
        }
        return files
    }
}

/// `git status --porcelain=v1 -z`, read.
enum GitStatus {
    struct Parsed: Equatable {
        var marks: [String: GitMark] = [:]
        var untrackedFolders: [String] = []
        /// Every folder with a changed file somewhere inside it.
        var changedFolders: Set<String> {
            var folders = Set<String>()
            for path in Array(marks.keys) + untrackedFolders {
                var folder = (path as NSString).deletingLastPathComponent
                while !folder.isEmpty {
                    folders.insert(folder)
                    folder = (folder as NSString).deletingLastPathComponent
                }
            }
            return folders.union(untrackedFolders)
        }
    }

    /// Paths come from the repository's root; prefix is where the editor's
    /// folder sits in it ("desktop/"), and only files under it are kept.
    static func parse(_ output: String, prefix: String) -> Parsed {
        var parsed = Parsed()
        var records = output.split(separator: "\0", omittingEmptySubsequences: true).map(String.init)[...]
        while let record = records.popFirst() {
            guard record.count > 3 else { continue }
            let x = record[record.startIndex]
            let y = record[record.index(after: record.startIndex)]
            var path = String(record.dropFirst(3))
            // A rename or copy is followed by the path it came from.
            if x == "R" || x == "C" { _ = records.popFirst() }
            guard path.hasPrefix(prefix) else { continue }
            path = String(path.dropFirst(prefix.count))
            let mark: GitMark
            if x == "?" && y == "?" {
                if path.hasSuffix("/") {
                    parsed.untrackedFolders.append(String(path.dropLast()))
                    continue
                }
                mark = .untracked
            } else if x == "U" || y == "U" || (x == "A" && y == "A") || (x == "D" && y == "D") {
                mark = .conflicted
            } else if x == "D" || y == "D" {
                mark = .deleted
            } else if x == "A" {
                mark = .added
            } else if x == "R" || y == "R" {
                mark = .renamed
            } else {
                mark = .modified
            }
            if !path.isEmpty { parsed.marks[path] = mark }
        }
        return parsed
    }
}

/// Runs git without taking its index lock, so a refresh never gets in the
/// way of an agent committing at the same moment.
enum Git {
    static func run(_ arguments: [String], in folder: URL) async -> String? {
        await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .utility).async {
                let process = Process()
                process.executableURL = URL(fileURLWithPath: "/usr/bin/git")
                process.arguments = ["-C", folder.path] + arguments
                var environment = ProcessInfo.processInfo.environment
                environment["GIT_OPTIONAL_LOCKS"] = "0"
                process.environment = environment
                let output = Pipe()
                process.standardOutput = output
                process.standardError = FileHandle.nullDevice
                do {
                    try process.run()
                } catch {
                    continuation.resume(returning: nil)
                    return
                }
                let data = output.fileHandleForReading.readDataToEndOfFile()
                process.waitUntilExit()
                continuation.resume(returning: process.terminationStatus == 0 ? String(decoding: data, as: UTF8.self) : nil)
            }
        }
    }
}

/// Finds a file by a few of its letters in order: "atv" finds
/// AgentThreadView.swift. Letters in the file's name, at the start of
/// words and next to each other count most, and shorter paths win ties.
enum Fuzzy {
    static func score(_ query: String, _ path: String) -> Int? {
        let needle = Array(query.lowercased().filter { !$0.isWhitespace })
        guard !needle.isEmpty else { return 0 }
        let name = (path as NSString).lastPathComponent
        // The letters as typed, together, beat the same letters scattered:
        // "need" is the word in "Show Agents That Need You", not n…e…e…d.
        let typed = query.lowercased().trimmingCharacters(in: .whitespaces)
        for (candidate, base) in [(name, 40_000), (path, 30_000)] {
            if let found = candidate.lowercased().range(of: typed) {
                let start = found.lowerBound == candidate.startIndex
                    || " /._-".contains(candidate[candidate.index(before: found.lowerBound)])
                return base + (start ? 5_000 : 0) - path.count
            }
        }
        if let inName = match(needle, name) {
            return 10_000 + inName * 10 - path.count
        }
        guard let inPath = match(needle, path) else { return nil }
        return inPath * 10 - path.count
    }

    private static func match(_ needle: [Character], _ candidate: String) -> Int? {
        let original = Array(candidate)
        let lower = Array(candidate.lowercased())
        guard lower.count == original.count else {
            return candidate.lowercased().contains(String(needle)) ? 1 : nil
        }
        var score = 0
        var found = 0
        var last = -2
        for (index, character) in lower.enumerated() where found < needle.count {
            guard character == needle[found] else { continue }
            var points = 1
            if index == last + 1 { points += 5 }
            if index == 0 || "/._- ".contains(original[index - 1])
                || (original[index].isUppercase && original[index - 1].isLowercase) {
                points += 8
            }
            score += points
            last = index
            found += 1
        }
        return found == needle.count ? score : nil
    }
}
