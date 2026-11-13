import AppKit
import SwiftUI

/// Files from Finder: double-clicked when Agentman opens their kind, picked
/// with Open With, or dropped on the Dock icon. Each opens in the editor, in
/// the project it belongs to.
extension EditorOpener {
    /// Opens a file in the editor, or a folder as its own.
    @MainActor
    static func open(file url: URL, with openWindow: OpenWindowAction) {
        let path = url.resolvingSymlinksInPath().standardizedFileURL.path
        var directory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: path, isDirectory: &directory) else { return }
        if directory.boolValue {
            open(folder: path, with: openWindow)
            return
        }
        let folder = folder(for: path, open: EditorRegistry.shared.openFolders, home: NSHomeDirectory())
        open(folder: folder, file: String(path.dropFirst(folder.count + 1)), with: openWindow)
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
