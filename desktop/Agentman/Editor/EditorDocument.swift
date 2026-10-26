import AppKit
import Observation

/// A file open in the editor: its text as edited and as last saved, where
/// the cursor is, and whether the file changed on disk under it, as it does
/// whenever an agent edits it.
@MainActor @Observable
final class EditorDocument: Identifiable {
    enum Kind: Equatable { case text, image, binary, tooLarge, missing }

    nonisolated let id = UUID()
    /// The file's path inside the folder; for a file from outside it, opened
    /// by Go to Definition, its path from /.
    private(set) var path: String
    /// A file from outside the folder (a dependency, the standard library):
    /// shown, never edited.
    let readOnly: Bool
    private(set) var url: URL
    private(set) var kind: Kind = .text
    /// The text as edited; the text view writes it on every change.
    var text = ""
    private(set) var saved = ""
    /// Bumped whenever the text is replaced from outside the text view, by a
    /// reload from disk or a revert, so the view takes the new text.
    private(set) var revision = 0
    private(set) var modified: Date?
    /// The file changed on disk while it had unsaved edits.
    private(set) var changedOnDisk = false
    private(set) var deletedOnDisk = false
    /// Where the cursor is, from 1.
    var line = 1
    var column = 1
    private(set) var indent = "    "
    private(set) var crlf = false
    private(set) var image: NSImage?
    var problem: String?
    /// Bumped by each save, so the editor's page can tell the language
    /// server.
    private(set) var saves = 0
    /// How the file's language server is doing, from the editor's page.
    var language: LanguageStatus?
    /// The language server's errors and warnings in the file.
    var problems = Problems()
    /// Each file keeps its own undo history, apart from the others.
    @ObservationIgnored let undo = UndoManager()

    static let maxBytes = 8 << 20
    static let imageTypes: Set<String> = ["png", "jpg", "jpeg", "gif", "webp", "heic", "tiff", "tif", "bmp", "ico", "icns"]

    var isDirty: Bool { kind == .text && text != saved }
    var name: String { url.lastPathComponent }

    init(root: URL, path: String, readOnly: Bool = false) {
        self.path = path
        self.readOnly = readOnly
        url = root.appendingPathComponent(path)
        load()
    }

    /// The file moved, by a rename in the tree.
    func moved(to path: String, root: URL) {
        self.path = path
        url = root.appendingPathComponent(path)
    }

    func load() {
        let attributes = try? FileManager.default.attributesOfItem(atPath: url.path)
        guard let attributes else {
            kind = .missing
            return
        }
        modified = attributes[.modificationDate] as? Date
        changedOnDisk = false
        deletedOnDisk = false
        if Self.imageTypes.contains(url.pathExtension.lowercased()) {
            kind = .image
            image = NSImage(contentsOf: url)
            return
        }
        if ((attributes[.size] as? NSNumber)?.intValue ?? 0) > Self.maxBytes {
            kind = .tooLarge
            return
        }
        guard let data = try? Data(contentsOf: url) else {
            kind = .missing
            return
        }
        guard !data.prefix(8000).contains(0), let string = String(data: data, encoding: .utf8) else {
            kind = .binary
            return
        }
        kind = .text
        text = string
        saved = string
        crlf = string.contains("\r\n")
        indent = Self.indentUnit(of: string, path: url.path)
        revision += 1
    }

    /// Writes the text, keeping the file's permissions, and through a link
    /// to the file rather than over the link.
    @discardableResult
    func save() -> Bool {
        guard kind == .text, !readOnly else { return false }
        let target = url.resolvingSymlinksInPath()
        let permissions = (try? FileManager.default.attributesOfItem(atPath: target.path))?[.posixPermissions]
        do {
            try Data(text.utf8).write(to: target, options: .atomic)
            if let permissions {
                try? FileManager.default.setAttributes([.posixPermissions: permissions], ofItemAtPath: target.path)
            }
            saved = text
            modified = (try? FileManager.default.attributesOfItem(atPath: url.path))?[.modificationDate] as? Date
            changedOnDisk = false
            deletedOnDisk = false
            problem = nil
            saves += 1
            return true
        } catch {
            problem = "Couldn't save \(name): \(error.localizedDescription)"
            return false
        }
    }

    /// One edit from the editor: `length` UTF-16 units at `offset`, in the
    /// text as it was, replaced by `text`.
    struct Change: Equatable {
        var offset: Int
        var length: Int
        var text: String
    }

    /// Takes the editor's edits, last first, as Monaco reports them. Returns
    /// false, leaving the text as it was, when they don't fit it: then the
    /// editor's copy has to be fetched whole.
    @discardableResult
    func apply(_ changes: [Change], expecting length: Int) -> Bool {
        var edited = text
        for change in changes {
            let utf16 = edited.utf16
            guard change.offset >= 0, change.length >= 0, change.offset + change.length <= utf16.count,
                  let start = utf16.index(utf16.startIndex, offsetBy: change.offset, limitedBy: utf16.endIndex),
                  let end = utf16.index(start, offsetBy: change.length, limitedBy: utf16.endIndex)
            else { return false }
            edited.replaceSubrange(start..<end, with: change.text)
        }
        guard edited.utf16.count == length else { return false }
        text = edited
        return true
    }

    /// A language server's edit, in lines and UTF-16 characters from 0.
    struct LineEdit: Equatable {
        var startLine: Int
        var startCharacter: Int
        var endLine: Int
        var endCharacter: Int
        var text: String
    }

    /// Applies a language server's edits to a file that isn't in front, as
    /// a rename across files does; it is then unsaved, for the user to look
    /// over and save.
    @discardableResult
    func apply(_ edits: [LineEdit]) -> Bool {
        guard kind == .text, !readOnly else { return false }
        let units = Array(text.utf16)
        var starts = [0]
        for (index, unit) in units.enumerated() where unit == 10 { starts.append(index + 1) }
        func offset(_ line: Int, _ character: Int) -> Int? {
            guard line >= 0, character >= 0, line < starts.count else { return nil }
            var end = line + 1 < starts.count ? starts[line + 1] - 1 : units.count
            if end > starts[line], units[end - 1] == 13, line + 1 < starts.count { end -= 1 }
            return min(starts[line] + character, end)
        }
        var changes: [(index: Int, change: Change)] = []
        for (index, edit) in edits.enumerated() {
            guard let start = offset(edit.startLine, edit.startCharacter), let end = offset(edit.endLine, edit.endCharacter), end >= start
            else { return false }
            changes.append((index, Change(offset: start, length: end - start, text: edit.text)))
        }
        // Last first; edits at the same place go in the order given.
        changes.sort { $0.change.offset != $1.change.offset ? $0.change.offset > $1.change.offset : $0.index > $1.index }
        let length = changes.reduce(units.count) { $0 + $1.change.text.utf16.count - $1.change.length }
        return apply(changes.map(\.change), expecting: length)
    }

    /// The editor's whole text, when its edits and this copy fell apart, or
    /// when it evened out a file's mixed line endings as it opened it. A file
    /// without edits takes it as what's saved, so opening alone doesn't make
    /// it unsaved; the next save writes it that way.
    func adopt(_ shown: String) {
        if text == saved { saved = shown }
        text = shown
    }

    /// Back to the text as last saved.
    func revert() {
        guard isDirty else { return }
        text = saved
        revision += 1
    }

    /// Takes the version on disk, dropping unsaved edits.
    func reloadFromDisk() {
        load()
    }

    /// Keeps the edits, so a save writes over what changed on disk.
    func keepMine() {
        changedOnDisk = false
        modified = (try? FileManager.default.attributesOfItem(atPath: url.path))?[.modificationDate] as? Date
    }

    /// Looks at the file on disk: one without unsaved edits follows it,
    /// one with them says it changed.
    func checkDisk() {
        guard kind != .missing else { return }
        guard let attributes = try? FileManager.default.attributesOfItem(atPath: url.path) else {
            deletedOnDisk = true
            return
        }
        deletedOnDisk = false
        let now = attributes[.modificationDate] as? Date
        guard now != modified else { return }
        if isDirty {
            changedOnDisk = true
            return
        }
        if kind == .text, let data = try? Data(contentsOf: url), String(data: data, encoding: .utf8) == text {
            modified = now
            return
        }
        load()
    }

    /// What the file's lines are indented with: tabs, or the smallest step
    /// of spaces, 2 or 4; for a file with no indented lines, its language's
    /// usual step.
    nonisolated static func indentUnit(of text: String, path: String) -> String {
        var tabs = 0
        var steps: [Int: Int] = [:]
        var count = 0
        for line in text.split(separator: "\n", omittingEmptySubsequences: true) {
            count += 1
            if count > 2000 { break }
            if line.hasPrefix("\t") {
                tabs += 1
            } else if line.hasPrefix(" ") {
                let spaces = line.prefix { $0 == " " }.count
                guard line.count > spaces else { continue }
                if spaces % 4 == 0 { steps[4, default: 0] += 1 }
                if spaces % 2 == 0 && spaces % 4 != 0 { steps[2, default: 0] += 1 }
            }
        }
        let spaced = steps.values.reduce(0, +)
        if tabs > spaced { return "\t" }
        if spaced > 0 { return (steps[2] ?? 0) > 0 ? "  " : "    " }
        switch (path as NSString).pathExtension.lowercased() {
        case "js", "jsx", "ts", "tsx", "mjs", "cjs", "json", "yaml", "yml", "css", "scss", "html", "rb": return "  "
        case "go", "make": return "\t"
        default: return "    "
        }
    }

    /// The language's name, for the status bar.
    var languageName: String {
        let ext = url.pathExtension.lowercased()
        switch ext {
        case "swift": return "Swift"
        case "go": return "Go"
        case "ts", "mts", "cts": return "TypeScript"
        case "tsx": return "TypeScript React"
        case "js", "mjs", "cjs": return "JavaScript"
        case "jsx": return "JavaScript React"
        case "py": return "Python"
        case "rs": return "Rust"
        case "rb": return "Ruby"
        case "java": return "Java"
        case "kt", "kts": return "Kotlin"
        case "c", "h": return "C"
        case "cpp", "cc", "hpp": return "C++"
        case "m", "mm": return "Objective-C"
        case "md", "markdown": return "Markdown"
        case "json": return "JSON"
        case "yaml", "yml": return "YAML"
        case "toml": return "TOML"
        case "sh", "bash", "zsh": return "Shell"
        case "css": return "CSS"
        case "scss": return "SCSS"
        case "html", "htm": return "HTML"
        case "xml", "plist": return "XML"
        case "sql": return "SQL"
        default:
            let name = url.lastPathComponent.lowercased()
            if name == "dockerfile" { return "Dockerfile" }
            if name == "makefile" { return "Makefile" }
            return "Plain Text"
        }
    }
}

/// A language server's state, as the status bar shows it.
struct LanguageStatus: Equatable {
    var name: String
    var language: String
    /// starting, ready, restarting, failed or stopped.
    var state: String
    var detail: String
}

struct Problems: Equatable {
    var errors = 0
    var warnings = 0
}
