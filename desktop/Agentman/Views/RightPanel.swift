import AppKit
import SwiftUI

/// What the right panel shows, a tab each.
enum RightPanelTab: String, CaseIterable, Identifiable {
    case diff, files
    var id: String { rawValue }
    var title: String { self == .diff ? "Diff" : "Files" }
    var symbol: String { self == .diff ? "doc.text" : "folder" }
}

/// The panel at the window's right edge, beside the workspace card, as T3
/// Code has one: what the agent has changed as one diff, and the project's
/// files, each in a tab. It resizes from its leading edge and can take the
/// whole window.
struct RightPanel: View {
    static let openKey = "rightPanelOpen"
    static let tabKey = "rightPanelTab"
    static let tabsKey = "rightPanelTabs"
    static let widthKey = "rightPanelWidth"
    static let defaultWidth: Double = 540
    static let minWidth: Double = 360

    let session: Session
    @Binding var maximised: Bool
    let close: () -> Void
    @AppStorage(tabsKey) private var tabsRaw = RightPanelTab.diff.rawValue
    @AppStorage(tabKey) private var tab: RightPanelTab = .diff
    @Environment(\.detailHeaderLeading) private var headerLeading

    /// The tabs open, in order.
    static func tabs(_ raw: String) -> [RightPanelTab] {
        raw.split(separator: ",").compactMap { RightPanelTab(rawValue: String($0)) }
    }

    /// Opens the panel on a tab, adding the tab if it was closed. Through the
    /// defaults, so every view keeping them follows.
    static func show(_ tab: RightPanelTab) {
        let defaults = UserDefaults.standard
        var tabs = tabs(defaults.string(forKey: tabsKey) ?? RightPanelTab.diff.rawValue)
        if !tabs.contains(tab) { tabs.append(tab) }
        defaults.set(tabs.map(\.rawValue).joined(separator: ","), forKey: tabsKey)
        defaults.set(tab.rawValue, forKey: tabKey)
        defaults.set(true, forKey: openKey)
    }

    /// The panel's width in a window this wide: what it was dragged to, but
    /// never so wide the conversation has under `room` left.
    static func clamp(_ width: Double, available: CGFloat, room: CGFloat) -> CGFloat {
        CGFloat(min(max(width, minWidth), max(minWidth, Double(available - room))))
    }

    var body: some View {
        let tabs = Self.tabs(tabsRaw)
        let shown = tabs.contains(tab) ? tab : tabs.first ?? .diff
        VStack(spacing: 0) {
            HStack(spacing: 4) {
                ForEach(tabs) { item in
                    PanelTab(tab: item, selected: item == shown, select: { tab = item }, close: { closeTab(item) })
                }
                Menu {
                    ForEach(RightPanelTab.allCases) { item in
                        Button { Self.show(item) } label: { Label(item.title, systemImage: item.symbol) }
                    }
                } label: {
                    Image(systemName: "plus").font(.system(size: 11, weight: .medium))
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .foregroundStyle(Palette.muted)
                .padding(.leading, 4)
                .help("Open a panel")
                Spacer(minLength: 8)
                ChromeButton(symbol: maximised ? "arrow.down.right.and.arrow.up.left" : "arrow.up.left.and.arrow.down.right",
                             help: maximised ? "Restore the panel" : "Fill the window", active: maximised) {
                    maximised.toggle()
                }
                ChromeButton(symbol: "sidebar.right", help: "Close the panel (⇧⌘D)", active: true, action: close)
            }
            .padding(.leading, maximised ? headerLeading + 12 : 10)
            .padding(.trailing, 10)
            .frame(height: 32)
            // A shape, not a colour: a colour by the title bar spreads up
            // into it, over the tabs.
            Rectangle().fill(Palette.line).frame(height: 1)
            switch shown {
            case .diff:
                DiffPanel(sessionID: session.id, cwd: session.cwd, state: session.state, activity: session.lastActivityAt)
                    .equatable()
                    .id(session.id)
            case .files:
                FilesPanel(folder: session.cwd).equatable().id(session.cwd)
            }
        }
        .background(Palette.paper)
    }

    private func closeTab(_ item: RightPanelTab) {
        let rest = Self.tabs(tabsRaw).filter { $0 != item }
        if rest.isEmpty {
            close()
            return
        }
        tabsRaw = rest.map(\.rawValue).joined(separator: ",")
        if tab == item { tab = rest[0] }
    }
}

private struct PanelTab: View {
    let tab: RightPanelTab
    let selected: Bool
    let select: () -> Void
    let close: () -> Void
    @State private var hovering = false

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: tab.symbol).font(.system(size: 11))
            Text(tab.title).font(TextStyle.captionMedium)
            Button(action: close) {
                Image(systemName: "xmark").font(.system(size: 8, weight: .bold))
            }
            .buttonStyle(.plain)
            .opacity(hovering ? 1 : 0)
            .help("Close \(tab.title)")
        }
        .foregroundStyle(selected ? Palette.text : Palette.muted)
        .padding(.leading, 8)
        .padding(.trailing, 6)
        .frame(height: 26)
        .background(selected ? Palette.surface : hovering ? Palette.fill.opacity(0.5) : .clear, in: RoundedRectangle(cornerRadius: 7))
        .overlay(RoundedRectangle(cornerRadius: 7).strokeBorder(selected ? Palette.line : .clear))
        .contentShape(Rectangle())
        .onTapGesture(perform: select)
        .onHover { hovering = $0 }
        .accessibilityAddTraits(.isButton)
        .accessibilityLabel(tab.title)
    }
}

/// A small icon button for a panel's toolbar, lit while what it toggles is on.
private struct PanelButton: View {
    let symbol: String
    let help: String
    var on = false
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: 11.5))
                .foregroundStyle(on ? Palette.text : Palette.muted)
                .frame(width: 26, height: 24)
                .background(on ? Palette.fill : hovering ? Palette.fill.opacity(0.5) : .clear, in: RoundedRectangle(cornerRadius: 6))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(help)
        .accessibilityLabel(help)
    }
}

/// A file's status as git reports it (`git status --porcelain` "XY"), as a
/// letter and a colour: M modified, A added, D deleted, R renamed, U new.
enum ChangeStatus {
    static func letter(_ status: String) -> String {
        if status == "??" || status == "untracked" { return "U" }
        if status.contains("D") || status == "deleted" { return "D" }
        if status.contains("A") || status == "added" { return "A" }
        if status.contains("R") || status == "renamed" { return "R" }
        return "M"
    }

    static func tint(_ status: String) -> Color {
        switch letter(status) {
        case "U", "A": Palette.ok
        case "D": Palette.errorText
        default: Palette.working
        }
    }

    static func symbol(_ status: String) -> String {
        switch letter(status) {
        case "U", "A": "plus.circle"
        case "D": "minus.circle"
        case "R": "arrow.right.circle"
        default: "circle.inset.filled"
        }
    }

    /// Paths as a tree reads them: a folder's files together, folders before
    /// the files beside them, each level by name.
    static func treeOrder(_ a: String, _ b: String) -> Bool {
        let x = a.split(separator: "/"), y = b.split(separator: "/")
        for index in 0..<min(x.count, y.count) where x[index] != y[index] {
            let xFolder = index < x.count - 1, yFolder = index < y.count - 1
            if xFolder != yFolder { return xFolder }
            return x[index].localizedStandardCompare(y[index]) == .orderedAscending
        }
        return x.count < y.count
    }
}

// MARK: - Diff

/// Everything the agent's folder has changed since its last commit. Each
/// file's diff is read when it comes into view, laid out and coloured once,
/// away from the main thread, and read again only when the file's entry in
/// the list changes or a turn begins or ends.
@MainActor @Observable
final class DiffPanelModel {
    struct FileDiff: Sendable {
        var text: String
        var items: [DiffLayout.Item]
        var split: [DiffLayout.SplitItem]
        var digits: Int
        var truncated: Bool
        /// Each line's text as shown, by its key.
        var styled: [String: AttributedString]
    }

    private(set) var changes: [WorkspaceChange] = []
    private(set) var hidden = 0
    private(set) var problem: String?
    private(set) var loaded = false
    private(set) var ignoreWhitespace = false
    private(set) var diffs: [String: FileDiff] = [:]
    private(set) var failures: [String: String] = [:]
    /// Goes up for a file each time its diff should be read again.
    private(set) var versions: [String: Int] = [:]
    var collapsed: Set<String> = []
    /// When the list was last read.
    @ObservationIgnored private(set) var readAt: Date?
    @ObservationIgnored private var readVersions: [String: Int] = [:]
    @ObservationIgnored private var opened: [String: Set<Int>] = [:]
    @ObservationIgnored private var files: [String: [String]] = [:]
    @ObservationIgnored private var loading: Set<String> = []

    var added: Int { changes.reduce(0) { $0 + ($1.added ?? 0) } }
    var removed: Int { changes.reduce(0) { $0 + ($1.removed ?? 0) } }

    func version(_ path: String) -> Int { versions[path] ?? 0 }

    /// Reads the list again. A file whose entry changed is read again, and
    /// with `everything`, every file is.
    func refresh(store: AgentsStore, session: String, ignoreWhitespace: Bool, everything: Bool = false) async {
        readAt = Date()
        do {
            let result = try await store.workspace(.listChanges, session: session)
            let listed = (result.changes ?? []).sorted { ChangeStatus.treeOrder($0.path, $1.path) }
            let whitespace = ignoreWhitespace != self.ignoreWhitespace
            if whitespace {
                self.ignoreWhitespace = ignoreWhitespace
                opened = [:]
            }
            let before = Dictionary(changes.map { ($0.path, $0) }, uniquingKeysWith: { first, _ in first })
            for change in listed where everything || whitespace || before[change.path] != change {
                versions[change.path, default: 0] += 1
            }
            if listed != changes { changes = listed }
            if hidden != result.hidden ?? 0 { hidden = result.hidden ?? 0 }
            if problem != nil { problem = nil }
            let paths = Set(listed.map(\.path))
            if diffs.keys.contains(where: { !paths.contains($0) }) { diffs = diffs.filter { paths.contains($0.key) } }
        } catch {
            problem = error.localizedDescription
        }
        if !loaded { loaded = true }
    }

    /// Reads a file's diff, unless it was read since it last changed.
    func load(_ path: String, store: AgentsStore, session: String) async {
        let version = self.version(path)
        guard readVersions[path] != version, !loading.contains(path) else { return }
        loading.insert(path)
        do {
            let result = try await store.workspace(.fileDiff, session: session, path: path, ignoreWhitespace: ignoreWhitespace)
            let text = result.diff ?? ""
            // The same diff as before changes nothing on screen.
            if text != diffs[path]?.text || diffs[path]?.truncated != (result.truncated == true) {
                opened[path] = nil
                files[path] = nil
                diffs[path] = await Self.layout(path: path, text: text, truncated: result.truncated == true, opened: [], file: nil)
            }
            if failures[path] != nil { failures[path] = nil }
        } catch {
            failures[path] = error.localizedDescription
        }
        readVersions[path] = version
        loading.remove(path)
        // Changed again while it was being read.
        if self.version(path) != version { await load(path, store: store, session: session) }
    }

    /// Shows the unchanged lines a fold stands for, read from the file.
    func open(_ fold: DiffLayout.Fold, in path: String, store: AgentsStore, session: String) async {
        if files[path] == nil {
            guard let text = try? await store.workspace(.readFile, session: session, path: path).text else { return }
            files[path] = text.components(separatedBy: "\n")
        }
        opened[path, default: []].insert(fold.id)
        if let diff = diffs[path] {
            diffs[path] = await Self.layout(path: path, text: diff.text, truncated: diff.truncated, opened: opened[path] ?? [], file: files[path])
        }
    }

    func toggle(_ path: String) {
        if collapsed.contains(path) { collapsed.remove(path) } else { collapsed.insert(path) }
    }

    /// Folds every file, or opens them all when they all are.
    func toggleAll() {
        collapsed = collapsed.count == changes.count ? [] : Set(changes.map(\.path))
    }

    /// A diff parsed, laid out and coloured, off the main thread: a long
    /// diff's colouring is the slow part.
    nonisolated static func layout(path: String, text: String, truncated: Bool, opened: Set<Int>, file: [String]?) async -> FileDiff {
        await Task.detached(priority: .userInitiated) {
            let rows = DiffRow.parse(text)
            let items = DiffLayout.items(rows, expanded: opened, file: file)
            let language = CodeHighlighter.language(for: path)
            var styled: [String: AttributedString] = [:]
            for item in items {
                if case .line(let line) = item { styled[line.key] = DiffMetrics.text(line, language: language) }
            }
            return FileDiff(text: text, items: items, split: DiffLayout.split(items), digits: DiffLayout.digits(rows),
                            truncated: truncated, styled: styled)
        }.value
    }
}

/// The Diff tab: the uncommitted changes, file by file, stacked or split,
/// with the changed files in a tree beside them. Equal while the session's
/// parts it uses are: typing in the box beside it redraws the thread around
/// it, and walking every line of every diff at each key made typing slow.
struct DiffPanel: View, Equatable {
    @Environment(AgentsStore.self) private var store
    let sessionID: String
    let cwd: String
    let state: SessionState
    let activity: Int64?
    @State private var model = DiffPanelModel()
    @AppStorage("diffSplit") private var split = false
    @AppStorage("diffWrap") private var wrap = true
    @AppStorage("diffIgnoreWhitespace") private var ignoreWhitespace = false
    @AppStorage("diffFileTree") private var showTree = true
    /// The file last picked in the tree, and the one to scroll to.
    @State private var picked: String?
    @State private var scrollTo: String?
    /// The agent's state when the list was last read.
    @State private var seenState: SessionState?

    nonisolated static func == (a: DiffPanel, b: DiffPanel) -> Bool {
        a.sessionID == b.sessionID && a.cwd == b.cwd && a.state == b.state && a.activity == b.activity
    }

    static let treeWidth: CGFloat = 196
    /// How often the list is read while the agent works.
    static let every: TimeInterval = 2.5

    private struct Refresh: Equatable {
        let state: SessionState
        let activity: Int64?
        let ignoreWhitespace: Bool
    }

    var body: some View {
        VStack(spacing: 0) {
            toolbar
            Divider().overlay(Palette.line)
            content
        }
        // While the agent works, every few seconds; when a turn begins or
        // ends, everything again, at once; at once for the whitespace button.
        .task(id: Refresh(state: state, activity: activity, ignoreWhitespace: ignoreWhitespace)) {
            let turned = model.loaded && seenState != state
            if model.loaded, model.ignoreWhitespace == ignoreWhitespace {
                let since = Date().timeIntervalSince(model.readAt ?? .distantPast)
                try? await Task.sleep(for: .seconds(turned ? 0.3 : max(0.4, Self.every - since)))
            }
            guard !Task.isCancelled else { return }
            seenState = state
            await model.refresh(store: store, session: sessionID, ignoreWhitespace: ignoreWhitespace, everything: turned)
        }
    }

    private var toolbar: some View {
        HStack(spacing: 4) {
            // Only the uncommitted changes for now; a turn's or a branch's
            // join them here.
            Menu {
                Button { } label: { Label("Uncommitted", systemImage: "checkmark") }
            } label: {
                // One text: a menu's label puts an image of its own first.
                (Text("Uncommitted ") + Text(Image(systemName: "chevron.down")).font(.system(size: 8, weight: .semibold)))
                    .font(TextStyle.captionMedium)
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .fixedSize()
            .foregroundStyle(Palette.text)
            .help("Which changes to show")
            Spacer(minLength: 8)
            if !model.changes.isEmpty {
                HStack(spacing: 5) {
                    Text("+\(model.added)").foregroundStyle(Palette.ok)
                    Text("−\(model.removed)").foregroundStyle(Palette.errorText)
                }
                .font(Fonts.mono(11.5, .medium))
                .padding(.trailing, 4)
                PanelButton(symbol: model.collapsed.count == model.changes.count ? "rectangle.expand.vertical" : "rectangle.compress.vertical",
                            help: model.collapsed.count == model.changes.count ? "Expand all files" : "Collapse all files") {
                    model.toggleAll()
                }
            }
            HStack(spacing: 0) {
                PanelButton(symbol: "rectangle.split.1x2", help: "Stacked diff view", on: !split) { split = false }
                PanelButton(symbol: "rectangle.split.2x1", help: "Split diff view", on: split) { split = true }
            }
            .padding(1)
            .overlay(RoundedRectangle(cornerRadius: 7).strokeBorder(Palette.line))
            PanelButton(symbol: "arrow.turn.down.left", help: wrap ? "Don't wrap long lines" : "Wrap long lines", on: wrap) { wrap.toggle() }
            PanelButton(symbol: "paragraphsign", help: ignoreWhitespace ? "Show whitespace changes" : "Hide whitespace changes",
                        on: ignoreWhitespace) { ignoreWhitespace.toggle() }
            PanelButton(symbol: "list.bullet.indent", help: showTree ? "Hide file tree" : "Show file tree", on: showTree) { showTree.toggle() }
        }
        .padding(.horizontal, 10)
        .frame(height: 38)
    }

    @ViewBuilder private var content: some View {
        if !model.loaded {
            ProgressView().controlSize(.small).frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let problem = model.problem {
            note(problem)
        } else if model.changes.isEmpty {
            note(model.hidden > 0 ? "Only files holding secrets have changed; they aren't shown." : "No uncommitted changes.")
        } else {
            GeometryReader { geometry in
                let tree = showTree && geometry.size.width >= 480
                HStack(spacing: 0) {
                    sections(width: geometry.size.width - (tree ? Self.treeWidth + 1 : 0))
                    if tree {
                        Rectangle().fill(Palette.line).frame(width: 1)
                        ChangedFilesTree(changes: model.changes, picked: picked, hide: { showTree = false }) { path in
                            picked = path
                            model.collapsed.remove(path)
                            scrollTo = path
                        }
                        .frame(width: Self.treeWidth)
                    }
                }
            }
        }
    }

    private func note(_ text: String) -> some View {
        Text(text)
            .font(TextStyle.caption)
            .foregroundStyle(Palette.muted)
            .multilineTextAlignment(.center)
            .padding(28)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func sections(width: CGFloat) -> some View {
        ScrollViewReader { proxy in
            ScrollView(wrap || split ? .vertical : [.vertical, .horizontal]) {
                LazyVStack(alignment: .leading, spacing: 0, pinnedViews: [.sectionHeaders]) {
                    ForEach(model.changes) { change in
                        let collapsed = model.collapsed.contains(change.path)
                        Section {
                            if !collapsed { section(change, width: width) }
                        } header: {
                            DiffFileHeader(cwd: cwd, change: change, collapsed: collapsed, width: width) {
                                model.toggle(change.path)
                            }
                            .id(change.path)
                            .task(id: "\(change.path)#\(model.version(change.path))#\(collapsed)") {
                                guard !collapsed else { return }
                                await model.load(change.path, store: store, session: sessionID)
                            }
                        }
                    }
                }
                .padding(.bottom, 28)
            }
            .scrollIndicators(.never)
            .onChange(of: scrollTo) { _, path in
                guard let path else { return }
                proxy.scrollTo(path, anchor: .top)
                scrollTo = nil
            }
        }
    }

    @ViewBuilder private func section(_ change: WorkspaceChange, width: CGFloat) -> some View {
        if let diff = model.diffs[change.path] {
            if diff.items.isEmpty {
                sectionNote(diff.truncated ? "This diff is too large to show." : "No line changes: a binary file, or only its mode changed.", width: width)
            } else if split {
                ForEach(diff.split) { item in
                    switch item {
                    case .fold(let fold):
                        FoldRow(fold: fold, width: width) { open(fold, change.path) }
                    case .pair(let pair):
                        DiffPairRow(old: DiffSide(pair.old, old: true, in: diff), new: DiffSide(pair.new, old: false, in: diff),
                                    digits: diff.digits, wrap: wrap, width: width)
                            .equatable()
                    }
                }
            } else {
                ForEach(diff.items) { item in
                    switch item {
                    case .fold(let fold):
                        FoldRow(fold: fold, width: width) { open(fold, change.path) }
                    case .line(let line):
                        DiffLineRow(text: diff.styled[line.key] ?? AttributedString(line.row.text),
                                    number: line.row.kind == .remove ? line.row.oldNumber : line.row.newNumber,
                                    kind: line.row.kind, digits: diff.digits, wrap: wrap, width: width)
                            .equatable()
                    }
                }
            }
            if diff.truncated, !diff.items.isEmpty {
                sectionNote("The rest of this diff is too large to show.", width: width)
            }
        } else if let failure = model.failures[change.path] {
            sectionNote(failure, width: width)
        } else {
            ProgressView().controlSize(.small).padding(12).frame(width: width)
        }
    }

    private func sectionNote(_ text: String, width: CGFloat) -> some View {
        Text(text)
            .font(TextStyle.caption)
            .foregroundStyle(Palette.muted)
            .padding(.horizontal, 14)
            .padding(.vertical, 10)
            .frame(width: width, alignment: .leading)
    }

    private func open(_ fold: DiffLayout.Fold, _ path: String) {
        Task { await model.open(fold, in: path, store: store, session: sessionID) }
    }
}

/// How diff lines are drawn, shared by the stacked and split rows.
private enum DiffMetrics {
    static let font = Fonts.mono(12)
    static let size: CGFloat = 12

    /// The line-number column, for numbers this many digits long.
    static func gutter(_ digits: Int) -> CGFloat {
        CGFloat(digits) * ceil(size * 0.61) + 14
    }

    static func background(_ kind: DiffRow.Kind) -> Color {
        switch kind {
        case .add: Palette.diffAdd
        case .remove: Palette.diffRemove
        default: .clear
        }
    }

    static func edge(_ kind: DiffRow.Kind) -> Color {
        switch kind {
        case .add: Palette.ok
        case .remove: Palette.error
        default: .clear
        }
    }

    /// A line's text coloured as code, the words that changed marked. Its
    /// indent is in spaces that don't break, a tab four of them: a wrapped
    /// line broke at its indent, leaving a blank line over its code.
    static func text(_ line: DiffLayout.Line, language: CodeHighlighter.Language?) -> AttributedString {
        let source = line.row.text.isEmpty ? " " : line.row.text
        let indent = source.prefix { $0 == " " || $0 == "\t" }
        let shown = String(indent.flatMap { $0 == "\t" ? Array(repeating: Character("\u{00A0}"), count: 4) : [Character("\u{00A0}")] })
        let raw = shown + source.dropFirst(indent.count)
        let shift = shown.utf16.count - indent.utf16.count
        var string = AttributedString(raw)
        string.foregroundColor = Palette.text
        let length = (raw as NSString).length
        // A minified line is coloured plain: the expression over a very long
        // line is slow, and its colours would say little.
        if let language, length <= 1500 {
            for (range, token) in CodeHighlighter.tokens(in: raw, language: language) {
                if let found = Range(range, in: string) { string[found].foregroundColor = tint(token) }
            }
        }
        for changed in line.changed {
            let start = changed.lowerBound < indent.utf16.count ? changed.lowerBound : changed.lowerBound + shift
            guard start + changed.count <= length else { continue }
            if let found = Range(NSRange(location: start, length: changed.count), in: string) {
                string[found].backgroundColor = line.row.kind == .add ? Palette.diffAddWord : Palette.diffRemoveWord
            }
        }
        return string
    }

    private static func tint(_ token: CodeHighlighter.Token) -> Color {
        switch token {
        case .comment: Palette.codeComment
        case .string: Palette.codeString
        case .number: Palette.codeNumber
        case .keyword: Palette.codeKeyword
        case .type: Palette.codeType
        }
    }
}

/// A line of a stacked diff: its number, then its text. Equatable, so a
/// redraw of the panel skips every line that is the same.
private struct DiffLineRow: View, Equatable {
    let text: AttributedString
    let number: Int?
    let kind: DiffRow.Kind
    let digits: Int
    let wrap: Bool
    let width: CGFloat

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 0) {
            Text(number.map(String.init) ?? "")
                .foregroundStyle(Palette.faint)
                .frame(width: DiffMetrics.gutter(digits), alignment: .trailing)
                .padding(.trailing, 12)
            Text(text)
                .fixedSize(horizontal: !wrap, vertical: true)
                .frame(maxWidth: wrap ? .infinity : nil, alignment: .leading)
                .padding(.trailing, 12)
        }
        .font(DiffMetrics.font)
        .padding(.vertical, 2)
        .frame(width: wrap ? width : nil, alignment: .leading)
        .frame(minWidth: width, alignment: .leading)
        .background(DiffMetrics.background(kind))
        .overlay(alignment: .leading) { DiffMetrics.edge(kind).frame(width: 3) }
        .textSelection(.enabled)
    }
}

/// One side of a split diff's row, ready to draw.
private struct DiffSide: Equatable {
    let text: AttributedString
    let number: Int?
    let kind: DiffRow.Kind

    init?(_ line: DiffLayout.Line?, old: Bool, in diff: DiffPanelModel.FileDiff) {
        guard let line else { return nil }
        text = diff.styled[line.key] ?? AttributedString(line.row.text)
        number = old ? line.row.oldNumber : line.row.newNumber
        kind = line.row.kind
    }
}

/// A row of a split diff: the line as it was on the left, as it is on the
/// right, the same line on both when it didn't change.
private struct DiffPairRow: View, Equatable {
    let old: DiffSide?
    let new: DiffSide?
    let digits: Int
    let wrap: Bool
    let width: CGFloat

    var body: some View {
        let half = max((width - 1) / 2, 0)
        HStack(spacing: 0) {
            side(old).frame(width: half)
            Rectangle().fill(Palette.line).frame(width: 1)
            side(new).frame(width: half)
        }
        .fixedSize(horizontal: false, vertical: true)
        .font(DiffMetrics.font)
    }

    @ViewBuilder private func side(_ side: DiffSide?) -> some View {
        if let side {
            HStack(alignment: .firstTextBaseline, spacing: 0) {
                Text(side.number.map(String.init) ?? "")
                    .foregroundStyle(Palette.faint)
                    .frame(width: DiffMetrics.gutter(digits), alignment: .trailing)
                    .padding(.trailing, 10)
                Text(side.text)
                    .lineLimit(wrap ? nil : 1)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.trailing, 8)
            }
            .padding(.vertical, 2)
            .frame(maxHeight: .infinity, alignment: .top)
            .background(side.kind == .context ? .clear : DiffMetrics.background(side.kind))
            .overlay(alignment: .leading) { DiffMetrics.edge(side.kind).frame(width: 3) }
            .textSelection(.enabled)
        } else {
            Palette.fill.opacity(0.35).frame(maxHeight: .infinity)
        }
    }
}

/// Unchanged lines left out of a diff, as one line saying how many; a
/// click shows them.
private struct FoldRow: View {
    let fold: DiffLayout.Fold
    let width: CGFloat
    let open: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: open) {
            HStack(spacing: 6) {
                Image(systemName: "arrow.up.and.down").font(.system(size: 9, weight: .medium))
                Text(fold.label)
            }
            .font(Fonts.sans(11.5))
            .foregroundStyle(hovering ? Palette.text : Palette.muted)
            .padding(.horizontal, 10)
            .frame(maxWidth: .infinity, minHeight: 24, alignment: .leading)
            .background(Palette.fill.opacity(hovering ? 0.75 : 0.45), in: RoundedRectangle(cornerRadius: 5))
            .padding(.horizontal, 6)
            .padding(.vertical, 3)
            .frame(width: width)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help("Show the unchanged lines")
    }
}

/// A file's bar over its diff, kept at the top while its diff scrolls: a
/// click folds the diff away or opens it.
private struct DiffFileHeader: View {
    let cwd: String
    let change: WorkspaceChange
    let collapsed: Bool
    let width: CGFloat
    let toggle: () -> Void
    @Environment(\.openWindow) private var openWindow
    @State private var hovering = false
    @State private var copied = false

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: collapsed ? "chevron.right" : "chevron.down")
                .font(.system(size: 9, weight: .semibold))
                .foregroundStyle(Palette.faint)
                .frame(width: 10)
            Image(systemName: ChangeStatus.symbol(change.status))
                .font(.system(size: 11.5))
                .foregroundStyle(ChangeStatus.tint(change.status))
            Text(change.path)
                .font(Fonts.sans(12.5))
                .foregroundStyle(Palette.text)
                .lineLimit(1)
                .truncationMode(.head)
                .help(change.path)
            if hovering || copied {
                Button {
                    Clipboard.copy(change.path)
                    copied = true
                    Task { try? await Task.sleep(for: .seconds(1.2)); copied = false }
                } label: {
                    Image(systemName: copied ? "checkmark" : "doc.on.doc")
                        .font(.system(size: 10.5))
                        .foregroundStyle(Palette.muted)
                        .frame(width: 20, height: 20)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help("Copy path")
            }
            Spacer(minLength: 8)
            HStack(spacing: 6) {
                if let removed = change.removed, removed > 0 { Text("−\(removed)").foregroundStyle(Palette.errorText) }
                if let added = change.added, added > 0 { Text("+\(added)").foregroundStyle(Palette.ok) }
            }
            .font(Fonts.mono(11.5))
        }
        .padding(.horizontal, 10)
        .frame(width: width, height: 36)
        .background(Palette.paper)
        .overlay(alignment: .top) { Rectangle().fill(Palette.line).frame(height: 1) }
        .contentShape(Rectangle())
        .onTapGesture(perform: toggle)
        .onHover { hovering = $0 }
        .contextMenu {
            Button("Copy Path") { Clipboard.copy(change.path) }
            if FileManager.default.fileExists(atPath: cwd) {
                Button("Open in Editor") { EditorOpener.open(folder: cwd, file: change.path, with: openWindow) }
                Button("Show in Finder") {
                    NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: cwd).appendingPathComponent(change.path)])
                }
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isButton)
    }
}

/// The changed files as a tree beside their diffs; a file picked scrolls
/// its diff into view.
private struct ChangedFilesTree: View {
    let changes: [WorkspaceChange]
    let picked: String?
    let hide: () -> Void
    let pick: (String) -> Void
    @State private var closed: Set<String> = []

    private struct Node: Identifiable {
        let id: String
        let name: String
        let depth: Int
        let change: WorkspaceChange?
    }

    /// Folders and files in order, without what is inside a closed folder.
    private var nodes: [Node] {
        var nodes: [Node] = []
        var seen: Set<String> = []
        for change in changes {
            let parts = change.path.split(separator: "/").map(String.init)
            var shut = false
            for depth in 0..<max(parts.count - 1, 0) {
                let folder = parts[0...depth].joined(separator: "/")
                if !shut, seen.insert(folder).inserted {
                    nodes.append(Node(id: folder, name: parts[depth], depth: depth, change: nil))
                }
                if closed.contains(folder) { shut = true }
            }
            if !shut {
                nodes.append(Node(id: change.path, name: parts.last ?? change.path, depth: parts.count - 1, change: change))
            }
        }
        return nodes
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 6) {
                Text("Files").font(TextStyle.captionMedium).foregroundStyle(Palette.text)
                Spacer()
                Text("\(changes.count)").font(TextStyle.caption).foregroundStyle(Palette.faint)
                Button(action: hide) {
                    Image(systemName: "xmark").font(.system(size: 9, weight: .semibold))
                        .frame(width: 18, height: 18)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .foregroundStyle(Palette.muted)
                .help("Hide file tree")
            }
            .padding(.horizontal, 12)
            .frame(height: 36)
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 1) {
                    ForEach(nodes) { node in row(node) }
                }
                .padding(.horizontal, 6)
                .padding(.bottom, 12)
            }
            .scrollIndicators(.never)
        }
    }

    private func row(_ node: Node) -> some View {
        Button {
            if let change = node.change {
                pick(change.path)
            } else if closed.contains(node.id) {
                closed.remove(node.id)
            } else {
                closed.insert(node.id)
            }
        } label: {
            HStack(spacing: 6) {
                if let change = node.change {
                    FileTypeIcon(name: node.name, size: 13)
                    Text(node.name)
                        .foregroundStyle(picked == change.path ? Palette.working : Palette.text)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Spacer(minLength: 4)
                    Text(ChangeStatus.letter(change.status))
                        .font(Fonts.mono(10.5, .medium))
                        .foregroundStyle(ChangeStatus.tint(change.status))
                } else {
                    Image(systemName: closed.contains(node.id) ? "chevron.right" : "chevron.down")
                        .font(.system(size: 8, weight: .semibold))
                        .foregroundStyle(Palette.faint)
                        .frame(width: 13)
                    Text(node.name).foregroundStyle(Palette.textSecondary).lineLimit(1)
                    Spacer(minLength: 4)
                    Circle().fill(Palette.working.opacity(0.8)).frame(width: 5, height: 5)
                }
            }
            .font(Fonts.sans(12))
            .padding(.leading, CGFloat(node.depth) * 12 + 6)
            .padding(.trailing, 6)
            .frame(height: 24)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(node.change != nil && picked == node.id ? Palette.fill.opacity(0.8) : .clear, in: RoundedRectangle(cornerRadius: 5))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(node.id)
    }
}

// MARK: - Files

/// The Files tab: the whole project as a tree, as the editor has it, with
/// what git says changed and what it ignores dimmed; a search across every
/// file; a file opens in the editor.
struct FilesPanel: View, Equatable {
    let folder: String

    nonisolated static func == (a: FilesPanel, b: FilesPanel) -> Bool { a.folder == b.folder }

    @Environment(\.openWindow) private var openWindow
    @State private var query = ""
    /// What git ignores, as paths, folders ending in "/".
    @State private var ignored: Set<String> = []

    private var editor: EditorStore { EditorRegistry.shared.store(for: folder) }

    var body: some View {
        let editor = editor
        VStack(spacing: 0) {
            HStack(spacing: 6) {
                HStack(spacing: 6) {
                    Image(systemName: "magnifyingglass").font(.system(size: 11)).foregroundStyle(Palette.faint)
                    TextField("Search files", text: $query)
                        .textFieldStyle(.plain)
                        .font(TextStyle.caption)
                    if !query.isEmpty {
                        Button { query = "" } label: {
                            Image(systemName: "xmark.circle.fill").font(.system(size: 11)).foregroundStyle(Palette.faint)
                        }
                        .buttonStyle(.plain)
                        .help("Clear")
                    }
                }
                .padding(.horizontal, 8)
                .frame(height: 26)
                .background(Palette.fill.opacity(0.6), in: RoundedRectangle(cornerRadius: 7))
                PanelButton(symbol: "arrow.clockwise", help: "Refresh") {
                    Task { await reload(editor) }
                }
                PanelButton(symbol: "rectangle.compress.vertical", help: "Collapse all folders") {
                    for path in editor.entries(in: "").map(\.path) { collapse(path, in: editor) }
                }
            }
            .padding(.horizontal, 10)
            .frame(height: 38)
            Divider().overlay(Palette.line)
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 1) {
                    let filter = query.trimmingCharacters(in: .whitespaces)
                    if filter.isEmpty {
                        ForEach(rows(editor)) { row in
                            FileTreeRow(editor: editor, entry: row.entry, depth: row.depth,
                                        dim: dimmed(row.entry.path, directory: row.entry.isDirectory)) {
                                activate(row.entry, in: editor)
                            }
                        }
                    } else {
                        ForEach(editor.treeMatches(filter), id: \.self) { path in
                            let entry = FileEntry(path: path, name: (path as NSString).lastPathComponent, isDirectory: false)
                            FileTreeRow(editor: editor, entry: entry, depth: 0, dim: false, folder: (path as NSString).deletingLastPathComponent) {
                                activate(entry, in: editor)
                            }
                        }
                    }
                }
                .padding(.horizontal, 6)
                .padding(.vertical, 6)
            }
            .scrollIndicators(.never)
        }
        .task { await reload(editor) }
        .task(id: query.isEmpty) { if !query.isEmpty { await editor.loadAllFiles() } }
    }

    private struct Row: Identifiable {
        let entry: FileEntry
        let depth: Int
        var id: String { entry.path }
    }

    /// The tree as rows, the open folders' entries under them.
    private func rows(_ editor: EditorStore) -> [Row] {
        var rows: [Row] = []
        func add(_ folder: String, depth: Int) {
            for entry in editor.entries(in: folder) {
                rows.append(Row(entry: entry, depth: depth))
                if entry.isDirectory, editor.isExpanded(entry.path) { add(entry.path, depth: depth + 1) }
            }
        }
        add("", depth: 0)
        return rows
    }

    private func dimmed(_ path: String, directory: Bool) -> Bool {
        if ignored.contains(directory ? path + "/" : path) { return true }
        var folder = (path as NSString).deletingLastPathComponent
        while !folder.isEmpty {
            if ignored.contains(folder + "/") { return true }
            folder = (folder as NSString).deletingLastPathComponent
        }
        return false
    }

    private func activate(_ entry: FileEntry, in editor: EditorStore) {
        if entry.isDirectory {
            editor.setExpanded(entry.path, !editor.isExpanded(entry.path))
        } else {
            EditorOpener.open(folder: folder, file: entry.path, with: openWindow)
        }
    }

    private func collapse(_ path: String, in editor: EditorStore) {
        guard editor.isExpanded(path) else { return }
        for child in editor.entries(in: path) where child.isDirectory { collapse(child.path, in: editor) }
        editor.setExpanded(path, false)
    }

    private func reload(_ editor: EditorStore) async {
        await editor.refresh()
        let listed = await Git.run(["ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z"], in: editor.root) ?? ""
        ignored = Set(listed.split(separator: "\0").map(String.init))
    }
}

private struct FileTreeRow: View {
    let editor: EditorStore
    let entry: FileEntry
    let depth: Int
    let dim: Bool
    /// The folder a search result is in, shown after its name.
    var folder: String?
    let activate: () -> Void
    @State private var hovering = false

    var body: some View {
        let mark = entry.isDirectory ? nil : editor.mark(for: entry.path)
        let open = entry.isDirectory && editor.isExpanded(entry.path)
        Button(action: activate) {
            HStack(spacing: 6) {
                if entry.isDirectory {
                    Image(systemName: open ? "chevron.down" : "chevron.right")
                        .font(.system(size: 8, weight: .semibold))
                        .foregroundStyle(Palette.faint)
                        .frame(width: 10)
                } else {
                    Color.clear.frame(width: 10)
                }
                FileTypeIcon(name: entry.name, directory: entry.isDirectory, open: open, size: 14)
                Text(entry.name)
                    .foregroundStyle(mark?.tint ?? Palette.text)
                    .lineLimit(1)
                    .truncationMode(.middle)
                if let folder, !folder.isEmpty {
                    Text(folder)
                        .font(Fonts.sans(11))
                        .foregroundStyle(Palette.faint)
                        .lineLimit(1)
                        .truncationMode(.head)
                }
                Spacer(minLength: 4)
                if let mark {
                    Text(mark.letter).font(Fonts.mono(10.5, .medium)).foregroundStyle(mark.tint)
                } else if entry.isDirectory, editor.changedFolders.contains(entry.path) {
                    Circle().fill(Palette.gitModified.opacity(0.8)).frame(width: 5, height: 5)
                }
            }
            .font(Fonts.sans(12.5))
            .opacity(dim ? 0.45 : 1)
            .padding(.leading, CGFloat(depth) * 14 + 4)
            .padding(.trailing, 8)
            .frame(height: 24)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(hovering ? Palette.fill.opacity(0.6) : .clear, in: RoundedRectangle(cornerRadius: 5))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(entry.path)
        .contextMenu {
            Button("Copy Path") { Clipboard.copy(editor.root.appendingPathComponent(entry.path).path) }
            Button("Copy Relative Path") { Clipboard.copy(entry.path) }
            Button("Show in Finder") { NSWorkspace.shared.activateFileViewerSelecting([editor.root.appendingPathComponent(entry.path)]) }
        }
    }
}
