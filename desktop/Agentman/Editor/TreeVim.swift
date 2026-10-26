import AppKit
import SwiftUI

/// What y, c or x took in the tree, for p to put down.
struct TreeClipboard: Equatable {
    var paths: [String]
    /// Moved by p rather than copied.
    var cut: Bool
}

/// The file tree's Vim keys, in the manner of nvim-tree and NERDTree: j and
/// k move, l opens, h closes, a makes a file, r renames, d trashes, y, x and
/// p copy and move, Space marks, / filters, ? shows them all. On while the
/// Vim setting is, and only while the tree has the keyboard.
extension EditorStore {
    /// The rows the tree shows, top to bottom: the folder's own, with open
    /// folders' inside them, or what the filter found.
    func visibleTreeRows() -> [FileEntry] {
        if let filter = treeFilter, !filter.trimmingCharacters(in: .whitespaces).isEmpty {
            return treeMatches(filter).map { FileEntry(path: $0, name: ($0 as NSString).lastPathComponent, isDirectory: false) }
        }
        var rows: [FileEntry] = []
        func walk(_ folder: String) {
            for entry in entries(in: folder) {
                rows.append(entry)
                if entry.isDirectory, isExpanded(entry.path) { walk(entry.path) }
            }
        }
        walk("")
        return rows
    }

    /// The folder's files that match a filter, best first.
    func treeMatches(_ filter: String) -> [String] {
        allFiles
            .compactMap { path in Fuzzy.score(filter, path).map { (path, $0) } }
            .sorted { $0.1 > $1.1 }
            .prefix(200)
            .map(\.0)
    }

    /// The entry for a row's path, whether or not its folder is listed.
    func treeEntry(_ path: String) -> FileEntry? {
        let folder = (path as NSString).deletingLastPathComponent
        if let entry = entries(in: folder).first(where: { $0.path == path }) { return entry }
        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: root.appendingPathComponent(path).path, isDirectory: &isDirectory) else { return nil }
        return FileEntry(path: path, name: (path as NSString).lastPathComponent, isDirectory: isDirectory.boolValue)
    }

    // MARK: - Keys

    /// Acts on a key pressed in the tree; false leaves it to the tree.
    func handleTreeKey(_ event: NSEvent) -> Bool {
        let flags = event.modifierFlags.intersection(.deviceIndependentFlagsMask).subtracting([.capsLock, .numericPad, .function])
        let key = event.charactersIgnoringModifiers ?? ""
        let pending = treeKeys
        treeKeys = ""
        if flags == .control {
            if key == "w" {
                treeKeys = "<C-w>"
                return true
            }
            return false
        }
        guard flags.isSubset(of: .shift) else { return false }
        if pending == "<C-w>" {
            if key == "l" { focusEditor() }
            return true
        }
        if pending == "g" {
            switch key {
            case "g": moveTree(to: 0)
            case "y": copyTreePaths(relative: false)
            default: break
            }
            return true
        }
        switch event.keyCode {
        case 36, 76: openTreeRow(focus: true); return true
        case 53: return clearTreeState()
        default: break
        }
        switch key {
        case "j": moveTree(by: 1)
        case "k": moveTree(by: -1)
        case "G": moveTree(to: Int.max)
        case "g": treeKeys = "g"
        case "l", "o": openTreeRow(focus: true)
        case "h": closeTreeRow()
        case "/": treeFilter = treeFilter ?? ""
        case "a": creatingIn = targetFolder
        case "r":
            if let entry = currentTreeEntry { renamingInTree = entry.path }
        case "d": askToTrash()
        case "y", "c": takeForTree(cut: false)
        case "x": takeForTree(cut: true)
        case "p": problem = pasteInTree()
        case "Y": copyTreePaths(relative: true)
        case " ", "v": toggleMark()
        case "?": showTreeKeys.toggle()
        default: return false
        }
        return true
    }

    /// The row under the tree's cursor.
    var currentTreeEntry: FileEntry? {
        selection.flatMap(treeEntry)
    }

    /// Where a new file or a paste goes: the folder under the cursor, or
    /// the one around the file under it.
    var targetFolder: String {
        guard let entry = currentTreeEntry else { return "" }
        return entry.isDirectory ? entry.path : (entry.path as NSString).deletingLastPathComponent
    }

    /// Moves the cursor without opening what it lands on.
    func selectInTree(_ path: String?) {
        guard selection != path else { return }
        movingInTree = true
        selection = path
        DispatchQueue.main.async { [weak self] in
            guard let tree = self?.treeView else { return }
            if tree.selectedRow >= 0 { tree.scrollRowToVisible(tree.selectedRow) }
        }
    }

    func moveTree(by step: Int) {
        let rows = visibleTreeRows()
        guard !rows.isEmpty else { return }
        let index = selection.flatMap { path in rows.firstIndex { $0.path == path } }
        let next = index.map { min(max($0 + step, 0), rows.count - 1) } ?? (step > 0 ? 0 : rows.count - 1)
        selectInTree(rows[next].path)
    }

    func moveTree(to index: Int) {
        let rows = visibleTreeRows()
        guard !rows.isEmpty else { return }
        selectInTree(rows[min(index, rows.count - 1)].path)
    }

    /// l, o and Return: a folder opens (or the cursor goes into it if it
    /// is open); a file opens in a tab, which takes the keyboard.
    func openTreeRow(focus: Bool) {
        guard let entry = currentTreeEntry else { return moveTree(to: 0) }
        if entry.isDirectory {
            if isExpanded(entry.path), let first = entries(in: entry.path).first {
                selectInTree(first.path)
            } else {
                setExpanded(entry.path, true)
            }
            return
        }
        open(entry.path)
        if treeFilter != nil { treeFilter = nil }
        if focus { DispatchQueue.main.async { [weak self] in self?.focusEditor() } }
    }

    /// h: an open folder closes; otherwise the cursor goes to the folder
    /// around the row.
    func closeTreeRow() {
        guard let entry = currentTreeEntry else { return }
        if entry.isDirectory, isExpanded(entry.path) {
            setExpanded(entry.path, false)
        } else {
            let parent = (entry.path as NSString).deletingLastPathComponent
            if !parent.isEmpty { selectInTree(parent) }
        }
    }

    /// Escape: the filter, then the marks, then what was taken, go.
    func clearTreeState() -> Bool {
        if showTreeKeys {
            showTreeKeys = false
        } else if treeFilter != nil {
            treeFilter = nil
        } else if !marked.isEmpty {
            marked = []
        } else if treeClipboard != nil {
            treeClipboard = nil
        } else {
            return false
        }
        return true
    }

    // MARK: - Marks

    func toggleMark() {
        guard let path = selection else { return }
        if marked.remove(path) == nil { marked.insert(path) }
        moveTree(by: 1)
    }

    /// What an action is for: the marked rows, or the one under the cursor.
    var treeTargets: [String] {
        marked.isEmpty ? (selection.map { [$0] } ?? []) : marked.sorted()
    }

    // MARK: - Files

    /// a: makes what the name says in a folder. A name ending in / is a
    /// folder; one with / inside makes the folders on the way. Returns the
    /// problem, if any.
    func createInTree(_ input: String, in folder: String) -> String? {
        let name = input.trimmingCharacters(in: .whitespaces)
        guard !name.isEmpty else { return nil }
        let isFolder = name.hasSuffix("/")
        let parts = name.split(separator: "/").map(String.init)
        guard !parts.isEmpty, !parts.contains(where: { $0 == "." || $0 == ".." || $0.contains(":") }) else {
            return "That isn't a name."
        }
        let relative = ([folder].filter { !$0.isEmpty } + parts).joined(separator: "/")
        let url = root.appendingPathComponent(relative)
        guard !FileManager.default.fileExists(atPath: url.path) else { return "\(parts.joined(separator: "/")) is already there." }
        do {
            if isFolder {
                try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
            } else {
                try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
                guard FileManager.default.createFile(atPath: url.path, contents: Data()) else { return "Couldn't create \(name)." }
            }
        } catch {
            return error.localizedDescription
        }
        // Every folder on the way is listed and open, so the new row shows.
        var opened = folder
        relist(folder)
        for part in parts.dropLast() {
            opened = opened.isEmpty ? part : opened + "/" + part
            relist(opened)
            setExpanded(opened, true)
        }
        if !folder.isEmpty { setExpanded(folder, true) }
        if isFolder {
            setExpanded(relative, true)
            selectInTree(relative)
        } else {
            selectInTree(relative)
            open(relative)
        }
        return nil
    }

    func askToTrash() {
        trashing = treeTargets.compactMap(treeEntry)
    }

    /// Moves what was confirmed to the Trash; says what failed, if any.
    func trashConfirmed() -> String? {
        let entries = trashing
        trashing = []
        var problems: [String] = []
        for entry in entries {
            if let problem = trash(entry) { problems.append(problem) }
        }
        marked.subtract(entries.map(\.path))
        if problems.isEmpty, entries.count > 1 { flash("Moved \(entries.count) items to the Trash") }
        return problems.first
    }

    /// y, c and x: takes the marked rows, or the one under the cursor, and
    /// puts them on the Mac's clipboard too, as Finder would.
    func takeForTree(cut: Bool) {
        let paths = treeTargets
        guard !paths.isEmpty else { return }
        treeClipboard = TreeClipboard(paths: paths, cut: cut)
        marked = []
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.writeObjects(paths.map { root.appendingPathComponent($0) as NSURL })
        let what = paths.count == 1 ? (paths[0] as NSString).lastPathComponent : "\(paths.count) items"
        flash(cut ? "Cut \(what): p moves it" : "Copied \(what): p pastes it")
    }

    /// p: puts what was taken into the folder under the cursor. Copies get
    /// a new name beside a file of the same name; a move to where a file of
    /// that name is already is refused.
    func pasteInTree() -> String? {
        guard let clipboard = treeClipboard else { return nil }
        let folder = targetFolder
        let manager = FileManager.default
        var landed: String?
        for path in clipboard.paths {
            let source = root.appendingPathComponent(path)
            guard manager.fileExists(atPath: source.path) else { continue }
            let name = (path as NSString).lastPathComponent
            if clipboard.cut {
                let destination = folder.isEmpty ? name : folder + "/" + name
                if destination == path { continue }
                if destination.hasPrefix(path + "/") { return "\(name) can't go inside itself." }
                guard !manager.fileExists(atPath: root.appendingPathComponent(destination).path) else {
                    return "\(name) is already in \(folder.isEmpty ? self.name : folder)."
                }
                do {
                    try manager.moveItem(at: source, to: root.appendingPathComponent(destination))
                } catch {
                    return error.localizedDescription
                }
                for document in documents where document.path == path || document.path.hasPrefix(path + "/") {
                    document.moved(to: destination + document.path.dropFirst(path.count), root: root)
                }
                if expanded.contains(path) {
                    setExpanded(path, false)
                    setExpanded(destination, true)
                }
                relist((path as NSString).deletingLastPathComponent)
                landed = destination
            } else {
                let destination = Self.freeName(for: name, in: folder, root: root)
                do {
                    try manager.copyItem(at: source, to: root.appendingPathComponent(destination))
                } catch {
                    return error.localizedDescription
                }
                landed = destination
            }
        }
        relist(folder)
        if !folder.isEmpty { setExpanded(folder, true) }
        if let landed { selectInTree(landed) }
        let count = clipboard.paths.count
        flash(clipboard.cut ? "Moved \(count == 1 ? "1 item" : "\(count) items")" : "Pasted \(count == 1 ? "1 item" : "\(count) items")")
        // A move is done once; a copy can be pasted again.
        if clipboard.cut { treeClipboard = nil }
        return nil
    }

    /// A name for a copy that is free in the folder: "name copy.ext", then
    /// "name copy 2.ext".
    nonisolated static func freeName(for name: String, in folder: String, root: URL) -> String {
        let join = { (file: String) in folder.isEmpty ? file : folder + "/" + file }
        let manager = FileManager.default
        guard manager.fileExists(atPath: root.appendingPathComponent(join(name)).path) else { return join(name) }
        let ext = (name as NSString).pathExtension
        let base = ext.isEmpty || name.hasPrefix(".") && !name.dropFirst().contains(".") ? name : (name as NSString).deletingPathExtension
        let suffix = base == name ? "" : "." + ext
        var number = 1
        while true {
            let candidate = base + (number == 1 ? " copy" : " copy \(number)") + suffix
            if !manager.fileExists(atPath: root.appendingPathComponent(join(candidate)).path) { return join(candidate) }
            number += 1
        }
    }

    /// Y and gy: the path of the marked rows or the one under the cursor.
    func copyTreePaths(relative: Bool) {
        let paths = treeTargets
        guard !paths.isEmpty else { return }
        let text = paths.map { relative ? $0 : root.appendingPathComponent($0).path }.joined(separator: "\n")
        Clipboard.copy(text)
        flash(paths.count == 1 ? "Copied \(relative ? "relative " : "")path" : "Copied \(paths.count) paths")
    }
}

// MARK: - Views

/// / in the tree: a field above it that narrows it to the files that match.
struct TreeFilterBar: View {
    @Bindable var store: EditorStore
    @FocusState private var focused: Bool

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "line.3.horizontal.decrease")
                .font(.system(size: 10.5, weight: .semibold))
                .foregroundStyle(Palette.muted)
            TextField("Filter files", text: Binding(get: { store.treeFilter ?? "" }, set: { store.treeFilter = $0 }))
                .textFieldStyle(.plain)
                .font(Fonts.sans(12.5))
                .focused($focused)
                .onSubmit {
                    // Return goes down into what was found.
                    if let first = store.visibleTreeRows().first { store.selectInTree(first.path) }
                    store.focusTree()
                }
                .onKeyPress(.escape) {
                    store.treeFilter = nil
                    store.focusTree()
                    return .handled
                }
                .onKeyPress(.downArrow) {
                    if let first = store.visibleTreeRows().first { store.selectInTree(first.path) }
                    store.focusTree()
                    return .handled
                }
            Button {
                store.treeFilter = nil
                store.focusTree()
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .foregroundStyle(Palette.faint)
            }
            .buttonStyle(.plain)
            .help("Stop filtering (Escape)")
        }
        .padding(.horizontal, 9)
        .frame(height: 28)
        .background(Palette.fill, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
        .padding(.horizontal, 10)
        .padding(.vertical, 6)
        .onAppear { focused = true }
    }
}

/// a in the tree: the name of what to make, at the foot of the tree.
struct TreeCreateBar: View {
    @Bindable var store: EditorStore
    let folder: String
    @State private var name = ""
    @FocusState private var focused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            Text("New in \(folder.isEmpty ? store.name : folder)")
                .font(TextStyle.label)
                .foregroundStyle(Palette.faint)
                .lineLimit(1)
                .truncationMode(.head)
            TextField("name.ts, or folder/", text: $name)
                .textFieldStyle(.plain)
                .font(Fonts.mono(12))
                .focused($focused)
                .padding(.horizontal, 8)
                .frame(height: 26)
                .background(Palette.surface, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 7, style: .continuous).strokeBorder(Palette.working.opacity(0.6)))
                .onSubmit {
                    let problem = store.createInTree(name, in: folder)
                    store.creatingIn = nil
                    if let problem { store.problem = problem } else if name.hasSuffix("/") { store.focusTree() }
                }
                .onKeyPress(.escape) {
                    store.creatingIn = nil
                    store.focusTree()
                    return .handled
                }
            Text("End with / for a folder. Return makes it, Escape cancels.")
                .font(Fonts.sans(10.5))
                .foregroundStyle(Palette.faint)
        }
        .padding(10)
        .background(Palette.paper)
        .overlay(alignment: .top) { Rectangle().fill(Palette.line).frame(height: 1) }
        .onAppear { focused = true }
    }
}

/// r in the tree: the row's name, editable where it is. Return renames,
/// Escape or clicking away leaves it.
struct TreeRenameField: View {
    @Bindable var store: EditorStore
    let entry: FileEntry
    @State private var name = ""
    @FocusState private var focused: Bool

    var body: some View {
        TextField("", text: $name)
            .textFieldStyle(.plain)
            .font(Fonts.sans(12.5))
            .focused($focused)
            .padding(.horizontal, 4)
            .background(Palette.surface, in: RoundedRectangle(cornerRadius: 4, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 4, style: .continuous).strokeBorder(Palette.working.opacity(0.7)))
            .onSubmit {
                store.renamingInTree = nil
                store.problem = store.rename(entry, to: name)
                store.focusTree()
            }
            .onKeyPress(.escape) {
                store.renamingInTree = nil
                store.focusTree()
                return .handled
            }
            .onChange(of: focused) { _, now in
                if !now, store.renamingInTree == entry.path { store.renamingInTree = nil }
            }
            .onAppear {
                name = entry.name
                // The name without its extension selected, as Finder does.
                DispatchQueue.main.async {
                    focused = true
                    if let editor = NSApp.keyWindow?.firstResponder as? NSTextView {
                        let stem = entry.isDirectory ? entry.name : (entry.name as NSString).deletingPathExtension
                        editor.setSelectedRange(NSRange(location: 0, length: (stem.isEmpty ? entry.name : stem).utf16.count))
                    }
                }
            }
    }
}

/// ?: the tree's keys, over it.
struct TreeKeysSheet: View {
    let close: () -> Void

    private static let groups: [(String, [(String, String)])] = [
        ("Move", [
            ("j  k", "Down, up"),
            ("gg  G", "Top, bottom"),
            ("l  o  ⏎", "Open the file, or the folder"),
            ("h", "Close the folder, or go to it"),
            ("/", "Filter the files"),
            ("⌃W l", "To the editor (⌃W h back)"),
        ]),
        ("Files", [
            ("a", "New file; end with / for a folder"),
            ("r", "Rename"),
            ("d", "Move to the Trash"),
            ("y  c", "Copy"),
            ("x", "Cut, to move"),
            ("p", "Paste into the folder"),
            ("Y  gy", "Copy the relative, full path"),
            ("␣  v", "Mark, for several at once"),
            ("Esc", "Clear the filter, marks, clipboard"),
        ]),
    ]

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Tree keys")
                    .font(Fonts.sans(13, .semibold))
                    .foregroundStyle(Palette.text)
                Spacer()
                Button(action: close) {
                    Image(systemName: "xmark")
                        .font(.system(size: 10, weight: .bold))
                        .foregroundStyle(Palette.muted)
                }
                .buttonStyle(.plain)
                .help("Close (? or Escape)")
            }
            ForEach(Self.groups, id: \.0) { group in
                VStack(alignment: .leading, spacing: 5) {
                    Text(group.0.uppercased())
                        .font(TextStyle.label)
                        .foregroundStyle(Palette.faint)
                    ForEach(group.1, id: \.0) { key, what in
                        HStack(alignment: .firstTextBaseline, spacing: 10) {
                            Text(key)
                                .font(Fonts.mono(11.5, .medium))
                                .foregroundStyle(Palette.workingText)
                                .frame(width: 62, alignment: .leading)
                            Text(what)
                                .font(Fonts.sans(12))
                                .foregroundStyle(Palette.textSecondary)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
            }
        }
        .padding(12)
        .frame(maxWidth: 300)
        .background(Palette.surface, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(Palette.line))
        .shadow(color: .black.opacity(0.18), radius: 20, y: 8)
    }
}
