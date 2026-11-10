import Foundation

/// The folders the filter offers: where agents worked lately, and those the
/// user chose themselves, newest first. Left out are the places apps keep
/// for themselves (temporary folders, ~/Library) and folders since deleted.
/// Any other folder is a "Choose a folder…" away.
enum FolderChoices {
    /// How many are offered.
    static let shown = 8
    /// How many folders the user chose are remembered.
    static let kept = 10
    static let chosenKey = "chosenFolders"

    static func make(folders: [Folder], chosen: [String: Date], selected: String?, home: String = NSHomeDirectory(),
                     exists: (String) -> Bool = { FileManager.default.fileExists(atPath: $0) }) -> [String] {
        var latest: [String: Date] = [:]
        for folder in folders {
            latest[folder.path] = Date(timeIntervalSince1970: Double(folder.lastActivityAt ?? 0) / 1000)
        }
        for (path, at) in chosen {
            latest[path] = max(latest[path] ?? .distantPast, at)
        }
        var paths = latest
            .filter { !isAppPlace($0.key, home: home) && exists($0.key) }
            .sorted { ($0.value, $0.key) > ($1.value, $1.key) }
            .prefix(shown)
            .map(\.key)
        if let selected, !paths.contains(selected) {
            paths.insert(selected, at: 0)
        }
        return paths
    }

    /// A place an app keeps for itself rather than one the user works in.
    static func isAppPlace(_ path: String, home: String) -> Bool {
        ["/tmp/", "/private/tmp/", "/var/folders/", "/private/var/folders/", home + "/Library/"]
            .contains { path.hasPrefix($0) }
    }

    /// Where a folder is: the folder holding it, from home.
    static func place(of path: String, home: String = NSHomeDirectory()) -> String {
        let parent = (path as NSString).deletingLastPathComponent
        if parent == home { return "~" }
        if parent.hasPrefix(home + "/") { return "~" + parent.dropFirst(home.count) }
        return parent
    }

    /// The folders the user chose, with when.
    static func chosen(_ defaults: UserDefaults = .standard) -> [String: Date] {
        let stored = defaults.dictionary(forKey: chosenKey) as? [String: Double] ?? [:]
        return stored.mapValues { Date(timeIntervalSince1970: $0) }
    }

    /// Remembers a folder the user chose, keeping the newest few.
    static func remember(_ path: String, at now: Date = .now, _ defaults: UserDefaults = .standard) {
        var all = chosen(defaults)
        all[path] = now
        let newest = all.sorted { $0.value > $1.value }.prefix(kept)
        defaults.set(Dictionary(uniqueKeysWithValues: newest.map { ($0.key, $0.value.timeIntervalSince1970) }), forKey: chosenKey)
    }
}
