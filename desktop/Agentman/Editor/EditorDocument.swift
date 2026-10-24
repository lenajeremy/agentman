import AppKit
import Observation

/// A file open in the editor: its text as edited and as last saved, where
/// the cursor is, and whether the file changed on disk under it, as it does
/// whenever an agent edits it.
@MainActor @Observable
final class EditorDocument: Identifiable {
    enum Kind: Equatable { case text, image, binary, tooLarge, missing }

    nonisolated let id = UUID()
    /// The file's path inside the folder.
    private(set) var path: String
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
    /// Each file keeps its own undo history, apart from the others.
    @ObservationIgnored let undo = UndoManager()

    static let maxBytes = 8 << 20
    static let imageTypes: Set<String> = ["png", "jpg", "jpeg", "gif", "webp", "heic", "tiff", "tif", "bmp", "ico", "icns"]

    var isDirty: Bool { kind == .text && text != saved }
    var name: String { url.lastPathComponent }

    init(root: URL, path: String) {
        self.path = path
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
        guard kind == .text else { return false }
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
            return true
        } catch {
            problem = "Couldn't save \(name): \(error.localizedDescription)"
            return false
        }
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
