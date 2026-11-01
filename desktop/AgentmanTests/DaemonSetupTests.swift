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

    private func setup(am: FakeAm, automatic: Bool = true, allowed: Bool = true) -> DaemonSetup {
        let setup = DaemonSetup(runner: am, paths: DaemonSetup.Paths(bundled: Self.bundled, home: home.path),
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

    private func store(_ am: FakeAm) -> AgentsStore {
        AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-setup.sock"), setup: setup(am: am, automatic: false))
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
