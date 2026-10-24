import Foundation

/// Splits a byte stream into newline-terminated lines.
///
/// The socket carries one JSON envelope per line, and a read can end anywhere:
/// in the middle of a line, or with several lines at once. Whatever follows the
/// last newline is kept for the next read.
struct LineFramer {
    private var pending = Data()
    /// A line longer than this is discarded: the daemon caps its local
    /// socket's frames at 16 MB, so anything bigger is not a frame.
    let maxLine: Int

    init(maxLine: Int = 16 << 20) {
        self.maxLine = maxLine
    }

    mutating func append(_ bytes: Data) -> [Data] {
        pending.append(bytes)
        var lines: [Data] = []
        while let newline = pending.firstIndex(of: 0x0A) {
            let line = pending[pending.startIndex..<newline]
            if !line.isEmpty, line.count <= maxLine {
                lines.append(Data(line))
            }
            pending = Data(pending[pending.index(after: newline)...])
        }
        if pending.count > maxLine {
            pending.removeAll()
        }
        return lines
    }

    mutating func reset() {
        pending.removeAll()
    }
}
