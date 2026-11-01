import Foundation
import Observation

/// Keeps am serve right for this app, with the am the app carries
/// (Contents/Helpers/am, built by scripts/build-am.sh).
///
/// When the app starts or connects, it looks: is there a copy of am the app
/// manages, is that copy as new as the one the app carries, does a daemon
/// answer, and is it new enough for this app? If not, it sets up. The first
/// time, a welcome screen says what that does and waits for the user; after
/// that, as after the app updates itself, it does it quietly, with a note in
/// the window's footer.
///
/// Setting up is all am setup, which is idempotent and asks nothing: it
/// copies the app's am to ~/.agentman/bin/am, connects the agents to it, and
/// runs the login service on that copy, taking over from any other daemon.
///
/// It only ever runs am on a released app, or on a Debug build started with
/// AGENTMAN_SETUP=1, and never under tests (`mayRunHere`). Everything it runs
/// goes through `runner`, which tests replace with one that runs nothing.
@MainActor @Observable
final class DaemonSetup {
    /// The API level this app needs of the daemon (internal/protocol's
    /// APILevel). Raise it when the app comes to rely on something only a
    /// newer daemon does: a daemon below it is upgraded by itself.
    nonisolated static let requiredDaemonAPILevel = 1

    /// Where everything is.
    struct Paths: Sendable {
        /// The am this app carries, when it carries one.
        var bundled: String?
        /// The home ~/.agentman is in.
        var home: String

        /// The copy the app manages, which the login service runs.
        var managed: String { (home as NSString).appendingPathComponent(".agentman/bin/am") }
        /// The file beside it that marks it as the app's (internal/update).
        var marker: String { (home as NSString).appendingPathComponent(".agentman/bin/am.managed.json") }
        var socket: String { (home as NSString).appendingPathComponent(".agentman/am.sock") }
        var log: String { (home as NSString).appendingPathComponent(".agentman/serve.log") }

        /// This app's.
        static var app: Paths {
            let bundled = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/am").path
            return Paths(bundled: FileManager.default.isExecutableFile(atPath: bundled) ? bundled : nil,
                         home: NSHomeDirectory())
        }
    }

    /// Why the daemon isn't right for this app.
    enum Reason: Equatable, Sendable {
        /// ~/.agentman/bin/am isn't the app's copy, or isn't there.
        case noManagedCopy
        /// The app's copy is an older release than the am the app carries.
        case managedOlder(String)
        /// Nothing answers on the socket.
        case noDaemon
        /// The daemon answers at an API level below the app's.
        case daemonTooOld(Int)
    }

    enum Phase: Equatable {
        /// Not run since launch, or since the screen opened.
        case ready
        case running
        /// Done, with am setup's summary.
        case finished(SetupLine)
        case failed(Failure)
    }

    /// Why setting up didn't work.
    struct Failure: Equatable {
        /// What went wrong, in am's words where it said.
        var message: String
        /// The step that failed, if one did: its own line says why.
        var step: String?
        /// The daemon's log, which says more.
        var log: String
    }

    /// A line for the window's footer, about a run nobody watched.
    enum Note: Equatable {
        case updating
        case updated(String)
        case failed(String)
    }

    private(set) var phase: Phase = .ready
    /// The steps am setup has said, in the order it said them.
    private(set) var steps: [SetupLine] = []
    /// The welcome and setup screen, over the main window.
    var showsScreen = false
    /// Why the last look wanted to set up; empty when all was well.
    private(set) var reasons: [Reason] = []
    /// What the am the app carries, and the copy it manages, say they are.
    private(set) var bundled: AmVersion?
    private(set) var managed: AmVersion?
    private(set) var note: Note?

    let runner: any AmRunner
    let paths: Paths
    /// Whether this app may run am setup here at all, from the screen or
    /// from Settings.
    let allowed: Bool
    /// Whether it looks, and sets up, by itself.
    let automatic: Bool
    private let defaults: UserDefaults
    private let appVersion: String

    /// The last the connection said, and whether a daemon's details are
    /// awaited since it connected.
    @ObservationIgnored private var connection: DaemonClient.Status = .connecting
    @ObservationIgnored private var awaitingDaemon = false
    @ObservationIgnored private var absence: Task<Void, Never>?
    @ObservationIgnored private var lookedForAbsence = false
    /// It sets up by itself at most once a launch, so a setup that can't
    /// fix something doesn't run again at every reconnect.
    @ObservationIgnored private var ranByItself = false
    @ObservationIgnored private var noteClear: Task<Void, Never>?

    /// Set once setting up has worked: from then on it sets up quietly.
    static let welcomedKey = "daemonSetUp"
    /// How long a missing daemon is given at launch before the app sets up.
    var absenceGrace: Duration = .seconds(3)

    init(runner: any AmRunner = ProcessAmRunner(), paths: Paths = .app, allowed: Bool = DaemonSetup.mayRunHere,
         automatic: Bool? = nil, defaults: UserDefaults = .standard,
         appVersion: String = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "") {
        self.runner = runner
        self.paths = paths
        self.allowed = allowed && paths.bundled != nil
        // By itself only on the user's own daemon: an app pointed at a test
        // daemon's socket leaves ~/.agentman alone.
        self.automatic = automatic ?? (self.allowed && DaemonClient.defaultSocketPath == paths.socket)
        self.defaults = defaults
        self.appVersion = appVersion
    }

    /// Whether this copy of the app may run am setup on this Mac: a released
    /// one, or a Debug build started with AGENTMAN_SETUP=1. Never under
    /// tests, which run inside the app.
    nonisolated static var mayRunHere: Bool {
        guard !AgentmanApp.underTests else { return false }
        #if DEBUG
        return ProcessInfo.processInfo.environment["AGENTMAN_SETUP"] == "1"
        #else
        return true
        #endif
    }

    /// Whether setting up has worked on this Mac before.
    var welcomed: Bool {
        get { defaults.bool(forKey: Self.welcomedKey) }
        set { defaults.set(newValue, forKey: Self.welcomedKey) }
    }

    var running: Bool { phase == .running }

    // MARK: - Looking

    /// The connection to am serve changed.
    func connectionChanged(_ status: DaemonClient.Status) {
        connection = status
        guard automatic else { return }
        switch status {
        case .connected:
            absence?.cancel()
            absence = nil
            // The session list that follows says what the daemon is.
            awaitingDaemon = true
        case .connecting:
            break
        case .unavailable:
            awaitingDaemon = false
            // Missing at launch is given a moment: the connection is tried
            // again, and a daemon that is starting comes up. Going away later
            // is a restart, which the login service sees to.
            guard !lookedForAbsence, absence == nil else { return }
            absence = Task { [weak self] in
                guard let self else { return }
                try? await Task.sleep(for: absenceGrace)
                guard !Task.isCancelled else { return }
                absence = nil
                guard case .unavailable = connection else { return }
                lookedForAbsence = true
                await look(daemon: nil)
            }
        }
    }

    /// The daemon said what it is, on a session list.
    func daemonReported(_ info: DaemonInfo) {
        guard automatic, awaitingDaemon else { return }
        awaitingDaemon = false
        lookedForAbsence = true
        Task { await look(daemon: info) }
    }

    /// Whether the daemon is right for this app, and setting it up if not:
    /// with the welcome screen until setting up has worked once, quietly
    /// after that.
    func look(daemon: DaemonInfo?) async {
        guard !running else { return }
        await readVersions()
        reasons = Self.reasons(bundled: bundled, managed: managed, daemon: daemon)
        guard automatic, !reasons.isEmpty, !ranByItself, !showsScreen else { return }
        ranByItself = true
        if welcomed {
            await run()
        } else {
            phase = .ready
            steps = []
            showsScreen = true
        }
    }

    /// What is wrong, if anything, from what the app carries, the copy it
    /// manages, and the daemon that answers (nil when none does).
    nonisolated static func reasons(bundled: AmVersion?, managed: AmVersion?, daemon: DaemonInfo?,
                                    required: Int = requiredDaemonAPILevel) -> [Reason] {
        // Without an am of its own the app has nothing to set up with.
        guard let bundled else { return [] }
        var reasons: [Reason] = []
        if let managed {
            if Release.before(managed.version, bundled.version) { reasons.append(.managedOlder(managed.version)) }
        } else {
            reasons.append(.noManagedCopy)
        }
        if let daemon {
            let level = daemon.apiLevel ?? 0
            if level < required { reasons.append(.daemonTooOld(level)) }
        } else {
            reasons.append(.noDaemon)
        }
        return reasons
    }

    /// Reads what the am the app carries says it is, once, and what the copy
    /// it manages says, each time: it may have been upgraded since.
    func readVersions() async {
        if bundled == nil, let am = paths.bundled {
            bundled = try? await runner.version(of: am)
        }
        let files = FileManager.default
        if files.fileExists(atPath: paths.marker), files.isExecutableFile(atPath: paths.managed) {
            managed = try? await runner.version(of: paths.managed)
        } else {
            managed = nil
        }
    }

    // MARK: - Setting up

    /// The welcome screen's button, and Set Up from elsewhere: am setup,
    /// shown step by step on the screen when it is open, and in the footer's
    /// note when it isn't.
    func run() async {
        guard allowed, let am = paths.bundled, !running else { return }
        let quiet = !showsScreen
        phase = .running
        steps = []
        if quiet { show(.updating) }
        let (lines, sink) = AsyncStream.makeStream(of: String.self)
        let runner = runner
        let arguments = ["setup", "-json", "-from", am, "-app-version", appVersion]
        let ran = Task.detached {
            let exit = await runner.run(am, arguments) { sink.yield($0) }
            sink.finish()
            return exit
        }
        var summary: SetupLine?
        let decoder = JSONDecoder()
        for await text in lines {
            guard let line = try? decoder.decode(SetupLine.self, from: Data(text.utf8)) else { continue }
            if line.isSummary {
                summary = line
            } else {
                steps.append(line)
            }
        }
        let exit = await ran.value
        phase = Self.outcome(summary: summary, steps: steps, exit: exit, log: paths.log)
        switch phase {
        case .finished(let summary):
            welcomed = true
            reasons = []
            if quiet { show(.updated(summary.version ?? bundled?.version ?? "")) }
        case .failed(let failure):
            if quiet { show(.failed(failure.message)) }
        default:
            break
        }
        await readVersions()
    }

    /// The welcome screen's "Not now": the app leaves the daemon as it is
    /// until it is next launched, or set up from Settings.
    func notNow() {
        showsScreen = false
        if phase != .running { phase = .ready }
    }

    /// What a run of am setup came to, from its summary, the steps it said
    /// and how it ended. Set up, but with a daemon still too old (am setup
    /// keeps a development build in the app's place), is no success.
    nonisolated static func outcome(summary: SetupLine?, steps: [SetupLine], exit: ProgramExit, log: String,
                                    required: Int = requiredDaemonAPILevel) -> Phase {
        let log = summary?.log ?? log
        if let summary, summary.ok == true {
            guard (summary.apiLevel ?? 0) >= required else {
                var message = "am serve \(summary.version ?? "") still answers as too old for this app."
                if let kept = steps.first(where: { $0.step == "binary" })?.detail, kept.contains("kept") {
                    message += " " + kept.prefix(1).uppercased() + kept.dropFirst() + "."
                }
                return .failed(Failure(message: message, step: nil, log: log))
            }
            return .finished(summary)
        }
        if let failed = steps.first(where: \.didFail) {
            return .failed(Failure(message: failed.detail ?? "It didn't say why.", step: failed.step, log: log))
        }
        let said = plain(exit.error)
        return .failed(Failure(message: said.isEmpty ? "am setup stopped without saying why." : said, step: nil, log: log))
    }

    /// am's error as a sentence: without the "am: setup: " it starts with.
    nonisolated static func plain(_ error: String) -> String {
        var text = error.trimmingCharacters(in: .whitespacesAndNewlines)
        for prefix in ["am: ", "setup: ", "upgrade: "] where text.hasPrefix(prefix) {
            text.removeFirst(prefix.count)
        }
        guard let first = text.first else { return text }
        return first.uppercased() + text.dropFirst()
    }

    private func show(_ note: Note) {
        noteClear?.cancel()
        self.note = note
        // News that it worked goes after a while; a failure stays until the
        // next run, as Settings says what to do about it.
        guard case .updated = note else { return }
        noteClear = Task { [weak self] in
            try? await Task.sleep(for: .seconds(10))
            guard !Task.isCancelled else { return }
            self?.note = nil
        }
    }

    func dismissNote() {
        noteClear?.cancel()
        note = nil
    }
}

/// Release versions as am compares them (internal/update): three numbers, a
/// "v" before them or not. Anything else, a Debug build's "0.38.0-dev" among
/// them, isn't a release, and is never older or newer than one.
enum Release {
    static func parse(_ text: String) -> [Int]? {
        var text = text.trimmingCharacters(in: .whitespaces)
        if text.hasPrefix("v") { text.removeFirst() }
        let parts = text.split(separator: ".", omittingEmptySubsequences: false)
        guard parts.count == 3 else { return nil }
        let numbers = parts.compactMap { Int($0) }
        return numbers.count == 3 && numbers.allSatisfy { $0 >= 0 } ? numbers : nil
    }

    /// Whether release a is older than release b; false when either isn't
    /// a release.
    static func before(_ a: String, _ b: String) -> Bool {
        guard let x = parse(a), let y = parse(b) else { return false }
        return x.lexicographicallyPrecedes(y)
    }
}
