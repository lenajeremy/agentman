import AppKit
import SwiftUI

/// Files from Finder: double-clicked when Agentman opens their kind, picked
/// with Open With, or dropped on the Dock icon. Each opens in the editor, in
/// the project it belongs to.
extension EditorOpener {
    /// Opens a file in the editor, or a folder as its own.
    @MainActor
    static func open(file url: URL, with openWindow: OpenWindowAction) {
        let path = realPath(url.path)
        var directory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: path, isDirectory: &directory) else { return }
        if directory.boolValue {
            open(folder: path, with: openWindow)
            return
        }
        let place = place(file: path, open: EditorRegistry.shared.openFolders, home: realPath(NSHomeDirectory()))
        open(folder: place.folder, file: place.file, with: openWindow)
    }

    /// The editor a file opens in, and the file's path inside it. Folders
    /// are compared by their real paths, as `am open` and the agents name
    /// them, so an editor opened through a link still takes its files; the
    /// one it opens in keeps the path it was opened with.
    nonisolated static func place(file: String, open: [String], home: String,
                                  real: (String) -> String = realPath,
                                  isRepository: (String) -> Bool = { FileManager.default.fileExists(atPath: $0 + "/.git") })
        -> (folder: String, file: String) {
        let reals = open.map(real)
        let folder = folder(for: file, open: reals, home: home, isRepository: isRepository)
        let editor = reals.firstIndex(of: folder).map { open[$0] } ?? folder
        return (editor, String(file.dropFirst(folder.count + 1)))
    }

    /// A path with its links followed, as realpath(3) gives it: /tmp is
    /// /private/tmp, as `am open` and the agents name it, where Foundation's
    /// resolvingSymlinksInPath would drop /private.
    nonisolated static func realPath(_ path: String) -> String {
        guard let resolved = realpath(path, nil) else { return URL(fileURLWithPath: path).standardizedFileURL.path }
        defer { free(resolved) }
        return String(cString: resolved)
    }

    /// The folder a file opens in: the editor open on a folder holding it,
    /// the deepest if several are; else the top of its git repository; else
    /// its own folder. A repository at the home folder or above isn't a
    /// project, so the file's own folder it is then.
    nonisolated static func folder(for file: String, open: [String], home: String,
                                   isRepository: (String) -> Bool = { FileManager.default.fileExists(atPath: $0 + "/.git") }) -> String {
        let parent = (file as NSString).deletingLastPathComponent
        if let editor = open.filter({ parent == $0 || parent.hasPrefix($0 + "/") }).max(by: { $0.count < $1.count }) {
            return editor
        }
        var folder = parent
        while folder != "/", !folder.isEmpty, folder != home {
            if isRepository(folder) { return folder }
            folder = (folder as NSString).deletingLastPathComponent
        }
        return parent
    }
}

extension EditorRegistry {
    /// The folders with an editor window showing.
    var openFolders: [String] {
        stores.values.filter { $0.window?.isVisible == true }.map(\.root.path)
    }
}
