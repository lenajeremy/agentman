import AppKit
import UniformTypeIdentifiers

/// Agentman as what opens a kind of file when it is double-clicked, as
/// Finder's Get Info ▸ Open with ▸ Change All sets it. What opened it
/// before is kept, and given back when this is turned off.
@MainActor
enum FileHandlers {
    enum Kind: String, CaseIterable, Identifiable {
        case markdown, text

        var id: String { rawValue }

        var title: String {
            switch self {
            case .markdown: "Open Markdown files"
            case .text: "Open plain text files"
            }
        }

        var detail: String {
            switch self {
            case .markdown: "Double-clicking a .md file opens it in the editor, in its project."
            case .text: "The same for .txt files."
            }
        }

        var type: UTType {
            switch self {
            case .markdown: UTType("net.daringfireball.markdown") ?? UTType(filenameExtension: "md") ?? .plainText
            case .text: .plainText
            }
        }
    }

    /// Whether Agentman opens this kind of file now, however that was set.
    static func opens(_ kind: Kind) -> Bool { opens(kind.type) }

    static func opens(_ type: UTType, app: Bundle = .main) -> Bool {
        guard let handler = NSWorkspace.shared.urlForApplication(toOpen: type) else { return false }
        return Bundle(url: handler)?.bundleIdentifier == app.bundleIdentifier
    }

    /// Makes Agentman what opens this kind of file, or gives it back to what
    /// opened it before (TextEdit when nothing had). macOS asks the user to
    /// confirm; a no throws, as cancelled says. Throws what macOS said when
    /// it refused.
    static func set(_ kind: Kind, on: Bool) async throws { try await set(kind.type, on: on) }

    static func set(_ type: UTType, on: Bool, app: Bundle = .main, defaults: UserDefaults = .standard) async throws {
        let workspace = NSWorkspace.shared
        let key = "fileHandlerBefore." + type.identifier
        if on {
            if let handler = workspace.urlForApplication(toOpen: type),
               Bundle(url: handler)?.bundleIdentifier != app.bundleIdentifier {
                defaults.set(handler.path, forKey: key)
            }
            try await workspace.setDefaultApplication(at: app.bundleURL, toOpen: type)
        } else {
            let before = defaults.string(forKey: key).map { URL(fileURLWithPath: $0) }
                .flatMap { FileManager.default.fileExists(atPath: $0.path) ? $0 : nil }
                ?? URL(fileURLWithPath: "/System/Applications/TextEdit.app")
            try await workspace.setDefaultApplication(at: before, toOpen: type)
            defaults.removeObject(forKey: key)
        }
    }

    /// The user said no when macOS asked to confirm: userCanceledErr, as
    /// itself or under the error NSWorkspace wraps it in.
    nonisolated static func cancelled(_ error: Error) -> Bool {
        let error = error as NSError
        if error.domain == NSCocoaErrorDomain, error.code == NSUserCancelledError { return true }
        if error.domain == NSOSStatusErrorDomain, error.code == Int(userCanceledErr) { return true }
        return (error.userInfo[NSUnderlyingErrorKey] as? NSError).map(cancelled) ?? false
    }
}
