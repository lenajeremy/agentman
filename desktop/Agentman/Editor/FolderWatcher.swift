import CoreServices
import Foundation

/// Files created, changed and deleted under a folder, from FSEvents, a
/// moment after they happen: how the language servers hear about files an
/// agent changes that aren't open in the editor.
@MainActor
final class FolderWatcher {
    /// A file and what happened to it, as LSP numbers it: 1 created,
    /// 2 changed, 3 deleted.
    struct Change: Equatable {
        var path: String
        var type: Int
    }

    private nonisolated(unsafe) var stream: FSEventStreamRef?
    private let changed: ([Change]) -> Void

    init(folder: URL, changed: @escaping ([Change]) -> Void) {
        self.changed = changed
        var context = FSEventStreamContext(version: 0, info: Unmanaged.passUnretained(self).toOpaque(), retain: nil, release: nil, copyDescription: nil)
        let callback: FSEventStreamCallback = { _, info, count, paths, flags, _ in
            guard let info else { return }
            let watcher = Unmanaged<FolderWatcher>.fromOpaque(info).takeUnretainedValue()
            let list = Unmanaged<CFArray>.fromOpaque(paths).takeUnretainedValue() as? [String] ?? []
            let flagList = Array(UnsafeBufferPointer(start: flags, count: count))
            MainActor.assumeIsolated { watcher.received(zip(list, flagList).map { ($0, $1) }) }
        }
        let flags = FSEventStreamCreateFlags(kFSEventStreamCreateFlagFileEvents | kFSEventStreamCreateFlagUseCFTypes | kFSEventStreamCreateFlagNoDefer)
        guard let stream = FSEventStreamCreate(nil, callback, &context, [folder.path] as CFArray,
                                               FSEventStreamEventId(kFSEventStreamEventIdSinceNow), 0.3, flags)
        else { return }
        self.stream = stream
        FSEventStreamSetDispatchQueue(stream, .main)
        FSEventStreamStart(stream)
    }

    deinit {
        guard let stream else { return }
        FSEventStreamStop(stream)
        FSEventStreamInvalidate(stream)
        FSEventStreamRelease(stream)
    }

    private func received(_ events: [(String, FSEventStreamEventFlags)]) {
        var changes: [String: Int] = [:]
        var order: [String] = []
        for (path, flags) in events {
            guard let type = Self.type(path: path, flags: flags) else { continue }
            if changes[path] == nil { order.append(path) }
            changes[path] = type
        }
        let list = order.compactMap { path in changes[path].map { Change(path: path, type: $0) } }
        if !list.isEmpty { changed(list) }
    }

    /// What happened to a file, or nil for what servers needn't hear: folders,
    /// git's own files, and installed packages.
    nonisolated static func type(path: String, flags: FSEventStreamEventFlags) -> Int? {
        func has(_ flag: Int) -> Bool { flags & FSEventStreamEventFlags(flag) != 0 }
        guard has(kFSEventStreamEventFlagItemIsFile),
              !path.contains("/.git/"), !path.contains("/node_modules/")
        else { return nil }
        let exists = FileManager.default.fileExists(atPath: path)
        if !exists { return 3 }
        if has(kFSEventStreamEventFlagItemCreated) || has(kFSEventStreamEventFlagItemRenamed) { return 1 }
        return 2
    }
}
