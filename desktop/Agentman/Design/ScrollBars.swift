import Foundation

/// Scroll bars thin and shown only while scrolling, as with a trackpad, even
/// with a mouse plugged in: macOS's thick, always-on bars sit heavy beside
/// the sidebar and every list. Someone who chose "Always" in System Settings
/// keeps them.
///
/// The choice goes in the launch arguments' domain, not the registered
/// defaults: System Settings may have written "Automatic" for everyone, and
/// that would win over a registered default.
enum ScrollBars {
    static let key = "AppleShowScrollBars"

    /// The launch arguments with thin bars asked for, or nil to leave them:
    /// when the system's choice is "Always", or a launch argument already
    /// says.
    static func arguments(_ arguments: [String: Any], system: String?) -> [String: Any]? {
        guard system != "Always", arguments[key] == nil else { return nil }
        var arguments = arguments
        arguments[key] = "WhenScrolling"
        return arguments
    }

    /// Asks for thin bars; before any window, so every scroll view starts so.
    static func apply(_ defaults: UserDefaults = .standard) {
        let system = defaults.persistentDomain(forName: UserDefaults.globalDomain)?[key] as? String
        if let changed = arguments(defaults.volatileDomain(forName: UserDefaults.argumentDomain), system: system) {
            defaults.setVolatileDomain(changed, forName: UserDefaults.argumentDomain)
        }
    }
}
