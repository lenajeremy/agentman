import SwiftUI

// Search across the folder's files on ⇧⌘F, in the palette Go to File
// opens: the words, how they match, and every line they are on, a match
// opening its file at the match. It searches when typing stops for a
// second, or at once on Return, never on every key: a big folder takes a
// moment.

/// How the words are matched.
nonisolated struct SearchOptions: Hashable, Sendable {
    var caseSensitive = false
    var wholeWord = false
    var regex = false
}

/// A line the words were found on, as many times as they are on it.
nonisolated struct SearchMatch: Identifiable, Hashable, Sendable {
    let path: String
    /// From 1.
    let line: Int
    /// The first match on the line, from 1, in UTF-16 units, as the editor
    /// counts them.
    let column: Int
    let length: Int
    /// The line, trimmed and clipped around the first match.
    let preview: String
    /// Where the matches are in the preview, in UTF-16 units.
    var highlights: [NSRange]
    /// How many times the words are on the line.
    var count = 1

    var id: String { "\(path):\(line):\(column)" }
}

nonisolated struct SearchFile: Identifiable, Sendable {
    let path: String
    let matches: [SearchMatch]
    var id: String { path }
}

nonisolated struct SearchResults: Sendable {
    let query: String
    let options: SearchOptions
    var files: [SearchFile] = []
    var total = 0
    /// Stopped at FolderSearch.maxMatches.
    var truncated = false
    /// Why there are none: words that aren't a regular expression.
    var problem: String?
}

nonisolated enum FolderSearch {
    /// Enough to read through; more says to narrow the words instead.
    static let maxMatches = 2000
    /// Bigger files are data, not code.
    static let maxFileBytes = 2 << 20
    static let previewChars = 160

    static func expression(_ query: String, _ options: SearchOptions) throws -> NSRegularExpression {
        var pattern = options.regex ? query : NSRegularExpression.escapedPattern(for: query)
        if options.wholeWord { pattern = "\\b(?:" + pattern + ")\\b" }
        return try NSRegularExpression(pattern: pattern, options: options.caseSensitive ? [] : [.caseInsensitive])
    }

    /// Searches files, relative to root, in order. Text given for a path
    /// (an open file's unsaved edits) is searched instead of the file on
    /// disk.
    static func run(_ query: String, options: SearchOptions, files: [String], root: URL,
                    edited: [String: String] = [:]) -> SearchResults {
        var results = SearchResults(query: query, options: options)
        guard !query.isEmpty else { return results }
        let expression: NSRegularExpression
        do {
            expression = try self.expression(query, options)
        } catch {
            results.problem = "Not a regular expression"
            return results
        }
        // By path, as git lists them: a folder walked comes in disk order.
        for path in files.sorted() {
            if Task.isCancelled || results.truncated { break }
            // A folder walked, not listed by git: what git would ignore.
            if path.split(separator: "/").dropLast().contains(where: { EditorStore.quietFolders.contains(String($0)) }) { continue }
            let text: NSString
            if let open = edited[path] {
                text = open as NSString
            } else {
                guard let data = try? Data(contentsOf: root.appendingPathComponent(path), options: .mappedIfSafe),
                      data.count <= maxFileBytes, !data.prefix(8000).contains(0) else { continue }
                text = String(decoding: data, as: UTF8.self) as NSString
            }
            let found = matches(of: expression, in: text, path: path, room: maxMatches - results.total)
            guard !found.matches.isEmpty else { continue }
            results.files.append(SearchFile(path: path, matches: found.matches))
            results.total += found.matches.reduce(0) { $0 + $1.count }
            results.truncated = found.more
        }
        return results
    }

    /// The lines of one file the words are on, with at most room matches
    /// between them, and whether there were more.
    static func matches(of expression: NSRegularExpression, in text: NSString, path: String,
                        room: Int) -> (matches: [SearchMatch], more: Bool) {
        var out: [SearchMatch] = []
        var more = false
        var line = 1, counted = 0, found = 0
        // Where the line's preview starts in the line, for its later matches.
        var shift = 0
        expression.enumerateMatches(in: text as String, range: NSRange(location: 0, length: text.length)) { result, _, stop in
            guard let range = result?.range, range.length > 0 else { return }
            if found == room {
                more = true
                stop.pointee = true
                return
            }
            found += 1
            // Lines counted on from the last match: one pass through the file.
            for index in counted..<range.location where text.character(at: index) == 10 {
                line += 1
            }
            counted = range.location
            let lineRange = text.lineRange(for: NSRange(location: range.location, length: 0))
            let column = range.location - lineRange.location
            let length = min(range.length, lineRange.location + lineRange.length - range.location)
            if var last = out.last, last.line == line {
                // Another on the same line: marked in its row, where it shows.
                last.count += 1
                let start = column - shift
                if start >= 0, start + length <= (last.preview as NSString).length {
                    last.highlights.append(NSRange(location: start, length: length))
                }
                out[out.count - 1] = last
                return
            }
            let preview = self.preview(of: text.substring(with: lineRange), at: column, length: length)
            shift = preview.shift
            out.append(SearchMatch(path: path, line: line, column: column + 1, length: range.length,
                                   preview: preview.text, highlights: [preview.highlight]))
        }
        return (out, more)
    }

    /// The line as a result shows it: without its indent or line end, and a
    /// long one cut down to the part around the match. shift is how much
    /// sooner a column of the line is in the preview.
    static func preview(of line: String, at column: Int, length: Int) -> (text: String, highlight: NSRange, shift: Int) {
        var text = line as NSString
        var start = column
        while text.length > 0, text.character(at: text.length - 1) == 10 || text.character(at: text.length - 1) == 13 {
            text = text.substring(to: text.length - 1) as NSString
        }
        var indent = 0
        while indent < text.length, indent < start, text.character(at: indent) == 32 || text.character(at: indent) == 9 {
            indent += 1
        }
        text = text.substring(from: indent) as NSString
        start -= indent
        var prefix = "", cut = 0
        if text.length > previewChars, start > 40 {
            cut = start - 40
            text = text.substring(from: cut) as NSString
            start -= cut
            prefix = "…"
        }
        if text.length > previewChars {
            text = (text.substring(to: previewChars) + "…") as NSString
        }
        let length = max(0, min(length, text.length - start))
        let lead = (prefix as NSString).length
        return (prefix + (text as String), NSRange(location: start + lead, length: length), indent + cut - lead)
    }
}

// MARK: - Searching

extension EditorStore {
    /// ⇧⌘F: the search palette, with what was searched last.
    func showSearch() {
        quickOpen = false
        searchOpen = true
    }

    /// Searches the folder for query, unless that search is already done or
    /// under way. Files with unsaved edits are searched as edited.
    func runSearch(_ query: String) {
        let options = searchOptions
        searchQuery = query
        if let results = searchResults, results.query == query, results.options == options { return }
        if searchRunning, searching == SearchKey(query: query, options: options) { return }
        searchWork?.cancel()
        guard !query.isEmpty else {
            searchResults = nil
            searchRunning = false
            return
        }
        searchRunning = true
        searching = SearchKey(query: query, options: options)
        let root = self.root
        var edited: [String: String] = [:]
        for document in documents where document.isDirty && !document.readOnly {
            edited[document.path] = document.text
        }
        Task { [weak self] in
            await self?.loadAllFiles()
            guard let self, self.searching == SearchKey(query: query, options: options) else { return }
            let files = self.allFiles
            let work = Task.detached(priority: .userInitiated) { @Sendable [files, edited] () -> SearchResults in
                FolderSearch.run(query, options: options, files: files, root: root, edited: edited)
            }
            self.searchWork = work
            let results = await work.value
            guard self.searching == SearchKey(query: query, options: options), !work.isCancelled else { return }
            self.searchResults = results
            self.searchRunning = false
        }
    }

    /// Opens a match's file with the match selected.
    func openMatch(_ match: SearchMatch) {
        searchOpen = false
        open(match.path)
        if let document = active {
            monaco?.reveal(document.id, line: match.line, column: match.column, length: match.length)
        }
    }
}

nonisolated struct SearchKey: Equatable {
    let query: String
    let options: SearchOptions
}

/// The search palette: Go to File's, for the words in the files.
struct GoToText: View {
    @Bindable var store: EditorStore
    @State private var typing: Task<Void, Never>?

    var body: some View {
        PaletteView(
            placeholder: "Search in \(store.name)",
            results: results,
            choose: { item in
                if let match = store.searchResults?.files.lazy.flatMap(\.matches).first(where: { $0.id == item.id }) {
                    store.openMatch(match)
                }
            },
            dismiss: { store.searchOpen = false },
            typed: { query in
                // A second after the typing stops.
                typing?.cancel()
                typing = Task {
                    try? await Task.sleep(for: .seconds(1))
                    guard !Task.isCancelled else { return }
                    store.runSearch(query)
                }
            },
            submit: { query in
                // Return searches at once when what shows is for other words.
                guard store.searchResults?.query != query || store.searchResults?.options != store.searchOptions else { return false }
                typing?.cancel()
                store.runSearch(query)
                return true
            },
            accessory: AnyView(options),
            status: status,
            query: store.searchQuery
        )
        .onChange(of: store.searchOptions) { store.runSearch(store.searchQuery) }
    }

    private func results(_ query: String) -> [PaletteItem] {
        guard let results = store.searchResults, !results.query.isEmpty, !query.isEmpty else { return [] }
        return results.files.flatMap { file in
            file.matches.map { match in
                PaletteItem(id: match.id, title: match.preview,
                            detail: "\(Self.place(file.path)):\(match.line)",
                            icon: FileIcon.symbol(file.path), tint: FileIcon.tint(file.path),
                            fileIcon: FileIcon.asset(file.path), line: highlighted(match))
            }
        }
    }

    private var status: String? {
        if let problem = store.searchResults?.problem { return problem }
        if store.searchRunning { return "Searching…" }
        guard let results = store.searchResults, !results.query.isEmpty else { return nil }
        if results.total == 0 { return "No results" }
        let files = results.files.count
        return "\(results.total) result\(results.total == 1 ? "" : "s") in \(files) file\(files == 1 ? "" : "s")"
            + (results.truncated ? ", the first \(FolderSearch.maxMatches)" : "")
    }

    private var options: some View {
        HStack(spacing: 2) {
            toggle("Aa", \.caseSensitive, help: "Match Case")
            toggle("ab", \.wholeWord, help: "Match Whole Word", underline: true)
            toggle(".*", \.regex, help: "Use Regular Expression")
        }
    }

    private func toggle(_ label: String, _ option: WritableKeyPath<SearchOptions, Bool>, help: String,
                        underline: Bool = false) -> some View {
        let on = store.searchOptions[keyPath: option]
        return Button { store.searchOptions[keyPath: option].toggle() } label: {
            Text(label)
                .font(Fonts.mono(11))
                .underline(underline)
                .foregroundStyle(on ? Palette.onInverse : Palette.muted)
                .frame(width: 24, height: 20)
                .background(on ? Palette.inverse : Color.clear, in: RoundedRectangle(cornerRadius: 5, style: .continuous))
        }
        .buttonStyle(.plain)
        .focusable(false)
        .help(help)
    }

    /// A file named with its folder: two files of one name are told apart.
    private static func place(_ path: String) -> String {
        let parts = path.split(separator: "/")
        return parts.suffix(2).joined(separator: "/")
    }

    private func highlighted(_ match: SearchMatch) -> AttributedString {
        var text = AttributedString(match.preview)
        for highlight in match.highlights {
            guard let range = Range(highlight, in: match.preview), let span = Range(range, in: text) else { continue }
            text[span].foregroundColor = Palette.text
            text[span].backgroundColor = Palette.workingWash
            text[span].inlinePresentationIntent = .stronglyEmphasized
        }
        return text
    }
}
