import AppKit
import SwiftUI

extension FocusedValues {
    /// The editor of the window in front, for the menu's Save and Go to File.
    @Entry var editorStore: EditorStore?
}

/// Opens a folder in the editor, at a file when one is given.
enum EditorOpener {
    /// Opens a folder's editor, with a file in front; showingAgent puts the
    /// agent working on the folder in the panel, as `am open` asks.
    @MainActor
    static func open(folder: String, file: String? = nil, showingAgent: Bool = false, with openWindow: OpenWindowAction) {
        let store = EditorRegistry.shared.store(for: folder)
        if let file, !file.isEmpty { store.open(file) }
        if showingAgent { store.showsFolderAgent = true }
        openWindow(id: "editor", value: store.root.path)
    }

    /// Opens the folder's editor with its terminal showing.
    @MainActor
    static func openTerminal(folder: String, with openWindow: OpenWindowAction) {
        let store = EditorRegistry.shared.store(for: folder)
        store.showTerminal = true
        openWindow(id: "editor", value: store.root.path)
    }

    /// Opens an agent's folder in the editor with the agent's own live
    /// terminal in front.
    @MainActor
    static func openAgentTerminal(folder: String, terminal: SessionTerminal, title: String, with openWindow: OpenWindowAction) {
        let store = EditorRegistry.shared.store(for: folder)
        store.showAgentTerminal(terminal, title: title)
        openWindow(id: "editor", value: store.root.path)
    }

    @MainActor
    static func chooseFolder() -> String? {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.directoryURL = URL(fileURLWithPath: NSHomeDirectory())
        panel.message = "Choose a folder to open in the editor"
        panel.prompt = "Open"
        return panel.runModal() == .OK ? panel.url?.path : nil
    }
}

/// A small editor for one folder: its files in a tree with what git says
/// changed, the files open in tabs, and a status bar, in the manner of VS
/// Code, Zed and the code views in v0 and AI Studio.
struct EditorWindow: View {
    @State private var headerLeading: CGFloat = 82
    @Environment(AgentsStore.self) private var agents
    @State private var store: EditorStore
    @State private var naming: Naming?
    @State private var keyMonitor: Any?
    @AppStorage("terminalHeight") private var terminalHeight = 260.0
    @AppStorage("agentPanelWidth") private var panelWidth = 400.0

    init(folder: String, client: DaemonClient) {
        let store = EditorRegistry.shared.store(for: folder)
        store.client = client
        _store = State(initialValue: store)
    }

    var body: some View {
        VStack(spacing: 0) {
            editorHeader
            HSplitView {
            FileTree(store: store, naming: $naming)
                .frame(width: 240)

            VStack(spacing: 0) {
                HStack(spacing: 0) {
                    VStack(spacing: 0) {
                        if !store.documents.isEmpty {
                            TabStrip(store: store)
                        }
                        if let document = store.active {
                            PathBar(store: store, document: document)
                            DiskBanner(document: document, close: { store.close(document) })
                        }
                        ZStack {
                            EditorPanes(store: store)
                            if let document = store.active, document.kind != .text {
                                NonTextView(document: document)
                                    .modifier(InFrontPane(store: store))
                            }
                            if let document = store.active, document.isMarkdown, document.previewing {
                                MarkdownPreview(store: store, document: document)
                                    .modifier(InFrontPane(store: store))
                            }
                            if store.active == nil, store.panes.count == 1 {
                                EditorEmpty(store: store)
                            }
                        }
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                        if store.showTerminal {
                            PanelDivider(height: $terminalHeight, maximum: (store.window?.frame.height ?? 800) * 0.75)
                                .padding(.vertical, -3)
                                .zIndex(1)
                            TerminalPanel(store: store)
                                .frame(height: terminalHeight)
                        }
                    }
                    .frame(minWidth: 320)
                    .overlay {
                        if store.quickOpen {
                            GoToFile(store: store)
                        } else if store.searchOpen {
                            GoToText(store: store)
                        }
                    }
                    // Agentman beside the code: the folder's agent, or any
                    // other, and Agentman itself.
                    if store.showAgentPanel {
                        ColumnDivider(width: $panelWidth, maximum: (store.window?.frame.width ?? 1200) * 0.6)
                            .zIndex(1)
                        AgentPanel(editor: store)
                            .frame(width: min(panelWidth, max((store.window?.frame.width ?? 1200) * 0.6, ColumnDivider.minimum)))
                            .transition(.move(edge: .trailing).combined(with: .opacity))
                    }
                }
                StatusBar(store: store)
            }
            .background(Palette.paper)
            .animation(.easeOut(duration: 0.18), value: store.showAgentPanel)
            }
        }
        .background(Palette.paper)
        .ignoresSafeArea(.container, edges: .top)
        // An image from the agent panel, full size over the editor.
        .overlay {
            if let box = agents.lightbox, box.place == store.root.path {
                LightboxView(box: box)
            }
        }
        .animation(.easeOut(duration: 0.15), value: agents.lightbox?.id)
        .navigationTitle(store.name)
        .focusedSceneValue(\.editorStore, store)
        .background(WindowReader { window in
            WindowChrome.configure(window)
            headerLeading = WindowChrome.leading(window)
            // One window per folder. macOS can restore a second one beside
            // the first; it gives way, since both would show the same tabs
            // and only one can hold the editor's page.
            if let first = store.window, first !== window, first.isVisible {
                window.close()
            } else if store.window !== window {
                store.window = window
                store.monaco?.place()
            }
        })
        // Agents change files while the editor is open: the tree, git's
        // marks and the open files follow them.
        .task {
            while !Task.isCancelled {
                await store.refresh()
                try? await Task.sleep(for: .seconds(2))
            }
        }
        .onAppear(perform: installKeys)
        .onChange(of: FolderAgentWait(asked: store.showsFolderAgent, loaded: agents.sessionsLoaded, sessions: agents.sessions.map(\.id)),
                  initial: true) {
            store.showFolderAgent(in: agents.sessions, loaded: agents.sessionsLoaded)
        }
        .onAppear {
            // For screenshots and tests: the tree's keys, a filter, marks or
            // the box for a new file.
            guard let tree = ProcessInfo.processInfo.environment["AGENTMAN_TREE"] else { return }
            for part in tree.split(separator: ";").map(String.init) {
                if part == "keys" { store.showTreeKeys = true }
                if part == "create" { store.creatingIn = "" }
                if part.hasPrefix("filter:") { store.treeFilter = String(part.dropFirst(7)) }
                if part.hasPrefix("mark:") { store.marked = Set(part.dropFirst(5).split(separator: ",").map(String.init)) }
            }
        }
        .onDisappear {
            if let keyMonitor { NSEvent.removeMonitor(keyMonitor) }
            keyMonitor = nil
        }
        .confirmationDialog(
            store.closing.map { "Save the changes to \($0.name)?" } ?? "",
            isPresented: Binding(get: { store.closing != nil }, set: { if !$0 { store.closing = nil } })
        ) {
            if let document = store.closing {
                Button("Save") {
                    if document.save() { store.close(document) }
                    store.closing = nil
                }
                Button("Don't Save", role: .destructive) { store.close(document) }
                Button("Cancel", role: .cancel) { store.closing = nil }
            }
        } message: {
            Text("Your changes are lost if you don't save them.")
        }
        .alert(naming?.title ?? "", isPresented: Binding(get: { naming != nil }, set: { if !$0 { naming = nil } })) {
            TextField("Name", text: Binding(get: { naming?.name ?? "" }, set: { naming?.name = $0 }))
            Button(naming?.action ?? "OK") {
                if let naming { store.problem = naming.apply(store) }
                naming = nil
            }
            Button("Cancel", role: .cancel) { naming = nil }
        }
        .alert(store.problem ?? "", isPresented: Binding(get: { store.problem != nil }, set: { if !$0 { store.problem = nil } })) {
            Button("OK", role: .cancel) { store.problem = nil }
        }
    }

    private var editorHeader: some View {
        HStack(spacing: 10) {
            HStack(spacing: 7) {
                AgentmanBadge(size: 20)
                Text("Agentman").font(Fonts.sans(13, .semibold))
            }
            .padding(.leading, headerLeading)
            .frame(width: 240, alignment: .leading)
            Image(systemName: "folder").foregroundStyle(Palette.muted)
            Text(store.name).font(Fonts.sans(13, .medium))
            if let branch = store.branch {
                Text("/").foregroundStyle(Palette.faint)
                Text(branch).font(Fonts.sans(12)).foregroundStyle(Palette.muted)
            }
            Spacer(minLength: 8)
            ChromeButton(symbol: "magnifyingglass", help: "Go to file (⌘P)") { store.quickOpen = true }
            ChromeButton(symbol: "terminal", help: "Toggle terminal", active: store.showTerminal) { store.showTerminal.toggle() }
            ChromeButton(symbol: "folder", help: "Show in Finder") {
                NSWorkspace.shared.activateFileViewerSelecting([store.active?.url ?? store.root])
            }
            ChromeButton(symbol: "sidebar.right", help: "Toggle agent conversation", active: store.showAgentPanel) { store.showAgentPanel.toggle() }
        }
        .padding(.trailing, 14)
        .frame(height: 32)
        .background(Palette.paper)
        .overlay(alignment: .bottom) { Rectangle().fill(Palette.line).frame(height: 1) }
    }

    /// ⌘W closes the tab in front, and the window once no tab is left.
    private func installKeys() {
        guard keyMonitor == nil else { return }
        let store = self.store
        keyMonitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { event in
            // ⌘Z and ⇧⌘Z in the tree undo and redo its file changes, as in
            // Finder, with Vim or without.
            let flags = event.modifierFlags.intersection(.deviceIndependentFlagsMask).subtracting([.capsLock, .numericPad, .function])
            if event.window === store.window, let tree = store.treeView, store.window?.firstResponder === tree,
               flags == .command || flags == [.command, .shift], event.charactersIgnoringModifiers?.lowercased() == "z" {
                if flags.contains(.shift) { store.redoTreeChange() } else { store.undoTreeChange() }
                return nil
            }
            // Vim's keys in the tree, while it has the keyboard.
            if event.window === store.window, UserDefaults.standard.bool(forKey: "editorVim"),
               let tree = store.treeView, store.window?.firstResponder === tree,
               store.handleTreeKey(event) {
                return nil
            }
            guard event.window === store.window,
                  event.modifierFlags.intersection(.deviceIndependentFlagsMask) == .command,
                  event.charactersIgnoringModifiers == "w",
                  let document = store.active else { return event }
            store.requestClose(document)
            return nil
        }
    }
}

/// Asking for a name: a new file or folder, or a new name for one.
struct Naming {
    enum Kind { case file, folder, rename(FileEntry) }
    let kind: Kind
    let folder: String
    var name = ""

    var title: String {
        switch kind {
        case .file: "New File"
        case .folder: "New Folder"
        case .rename(let entry): "Rename \(entry.name)"
        }
    }

    var action: String {
        if case .rename = kind { return "Rename" }
        return "Create"
    }

    @MainActor
    func apply(_ store: EditorStore) -> String? {
        switch kind {
        case .file: store.newFile(in: folder, named: name)
        case .folder: store.newFolder(in: folder, named: name)
        case .rename(let entry): store.rename(entry, to: name)
        }
    }
}

/// What showing the folder's agent waits on: being asked, and the agents.
private struct FolderAgentWait: Equatable {
    let asked: Bool
    let loaded: Bool
    let sessions: [String]
}

// MARK: - Tree

private struct FileTree: View {
    @Bindable var store: EditorStore
    @Binding var naming: Naming?

    var body: some View {
        let filter = store.treeFilter?.trimmingCharacters(in: .whitespaces) ?? ""
        List(selection: $store.selection) {
            if filter.isEmpty {
                Section {
                    FileTreeLevel(store: store, folder: "")
                } header: {
                    Text(store.name)
                        .font(TextStyle.label)
                        .foregroundStyle(Palette.faint)
                }
            } else {
                // What / found, as paths, best first.
                Section {
                    ForEach(store.treeMatches(filter), id: \.self) { path in
                        FilterRow(store: store, path: path)
                            .tag(path)
                    }
                } header: {
                    Text("MATCHING “\(filter)”")
                        .font(TextStyle.label)
                        .foregroundStyle(Palette.faint)
                        .lineLimit(1)
                }
            }
        }
        .listStyle(.sidebar)
        .scrollContentBackground(.hidden)
        .background(Palette.sidebar)
        .tint(Palette.muted)
        .background(SiblingReader<NSOutlineView> { store.treeView = $0 })
        .safeAreaInset(edge: .top, spacing: 0) {
            VStack(spacing: 0) {
                HStack {
                    Text("Files").font(Fonts.sans(12, .medium))
                    Spacer()
                    ChromeButton(symbol: "doc.badge.plus", help: "New file") { naming = Naming(kind: .file, folder: "") }
                    ChromeButton(symbol: "folder.badge.plus", help: "New folder") { naming = Naming(kind: .folder, folder: "") }
                }
                .foregroundStyle(Palette.muted)
                .padding(.horizontal, 14)
                .frame(height: 42)
                if store.treeFilter != nil {
                TreeFilterBar(store: store)
                    .task { await store.loadAllFiles() }
                }
            }
            .background(Palette.sidebar)
        }
        .safeAreaInset(edge: .bottom, spacing: 0) {
            if !store.trashing.isEmpty {
                TreeTrashBar(store: store)
            } else if let folder = store.creatingIn {
                TreeCreateBar(store: store, folder: folder)
                    .id(folder)
            }
        }
        .overlay(alignment: .bottom) {
            if store.showTreeKeys {
                TreeKeysSheet { store.showTreeKeys = false }
                    .padding(.horizontal, 8)
                    .padding(.bottom, 10)
                    .transition(.opacity.combined(with: .move(edge: .bottom)))
            }
        }
        .animation(.easeOut(duration: 0.15), value: store.showTreeKeys)
        .onChange(of: store.selection) { _, path in
            // Moved to with the keys, a file is only pointed at.
            if store.movingInTree {
                store.movingInTree = false
                return
            }
            guard let path, let entry = store.treeEntry(path), !entry.isDirectory else { return }
            if store.active?.path != path { store.open(path) }
        }
        .contextMenu(forSelectionType: String.self) { paths in
            menu(for: paths.first.flatMap(entry))
        } primaryAction: { paths in
            guard let path = paths.first, let entry = entry(path) else { return }
            if entry.isDirectory {
                store.setExpanded(path, !store.isExpanded(path))
            } else {
                store.open(path)
            }
        }
    }

    private func entry(_ path: String) -> FileEntry? {
        store.entries(in: (path as NSString).deletingLastPathComponent).first { $0.path == path }
    }

    @ViewBuilder private func menu(for entry: FileEntry?) -> some View {
        let folder = entry.map { $0.isDirectory ? $0.path : ($0.path as NSString).deletingLastPathComponent } ?? ""
        Button("New File…") { naming = Naming(kind: .file, folder: folder) }
        Button("New Folder…") { naming = Naming(kind: .folder, folder: folder) }
        if let entry {
            Divider()
            Button("Rename…") { naming = Naming(kind: .rename(entry), folder: folder, name: entry.name) }
            Button("Show in Finder") {
                NSWorkspace.shared.activateFileViewerSelecting([store.root.appendingPathComponent(entry.path)])
            }
            Button("Copy Path") { Clipboard.copy(store.root.appendingPathComponent(entry.path).path) }
            Button("Copy Relative Path") { Clipboard.copy(entry.path) }
            Divider()
            Button("Move to Trash…", role: .destructive) {
                store.trashing = [entry]
                store.focusTree()
            }
        }
    }
}

private struct FileTreeLevel: View {
    let store: EditorStore
    let folder: String

    var body: some View {
        ForEach(store.entries(in: folder)) { entry in
            if entry.isDirectory {
                DisclosureGroup(isExpanded: Binding(
                    get: { store.isExpanded(entry.path) },
                    set: { store.setExpanded(entry.path, $0) }
                )) {
                    FileTreeLevel(store: store, folder: entry.path)
                } label: {
                    FileRow(store: store, entry: entry)
                }
                .tag(entry.path)
            } else {
                FileRow(store: store, entry: entry)
                    .tag(entry.path)
            }
        }
    }
}

private struct FileRow: View {
    let store: EditorStore
    let entry: FileEntry

    var body: some View {
        let mark = entry.isDirectory ? nil : store.mark(for: entry.path)
        let quiet = entry.name.hasPrefix(".") || EditorStore.quietFolders.contains(entry.name)
        let open = store.documents.first { $0.path == entry.path }
        HStack(spacing: 6) {
            FileTypeIcon(name: entry.name, directory: entry.isDirectory,
                         open: entry.isDirectory && store.isExpanded(entry.path), size: 15)
                .frame(width: 16)
            if store.renamingInTree == entry.path {
                TreeRenameField(store: store, entry: entry)
            } else {
                Text(entry.name)
                    .font(Fonts.sans(12.5))
                    .foregroundStyle(store.marked.contains(entry.path) ? Palette.workingText : mark?.tint ?? (quiet ? Palette.faint : Palette.text))
                    .strikethrough(mark == .deleted)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            Spacer(minLength: 4)
            if store.marked.contains(entry.path) {
                Image(systemName: "checkmark.circle.fill")
                    .font(.system(size: 11))
                    .foregroundStyle(Palette.working)
                    .help("Marked: y, x and d act on every marked row")
            }
            if open?.isDirty == true {
                Circle().fill(Palette.textSecondary).frame(width: 6, height: 6)
                    .help("Unsaved changes")
            }
            if let mark {
                Text(mark.letter)
                    .font(Fonts.mono(10.5, .medium))
                    .foregroundStyle(mark.tint)
            } else if entry.isDirectory, store.changedFolders.contains(entry.path) {
                Circle().fill(Palette.gitModified.opacity(0.8)).frame(width: 5, height: 5)
                    .help("Something inside changed")
            }
        }
        // Cut with x: faded until p puts it somewhere.
        .opacity(store.treeClipboard.map { $0.cut && $0.paths.contains(entry.path) } == true ? 0.45 : 1)
    }
}

/// A file / found: its name, and the folder it is in.
private struct FilterRow: View {
    let store: EditorStore
    let path: String

    private var name: some View {
        Text((path as NSString).lastPathComponent)
            .font(Fonts.sans(12.5))
            .foregroundStyle(store.marked.contains(path) ? Palette.workingText : Palette.text)
            .lineLimit(1)
    }

    var body: some View {
        let folder = (path as NSString).deletingLastPathComponent
        HStack(spacing: 6) {
            FileTypeIcon(name: (path as NSString).lastPathComponent, size: 15)
                .frame(width: 16)
            // The folder too when there is room for it, else the name alone.
            ViewThatFits(in: .horizontal) {
                HStack(spacing: 6) {
                    name.fixedSize()
                    if !folder.isEmpty {
                        Text(folder)
                            .font(Fonts.sans(11))
                            .foregroundStyle(Palette.faint)
                            .lineLimit(1)
                            .truncationMode(.head)
                            .frame(minWidth: 48, alignment: .leading)
                    }
                }
                name
            }
            Spacer(minLength: 0)
        }
        .help(path)
    }
}

/// A view of the file in front over its own split, below the split's title,
/// when the window is split; over the whole editor otherwise.
private struct InFrontPane: ViewModifier {
    let store: EditorStore

    func body(content: Content) -> some View {
        if store.panes.count > 1, store.paneFrames.indices.contains(store.focusedPane) {
            let frame = store.paneFrames[store.focusedPane]
            content
                .frame(width: frame.upperBound - frame.lowerBound)
                .padding(.top, 26)
                .offset(x: frame.lowerBound)
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        } else {
            content
        }
    }
}

// MARK: - Tabs

private struct TabStrip: View {
    let store: EditorStore

    var body: some View {
        // Two tabs with one name show their folders too.
        let names = Dictionary(grouping: store.documents, by: \.name)
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 0) {
                ForEach(store.documents) { document in
                    EditorTab(store: store, document: document,
                              active: document.id == store.activeID,
                              folder: (names[document.name]?.count ?? 0) > 1 ? (document.path as NSString).deletingLastPathComponent : nil)
                }
            }
        }
        .frame(height: 38)
        .background(Palette.paper)
        .overlay(alignment: .bottom) { Rectangle().fill(Palette.line).frame(height: 1) }
        // Right-click past the tabs: all of them at once.
        .contextMenu {
            Button("Close All") { store.closeAll() }
            Button("Close Saved") { store.closeSaved() }
            if let active = store.active {
                Button("Close Others") { store.closeOthers(than: active) }
            }
        }
    }
}

private struct EditorTab: View {
    let store: EditorStore
    let document: EditorDocument
    let active: Bool
    let folder: String?
    @State private var hovering = false

    var body: some View {
        HStack(spacing: 7) {
            FileTypeIcon(name: document.name, size: 13)
            Text(document.name)
                .font(Fonts.sans(12.5, active ? .medium : .regular))
                .foregroundStyle(active ? Palette.text : Palette.muted)
                .strikethrough(document.deletedOnDisk)
            if let folder, !folder.isEmpty {
                Text(folder)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.faint)
                    .lineLimit(1)
                    .truncationMode(.head)
            }
            ZStack {
                if document.isDirty && !hovering {
                    Circle().fill(Palette.textSecondary).frame(width: 7, height: 7)
                } else {
                    Button {
                        store.requestClose(document)
                    } label: {
                        Image(systemName: "xmark")
                            .font(.system(size: 8.5, weight: .bold))
                            .foregroundStyle(Palette.muted)
                            .frame(width: 16, height: 16)
                            .background(hovering ? Palette.fill : .clear, in: RoundedRectangle(cornerRadius: 4))
                    }
                    .buttonStyle(.plain)
                    .opacity(hovering || active ? 1 : 0)
                    .help("Close (⌘W)")
                }
            }
            .frame(width: 16, height: 16)
        }
        .padding(.leading, 12)
        .padding(.trailing, 8)
        .frame(height: 38)
        .background(active ? Palette.fill.opacity(0.55) : .clear, in: RoundedRectangle(cornerRadius: 6))
        .padding(.horizontal, 3)
        .overlay(alignment: .bottom) {
            if active { Rectangle().fill(Palette.surface).frame(height: 1) }
        }
        .contentShape(Rectangle())
        .onTapGesture { store.activate(document) }
        .onHover { hovering = $0 }
        .help(document.path)
        .contextMenu {
            Button("Close") { store.requestClose(document) }
            Button("Close Others") { store.closeOthers(than: document) }
            Button("Close Saved") { store.closeSaved() }
            Button("Close All") { store.closeAll() }
            Divider()
            Button("Open to the Side") { store.openToSide(document) }
                .disabled(store.panes.count >= EditorStore.maxPanes)
            Divider()
            Button("Show in Finder") { NSWorkspace.shared.activateFileViewerSelecting([document.url]) }
            Button("Copy Path") { Clipboard.copy(document.url.path) }
            Button("Copy Relative Path") { Clipboard.copy(document.path) }
        }
    }
}

/// Where the file sits, and Save and Revert while it has changes.
private struct PathBar: View {
    let store: EditorStore
    let document: EditorDocument

    var body: some View {
        HStack(spacing: 4) {
            let parts = document.readOnly
                ? [(document.url.deletingLastPathComponent().path as NSString).abbreviatingWithTildeInPath, document.name]
                : [store.name] + document.path.split(separator: "/").map(String.init)
            ForEach(Array(parts.enumerated()), id: \.offset) { index, part in
                if index > 0 {
                    Image(systemName: "chevron.right")
                        .font(.system(size: 7.5, weight: .semibold))
                        .foregroundStyle(Palette.faint)
                }
                Text(part)
                    .font(TextStyle.caption)
                    .foregroundStyle(index == parts.count - 1 ? Palette.textSecondary : Palette.faint)
                    .lineLimit(1)
            }
            Spacer(minLength: 12)
            if document.isMarkdown {
                Button { store.togglePreview() } label: {
                    Label(document.previewing ? "Edit" : "Preview", systemImage: document.previewing ? "pencil" : "eye")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                }
                .buttonStyle(.plain)
                .help(document.previewing ? "Back to the text (⇧⌘V)" : "Read it rendered (⇧⌘V)")
            }
            if document.readOnly {
                Text("Read only")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.faint)
                    .help("A file from outside the folder, opened to read")
            }
            if document.isDirty {
                Button("Revert") { document.revert() }
                    .buttonStyle(.plain)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
                    .help("Back to the saved file")
                Button {
                    document.save()
                } label: {
                    Text("Save")
                        .font(TextStyle.captionMedium)
                        .foregroundStyle(Palette.onInverse)
                        .padding(.horizontal, 10)
                        .frame(height: 20)
                        .background(Palette.inverse, in: Capsule())
                }
                .buttonStyle(.plain)
                .help("Save (⌘S)")
            }
        }
        .padding(.horizontal, 14)
        .frame(height: 28)
        .background(Palette.paper)
        .alert(document.problem ?? "", isPresented: Binding(get: { document.problem != nil }, set: { if !$0 { document.problem = nil } })) {
            Button("OK", role: .cancel) { document.problem = nil }
        }
    }
}

/// Says when an agent, or anything else, changed or deleted the file while
/// it had unsaved edits, and what can be done about it.
private struct DiskBanner: View {
    let document: EditorDocument
    let close: () -> Void

    var body: some View {
        if document.changedOnDisk || document.deletedOnDisk {
            HStack(spacing: 10) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(Palette.needsYou)
                Text(document.deletedOnDisk
                     ? "This file was deleted on disk. Save to put it back."
                     : "This file changed on disk while you were editing it.")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.text)
                Spacer()
                if document.deletedOnDisk {
                    Button("Close", action: close)
                } else {
                    Button("Keep Mine") { document.keepMine() }
                        .help("Keep your edits; saving writes over the version on disk")
                    Button("Use the Disk Version") { document.reloadFromDisk() }
                        .buttonStyle(.borderedProminent)
                }
            }
            .controlSize(.small)
            .padding(.horizontal, 14)
            .padding(.vertical, 7)
            .background(Palette.needsYouWash)
            .overlay(alignment: .bottom) { Rectangle().fill(Palette.needsYouEdge).frame(height: 1) }
        }
    }
}

// MARK: - The text

/// The open files' text: Monaco, with a model for each tab, or with
/// `-legacyEditor YES` the text views from before it.
private struct EditorPanes: View {
    let store: EditorStore
    @AppStorage("editorFontSize") private var fontSize = 14.0
    @AppStorage("editorVim") private var vim = false

    var body: some View {
        if MonacoView.legacy {
            LegacyPanes(store: store, fontSize: fontSize)
        } else {
            MonacoView(
                store: store,
                tabs: store.documents.filter { $0.kind == .text }.map(MonacoView.Tab.init),
                panes: store.panes,
                focusedPane: store.focusedPane,
                fontSize: fontSize,
                vim: vim
            )
        }
    }
}

/// Every open file's text view at once, only the one in front shown, so
/// each keeps its scroll, selection and undo while another is in front.
private struct LegacyPanes: View {
    let store: EditorStore
    let fontSize: Double

    var body: some View {
        ZStack {
            ForEach(store.documents.filter { $0.kind == .text }) { document in
                CodeTextView(
                    text: document.text,
                    path: document.path,
                    editable: true,
                    revision: document.revision,
                    fontSize: fontSize,
                    visible: document.id == store.activeID,
                    indentUnit: document.indent,
                    undoManager: document.undo,
                    onChange: { document.text = $0 },
                    onCursor: { line, column in
                        document.line = line
                        document.column = column
                    }
                )
            }
        }
    }
}

/// A Markdown file as it reads: headings, lists, tables, code and pictures,
/// rendered as the conversation renders an agent's reply, and kept up with
/// the text as it is edited.
private struct MarkdownPreview: View {
    let store: EditorStore
    let document: EditorDocument

    var body: some View {
        ScrollView {
            MarkdownView(document.text)
                .environment(\.markdownFolder, document.url.deletingLastPathComponent())
                // Links to the folder's files open them here.
                .environment(\.openURL, OpenURLAction { store.followLink($0, from: document) })
                .frame(maxWidth: 760, alignment: .leading)
                .padding(.horizontal, 40)
                .padding(.vertical, 32)
                .frame(maxWidth: .infinity)
        }
        .background(Palette.paper)
        // Back on this file after another tab, the text under the preview
        // took the keyboard again.
        .onAppear { DispatchQueue.main.async { store.window?.makeFirstResponder(nil) } }
    }
}

/// A file the editor shows but doesn't edit: an image, or one it can't.
private struct NonTextView: View {
    let document: EditorDocument

    var body: some View {
        Group {
            if document.kind == .image, let image = document.image {
                ScrollView([.vertical, .horizontal]) {
                    Image(nsImage: image)
                        .resizable()
                        .scaledToFit()
                        .frame(maxWidth: max(image.size.width, 120), maxHeight: max(image.size.height, 120))
                        .padding(24)
                }
            } else {
                VStack(spacing: 10) {
                    Image(systemName: document.kind == .missing ? "questionmark.folder" : "doc.badge.ellipsis")
                        .font(.system(size: 28))
                        .foregroundStyle(Palette.faint)
                    Text(message)
                        .font(TextStyle.body)
                        .foregroundStyle(Palette.textSecondary)
                    if document.kind != .missing {
                        Button("Open with Its App") { NSWorkspace.shared.open(document.url) }
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Palette.paper)
    }

    private var message: String {
        switch document.kind {
        case .binary: "This file isn't text."
        case .tooLarge: "This file is too large to edit here."
        case .missing: "This file isn't there any more."
        default: ""
        }
    }
}

/// No file open: how to find one, and what agents changed lately.
private struct EditorEmpty: View {
    let store: EditorStore

    var body: some View {
        VStack(spacing: 18) {
            VStack(spacing: 6) {
                Image(systemName: "chevron.left.forwardslash.chevron.right")
                    .font(.system(size: 26, weight: .light))
                    .foregroundStyle(Palette.faint)
                Text(store.name)
                    .font(TextStyle.title)
                    .foregroundStyle(Palette.text)
                Text((store.root.path as NSString).abbreviatingWithTildeInPath)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.faint)
            }
            VStack(alignment: .leading, spacing: 8) {
                shortcut("Go to File", "⌘P") { store.quickOpen = true }
                shortcut("Save", "⌘S", action: nil)
                shortcut("Close Tab", "⌘W", action: nil)
                shortcut("Comment Lines", "⌘/", action: nil)
            }
            let changed = store.marks.filter { $0.value != .deleted }.keys.sorted().prefix(8)
            if !changed.isEmpty {
                VStack(alignment: .leading, spacing: 6) {
                    Text("Changed")
                        .font(TextStyle.label)
                        .foregroundStyle(Palette.faint)
                    ForEach(Array(changed), id: \.self) { path in
                        Button {
                            store.open(path)
                        } label: {
                            HStack(spacing: 6) {
                                Text(store.marks[path]?.letter ?? "")
                                    .font(Fonts.mono(10.5, .medium))
                                    .foregroundStyle(store.marks[path]?.tint ?? Palette.muted)
                                    .frame(width: 12)
                                Text(path)
                                    .font(TextStyle.caption)
                                    .foregroundStyle(Palette.textSecondary)
                                    .lineLimit(1)
                                    .truncationMode(.head)
                            }
                        }
                        .buttonStyle(.plain)
                    }
                }
                .frame(width: 300, alignment: .leading)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Palette.paper)
    }

    private func shortcut(_ title: String, _ keys: String, action: (() -> Void)?) -> some View {
        HStack {
            if let action {
                Button(title, action: action).buttonStyle(.link)
            } else {
                Text(title).foregroundStyle(Palette.muted)
            }
            Spacer()
            Text(keys).foregroundStyle(Palette.faint)
        }
        .font(TextStyle.caption)
        .frame(width: 220)
    }
}

/// Branch and changes on the left; the cursor and the file's settings on
/// the right.
private struct StatusBar: View {
    let store: EditorStore

    var body: some View {
        HStack(spacing: 14) {
            if let mode = store.vimMode {
                VimModeLabel(mode: mode, keys: store.vimKeys)
            }
            if let notice = store.notice {
                Text(notice)
                    .foregroundStyle(Palette.textSecondary)
                    .transition(.opacity)
            }
            if let branch = store.branch {
                Label(branch, systemImage: "arrow.triangle.branch")
                    .labelStyle(.titleAndIcon)
            }
            if !store.marks.isEmpty {
                Text("\(store.marks.count) changed")
            }
            Spacer()
            if let document = store.active {
                if document.kind == .text {
                    ProblemCount(store: store, problems: document.problems)
                    Text(verbatim: "Ln \(document.line), Col \(document.column)")
                    Text(document.indent == "\t" ? "Tabs" : "Spaces: \(document.indent.count)")
                    Text("UTF-8")
                    Text(document.crlf ? "CRLF" : "LF")
                }
                LanguageLabel(store: store, document: document)
            }
        }
        .font(Fonts.sans(11))
        .foregroundStyle(Palette.muted)
        .padding(.horizontal, 12)
        .frame(height: 24)
        .background(Palette.paper)
        .overlay(alignment: .top) { Rectangle().fill(Palette.line).frame(height: 1) }
    }
}

/// Vim's mode, as Vim shows it, tinted by kind: normal quiet, insert in the
/// brand colour, visual in orange; then the keys of a command being typed.
private struct VimModeLabel: View {
    let mode: String
    let keys: String

    var body: some View {
        HStack(spacing: 8) {
            Text(mode.uppercased())
                .font(Fonts.mono(10, .semibold))
                .tracking(0.6)
                .foregroundStyle(tint)
                .padding(.horizontal, 6)
                .frame(height: 16)
                .background(tint.opacity(0.14), in: RoundedRectangle(cornerRadius: 4, style: .continuous))
            if !keys.isEmpty {
                Text(keys)
                    .font(Fonts.mono(11))
                    .foregroundStyle(Palette.textSecondary)
            }
        }
        .help("Vim mode. Turn Vim off in the Editor menu.")
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Vim \(mode) mode")
    }

    private var tint: Color {
        switch mode {
        case "insert": Palette.workingText
        case "replace": Palette.errorText
        case _ where mode.hasPrefix("visual"): Palette.needsYouText
        default: Palette.textSecondary
        }
    }
}

/// The language server's errors and warnings in the file; a click goes to
/// the next one.
private struct ProblemCount: View {
    let store: EditorStore
    let problems: Problems

    var body: some View {
        if problems.errors + problems.warnings > 0 {
            Button {
                store.monaco?.run("editor.action.marker.next")
            } label: {
                HStack(spacing: 8) {
                    if problems.errors > 0 {
                        Label("\(problems.errors)", systemImage: "xmark.circle")
                            .foregroundStyle(Palette.errorText)
                    }
                    if problems.warnings > 0 {
                        Label("\(problems.warnings)", systemImage: "exclamationmark.triangle")
                            .foregroundStyle(Palette.needsYouText)
                    }
                }
                .labelStyle(.titleAndIcon)
            }
            .buttonStyle(.plain)
            .help("Go to the next problem (F8)")
        }
    }
}

/// The file's language, and how its language server is doing: starting,
/// working, or why it isn't there, with a way to start it again.
private struct LanguageLabel: View {
    let store: EditorStore
    let document: EditorDocument

    var body: some View {
        HStack(spacing: 6) {
            if let status = document.language {
                switch status.state {
                case "installing", "starting", "restarting":
                    ProgressView().controlSize(.mini)
                    Text(status.detail.isEmpty ? "Starting \(status.name)…" : status.detail)
                        .lineLimit(1)
                case "failed":
                    Button {
                        store.monaco?.restartLanguage(for: document)
                    } label: {
                        Label(status.detail, systemImage: "exclamationmark.triangle")
                            .labelStyle(.titleAndIcon)
                            .lineLimit(1)
                            .truncationMode(.tail)
                            .frame(maxWidth: 360)
                            .foregroundStyle(Palette.needsYouText)
                    }
                    .buttonStyle(.plain)
                    .help("\(status.detail)\nClick to try again.")
                default:
                    if !status.detail.isEmpty {
                        // A server's warning can run longer than the bar.
                        Text(status.detail).lineLimit(1).frame(maxWidth: 280).help(status.detail)
                    }
                }
            }
            Text(document.languageName)
                .help(document.language?.state == "ready" ? "\(document.language?.name ?? "") is running" : "")
            if document.language?.state == "ready" {
                Circle().fill(Palette.ok).frame(width: 5, height: 5)
            }
        }
    }
}

/// ⌘P: any file in the folder by a few of its letters.
private struct GoToFile: View {
    let store: EditorStore

    var body: some View {
        PaletteView(
            placeholder: "Go to file",
            results: results,
            choose: { item in
                store.quickOpen = false
                store.open(item.id)
            },
            dismiss: { store.quickOpen = false }
        )
        .task { await store.loadAllFiles() }
    }

    private func results(_ query: String) -> [PaletteItem] {
        let paths: [String]
        if query.trimmingCharacters(in: .whitespaces).isEmpty {
            // Open files first, then what changed.
            let open = store.documents.map(\.path)
            paths = open + store.marks.keys.sorted().filter { !open.contains($0) }
        } else {
            paths = store.allFiles
                .compactMap { path in Fuzzy.score(query, path).map { (path, $0) } }
                .sorted { $0.1 > $1.1 }
                .prefix(60)
                .map(\.0)
        }
        return paths.prefix(60).map { path in
            let folder = (path as NSString).deletingLastPathComponent
            return PaletteItem(id: path, title: (path as NSString).lastPathComponent,
                               detail: folder.isEmpty ? nil : folder,
                               icon: FileIcon.symbol(path), tint: FileIcon.tint(path),
                               fileIcon: FileIcon.asset(path))
        }
    }
}

/// Finds the AppKit view a SwiftUI view beside it is drawn with, such as a
/// List's outline view.
struct SiblingReader<Found: NSView>: NSViewRepresentable {
    let found: (Found) -> Void

    func makeNSView(context: Context) -> NSView { NSView() }

    func updateNSView(_ view: NSView, context: Context) {
        DispatchQueue.main.async {
            var container = view.superview
            while let current = container {
                if let match = current.firstDescendant(Found.self) { return found(match) }
                container = current.superview
            }
        }
    }
}

extension NSView {
    /// The first view of a kind inside this one, depth first.
    func firstDescendant<T: NSView>(_ type: T.Type) -> T? {
        for view in subviews {
            if let match = view as? T ?? view.firstDescendant(type) { return match }
        }
        return nil
    }
}

/// Hands the hosting window to a view that needs it.
struct WindowReader: NSViewRepresentable {
    let found: (NSWindow) -> Void

    func makeNSView(context: Context) -> Probe { Probe() }

    func updateNSView(_ view: Probe, context: Context) {
        view.found = found
        DispatchQueue.main.async { [weak view] in view?.report() }
    }

    final class Probe: NSView {
        var found: ((NSWindow) -> Void)?

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            NotificationCenter.default.removeObserver(self)
            if let window {
                for name in [NSWindow.didEnterFullScreenNotification, NSWindow.didExitFullScreenNotification,
                             NSWindow.didResizeNotification] {
                    NotificationCenter.default.addObserver(self, selector: #selector(changed), name: name, object: window)
                }
            }
            // After this layout pass: what it reports changes SwiftUI state.
            DispatchQueue.main.async { [weak self] in self?.report() }
        }

        @objc private func changed(_ notification: Notification) { report() }

        func report() {
            if let window { found?(window) }
        }

        deinit { NotificationCenter.default.removeObserver(self) }
    }
}

/// The editor's menu commands.
struct EditorCommands: Commands {
    @FocusedValue(\.editorStore) private var editor
    @Environment(\.openWindow) private var openWindow
    @AppStorage("editorVim") private var vim = false

    var body: some Commands {
        CommandGroup(replacing: .saveItem) {
            Button("Save") { editor?.saveActive() }
                .keyboardShortcut("s", modifiers: .command)
                .disabled(editor == nil)
            Button("Save All") { editor?.saveAll() }
                .keyboardShortcut("s", modifiers: [.command, .option])
                .disabled(editor == nil)
            Button("Revert to Saved") { editor?.active?.revert() }
                .disabled(editor == nil)
        }
        CommandGroup(replacing: .printItem) {}
        CommandGroup(after: .newItem) {
            Button("Open Folder in Editor…") {
                if let folder = EditorOpener.chooseFolder() {
                    EditorOpener.open(folder: folder, with: openWindow)
                }
            }
            .keyboardShortcut("o", modifiers: .command)
            Button("Go to File…") {
                editor?.searchOpen = false
                editor?.quickOpen = true
            }
            .keyboardShortcut("p", modifiers: .command)
            .disabled(editor == nil)
            Button("Search in Folder…") { editor?.showSearch() }
                .keyboardShortcut("f", modifiers: [.command, .shift])
                .disabled(editor == nil)
            Button("Toggle Agent Panel") { editor?.showAgentPanel.toggle() }
                .keyboardShortcut("l", modifiers: .command)
                .disabled(editor == nil)
        }
        CommandMenu("Terminal") {
            // Titles here don't follow the editor's state, so they never
            // depend on it: one toggle, and commands that do nothing when
            // there is nothing to act on.
            Button("Toggle Terminal") { editor?.showTerminal.toggle() }
                .keyboardShortcut("`", modifiers: .control)
                .disabled(editor == nil)
            Button("New Terminal") { editor?.newTerminal() }
                .keyboardShortcut("`", modifiers: [.control, .shift])
                .disabled(editor == nil)
            // Cmd-K clears from inside the terminal, as in Terminal; the
            // shortcut in menus is the command palette's.
            Button("Clear Terminal") { editor?.activeTerminal?.clear() }
                .disabled(editor == nil)
            Divider()
            Button("Stop and Close Terminal") {
                if let session = editor?.activeTerminal { editor?.closeTerminal(session) }
            }
            .disabled(editor == nil)
        }
        CommandMenu("Editor") {
            Button("Toggle Markdown Preview") { editor?.togglePreview() }
                .keyboardShortcut("v", modifiers: [.command, .shift])
                .disabled(editor?.active?.isMarkdown != true)
            Divider()
            Button("Split Editor Right") { editor?.split() }
                .keyboardShortcut("\\", modifiers: .command)
                .disabled(editor == nil || (editor?.panes.count ?? 0) >= EditorStore.maxPanes)
            Button("Close Split") { if let editor { editor.closePane(editor.focusedPane) } }
                .disabled((editor?.panes.count ?? 0) < 2)
            Divider()
            // The language server's; Monaco has the same keys, so these work
            // from the menu and from the keyboard alike.
            Button("Go to Definition") { perform("editor.action.revealDefinition") { _ in } }
                .keyboardShortcut(KeyEquivalent(Character(UnicodeScalar(NSF12FunctionKey)!)), modifiers: [])
                .disabled(editor == nil)
            Button("Find References") { perform("editor.action.goToReferences") { _ in } }
                .keyboardShortcut(KeyEquivalent(Character(UnicodeScalar(NSF12FunctionKey)!)), modifiers: .shift)
                .disabled(editor == nil)
            Button("Rename Symbol…") { perform("editor.action.rename") { _ in } }
                .keyboardShortcut(KeyEquivalent(Character(UnicodeScalar(NSF2FunctionKey)!)), modifiers: [])
                .disabled(editor == nil)
            Button("Format Document") { perform("editor.action.formatDocument") { _ in } }
                .keyboardShortcut("f", modifiers: [.option, .shift])
                .disabled(editor == nil)
            Button("Go to Symbol…") { perform("editor.action.quickOutline") { _ in } }
                .keyboardShortcut("o", modifiers: [.command, .shift])
                .disabled(editor == nil)
            Button("Next Problem") { perform("editor.action.marker.next") { _ in } }
                .keyboardShortcut(KeyEquivalent(Character(UnicodeScalar(NSF8FunctionKey)!)), modifiers: [])
                .disabled(editor == nil)
            Divider()
            Button("Comment Lines") { perform("editor.action.commentLine") { $0.toggleComment() } }
                .disabled(editor == nil)
            Button("Indent Lines") { perform("editor.action.indentLines") { $0.shiftLines(by: 1) } }
                .disabled(editor == nil)
            Button("Outdent Lines") { perform("editor.action.outdentLines") { $0.shiftLines(by: -1) } }
                .disabled(editor == nil)
            Divider()
            Toggle("Vim Mode", isOn: $vim)
            Divider()
            Button("Bigger Text") { adjustFont(1) }
                .keyboardShortcut("+", modifiers: .command)
                .disabled(editor == nil)
            Button("Smaller Text") { adjustFont(-1) }
                .keyboardShortcut("-", modifiers: .command)
                .disabled(editor == nil)
            Button("Actual Size") { UserDefaults.standard.set(14.0, forKey: "editorFontSize") }
                .keyboardShortcut("0", modifiers: .command)
                .disabled(editor == nil)
        }
    }

    /// Runs a Monaco action in the editor in front, or with the text view
    /// from before Monaco, what that does instead.
    @MainActor private func perform(_ action: String, legacy: (EditorTextView) -> Void) {
        if let monaco = editor?.monaco, !MonacoView.legacy {
            monaco.run(action)
        } else if let textView = editor?.window?.firstResponder as? EditorTextView {
            legacy(textView)
        }
    }

    @MainActor private func adjustFont(_ step: Double) {
        let now = UserDefaults.standard.object(forKey: "editorFontSize") as? Double ?? 14
        UserDefaults.standard.set(min(max(now + step, 9), 28), forKey: "editorFontSize")
    }
}

/// A single title row: the content fills the frame behind native controls.
@MainActor
enum WindowChrome {
    static func leading(_ window: NSWindow) -> CGFloat {
        window.styleMask.contains(.fullScreen) ? 16 : 82
    }

    static func configure(_ window: NSWindow) {
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        if !window.styleMask.contains(.fullSizeContentView) {
            window.styleMask.insert(.fullSizeContentView)
        }
    }
}
