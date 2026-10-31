import Foundation

/// A file's diff laid out to read, as T3 Code shows one: hunk headers gone,
/// the unchanged stretch before each hunk folded to a line saying how long it
/// is, and the words that changed inside a changed line marked. Stacked, it
/// is one column; split, what the file was beside what it became.
enum DiffLayout {
    /// Unchanged lines the diff leaves out before a hunk: where they start on
    /// each side and how many there are.
    struct Fold: Hashable {
        let id: Int
        let oldStart: Int
        let newStart: Int
        let count: Int

        var label: String { "\(count) unmodified line\(count == 1 ? "" : "s")" }
    }

    /// A line as shown, with the parts of its text, in UTF-16 offsets, that
    /// differ from the line it replaced or was replaced by.
    struct Line: Hashable {
        let key: String
        let row: DiffRow
        var changed: [Range<Int>] = []
    }

    enum Item: Hashable, Identifiable {
        case fold(Fold)
        case line(Line)

        var id: String {
            switch self {
            case .fold(let fold): "f\(fold.id)"
            case .line(let line): line.key
            }
        }
    }

    /// One row of a split diff: the old line on the left, the new on the
    /// right, either missing where a change added or removed more lines
    /// than it replaced.
    struct Pair: Hashable {
        let key: String
        let old: Line?
        let new: Line?
    }

    enum SplitItem: Hashable, Identifiable {
        case fold(Fold)
        case pair(Pair)

        var id: String {
            switch self {
            case .fold(let fold): "f\(fold.id)"
            case .pair(let pair): pair.key
            }
        }
    }

    /// The diff's rows as items. A fold in `expanded` is shown as its lines,
    /// read from `file` (the file as it is now, a line each), when given.
    static func items(_ rows: [DiffRow], expanded: Set<Int> = [], file: [String]? = nil) -> [Item] {
        var items: [Item] = []
        var nextOld = 1, nextNew = 1
        var folds = 0
        let header = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/
        for row in rows {
            switch row.kind {
            case .meta:
                continue
            case .hunk:
                guard let match = row.text.firstMatch(of: header) else { continue }
                let old = Int(match.1) ?? 0, new = Int(match.2) ?? 0
                // A new or deleted file starts at 0 on its empty side, where
                // nothing came before.
                let hidden = old == 0 || new == 0 ? 0 : min(old - nextOld, new - nextNew)
                if hidden > 0 {
                    let fold = Fold(id: folds, oldStart: nextOld, newStart: nextNew, count: hidden)
                    folds += 1
                    if expanded.contains(fold.id), let file {
                        for offset in 0..<hidden {
                            let number = fold.newStart + offset
                            let text = number - 1 < file.count ? file[number - 1] : ""
                            let context = DiffRow(id: -1, kind: .context, text: text,
                                                  oldNumber: fold.oldStart + offset, newNumber: number)
                            items.append(.line(Line(key: "x\(fold.id).\(offset)", row: context)))
                        }
                    } else {
                        items.append(.fold(fold))
                    }
                }
                nextOld = max(old, 1)
                nextNew = max(new, 1)
            case .context:
                items.append(.line(Line(key: "l\(row.id)", row: row)))
                nextOld = (row.oldNumber ?? nextOld) + 1
                nextNew = (row.newNumber ?? nextNew) + 1
            case .add:
                items.append(.line(Line(key: "l\(row.id)", row: row)))
                nextNew = (row.newNumber ?? nextNew) + 1
            case .remove:
                items.append(.line(Line(key: "l\(row.id)", row: row)))
                nextOld = (row.oldNumber ?? nextOld) + 1
            }
        }
        markChangedWords(&items)
        return items
    }

    /// The same items side by side: a run of removed lines beside the run
    /// of added lines that follows it, line for line.
    static func split(_ items: [Item]) -> [SplitItem] {
        var result: [SplitItem] = []
        var removed: [Line] = []
        var added: [Line] = []
        func flush() {
            for index in 0..<max(removed.count, added.count) {
                let old = index < removed.count ? removed[index] : nil
                let new = index < added.count ? added[index] : nil
                result.append(.pair(Pair(key: "p" + (old ?? new)!.key, old: old, new: new)))
            }
            removed = []
            added = []
        }
        for item in items {
            switch item {
            case .fold(let fold):
                flush()
                result.append(.fold(fold))
            case .line(let line):
                switch line.row.kind {
                case .remove:
                    if !added.isEmpty { flush() }
                    removed.append(line)
                case .add:
                    added.append(line)
                default:
                    flush()
                    result.append(.pair(Pair(key: "p" + line.key, old: line, new: line)))
                }
            }
        }
        flush()
        return result
    }

    /// The widest line number in the diff, in digits, for its gutter.
    static func digits(_ rows: [DiffRow]) -> Int {
        let widest = rows.reduce(0) { max($0, $1.oldNumber ?? 0, $1.newNumber ?? 0) }
        return max(String(widest).count, 2)
    }

    /// Pairs each removed line with the added line in the same place in the
    /// change after it and marks what differs between the two.
    private static func markChangedWords(_ items: inout [Item]) {
        var index = 0
        while index < items.count {
            guard case .line(let first) = items[index], first.row.kind == .remove else { index += 1; continue }
            var removes: [Int] = []
            while index < items.count, case .line(let line) = items[index], line.row.kind == .remove {
                removes.append(index)
                index += 1
            }
            var adds: [Int] = []
            while index < items.count, case .line(let line) = items[index], line.row.kind == .add {
                adds.append(index)
                index += 1
            }
            for (old, new) in zip(removes, adds) {
                guard case .line(var before) = items[old], case .line(var after) = items[new] else { continue }
                let (was, now) = changedWords(before.row.text, after.row.text)
                if let was, let now {
                    before.changed = was.isEmpty ? [] : [was]
                    after.changed = now.isEmpty ? [] : [now]
                    items[old] = .line(before)
                    items[new] = .line(after)
                }
            }
        }
    }

    /// What differs between a line and the line it became, widened to whole
    /// words: the text between what they start and end with in common. Nil
    /// when they share too little for the mark to say anything.
    static func changedWords(_ old: String, _ new: String) -> (Range<Int>?, Range<Int>?) {
        let a = Array(old.utf16), b = Array(new.utf16)
        var prefix = 0
        while prefix < a.count, prefix < b.count, a[prefix] == b[prefix] { prefix += 1 }
        var suffix = 0
        while suffix < a.count - prefix, suffix < b.count - prefix,
              a[a.count - 1 - suffix] == b[b.count - 1 - suffix] { suffix += 1 }
        let was = widen(prefix..<(a.count - suffix), in: a)
        let now = widen(prefix..<(b.count - suffix), in: b)
        let shared = Double(prefix + suffix)
        guard shared > 0, shared / Double(max(a.count, b.count, 1)) >= 0.3 else { return (nil, nil) }
        return (was, now)
    }

    private static func widen(_ range: Range<Int>, in text: [UInt16]) -> Range<Int> {
        guard !range.isEmpty else { return range }
        func word(_ unit: UInt16) -> Bool {
            guard let scalar = Unicode.Scalar(unit) else { return true }
            return CharacterSet.alphanumerics.contains(scalar) || scalar == "_"
        }
        var start = range.lowerBound, end = range.upperBound
        while start > 0, word(text[start - 1]), word(text[start]) { start -= 1 }
        while end < text.count, word(text[end - 1]), word(text[end]) { end += 1 }
        return start..<end
    }
}
