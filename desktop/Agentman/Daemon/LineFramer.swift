import Foundation

/// Splits a byte stream into newline-terminated lines.
///
/// The socket carries one JSON envelope per line, and a read can end anywhere:
/// in the middle of a line, or with several lines at once. Whatever follows the
/// last newline is kept for the next read.
///
/// Each byte is looked at once. A frame of a few megabytes, a page of long
/// tool output, arrives in many reads: searched from its start at every
/// read, and copied again after every line, it took time that grew with
/// the square of its size.
struct LineFramer {
    private var pending = Data()
    /// How much of `pending` is known to hold no newline.
    private var searched = 0
    /// A line longer than this is discarded: the daemon caps its local
    /// socket's frames at 16 MB, so anything bigger is not a frame.
    let maxLine: Int

    init(maxLine: Int = 16 << 20) {
        self.maxLine = maxLine
    }

    mutating func append(_ bytes: Data) -> [Data] {
        pending.append(bytes)
        var lines: [Data] = []
        var start = pending.startIndex
        var from = pending.startIndex + searched
        while let newline = pending[from...].firstIndex(of: 0x0A) {
            let line = pending[start..<newline]
            if !line.isEmpty, line.count <= maxLine {
                lines.append(Data(line))
            }
            start = pending.index(after: newline)
            from = start
        }
        // What is left is the start of a line still to come, searched through.
        if start != pending.startIndex {
            pending = Data(pending[start...])
        }
        searched = pending.count
        if pending.count > maxLine {
            reset()
        }
        return lines
    }

    mutating func reset() {
        pending.removeAll()
        searched = 0
    }
}
