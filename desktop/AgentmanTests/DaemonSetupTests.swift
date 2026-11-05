import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// Setting up am serve from the app: when it is needed, the welcome screen
/// the first time and quiet runs after, what am setup's lines come to, and
/// pictures of the screen, which run with TEST_RUNNER_AGENTMAN_SHOTS=<folder>.
///
/// Nothing here runs am setup, am upgrade or launchctl: am is a FakeAm that
/// answers with canned lines, and ~/.agentman is a folder of the test's own.
@MainActor
final class DaemonSetupTests: XCTestCase {
    private var home: URL!
    private var defaults: UserDefaults!
    private let suite = "dev.agentman.mac.tests.setup"

    override func setUp() async throws {
        continueAfterFailure = false
        home = FileManager.default.temporaryDirectory.appendingPathComponent("am-setup-\(UUID().uuidString.prefix(8))")
        try FileManager.default.createDirectory(at: home.appendingPathComponent(".agentman/bin"), withIntermediateDirectories: true)
        defaults = UserDefaults(suiteName: suite)
        defaults.removePersistentDomain(forName: suite)
    }

    override func tearDown() async throws {
        try? FileManager.default.removeItem(at: home)
        defaults.removePersistentDomain(forName: suite)
    }

    nonisolated static let bundled = "/Applications/Agentman.app/Contents/Helpers/am"

    /// What am setup says on a Mac where everything goes well.
    static let goodRun = [
        #"{"step":"config","status":"changed","detail":"~/.agentman/config.json, made with a new hook token","path":"/Users/me/.agentman/config.json"}"#,
        #"{"step":"binary","status":"changed","detail":"~/.agentman/bin/am, agentman 0.39.0, installed from /Applications/Agentman.app/Contents/Helpers/am","path":"/Users/me/.agentman/bin/am","version":"0.39.0"}"#,
        #"{"step":"hooks","status":"changed","detail":"hooks call ~/.agentman/bin/am; set up for claude, codex","agents":[{"agent":"claude","status":"changed"},{"agent":"codex","status":"changed"},{"agent":"opencode","status":"unchanged"}]}"#,
        #"{"step":"tools","status":"unchanged","detail":"am mcp from ~/.agentman/bin/am; already set up for every agent","agents":[{"agent":"claude","status":"unchanged"},{"agent":"codex","status":"unchanged"}]}"#,
        #"{"step":"service","status":"changed","detail":"runs ~/.agentman/bin/am at login; took over from /opt/homebrew/bin/am","path":"/Users/me/Library/LaunchAgents/dev.agentman.serve.plist"}"#,
        #"{"step":"daemon","status":"unchanged","detail":"am serve 0.39.0 answers on ~/.agentman/am.sock","version":"0.39.0","apiLevel":1}"#,
        #"{"step":"summary","ok":true,"binary":"/Users/me/.agentman/bin/am","managed":true,"version":"0.39.0","apiLevel":1,"install":"managed","socket":"/Users/me/.agentman/am.sock","log":"/Users/me/.agentman/serve.log"}"#,
    ]

    static let failedRun = Array(goodRun.prefix(4)) + [
        #"{"step":"service","status":"failed","detail":"launchctl bootstrap gui/501 ~/Library/LaunchAgents/dev.agentman.serve.plist: Bootstrap failed: 5: Input/output error"}"#,
        #"{"step":"summary","ok":false,"binary":"/Users/me/.agentman/bin/am","managed":true,"apiLevel":0,"socket":"/Users/me/.agentman/am.sock","log":"/Users/me/.agentman/serve.log","failed":["service"]}"#,
    ]

    /// A setup on the test's home, whose shell finds tmux unless told not to.
    private func setup(am: FakeAm, automatic: Bool = true, allowed: Bool = true, tmux: Bool = true) -> DaemonSetup {
        var paths = DaemonSetup.Paths(bundled: Self.bundled, home: home.path)
        paths.toolFolders = []
        let tools = home.appendingPathComponent("tools")
        try? FileManager.default.createDirectory(at: tools, withIntermediateDirectories: true)
        if tmux { _ = try? program("tmux", in: tools.path) }
        let setup = DaemonSetup(runner: am, shell: FakeShell(path: tmux ? [tools.path] : []), paths: paths,
                                allowed: allowed, automatic: automatic, defaults: defaults, appVersion: "0.39.0")
        setup.absenceGrace = .milliseconds(30)
        return setup
    }

    /// Puts the app's copy of am in the test's home, marked as the app's.
    private func manage(_ version: String, am: FakeAm) throws {
        let managed = home.appendingPathComponent(".agentman/bin/am")
        try Data("#!/bin/sh\n".utf8).write(to: managed)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: managed.path)
        try Data(#"{"managedBy":"Agentman for Mac"}"#.utf8).write(to: home.appendingPathComponent(".agentman/bin/am.managed.json"))
        am.versions[managed.path] = AmVersion(version: version, apiLevel: 1, install: "managed", managed: true)
    }

    private func until(_ condition: () -> Bool, file: StaticString = #filePath, line: UInt = #line) async {
        let deadline = Date().addingTimeInterval(3)
        while !condition(), Date() < deadline {
            try? await Task.sleep(for: .milliseconds(10))
        }
        XCTAssertTrue(condition(), "timed out", file: file, line: line)
    }

    // MARK: When

    func testWhatCallsForSettingUp() {
        let bundled = AmVersion(version: "0.39.0", apiLevel: 1)
        let fine = DaemonInfo(version: "0.39.0", apiLevel: 1, install: "managed")
        XCTAssertEqual(DaemonSetup.reasons(bundled: bundled, managed: AmVersion(version: "0.39.0"), daemon: fine), [])
        XCTAssertEqual(DaemonSetup.reasons(bundled: bundled, managed: nil, daemon: fine), [.noManagedCopy])
        XCTAssertEqual(DaemonSetup.reasons(bundled: bundled, managed: AmVersion(version: "0.38.2"), daemon: fine), [.managedOlder("0.38.2")])
        XCTAssertEqual(DaemonSetup.reasons(bundled: bundled, managed: AmVersion(version: "0.39.0"), daemon: nil), [.noDaemon])
        XCTAssertEqual(DaemonSetup.reasons(bundled: bundled, managed: AmVersion(version: "0.39.0"), daemon: DaemonInfo(version: "0.37.0")),
                       [.daemonTooOld(0)], "a daemon from before API levels is level 0")
        XCTAssertEqual(DaemonSetup.reasons(bundled: bundled, managed: nil, daemon: DaemonInfo(version: "0.37.0", install: "homebrew")),
                       [.noManagedCopy, .daemonTooOld(0)])
        // Newer than the app is fine, and so is a build of the app's copy
        // made by hand, which is no release to compare.
        XCTAssertEqual(DaemonSetup.reasons(bundled: bundled, managed: AmVersion(version: "0.41.0"),
                                           daemon: DaemonInfo(version: "0.41.0", apiLevel: 3)), [])
        XCTAssertEqual(DaemonSetup.reasons(bundled: bundled, managed: AmVersion(version: "0.38.0-12-gabc"), daemon: fine), [])
        // Without an am of its own the app has nothing to set up with.
        XCTAssertEqual(DaemonSetup.reasons(bundled: nil, managed: nil, daemon: nil), [])
        // A newer daemon the user installed themselves is kept while it
        // serves what the app needs; an equal one, or one too old, is not.
        XCTAssertEqual(DaemonSetup.reasons(bundled: bundled, managed: nil,
                                           daemon: DaemonInfo(version: "0.40.0", apiLevel: 1, install: "homebrew")), [])
        XCTAssertEqual(DaemonSetup.reasons(bundled: bundled, managed: nil,
                                           daemon: DaemonInfo(version: "0.39.0", apiLevel: 1, install: "homebrew")), [.noManagedCopy])
        XCTAssertEqual(DaemonSetup.reasons(bundled: bundled, managed: nil,
                                           daemon: DaemonInfo(version: "0.40.0", apiLevel: 0, install: "homebrew")),
                       [.noManagedCopy, .daemonTooOld(0)])
    }

    func testReleasesCompareAsAmComparesThem() {
        XCTAssertTrue(Release.before("0.38.0", "0.39.0"))
        XCTAssertTrue(Release.before("v0.9.9", "0.10.0"))
        XCTAssertFalse(Release.before("0.39.0", "0.39.0"))
        XCTAssertFalse(Release.before("0.40.0", "0.39.0"))
        XCTAssertFalse(Release.before("0.38.0-dev", "0.39.0"), "a development build is no release")
        XCTAssertFalse(Release.before("0.38.0", "dev"))
        XCTAssertNil(Release.parse("0.38"))
    }

    // MARK: First launch

    func testTheFirstLaunchWelcomesBeforeSettingUp() async throws {
        let am = FakeAm(setup: Self.goodRun)
        let setup = setup(am: am)
        setup.connectionChanged(.unavailable("am serve isn't running"))
        await until { setup.showsScreen }
        XCTAssertEqual(setup.reasons, [.noManagedCopy, .noDaemon])
        XCTAssertEqual(setup.phase, .ready)
        XCTAssertEqual(am.runs, [], "nothing runs until the user says to")

        await setup.run()
        XCTAssertEqual(am.runs, [[Self.bundled, "setup", "-json", "-from", Self.bundled, "-app-version", "0.39.0"]])
        XCTAssertEqual(setup.steps.map(\.step), SetupStepName.order)
        XCTAssertEqual(setup.steps[2].agents?.map(\.agent), ["claude", "codex", "opencode"])
        guard case .finished(let summary) = setup.phase else { return XCTFail("\(setup.phase)") }
        XCTAssertEqual(summary.version, "0.39.0")
        XCTAssertTrue(setup.welcomed)
        XCTAssertNil(setup.note, "the screen showed it all")
        XCTAssertTrue(setup.showsScreen, "it stays for the user to close")
    }

    func testNotNowLeavesTheDaemonAlone() async throws {
        let am = FakeAm(setup: Self.goodRun)
        let setup = setup(am: am)
        setup.connectionChanged(.connected)
        setup.daemonReported(DaemonInfo(version: "0.38.0", apiLevel: 1, install: "homebrew"))
        await until { setup.showsScreen }
        XCTAssertEqual(setup.reasons, [.noManagedCopy])
        setup.notNow()
        XCTAssertFalse(setup.showsScreen)
        // Reconnecting this launch doesn't ask again.
        setup.connectionChanged(.unavailable("am serve stopped"))
        setup.connectionChanged(.connected)
        setup.daemonReported(DaemonInfo(version: "0.38.0", apiLevel: 1, install: "homebrew"))
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertFalse(setup.showsScreen)
        XCTAssertEqual(am.runs, [])
        XCTAssertFalse(setup.welcomed)
    }

    // A daemon answering at once is looked at once the session list says
    // what it is, not at the connection: the details then are the last
    // daemon's.
    func testADaemonIsJudgedByItsOwnDetails() async throws {
        let am = FakeAm(setup: Self.goodRun)
        try manage("0.39.0", am: am)
        let setup = setup(am: am)
        setup.daemonReported(DaemonInfo(version: "0.30.0"))
        try await Task.sleep(for: .milliseconds(50))
        XCTAssertEqual(setup.reasons, [], "not looked at before it connected")
        setup.connectionChanged(.connected)
        setup.daemonReported(DaemonInfo(version: "0.39.0", apiLevel: 1, install: "managed"))
        try await Task.sleep(for: .milliseconds(50))
        XCTAssertEqual(setup.managed?.version, "0.39.0")
        XCTAssertEqual(setup.reasons, [])
        XCTAssertFalse(setup.showsScreen)
        XCTAssertEqual(am.runs, [])
    }

    // MARK: Afterwards

    func testAfterTheFirstTimeItSetsUpQuietlyOnceALaunch() async throws {
        let am = FakeAm(setup: Self.goodRun)
        try manage("0.38.0", am: am)
        defaults.set(true, forKey: DaemonSetup.welcomedKey)
        let setup = setup(am: am)
        setup.connectionChanged(.connected)
        setup.daemonReported(DaemonInfo(version: "0.38.0", apiLevel: 1, install: "managed"))
        await until { setup.note == .updated("0.39.0") }
        XCTAssertFalse(setup.showsScreen, "quietly")
        XCTAssertEqual(am.runs.count, 1)

        // The app's copy was replaced (as the fake's next answer says), the
        // daemon restarted on it: nothing more to do.
        try manage("0.39.0", am: am)
        setup.connectionChanged(.unavailable("am serve stopped"))
        setup.connectionChanged(.connected)
        setup.daemonReported(DaemonInfo(version: "0.39.0", apiLevel: 1, install: "managed"))
        await until { setup.managed?.version == "0.39.0" }
        XCTAssertEqual(setup.reasons, [])

        // Still too old after that is left to Settings, not run again.
        setup.connectionChanged(.unavailable("am serve stopped"))
        setup.connectionChanged(.connected)
        setup.daemonReported(DaemonInfo(version: "0.37.0"))
        await until { setup.reasons == [.daemonTooOld(0)] }
        XCTAssertEqual(am.runs.count, 1)
    }

    func testAQuietRunThatFailsLeavesANote() async throws {
        let am = FakeAm(setup: Self.failedRun, status: 1, error: "am: setup: service didn't work, as said above")
        defaults.set(true, forKey: DaemonSetup.welcomedKey)
        let setup = setup(am: am)
        setup.connectionChanged(.unavailable("am serve isn't running"))
        await until { setup.note != nil && setup.note != .updating }
        guard case .failed(let failure) = setup.phase else { return XCTFail("\(setup.phase)") }
        XCTAssertEqual(failure.step, "service")
        XCTAssertTrue(failure.message.hasPrefix("launchctl bootstrap"), failure.message)
        XCTAssertEqual(failure.log, "/Users/me/.agentman/serve.log")
        XCTAssertEqual(setup.note, .failed(failure.message))

        // Put right some other way, the note goes.
        try manage("0.39.0", am: am)
        setup.connectionChanged(.connected)
        setup.daemonReported(DaemonInfo(version: "0.39.0", apiLevel: 1, install: "managed"))
        await until { setup.note == nil }
        XCTAssertEqual(am.runs.count, 1)
    }

    func testWhatARunComesTo() {
        let lines = Self.goodRun.map { try! JSONDecoder().decode(SetupLine.self, from: Data($0.utf8)) }
        let steps = Array(lines.dropLast())
        let ok = ProgramExit(status: 0, error: "")
        XCTAssertEqual(DaemonSetup.outcome(summary: lines.last, steps: steps, exit: ok, log: "/l"), .finished(lines.last!))

        // Set up, but the daemon still too old: a development build kept in
        // the app's place.
        var kept = steps
        kept[1].detail = "~/.agentman/bin/am, agentman 0.37.0-4-gabc, kept: it is 0.37.0-4-gabc, a development build (-force replaces it)"
        var old = lines.last!
        old.apiLevel = 0
        guard case .failed(let failure) = DaemonSetup.outcome(summary: old, steps: kept, exit: ok, log: "/l") else { return XCTFail() }
        XCTAssertTrue(failure.message.contains("too old"), failure.message)
        XCTAssertTrue(failure.message.contains("a development build"), failure.message)

        // am that couldn't even start says why on standard error.
        let crashed = ProgramExit(status: 1, error: "am: setup: unexpected argument \"-app-version\"")
        XCTAssertEqual(DaemonSetup.outcome(summary: nil, steps: [], exit: crashed, log: "/l"),
                       .failed(DaemonSetup.Failure(message: "Unexpected argument \"-app-version\"", step: nil, log: "/l")))
        XCTAssertEqual(DaemonSetup.outcome(summary: nil, steps: [], exit: ProgramExit(status: 9, error: ""), log: "/l"),
                       .failed(DaemonSetup.Failure(message: "am setup stopped without saying why.", step: nil, log: "/l")))
    }

    // MARK: Settings

    func testWhatSettingsSaysOfAmServe() {
        let bundled = AmVersion(version: "0.39.0", apiLevel: 1)
        func status(_ daemon: DaemonInfo?, managed: AmVersion? = AmVersion(version: "0.39.0"), allowed: Bool = true) -> DaemonSetup.ServeStatus {
            DaemonSetup.serveStatus(daemon: daemon, bundled: bundled, managed: managed, allowed: allowed)
        }
        // None running: the app can set one up; without its am, as before.
        XCTAssertEqual(status(nil).action, .setUp("Set Up"))
        XCTAssertEqual(status(nil, allowed: false), DaemonSetup.ServeStatus(version: "Not connected"))

        let current = status(DaemonInfo(version: "0.39.0", apiLevel: 1, install: "managed", latest: "0.39.0", behind: 0, upgrade: "am upgrade"))
        XCTAssertEqual(current.detail, "Run by Agentman for Mac from ~/.agentman/bin/am. Up to date.")
        XCTAssertNil(current.action)

        // Behind the newest release: am upgrade, from here.
        let behind = status(DaemonInfo(version: "0.39.0", apiLevel: 1, install: "managed", latest: "0.40.0", behind: 1, upgrade: "am upgrade"))
        XCTAssertEqual(behind.detail, "Run by Agentman for Mac from ~/.agentman/bin/am. 0.40.0 is out.")
        XCTAssertEqual(behind.action, .upgrade)

        // Behind the app: am setup brings it to the app's.
        let older = status(DaemonInfo(version: "0.38.0", apiLevel: 1, install: "managed"), managed: AmVersion(version: "0.38.0"))
        XCTAssertEqual(older.detail, "Run by Agentman for Mac from ~/.agentman/bin/am. This app carries 0.39.0.")
        XCTAssertEqual(older.action, .setUp("Upgrade"))

        // Too old for the app, from Homebrew: the app's copy takes over.
        let tooOld = status(DaemonInfo(version: "0.37.0", install: "homebrew"), managed: nil)
        XCTAssertEqual(tooOld.detail, "Installed with Homebrew. Too old for this version of the app; the app's own copy takes its place.")
        XCTAssertEqual(tooOld.action, .setUp("Upgrade"))

        // Homebrew's, current for the app but behind its release: Homebrew
        // upgrades it, from a terminal, as before.
        let brew = status(DaemonInfo(version: "0.39.0", apiLevel: 1, install: "homebrew", latest: "0.40.0", behind: 1,
                                     upgrade: "brew upgrade --cask lenajeremy/agentman/agentman"), managed: nil)
        XCTAssertEqual(brew.detail, "0.40.0 is out. Run brew upgrade --cask lenajeremy/agentman/agentman in a terminal.")
        XCTAssertEqual(brew.action, .copy("brew upgrade --cask lenajeremy/agentman/agentman"))

        // A newer daemon than the app is left be.
        let newer = status(DaemonInfo(version: "0.41.0", apiLevel: 2, install: "managed", latest: "0.41.0", behind: 0),
                           managed: AmVersion(version: "0.41.0"))
        XCTAssertNil(newer.action)
    }

    func testUpgradingTheAppsCopy() async throws {
        let am = FakeAm(setup: [], status: 1, error: "am: upgrade: couldn't find out what the latest release is: no network")
        try manage("0.39.0", am: am)
        let setup = setup(am: am, automatic: false)
        await setup.upgrade()
        XCTAssertEqual(am.runs, [[home.appendingPathComponent(".agentman/bin/am").path, "upgrade"]])
        XCTAssertEqual(setup.upgradeProblem, "Couldn't find out what the latest release is: no network")
        XCTAssertFalse(setup.upgrading)
        XCTAssertEqual(setup.serveStatus(daemon: DaemonInfo(version: "0.39.0", apiLevel: 1, install: "managed"), connected: true).problem,
                       setup.upgradeProblem)
    }

    // MARK: The am command

    /// A folder of the test's own, standing for one on PATH.
    private func folder(_ name: String) throws -> String {
        let url = home.appendingPathComponent(name)
        try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
        return url.path
    }

    private func program(_ name: String, in folder: String) throws -> String {
        let path = (folder as NSString).appendingPathComponent(name)
        try Data("#!/bin/sh\n".utf8).write(to: URL(fileURLWithPath: path))
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: path)
        return path
    }

    private func setup(am: FakeAm, shell: FakeShell, brew: String, local: String, tools: [String] = []) -> DaemonSetup {
        var paths = DaemonSetup.Paths(bundled: Self.bundled, home: home.path)
        paths.commandFolders = (brew, local)
        paths.toolFolders = tools
        return DaemonSetup(runner: am, shell: shell, paths: paths, allowed: true, automatic: false, defaults: defaults, appVersion: "0.39.0")
    }

    func testWhereTheLinkGoes() {
        let brew = "/opt/homebrew/bin", local = "/usr/local/bin"
        let everything: (String) -> Bool = { _ in true }
        XCTAssertTrue(DaemonSetup.linkFolder(path: [brew, local, "/usr/bin"], preferred: brew, fallback: local, writable: everything) == (brew, false))
        XCTAssertTrue(DaemonSetup.linkFolder(path: [local, brew], preferred: brew, fallback: local, writable: everything) == (local, false),
                      "PATH looks in /usr/local/bin first")
        XCTAssertTrue(DaemonSetup.linkFolder(path: ["/usr/bin", local], preferred: brew, fallback: local, writable: everything) == (local, false))
        XCTAssertTrue(DaemonSetup.linkFolder(path: [brew, local], preferred: brew, fallback: local, writable: { _ in false }) == (local, true),
                      "neither is the user's to write: the administrator prompt")
    }

    func testFindingAmOnPath() throws {
        let first = try folder("first"), second = try folder("second")
        try FileManager.default.createDirectory(atPath: (first as NSString).appendingPathComponent("am"), withIntermediateDirectories: true)
        let am = try program("am", in: second)
        XCTAssertEqual(DaemonSetup.find("am", in: [first, second])?.path, am, "a folder called am is no program")
        XCTAssertNil(DaemonSetup.find("tmux", in: [first, second]))
        XCTAssertEqual(DaemonSetup.origin("/opt/homebrew/Caskroom/agentman/0.39.0/am"), "from Homebrew")
        XCTAssertEqual(DaemonSetup.origin("/Applications/Agentman.app/Contents/Helpers/am"), "inside an app")
        XCTAssertEqual(DaemonSetup.origin("/usr/local/lib/node_modules/agentman/bin/am"), "from npm")
    }

    func testInstallingTheCommand() async throws {
        let am = FakeAm(setup: [])
        try manage("0.39.0", am: am)
        let brew = try folder("brew"), local = try folder("local")
        let shell = FakeShell(path: [brew, local, "/usr/bin"])
        let setup = setup(am: am, shell: shell, brew: brew, local: local)
        XCTAssertEqual(setup.commandStatus().detail, "Looking…")
        await setup.lookAtShell()
        let before = setup.commandStatus()
        XCTAssertEqual(before.installTo, brew)
        XCTAssertFalse(before.installed)

        await setup.installCommand()
        XCTAssertEqual(shell.links, [.init(link: brew + "/am", target: setup.paths.managed, admin: false)])
        let after = setup.commandStatus()
        XCTAssertTrue(after.installed, after.detail)
        XCTAssertNil(after.problem)
        XCTAssertNil(after.installTo)
    }

    func testInstallAsksForThePasswordWhereItMust() async throws {
        let am = FakeAm(setup: [])
        try manage("0.39.0", am: am)
        let local = home.appendingPathComponent("not-yet").path
        let shell = FakeShell(path: [local, "/usr/bin"])
        let setup = setup(am: am, shell: shell, brew: home.appendingPathComponent("no-brew").path, local: local)
        await setup.lookAtShell()
        XCTAssertTrue(setup.commandStatus().detail.hasSuffix("and asks for your password once."), setup.commandStatus().detail)
        await setup.installCommand()
        XCTAssertEqual(shell.links.map(\.admin), [true])
        XCTAssertTrue(setup.commandStatus().installed)

        // Cancelled at the prompt: nothing said.
        let cancelling = FakeShell(path: [home.appendingPathComponent("other").path], result: .cancelled)
        let again = self.setup(am: am, shell: cancelling, brew: "/nonexistent-brew", local: home.appendingPathComponent("other").path)
        await again.lookAtShell()
        await again.installCommand()
        XCTAssertEqual(cancelling.links.count, 1)
        XCTAssertNil(again.commandStatus().problem)
        XCTAssertFalse(again.commandStatus().installed)
    }

    func testAnAmAlreadyOnPathIsSaidAndKept() async throws {
        let am = FakeAm(setup: [])
        try manage("0.39.0", am: am)
        let brew = try folder("brew"), local = try folder("local")
        let theirs = try program("am", in: brew)
        let setup = setup(am: am, shell: FakeShell(path: [brew, local]), brew: brew, local: local)
        await setup.lookAtShell()
        let status = setup.commandStatus()
        XCTAssertEqual(status.detail, "\(theirs), installed with the install script or by hand. It works with the app's am serve.")
        XCTAssertNil(status.installTo)

        // An am the PATH doesn't reach is left as it is.
        let hidden = try folder("hidden")
        _ = try program("am", in: hidden)
        let shell = FakeShell(path: [local])
        let other = self.setup(am: am, shell: shell, brew: "/nonexistent-brew", local: hidden)
        await other.lookAtShell()
        await other.installCommand()
        XCTAssertEqual(shell.links, [])
        XCTAssertEqual(other.commandStatus().problem, "There is already an am at \(hidden)/am, which your PATH doesn't reach.")
    }

    func testTmuxIsLookedForOnPathAndWhereItUsuallyIs() async throws {
        let usual = try folder("usual")
        let shell = FakeShell(path: [try folder("bin")])
        let none = setup(am: FakeAm(setup: []), shell: shell, brew: "/nonexistent-brew", local: "/nonexistent-local", tools: [usual])
        XCTAssertFalse(none.tmuxMissing, "not said before it has looked")
        await none.lookAtShell()
        XCTAssertTrue(none.tmuxMissing)

        // Where Homebrew puts it counts too, so a login shell that didn't
        // say its PATH doesn't make tmux look missing.
        let tmux = try program("tmux", in: usual)
        await none.lookAtShell()
        XCTAssertFalse(none.tmuxMissing)
        XCTAssertEqual(none.shellTools?.tmux, tmux)
    }

    func testWithoutTheAppsCopyThereIsNothingToLink() async throws {
        let local = try folder("local")
        let setup = setup(am: FakeAm(setup: []), shell: FakeShell(path: [local]), brew: "/nonexistent-brew", local: local)
        await setup.lookAtShell()
        XCTAssertNil(setup.commandStatus().installTo)
        XCTAssertTrue(setup.commandStatus().detail.contains("Set up Agentman first"))
    }

    // MARK: Never on this Mac

    func testNothingRunsWithoutLeave() async throws {
        let am = FakeAm(setup: Self.goodRun)
        let setup = setup(am: am, automatic: false, allowed: false)
        setup.connectionChanged(.unavailable("am serve isn't running"))
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertFalse(setup.showsScreen)
        await setup.run()
        XCTAssertEqual(am.runs, [])
    }

    // The app under test is the tests' host: its own setup must never run.
    func testTheAppUnderTestNeverSetsUp() async {
        XCTAssertFalse(DaemonSetup.mayRunHere)
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-setup.sock"))
        XCTAssertFalse(store.setup.allowed)
        XCTAssertFalse(store.setup.automatic)
        let refused = await ProcessAmRunner().run("/usr/bin/true", []) { _ in }
        XCTAssertEqual(refused.status, -1, "the real runner runs nothing under tests")
        let shell = MacShell()
        let path = await shell.loginPath()
        XCTAssertNil(path, "the user's shell profile isn't run")
        let linked = await shell.link("/nonexistent/am", to: "/nonexistent/target", admin: true)
        XCTAssertEqual(linked, .failed("Nothing is linked under tests."))
        XCTAssertFalse(store.setup.paths.home.hasPrefix(NSHomeDirectory() + "/"), "not even a look at the user's ~/.agentman")
    }

    // Restart Daemon with no service to restart offers Set Up Again, which
    // runs am setup on the screen (what it says is in DaemonRestartTests).
    func testSettingUpAgainFromRestartDaemon() async {

        let am = FakeAm(setup: Self.goodRun)
        let setup = setup(am: am, automatic: false)
        setup.runOnScreen()
        XCTAssertTrue(setup.showsScreen)
        await until { if case .finished = setup.phase { true } else { false } }
        XCTAssertEqual(am.runs.count, 1)
        XCTAssertNil(setup.note, "on the screen, not in the footer")
    }

    func testAppleScriptQuoting() {
        XCTAssertEqual(MacShell.quote(#"/Users/a "b"\c"#), #""/Users/a \"b\"\\c""#)
    }

    // The am this build carries says it is the app's (it only says its
    // version: nothing else of am runs under tests).
    func testTheAppCarriesItsAm() async throws {
        let path = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/am").path
        guard FileManager.default.isExecutableFile(atPath: path) else { throw XCTSkip("built without am") }
        let version = try await ProcessAmRunner().version(of: path)
        XCTAssertEqual(version.install, "app")
        XCTAssertEqual(version.managed, false)
        XCTAssertGreaterThanOrEqual(version.apiLevel ?? 0, DaemonSetup.requiredDaemonAPILevel)
        XCTAssertEqual(version.appVersion, Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String)
    }

    // MARK: Pictures

    /// An agent's hooks that failed, as on a Mac where Codex's notify is
    /// another program's: the reason shows under the step.
    static let agentFailedRun = Array(goodRun.prefix(2)) + [
        #"{"step":"hooks","status":"failed","detail":"hooks call ~/.agentman/bin/am; set up for claude; couldn't set up codex","agents":[{"agent":"claude","status":"changed"},{"agent":"codex","status":"failed","error":"/Users/me/.codex/config.toml already defines `notify` for another program; Agentman leaves it as it is, so Codex's turns reach Agentman more slowly"}]}"#,
    ] + Array(goodRun.dropFirst(3).prefix(3)) + [
        #"{"step":"summary","ok":false,"binary":"/Users/me/.agentman/bin/am","managed":true,"apiLevel":1,"socket":"/Users/me/.agentman/am.sock","log":"/Users/me/.agentman/serve.log","failed":["hooks"]}"#,
    ]

    func testRenderSetupScreen() async throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let folder = URL(fileURLWithPath: out)
        let size = CGSize(width: 900, height: 720)
        for dark in [false, true] {
            let look = dark ? "dark" : "light"
            // Before.
            let welcome = store(FakeAm(setup: Self.goodRun))
            welcome.setup.showsScreen = true
            try render(SetupScreen(), size: size, dark: dark, store: welcome, to: folder.appendingPathComponent("setup-welcome-\(look).png"))
            // Before, on a Mac without tmux.
            let noTmux = store(FakeAm(setup: Self.goodRun), tmux: false)
            noTmux.setup.showsScreen = true
            await noTmux.setup.lookAtShell()
            XCTAssertTrue(noTmux.setup.tmuxMissing)
            try render(SetupScreen(), size: size, dark: dark, store: noTmux, to: folder.appendingPathComponent("setup-welcome-tmux-\(look).png"))

            // During: three steps said, the fourth under way.
            let during = store(FakeAm(setup: Array(Self.goodRun.prefix(3)), hold: true))
            during.setup.showsScreen = true
            let running = Task { await during.setup.run() }
            await until { during.setup.steps.count == 3 }
            try render(SetupScreen(), size: size, dark: dark, store: during, to: folder.appendingPathComponent("setup-running-\(look).png"))
            (during.setup.runner as? FakeAm)?.release()
            await running.value

            // After.
            let done = store(FakeAm(setup: Self.goodRun))
            done.setup.showsScreen = true
            await done.setup.run()
            try render(SetupScreen(), size: size, dark: dark, store: done, to: folder.appendingPathComponent("setup-done-\(look).png"))

            // A step that failed.
            let failed = store(FakeAm(setup: Self.failedRun, status: 1, error: "am: setup: service didn't work, as said above"))
            failed.setup.showsScreen = true
            await failed.setup.run()
            try render(SetupScreen(), size: size, dark: dark, store: failed, to: folder.appendingPathComponent("setup-failed-\(look).png"))

            // An agent's hooks that failed: the reason under the step.
            let agentFailed = store(FakeAm(setup: Self.agentFailedRun, status: 1, error: "am: setup: hooks didn't work, as said above"))
            agentFailed.setup.showsScreen = true
            await agentFailed.setup.run()
            try render(SetupScreen(), size: size, dark: dark, store: agentFailed, to: folder.appendingPathComponent("setup-agent-failed-\(look).png"))

            // The footer's notes on a quiet run.
            defaults.set(true, forKey: DaemonSetup.welcomedKey)
            for (name, am) in [("updating", FakeAm(setup: [], hold: true)), ("updated", FakeAm(setup: Self.goodRun)),
                               ("failed", FakeAm(setup: Self.failedRun, status: 1, error: ""))] {
                let quiet = store(am)
                let run = Task { await quiet.setup.run() }
                if name == "updating" { await until { quiet.setup.note == .updating } } else { await run.value }
                try render(ConnectionFooter(), size: CGSize(width: 300, height: 40), dark: dark, store: quiet,
                           to: folder.appendingPathComponent("footer-\(name)-\(look).png"))
                am.release()
                await run.value
            }
            defaults.removeObject(forKey: DaemonSetup.welcomedKey)
        }
    }

    /// Settings' am serve row in each state it can be in.
    func testRenderServeRow() async throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let managedPath = home.appendingPathComponent(".agentman/bin/am").path
        try manage("0.39.0", am: FakeAm(setup: []))
        func state(_ label: String, daemon: DaemonInfo?, managed: String? = "0.39.0", allowed: Bool = true,
                   am: FakeAm = FakeAm(setup: [])) -> (String, AgentsStore) {
            if let managed { am.versions[managedPath] = AmVersion(version: managed, apiLevel: 1, install: "managed", managed: true) }
            let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-setup.sock"),
                                    setup: setup(am: am, automatic: false, allowed: allowed))
            if let daemon {
                store.statusChanged(.connected)
                var event = DaemonEvent(type: "sessions")
                event.sessions = []
                event.daemon = daemon
                store.apply(event)
            }
            return (label, store)
        }
        let busy = FakeAm(setup: [], hold: true)
        let failing = FakeAm(setup: [], status: 1, error: "am: upgrade: couldn't find out what the latest release is: Get \"https://api.github.com/repos/lenajeremy/agentman/releases/latest\": dial tcp: lookup api.github.com: no such host")
        let states = [
            state("Nothing running", daemon: nil, managed: nil),
            state("The app's, up to date", daemon: DaemonInfo(version: "0.39.0", apiLevel: 1, install: "managed", latest: "0.39.0", behind: 0)),
            state("The app's, a newer release out", daemon: DaemonInfo(version: "0.39.0", apiLevel: 1, install: "managed", latest: "0.40.0", behind: 1, upgrade: "am upgrade")),
            state("The app's, behind the app", daemon: DaemonInfo(version: "0.38.0", apiLevel: 1, install: "managed", latest: "0.38.0", behind: 0), managed: "0.38.0"),
            state("Homebrew's, too old for the app", daemon: DaemonInfo(version: "0.37.0", install: "homebrew"), managed: nil),
            state("Homebrew's, a newer release out", daemon: DaemonInfo(version: "0.39.0", apiLevel: 1, install: "homebrew", latest: "0.40.0", behind: 1,
                                                                        upgrade: "brew upgrade --cask lenajeremy/agentman/agentman"), managed: nil),
            state("Upgrading", daemon: DaemonInfo(version: "0.39.0", apiLevel: 1, install: "managed", latest: "0.40.0", behind: 1), am: busy),
            state("The upgrade failed", daemon: DaemonInfo(version: "0.39.0", apiLevel: 1, install: "managed", latest: "0.40.0", behind: 1), am: failing),
            state("Without the app's am (a Debug build, or am alone)", daemon: nil, managed: nil, allowed: false),
        ]
        for (_, store) in states { await store.setup.readVersions() }
        XCTAssertEqual(states[3].1.setup.managed?.version, "0.38.0")
        let upgrading = Task { await states[6].1.setup.upgrade() }
        await until { states[6].1.setup.upgrading }
        await states[7].1.setup.upgrade()
        for dark in [false, true] {
            let page = VStack(alignment: .leading, spacing: 18) {
                ForEach(Array(states.enumerated()), id: \.offset) { _, state in
                    VStack(alignment: .leading, spacing: 6) {
                        Text(state.0).font(TextStyle.label).foregroundStyle(Palette.faint).padding(.leading, 4)
                        SettingsSection { DaemonServeRow() }
                            .environment(state.1)
                    }
                }
            }
            .padding(24)
            try render(page, size: CGSize(width: 620, height: 1180), dark: dark, store: states[0].1,
                       to: URL(fileURLWithPath: out).appendingPathComponent("about-serve-\(dark ? "dark" : "light").png"))
        }
        busy.release()
        await upgrading.value

        // The end of the General page, as Settings shows it: the Command
        // line section above About, on a Mac without tmux.
        let managed = "/Users/me/.agentman/bin/am"
        let command = DaemonSetup.commandStatus(
            tools: DaemonSetup.ShellTools(path: ["/opt/homebrew/bin", "/usr/local/bin"], am: nil), managed: managed,
            managedThere: true, allowed: true, folders: ("/opt/homebrew/bin", "/usr/local/bin"), writable: { _ in false })
        for dark in [false, true] {
            let page = SettingsPageView(title: "General") {
                CommandLineRows(command: command, tmuxMissing: true)
                SettingsSection(title: "About") {
                    SettingsRow(title: "Agentman for Mac") {
                        Text("0.39.0 (44)").font(TextStyle.caption).foregroundStyle(Palette.muted)
                    }
                    DaemonServeRow()
                }
            }
            try render(page, size: CGSize(width: 600, height: 580), dark: dark, store: states[2].1,
                       to: URL(fileURLWithPath: out).appendingPathComponent("settings-general-end-\(dark ? "dark" : "light").png"))
        }
    }

    /// Settings' am command row in each state, with a Mac's own paths.
    func testRenderCommandRow() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let managed = "/Users/me/.agentman/bin/am"
        let path = ["/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin"]
        func status(_ am: DaemonSetup.Found?, managedThere: Bool = true, writable: Bool = false) -> DaemonSetup.CommandStatus {
            DaemonSetup.commandStatus(tools: DaemonSetup.ShellTools(path: path, am: am), managed: managed, managedThere: managedThere,
                                      allowed: true, folders: ("/opt/homebrew/bin", "/usr/local/bin"), writable: { _ in writable })
        }
        var failed = status(nil)
        failed.problem = "The administrator prompt couldn't be shown: the app isn't allowed to ask for it."
        let states: [(String, DaemonSetup.CommandStatus)] = [
            ("Looking", DaemonSetup.commandStatus(tools: nil, managed: managed, managedThere: true, allowed: true,
                                                  folders: ("/opt/homebrew/bin", "/usr/local/bin"), writable: { _ in true })),
            ("Not on PATH: Install, with the password", status(nil)),
            ("Not on PATH: Install into Homebrew's folder", status(nil, writable: true)),
            ("Not on PATH, nothing set up yet", status(nil, managedThere: false)),
            ("Installed", status(DaemonSetup.Found(path: "/usr/local/bin/am", real: managed))),
            ("Homebrew's", status(DaemonSetup.Found(path: "/opt/homebrew/bin/am", real: "/opt/homebrew/Caskroom/agentman/0.39.0/am"))),
            ("Install failed", failed),
            ("tmux isn't installed", status(DaemonSetup.Found(path: "/usr/local/bin/am", real: managed))),
        ]
        for dark in [false, true] {
            let page = VStack(alignment: .leading, spacing: 18) {
                ForEach(Array(states.enumerated()), id: \.offset) { _, state in
                    VStack(alignment: .leading, spacing: 6) {
                        Text(state.0).font(TextStyle.label).foregroundStyle(Palette.faint).padding(.leading, 4)
                        CommandLineRows(command: state.1, tmuxMissing: state.0.hasPrefix("tmux"))
                    }
                }
            }
            .padding(24)
            try render(page, size: CGSize(width: 620, height: 1260), dark: dark, store: store(FakeAm(setup: [])),
                       to: URL(fileURLWithPath: out).appendingPathComponent("command-\(dark ? "dark" : "light").png"))
        }
    }

    private func store(_ am: FakeAm, tmux: Bool = true) -> AgentsStore {
        AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-setup.sock"), setup: setup(am: am, automatic: false, tmux: tmux))
    }

    private func render<V: View>(_ view: V, size: CGSize, dark: Bool, store: AgentsStore, to url: URL) throws {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentView = NSHostingView(rootView: view.environment(store)
            .defaultAppStorage(UserDefaults(suiteName: suite)!)
            .frame(width: size.width, height: size.height)
            .background(Palette.paper))
        window.orderFrontRegardless()
        RunLoop.main.run(until: Date().addingTimeInterval(0.8))
        let content = try XCTUnwrap(window.contentView)
        let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
        content.cacheDisplay(in: content.bounds, to: bitmap)
        try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: url)
        window.close()
    }
}

/// An am that runs nothing: it answers version with what it was given for
/// each path, and any run with the lines it was given, held back until
/// released when asked to, as a run under way.
final class FakeAm: AmRunner, @unchecked Sendable {
    private let lock = NSLock()
    private var _runs: [[String]] = []
    private let lines: [String]
    private let status: Int32
    private let error: String
    private var held: CheckedContinuation<Void, Never>?
    private var hold: Bool
    var versions: [String: AmVersion] = [DaemonSetupTests.bundled: AmVersion(version: "0.39.0", apiLevel: 1, install: "app", managed: false)]

    init(setup lines: [String], status: Int32 = 0, error: String = "", hold: Bool = false) {
        self.lines = lines
        self.status = status
        self.error = error
        self.hold = hold
    }

    var runs: [[String]] {
        lock.lock()
        defer { lock.unlock() }
        return _runs
    }

    func version(of am: String) async throws -> AmVersion {
        guard let version = lock.withLock({ versions[am] }) else { throw ProcessAmRunner.Unreadable(output: "no such am") }
        return version
    }

    func run(_ am: String, _ arguments: [String], line: @escaping @Sendable (String) -> Void) async -> ProgramExit {
        let holding = lock.withLock {
            _runs.append([am] + arguments)
            return hold
        }
        for each in lines { line(each) }
        if holding {
            await withCheckedContinuation { continuation in
                lock.withLock {
                    if hold { held = continuation } else { continuation.resume() }
                }
            }
        }
        return ProgramExit(status: status, error: error)
    }

    /// Lets a held run end.
    func release() {
        lock.lock()
        hold = false
        let waiting = held
        held = nil
        lock.unlock()
        waiting?.resume()
    }
}

/// A shell whose PATH is the test's folders and whose links go there: no
/// profile is run, nothing outside the test's folders is written, and no
/// password is asked for.
final class FakeShell: ShellSystem, @unchecked Sendable {
    struct Link: Equatable {
        var link: String
        var target: String
        var admin: Bool
    }

    private let lock = NSLock()
    private let path: [String]
    private let result: LinkResult
    private var made: [Link] = []

    init(path: [String], result: LinkResult = .linked) {
        self.path = path
        self.result = result
    }

    var links: [Link] { lock.withLock { made } }

    func loginPath() async -> [String]? { path }

    func link(_ link: String, to target: String, admin: Bool) async -> LinkResult {
        lock.withLock { made.append(Link(link: link, target: target, admin: admin)) }
        guard result == .linked else { return result }
        let files = FileManager.default
        try? files.createDirectory(atPath: (link as NSString).deletingLastPathComponent, withIntermediateDirectories: true)
        try? files.removeItem(atPath: link)
        do {
            try files.createSymbolicLink(atPath: link, withDestinationPath: target)
            return .linked
        } catch {
            return .failed(error.localizedDescription)
        }
    }
}
