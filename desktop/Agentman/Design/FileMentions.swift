import AppKit
import SwiftUI

extension EnvironmentValues {
    /// The editor's files, for `@` in a message box, where there is an
    /// editor around it (its agent panel); nil elsewhere.
    @Entry var fileMentions: FileMentions?
}

/// `@` and a few letters of a file's name in a message box: the folder's
/// files that match, to pick from with the arrow keys, Return or Tab, or the
/// pointer. A pick goes in as `@` and the file's path in the folder, drawn
/// as a chip and deleted as one; a message sent names each such file by a
/// path its reader can open.
@MainActor @Observable
final class FileMentions {
    let root: URL
    /// Every file in the folder, as paths inside it.
    @ObservationIgnored private let files: () -> [String]
    /// What to offer before anything is typed: the file in front first.
    @ObservationIgnored private let suggested: () -> [String]
    /// Lists the folder's files, when they haven't been yet.
    @ObservationIgnored private let load: () async -> Void
    /// What is typed after the `@`, while the list is open.
    private(set) var query: String?
    private(set) var results: [String] = []
    var selected = 0
    /// Where the `@` and what follows it are in the box, for the pick to
    /// replace.
    @ObservationIgnored private var range = NSRange(location: NSNotFound, length: 0)
    @ObservationIgnored weak var textView: NSTextView?
    @ObservationIgnored private var loaded = false

    static let shown = 8

    init(root: URL, files: @escaping () -> [String], suggested: @escaping () -> [String], load: @escaping () async -> Void) {
        self.root = root
        self.files = files
        self.suggested = suggested
        self.load = load
    }

    var isOpen: Bool { query != nil && !results.isEmpty }

    // MARK: - Typing

    /// Follows the box: open while the caret ends a word that starts with
    /// `@`, its letters the query.
    func update(text: String, caret: Int) {
        let ns = text as NSString
        guard caret <= ns.length, caret == ns.length || Self.isSpace(ns.character(at: caret)) else { return dismiss() }
        var start = caret
        while start > 0, !Self.isSpace(ns.character(at: start - 1)) { start -= 1 }
        let word = ns.substring(with: NSRange(location: start, length: caret - start))
        guard word.hasPrefix("@"), !word.dropFirst().contains("@") else { return dismiss() }
        let typed = String(word.dropFirst())
        // A chip already made isn't asked about again.
        if !typed.isEmpty, isFile(typed) { return dismiss() }
        if !loaded {
            loaded = true
            Task { [weak self] in
                await self?.load()
                self?.refresh()
            }
        }
        range = NSRange(location: start, length: caret - start)
        if query != typed { selected = 0 }
        query = typed
        refresh()
    }

    private func refresh() {
        guard let query else { return }
        if query.isEmpty {
            var seen = Set<String>()
            results = suggested().filter { seen.insert($0).inserted }.prefix(Self.shown).map { $0 }
        } else {
            results = files()
                .compactMap { path in Fuzzy.score(query, path).map { (path, $0) } }
                .sorted { $0.1 > $1.1 }
                .prefix(Self.shown)
                .map(\.0)
        }
        selected = min(selected, max(results.count - 1, 0))
    }

    func move(_ step: Int) {
        guard !results.isEmpty else { return }
        selected = (selected + step + results.count) % results.count
    }

    /// Puts the file picked, or the one highlighted, in place of `@query`.
    func choose(_ index: Int? = nil) {
        let index = index ?? selected
        guard let textView, results.indices.contains(index), range.location != NSNotFound,
              NSMaxRange(range) <= (textView.string as NSString).length else { return dismiss() }
        let replace = range
        let path = results[index]
        dismiss()
        textView.insertText("@\(path) ", replacementRange: replace)
    }

    func dismiss() {
        guard query != nil else { return }
        query = nil
        results = []
        selected = 0
        range = NSRange(location: NSNotFound, length: 0)
    }

    // MARK: - Chips

    /// Whether a path names something in the folder.
    func isFile(_ path: String) -> Bool {
        FileManager.default.fileExists(atPath: root.appendingPathComponent(path).path)
    }

    /// The `@path` words of a text that name files in the folder: each
    /// word's range, and the path. Punctuation that ends a sentence after a
    /// path isn't part of it.
    func chips(in text: String) -> [(range: NSRange, path: String)] {
        let ns = text as NSString
        var found: [(NSRange, String)] = []
        for match in Self.word.matches(in: text, range: NSRange(location: 0, length: ns.length)) {
            var range = match.range
            var path = ns.substring(with: match.range(at: 1))
            while !path.isEmpty, !isFile(path), let last = path.last, ".,;:!?)]}'\"".contains(last) {
                path.removeLast()
                range.length -= 1
            }
            if !path.isEmpty, isFile(path) { found.append((range, path)) }
        }
        return found
    }

    private static let word = try! NSRegularExpression(pattern: #"(?<![^\s])@([^\s@]+)"#)

    private static func isSpace(_ unit: unichar) -> Bool {
        guard let scalar = UnicodeScalar(unit) else { return false }
        return CharacterSet.whitespacesAndNewlines.contains(scalar)
    }

    // MARK: - Sending

    /// The message as it is sent: each file named with `@` by a path its
    /// reader can open. An agent gets it from its own folder when the file
    /// is inside, as its `@` mentions work; Agentman, and an agent working
    /// elsewhere, get the full path, which Agentman passes on as it is.
    func resolve(_ text: String, folder: String?) -> String {
        let base = folder.map { URL(fileURLWithPath: $0).standardizedFileURL.path }
        var result = text as NSString
        for chip in chips(in: text).reversed() {
            let absolute = root.appendingPathComponent(chip.path).standardizedFileURL.path
            var path = absolute
            if let base, absolute.hasPrefix(base + "/") { path = String(absolute.dropFirst(base.count + 1)) }
            result = result.replacingCharacters(in: chip.range, with: "@" + path) as NSString
        }
        return result as String
    }
}

/// The files that match what follows `@`, over the message box: the
/// highlighted one is what Return or Tab picks.
struct MentionList: View {
    let mentions: FileMentions

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(mentions.query?.isEmpty == false ? "Files" : "Suggested")
                .font(TextStyle.label)
                .foregroundStyle(Palette.faint)
                .padding(.horizontal, 8)
                .padding(.top, 4)
                .padding(.bottom, 3)
            ForEach(Array(mentions.results.enumerated()), id: \.element) { index, path in
                MentionRow(path: path, selected: index == mentions.selected)
                    .contentShape(Rectangle())
                    .onTapGesture { mentions.choose(index) }
                    .onHover { if $0 { mentions.selected = index } }
            }
        }
        .padding(5)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Palette.surface, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(Palette.line))
        .shadow(color: .black.opacity(0.12), radius: 14, y: 4)
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Files to mention")
    }
}

private struct MentionRow: View {
    let path: String
    let selected: Bool

    var body: some View {
        let folder = (path as NSString).deletingLastPathComponent
        HStack(spacing: 8) {
            FileTypeIcon(name: (path as NSString).lastPathComponent, size: 14)
                .frame(width: 16)
            Text((path as NSString).lastPathComponent)
                .font(Fonts.sans(13, selected ? .medium : .regular))
                .foregroundStyle(Palette.text)
                .lineLimit(1)
                .layoutPriority(1)
            if !folder.isEmpty {
                Text(folder)
                    .font(Fonts.sans(11.5))
                    .foregroundStyle(Palette.faint)
                    .lineLimit(1)
                    .truncationMode(.head)
            }
            Spacer(minLength: 0)
            if selected {
                Image(systemName: "return")
                    .font(.system(size: 9.5, weight: .semibold))
                    .foregroundStyle(Palette.faint)
            }
        }
        .padding(.horizontal, 8)
        .frame(height: 28)
        .background(selected ? Palette.fill : .clear, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}
