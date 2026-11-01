import AppKit
import Observation
import Sparkle
import SwiftUI

/// The app keeping itself current, through Sparkle. It reads the appcast on
/// the site (SUFeedURL in the Info.plist) and installs only what was signed
/// with the key whose public half is SUPublicEDKey, so a release that did not
/// come from scripts/release-mac.sh is refused even from the right address.
///
/// Sparkle asks once, on the second launch, whether to check by itself, and
/// then checks daily; Check for Updates… in the app menu checks now. Its own
/// windows offer the update and show the download. Settings › General ›
/// About shows where things stand, which is what this keeps for it.
@MainActor @Observable
final class AppUpdater: NSObject {
    static let shared = AppUpdater()

    /// Where an update stands.
    enum Status: Equatable {
        /// Not updating: under tests, and in Debug builds, which are an app
        /// of their own (dev.agentman.mac.debug) the release must not replace.
        case off
        /// Between checks.
        case idle
        case checking
        case upToDate
        /// A newer version was offered and not taken yet.
        case available(String)
        case downloading(String)
        /// Downloaded, and installing when the app quits.
        case ready(String)
        case failed(String)
    }

    var status: Status = .off
    /// When Sparkle last looked, kept by Sparkle across launches.
    var lastChecked: Date?
    /// False while Sparkle is busy in the background, or not running.
    var canCheck = false
    private var automatic = false

    private var controller: SPUStandardUpdaterController?
    private var observations: [NSKeyValueObservation] = []

    /// Whether Sparkle checks by itself: the user's answer to its prompt,
    /// and the switch in Settings after it.
    var checksAutomatically: Bool {
        get { automatic }
        set {
            controller?.updater.automaticallyChecksForUpdates = newValue
            automatic = newValue
        }
    }

    /// Starts Sparkle once the app is up. Debug builds and tests never do.
    func start() {
        #if !DEBUG
        guard controller == nil, !AgentmanApp.underTests else { return }
        let controller = SPUStandardUpdaterController(startingUpdater: true, updaterDelegate: self, userDriverDelegate: nil)
        self.controller = controller
        let updater = controller.updater
        status = .idle
        lastChecked = updater.lastUpdateCheckDate
        // Both change under Sparkle: the first while it downloads in the
        // background, the second when the user answers its prompt.
        observations = [
            updater.observe(\.canCheckForUpdates, options: [.initial, .new]) { [weak self] updater, _ in
                MainActor.assumeIsolated { self?.canCheck = updater.canCheckForUpdates }
            },
            updater.observe(\.automaticallyChecksForUpdates, options: [.initial, .new]) { [weak self] updater, _ in
                MainActor.assumeIsolated { self?.automatic = updater.automaticallyChecksForUpdates }
            },
        ]
        #endif
    }

    /// Checks now, showing Sparkle's window: what Check for Updates… does.
    /// With an update already found or downloaded, it offers that one.
    func check() {
        controller?.checkForUpdates(nil)
    }

    /// What the About row says under the app's version.
    static func detail(_ status: Status, lastChecked: Date?, automatic: Bool, now: Date = Date()) -> String {
        switch status {
        case .off:
            return "Updates are off in development builds."
        case .idle:
            if let lastChecked {
                let ago = RelativeDateTimeFormatter().localizedString(for: lastChecked, relativeTo: now)
                return "Last checked for updates \(ago)."
            }
            return automatic ? "Checks for updates every day." : "Doesn't check for updates by itself."
        case .checking:
            return "Checking for updates…"
        case .upToDate:
            return "Up to date."
        case .available(let version):
            return "Agentman \(version) is available."
        case .downloading(let version):
            return "Downloading Agentman \(version)…"
        case .ready(let version):
            return "Agentman \(version) installs when you quit."
        case .failed(let problem):
            return problem
        }
    }

    /// The About row's button: it always calls check(), which Sparkle turns
    /// into whichever step comes next.
    static func action(_ status: Status) -> String {
        switch status {
        case .available: "Update…"
        case .ready: "Restart to Update"
        default: "Check Now"
        }
    }

    /// Whether an error ending a check is worth showing: not "nothing new",
    /// and not the user cancelling or putting off the install.
    static func shows(_ error: NSError) -> Bool {
        guard error.domain == SUSparkleErrorDomain else { return true }
        let quiet: [SUError] = [.noUpdateError, .installationCanceledError, .installationAuthorizeLaterError]
        return !quiet.map { Int($0.rawValue) }.contains(error.code)
    }
}

extension AppUpdater: SPUUpdaterDelegate {
    func updater(_ updater: SPUUpdater, mayPerform updateCheck: SPUUpdateCheck) throws {
        status = .checking
    }

    func updater(_ updater: SPUUpdater, didFindValidUpdate item: SUAppcastItem) {
        status = .available(item.displayVersionString)
    }

    func updaterDidNotFindUpdate(_ updater: SPUUpdater, error: any Error) {
        status = .upToDate
    }

    func updater(_ updater: SPUUpdater, userDidMake choice: SPUUserUpdateChoice, forUpdate updateItem: SUAppcastItem,
                 state: SPUUserUpdateState) {
        let version = updateItem.displayVersionString
        switch choice {
        case .skip:
            status = .idle
        case .dismiss:
            status = state.stage == .installing ? .ready(version) : .available(version)
        default:
            break
        }
    }

    func updater(_ updater: SPUUpdater, willDownloadUpdate item: SUAppcastItem, with request: NSMutableURLRequest) {
        status = .downloading(item.displayVersionString)
    }

    func userDidCancelDownload(_ updater: SPUUpdater) {
        status = .idle
    }

    func updater(_ updater: SPUUpdater, failedToDownloadUpdate item: SUAppcastItem, error: any Error) {
        status = .failed(error.localizedDescription)
    }

    func updater(_ updater: SPUUpdater, willInstallUpdateOnQuit item: SUAppcastItem,
                 immediateInstallationBlock immediateInstallHandler: @escaping () -> Void) -> Bool {
        status = .ready(item.displayVersionString)
        return false
    }

    func updater(_ updater: SPUUpdater, didAbortWithError error: any Error) {
        if Self.shows(error as NSError) { status = .failed(error.localizedDescription) }
    }

    func updater(_ updater: SPUUpdater, didFinishUpdateCycleFor updateCheck: SPUUpdateCheck, error: (any Error)?) {
        lastChecked = updater.lastUpdateCheckDate
        if status == .checking { status = .idle }
    }
}

/// Check for Updates… in the app menu, greyed out while Sparkle can't take
/// a check.
struct CheckForUpdatesCommand: View {
    private var updater: AppUpdater { .shared }

    var body: some View {
        Button("Check for Updates…") { updater.check() }
            .disabled(!updater.canCheck)
    }
}
