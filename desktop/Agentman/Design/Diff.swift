import Foundation

/// A unified diff as rows to show, numbered by the lines they were and
/// became. The phone reads diffs the same way (mobile/lib/diff.ts).
struct DiffRow: Identifiable, Hashable {
    enum Kind: Hashable { case meta, hunk, add, remove, context }
    let id: Int
    let kind: Kind
    let text: String
    var oldNumber: Int?
    var newNumber: Int?

    /// Rows before a file's first `@@` are its `diff --git`/`---`/`+++`
    /// preamble and are dropped. A diff of several files, as an agent's edit
    /// of several is, names each file in a meta row over its hunks instead.
    static func parse(_ diff: String) -> [DiffRow] {
        var rows: [DiffRow] = []
        var old = 0, new = 0
        // The lines the hunk being read still has, by its header. Past them,
        // a preamble line starts the next file rather than being a change.
        var oldLeft = 0, newLeft = 0
        var inHunk = false
        var named: String?
        var names: Set<Int> = []
        let hunk = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/
        // What git writes ahead of a file's hunks.
        let preamble = /^(diff |index |--- |\+\+\+ |new file|deleted file|old mode|new mode|similarity |rename |copy |Binary files)/
        for line in diff.split(separator: "\n", omittingEmptySubsequences: false) {
            let line = String(line)
            if let m = line.firstMatch(of: hunk) {
                if let name = named {
                    names.insert(rows.count)
                    rows.append(DiffRow(id: rows.count, kind: .meta, text: name))
                    named = nil
                }
                inHunk = true
                old = Int(m.1) ?? 0
                oldLeft = m.2.flatMap { Int($0) } ?? 1
                new = Int(m.3) ?? 0
                newLeft = m.4.flatMap { Int($0) } ?? 1
                rows.append(DiffRow(id: rows.count, kind: .hunk, text: line))
                continue
            }
            if inHunk, oldLeft <= 0, newLeft <= 0, line.firstMatch(of: preamble) != nil { inHunk = false }
            guard inHunk else {
                named = preambleName(line) ?? named
                continue
            }
            if line.hasPrefix("\\") {
                rows.append(DiffRow(id: rows.count, kind: .meta, text: line))
            } else if line.hasPrefix("+") {
                rows.append(DiffRow(id: rows.count, kind: .add, text: String(line.dropFirst()), newNumber: new))
                new += 1
                newLeft -= 1
            } else if line.hasPrefix("-") {
                rows.append(DiffRow(id: rows.count, kind: .remove, text: String(line.dropFirst()), oldNumber: old))
                old += 1
                oldLeft -= 1
            } else {
                rows.append(DiffRow(id: rows.count, kind: .context, text: line.hasPrefix(" ") ? String(line.dropFirst()) : line,
                                    oldNumber: old, newNumber: new))
                old += 1
                new += 1
                oldLeft -= 1
                newLeft -= 1
            }
        }
        if let last = rows.last, last.kind == .context, last.text.isEmpty { rows.removeLast() }
        if names.count == 1 {
            rows = rows.filter { !names.contains($0.id) }.enumerated().map { index, row in
                DiffRow(id: index, kind: row.kind, text: row.text, oldNumber: row.oldNumber, newNumber: row.newNumber)
            }
        }
        return rows
    }

    /// The file a `---` or `+++` line names, by its own name; "+++" comes
    /// second, so it wins unless the file was deleted.
    private static func preambleName(_ line: String) -> String? {
        guard let header = line.firstMatch(of: /^(?:---|\+\+\+) (.+)$/), header.1 != "/dev/null" else { return nil }
        var path = Substring(header.1)
        if path.hasPrefix("a/") || path.hasPrefix("b/") { path = path.dropFirst(2) }
        return path.split(separator: "/").last.map(String.init) ?? String(path)
    }
}
