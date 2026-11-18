import AppKit
import SwiftUI

/// Links in what agents write. Agents link to files on the Mac as often as to
/// the web — `[CheckoutSheet.tsx](src/checkout/CheckoutSheet.tsx:42)`,
/// `[the log](/Users/me/project/build.log)` — and those have no scheme, which
/// macOS refused with "The application can't be opened. -50". A file opens
/// in the editor when the link is followed in the editor's agent panel, and
/// is shown in Finder anywhere else; a folder opens in Finder; anything with
/// a scheme of its own (the web, mail) opens as it would anywhere.
enum ReplyLinks {
    /// A file or folder a link names, and the line it points at.
    struct Target: Equatable {
        var url: URL
        var line: Int?
        var isDirectory: Bool
    }

    /// Where a link goes when it names something on this Mac: nil for a link
    /// with a scheme other than file, or one naming nothing that exists.
    /// A relative path is read from the agent's folder; "path:42",
    /// "path:42:7" and "path#L42" point at a line.
    static func target(of url: URL, folder: String?) -> Target? {
        let scheme = url.scheme.flatMap { isPath(url, scheme: $0) ? nil : $0 }
        if let scheme, scheme != "file" { return nil }
        var path: String
        if scheme == "file" {
            path = url.path(percentEncoded: false)
        } else if url.scheme != nil || url.path(percentEncoded: false).isEmpty {
            // "app.ts:42", which URL reads as a scheme and a resource.
            path = url.absoluteString
            if let fragment = url.fragment { path = String(path.dropLast(fragment.count + 1)) }
        } else {
            path = url.path(percentEncoded: false)
        }
        path = path.removingPercentEncoding ?? path
        guard !path.isEmpty else { return nil }
        var line: Int?
        if let fragment = url.fragment, let number = lineNumber(fragment) {
            line = number
        }
        if path.hasPrefix("~") {
            path = (path as NSString).expandingTildeInPath
        }
        for candidate in candidates(path) {
            for full in resolved(candidate.path, folder: folder) {
                var isDirectory: ObjCBool = false
                if FileManager.default.fileExists(atPath: full, isDirectory: &isDirectory) {
                    return Target(url: URL(fileURLWithPath: full).standardizedFileURL,
                                  line: candidate.line ?? line, isDirectory: isDirectory.boolValue)
                }
            }
        }
        return nil
    }

    /// Whether what URL took for a scheme is the start of a file's name: it
    /// has a dot ("app.ts:42"), or a line number follows it ("Makefile:12").
    static func isPath(_ url: URL, scheme: String) -> Bool {
        if scheme == "file" { return false }
        if scheme.contains(".") { return true }
        let rest = url.absoluteString.dropFirst(scheme.count + 1).prefix { $0 != "#" }
        return !rest.isEmpty && rest.allSatisfy { $0.isNumber || $0 == ":" }
    }

    /// The path as written, then without a ":line" or ":line:column" ending.
    private static func candidates(_ path: String) -> [(path: String, line: Int?)] {
        var out: [(String, Int?)] = [(path, nil)]
        let parts = path.split(separator: ":", omittingEmptySubsequences: false)
        if parts.count >= 2, let last = Int(parts[parts.count - 1]) {
            if parts.count >= 3, let line = Int(parts[parts.count - 2]) {
                out.append((parts.dropLast(2).joined(separator: ":"), line))
            }
            out.append((parts.dropLast().joined(separator: ":"), last))
        }
        return out
    }

    /// An absolute path as it is, then (a link agents write as
    /// "/src/app.ts", as on GitHub) from the folder; a relative one from the
    /// folder.
    private static func resolved(_ path: String, folder: String?) -> [String] {
        let inFolder = folder.map { URL(fileURLWithPath: $0).appendingPathComponent(path).path }
        if path.hasPrefix("/") {
            return [path] + (inFolder.map { [$0] } ?? [])
        }
        return inFolder.map { [$0] } ?? []
    }

    /// "L42" or "L42-L50" from a link's fragment.
    private static func lineNumber(_ fragment: String) -> Int? {
        guard fragment.hasPrefix("L") else { return nil }
        let digits = fragment.dropFirst().prefix { $0.isNumber }
        return Int(digits)
    }

    /// Shows a file selected in a Finder window, or opens a folder there.
    static func showInFinder(_ target: Target) {
        guard !AgentmanApp.underTests else { return }
        if target.isDirectory {
            NSWorkspace.shared.open(target.url)
        } else {
            NSWorkspace.shared.activateFileViewerSelecting([target.url])
        }
    }

    /// Opening links in a reply outside the editor: a file or folder in
    /// Finder.
    static func finder(folder: String?) -> OpenURLAction {
        OpenURLAction { url in
            switch decide(url, folder: folder) {
            case .file(let target):
                showInFinder(target)
                return .handled
            case .missing:
                return missing()
            case .elsewhere:
                return .systemAction
            }
        }
    }

    /// What following a link does.
    enum Decision: Equatable {
        /// A file or folder on this Mac.
        case file(Target)
        /// A path to nothing that exists.
        case missing
        /// The web, mail: opened as anywhere else.
        case elsewhere
    }

    static func decide(_ url: URL, folder: String?) -> Decision {
        if let target = target(of: url, folder: folder) { return .file(target) }
        return url.scheme.map { isPath(url, scheme: $0) } ?? true ? .missing : .elsewhere
    }

    /// A link to nothing on this Mac: a beep, rather than macOS's -50.
    static func missing() -> OpenURLAction.Result {
        if !AgentmanApp.underTests { NSSound.beep() }
        return .handled
    }
}

/// Follows a thread's file links to Finder, read from the agent's folder;
/// not in the editor's panel, which opens them itself.
struct FinderLinks: ViewModifier {
    let folder: String?
    let applies: Bool

    func body(content: Content) -> some View {
        if applies {
            content.environment(\.openURL, ReplyLinks.finder(folder: folder))
        } else {
            content
        }
    }
}
