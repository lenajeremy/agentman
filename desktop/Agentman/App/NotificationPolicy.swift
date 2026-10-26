import Foundation

/// When the Mac speaks up, as set in Settings > Notifications: which kinds,
/// who from, not from muted folders, and only what is urgent in quiet hours.
enum NotificationPolicy {
    enum Kind {
        /// An agent finished a turn.
        case finished
        /// An agent, or Agentman's card for it, waits on the user.
        case needsYou
        /// Agentman's own replies, briefs and what agents tell it.
        case agentman
        /// Agentman suggests a note.
        case note
    }

    enum Key {
        static let finished = "notifyFinished"
        static let needsYou = "notifyNeedsYou"
        static let agentman = "notifyAgentman"
        static let notes = "notifyNotes"
        static let sound = "notifySound"
        static let mutedFolders = "mutedFolders"
        static let quiet = "quietHours"
        static let quietStart = "quietStart"
        static let quietEnd = "quietEnd"
        static let quietLetsNeedsYou = "quietLetsNeedsYou"
        /// Each agent's own notifications while Agentman is on. Off unless
        /// switched on.
        static let everyAgent = "notifyEveryAgent"
    }

    /// Minutes after midnight quiet hours start and end, by default.
    static let defaultQuietStart = 22 * 60
    static let defaultQuietEnd = 8 * 60

    /// Whether to tell the user about something of kind, from an agent
    /// working in folder.
    static func allows(_ kind: Kind, folder: String? = nil, at now: Date = Date(),
                       calendar: Calendar = .current, defaults: UserDefaults = .standard) -> Bool {
        let key: String
        switch kind {
        case .finished: key = Key.finished
        case .needsYou: key = Key.needsYou
        case .agentman: key = Key.agentman
        case .note: key = Key.notes
        }
        guard flag(key, defaults) else { return false }
        if let folder, isMuted(folder, defaults: defaults) { return false }
        if inQuietHours(at: now, calendar: calendar, defaults: defaults) {
            return kind == .needsYou && flag(Key.quietLetsNeedsYou, defaults)
        }
        return true
    }

    /// Whether an agent notifies for itself. While Agentman is on, it hears
    /// every agent and tells the user what matters, so it is the one voice:
    /// an agent speaks only when the user asked to hear from it, or from
    /// every agent. Off, every agent speaks for itself.
    static func agentSpeaks(agentmanOn: Bool, asked: Bool, defaults: UserDefaults = .standard) -> Bool {
        !agentmanOn || asked || defaults.bool(forKey: Key.everyAgent)
    }

    static func sound(defaults: UserDefaults = .standard) -> Bool { flag(Key.sound, defaults) }

    static func mutedFolders(_ defaults: UserDefaults = .standard) -> [String] {
        defaults.stringArray(forKey: Key.mutedFolders) ?? []
    }

    /// A folder is muted when it, or a folder it is in, is on the list.
    static func isMuted(_ folder: String, defaults: UserDefaults = .standard) -> Bool {
        let folder = (folder as NSString).standardizingPath
        return mutedFolders(defaults).contains { muted in
            let muted = (muted as NSString).standardizingPath
            return folder == muted || folder.hasPrefix(muted + "/")
        }
    }

    static func setMuted(_ folder: String, _ muted: Bool, defaults: UserDefaults = .standard) {
        var folders = mutedFolders(defaults).filter { $0 != folder }
        if muted { folders.append(folder) }
        defaults.set(folders.sorted(), forKey: Key.mutedFolders)
    }

    /// Whether now falls in quiet hours, which may run past midnight.
    static func inQuietHours(at now: Date, calendar: Calendar = .current, defaults: UserDefaults = .standard) -> Bool {
        guard defaults.bool(forKey: Key.quiet) else { return false }
        let start = defaults.object(forKey: Key.quietStart) as? Int ?? defaultQuietStart
        let end = defaults.object(forKey: Key.quietEnd) as? Int ?? defaultQuietEnd
        let parts = calendar.dateComponents([.hour, .minute], from: now)
        let minute = (parts.hour ?? 0) * 60 + (parts.minute ?? 0)
        if start == end { return false }
        return start < end ? (minute >= start && minute < end) : (minute >= start || minute < end)
    }

    /// A setting that is on until the user switches it off.
    private static func flag(_ key: String, _ defaults: UserDefaults) -> Bool {
        defaults.object(forKey: key) as? Bool ?? true
    }
}
