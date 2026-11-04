import Foundation
import Observation

/// What AppCatchUp needs of the app's updater: AppUpdater, or a stand-in
/// under tests, where Sparkle never runs.
@MainActor
protocol AppUpdating: AnyObject {
    /// Where an update stands; .off where Sparkle doesn't run.
    var status: AppUpdater.Status { get }
    /// Checks now in Sparkle's window, which offers what it finds.
    func check()
    /// Checks in the background, and installs what it finds at quit.
    func updateInBackground()
    /// Installs a downloaded update now, restarting the app.
    func installNow()
}

extension AppUpdater: AppUpdating {}

/// The app catching up with a newer am serve.
///
/// The app and am carry the same version and stay matched both ways.
/// Updating the app brings the daemon forward with it (DaemonSetup); this is
/// the other way round, for a daemon updated on its own, with am upgrade,
/// Homebrew or the install script, to a newer release than the app. Nothing
/// is downgraded: the newer daemon keeps working while the app catches up.
///
/// Each time the daemon says what it is (on the session list that follows
/// connecting, and whenever it sends one), its version is compared with the
/// app's own, and when the daemon is the newer release:
///
/// - If am upgrade left "update" for that version or a newer one in
///   ~/.agentman/app-update.json, the user said yes in the terminal: the app
///   updates without asking, in the background, installing at quit, and the
///   window's footer offers Restart to Update.
/// - If it left "skip" for that version, the footer quietly offers the
///   update, and nothing pops up.
/// - Otherwise a pop-up asks, once a version. Not Now leaves the footer's
///   note until a newer am serve comes; "Update automatically when am serve
///   is newer", there and in Settings, updates later ones without asking.
///
/// Updating is Sparkle's, through the updater: never a downgrade, and if
/// the newest app is newer still, DaemonSetup then brings the daemon up to
/// it. Where Sparkle doesn't run (Debug builds, tests) nothing here shows;
/// Settings' About row says why.
@MainActor @Observable
final class AppCatchUp {
    /// What to do about the daemon's version.
    enum Decision: Equatable {
        /// It isn't a newer release than the app, or the app can't update.
        case nothing
        /// Update in the background, without asking.
        case update
        /// Offer the update quietly, in the footer.
        case note
        /// Ask, with the pop-up.
        case ask
    }

    /// app-update.json, as am upgrade writes it (internal/update/macapp.go).
    struct Intent: Codable, Equatable {
        /// The version am went to.
        var version: String
        /// "update" or "skip".
        var choice: String
        /// When, in milliseconds since 1970.
        var at: Int64?

        static let update = "update"
        static let skip = "skip"
    }

    /// A line for the window's footer.
    enum Note: Equatable {
        /// An update to the daemon's version waits for the user.
        case waiting(String)
        /// An update is downloaded, and installs at quit or on a restart.
        case restart(String)
    }

    /// The pop-up, for this version of am serve; nil when it isn't shown.
    private(set) var asking: String?
    /// The version the footer offers an update to, when it waits for the
    /// user.
    private(set) var waiting: String?
    /// The version an update without asking was started for, this launch.
    private(set) var started: String?
    /// The version the pop-up was shown for this launch: it asks once.
    @ObservationIgnored private var asked: String?

    let updater: any AppUpdating
    /// The home ~/.agentman is in.
    let home: String
    let appVersion: String
    private let defaults: UserDefaults

    /// "Update automatically when am serve is newer".
    static let automaticKey = "updateWithDaemon"
    /// The daemon version Not Now was said to: no pop-up again until a
    /// newer one.
    static let declinedKey = "updateWithDaemonDeclined"

    init(updater: any AppUpdating = AppUpdater.shared, home: String = DaemonSetup.Paths.app.home,
         defaults: UserDefaults = .standard,
         appVersion: String = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "") {
        self.updater = updater
        self.home = home
        self.defaults = defaults
        self.appVersion = appVersion
    }

    /// Whether a newer am serve updates the app without asking.
    var automatic: Bool {
        get { defaults.bool(forKey: Self.automaticKey) }
        set { defaults.set(newValue, forKey: Self.automaticKey) }
    }

    /// The daemon version the user last said Not Now to.
    var declined: String? { defaults.string(forKey: Self.declinedKey) }

    var intentPath: String { (home as NSString).appendingPathComponent(".agentman/app-update.json") }

    /// What am upgrade left for the app, if anything readable.
    func readIntent() -> Intent? {
        guard let data = FileManager.default.contents(atPath: intentPath) else { return nil }
        return try? JSONDecoder().decode(Intent.self, from: data)
    }

    // MARK: - Deciding

    /// The daemon said what it is.
    func daemonReported(_ info: DaemonInfo) {
        let version = info.version
        let decision = Self.decide(app: appVersion, daemon: version, intent: readIntent(), automatic: automatic,
                                   declined: declined, updatesOn: updater.status != .off)
        switch decision {
        case .nothing:
            asking = nil
            waiting = nil
        case .note:
            asking = nil
            waiting = version
        case .update:
            asking = nil
            waiting = nil
            guard started != version else { return }
            started = version
            updater.updateInBackground()
        case .ask:
            // Asked once this launch already: what the user said stands,
            // and the footer keeps a way to update.
            if asked == version {
                if asking == nil { waiting = version }
                return
            }
            asked = version
            waiting = nil
            asking = version
        }
    }

    /// What to do when am serve says it is daemon and the app is app. The
    /// terminal's choice comes first, for that version or a newer one; then
    /// Not Now, for that version or a newer one; then the automatic switch.
    nonisolated static func decide(app: String, daemon: String, intent: Intent?, automatic: Bool, declined: String?,
                                   updatesOn: Bool) -> Decision {
        guard updatesOn, Release.before(app, daemon) else { return .nothing }
        if let intent, Release.parse(intent.version) != nil, !Release.before(intent.version, daemon) {
            switch intent.choice {
            case Intent.update: return .update
            case Intent.skip: return .note
            default: break
            }
        }
        if let declined, Release.parse(declined) != nil, !Release.before(declined, daemon) {
            return .note
        }
        return automatic ? .update : .ask
    }

    // MARK: - Answering

    /// The pop-up's Update Agentman: Sparkle's window checks and installs.
    /// The footer keeps a way back to it if that window is closed.
    func accept(automatically: Bool) {
        guard let version = asking else { return }
        if automatically { automatic = true }
        asking = nil
        waiting = version
        updater.check()
    }

    /// The pop-up's Not Now: this version isn't asked about again, and the
    /// footer quietly offers the update.
    func notNow(automatically: Bool) {
        guard let version = asking else { return }
        if automatically { automatic = true }
        defaults.set(version, forKey: Self.declinedKey)
        asking = nil
        waiting = version
    }

    /// What the footer says, if anything: an update downloaded for am
    /// serve's sake, ready for a restart, or one waiting for the user.
    var note: Note? {
        guard started != nil || waiting != nil else { return nil }
        if case .ready(let version) = updater.status { return .restart(version) }
        return waiting.map(Note.waiting)
    }

    /// The footer's note, clicked: Sparkle's window for an update that
    /// waits, a restart into one that is downloaded.
    func act(on note: Note) {
        switch note {
        case .waiting: updater.check()
        case .restart: updater.installNow()
        }
    }
}
