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

    static func parse(_ diff: String) -> [DiffRow] {
        var rows: [DiffRow] = []
        var old = 0, new = 0
        var started = false
        let hunk = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/
        for line in diff.split(separator: "\n", omittingEmptySubsequences: false) {
            let line = String(line)
            if let m = line.firstMatch(of: hunk) {
                started = true
                old = Int(m.1) ?? 0
                new = Int(m.2) ?? 0
                rows.append(DiffRow(id: rows.count, kind: .hunk, text: line))
                continue
            }
            guard started else { continue }
            if line.hasPrefix("\\") {
                rows.append(DiffRow(id: rows.count, kind: .meta, text: line))
            } else if line.hasPrefix("+") {
                rows.append(DiffRow(id: rows.count, kind: .add, text: String(line.dropFirst()), newNumber: new))
                new += 1
            } else if line.hasPrefix("-") {
                rows.append(DiffRow(id: rows.count, kind: .remove, text: String(line.dropFirst()), oldNumber: old))
                old += 1
            } else {
                rows.append(DiffRow(id: rows.count, kind: .context, text: line.hasPrefix(" ") ? String(line.dropFirst()) : line,
                                    oldNumber: old, newNumber: new))
                old += 1
                new += 1
            }
        }
        if let last = rows.last, last.kind == .context, last.text.isEmpty { rows.removeLast() }
        return rows
    }
}
