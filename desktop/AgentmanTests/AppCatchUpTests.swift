import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// The app updating to match an am serve newer than itself: when it asks,
/// when it updates without asking, when it only says so in the footer, and
/// pictures of the pop-up, the footer's note and the Settings switch, which
/// run with TEST_RUNNER_AGENTMAN_SHOTS=<folder> set.
///
/// Nothing here reaches Sparkle: the updater is a FakeUpdater that counts
/// what it was asked, and ~/.agentman is a folder of the test's own.
@MainActor
final class AppCatchUpTests: XCTestCase {
    private var home: URL!
    private var defaults: UserDefaults!
    private let suite = "dev.agentman.mac.tests.catchup"

    override func setUp() async throws {
        continueAfterFailure = false
        home = FileManager.default.temporaryDirectory.appendingPathComponent("am-catchup-\(UUID().uuidString.prefix(8))")
        try FileManager.default.createDirectory(at: home.appendingPathComponent(".agentman"), withIntermediateDirectories: true)
        defaults = UserDefaults(suiteName: suite)
        defaults.removePersistentDomain(forName: suite)
    }

    override func tearDown() async throws {
        try? FileManager.default.removeItem(at: home)
        defaults.removePersistentDomain(forName: suite)
    }

    /// An app at 0.39.0 on the test's home, with Sparkle on unless said.
    private func catchUp(_ updater: FakeUpdater = FakeUpdater()) -> AppCatchUp {
        AppCatchUp(updater: updater, home: home.path, defaults: defaults, appVersion: "0.39.0")
    }

    /// What am upgrade leaves in ~/.agentman/app-update.json.
    private func terminalChose(_ choice: String, for version: String) throws {
        let body = #"{"version":"\#(version)","choice":"\#(choice)","at":1762214400000}"#
        try Data(body.utf8).write(to: home.appendingPathComponent(".agentman/app-update.json"))
    }

    private func daemon(_ version: String) -> DaemonInfo {
        DaemonInfo(version: version, apiLevel: 1, install: "homebrew")
    }

    // MARK: Deciding

    func testTheDecisionTable() {
        typealias Intent = AppCatchUp.Intent
        func decide(app: String = "0.39.0", daemon: String = "0.40.0", intent: Intent? = nil, automatic: Bool = false,
                    declined: String? = nil, on: Bool = true) -> AppCatchUp.Decision {
            AppCatchUp.decide(app: app, daemon: daemon, intent: intent, automatic: automatic, declined: declined, updatesOn: on)
        }
        // The first time, it asks.
        XCTAssertEqual(decide(), .ask)
        // Not newer: nothing, whatever was said.
        XCTAssertEqual(decide(daemon: "0.39.0"), .nothing)
        XCTAssertEqual(decide(daemon: "0.38.0", intent: Intent(version: "0.40.0", choice: Intent.update)), .nothing)
        XCTAssertEqual(decide(daemon: "0.39.0", automatic: true), .nothing)
        // Builds of unreleased work are never older or newer.
        XCTAssertEqual(decide(daemon: "0.40.0-dev"), .nothing)
        XCTAssertEqual(decide(app: "0.39.0-dev"), .nothing)
        XCTAssertEqual(decide(daemon: "dev", automatic: true), .nothing)
        // With Sparkle off (Debug builds, tests), never a pop-up or an
        // update, even when the terminal said yes.
        XCTAssertEqual(decide(on: false), .nothing)
        XCTAssertEqual(decide(intent: Intent(version: "0.40.0", choice: Intent.update), on: false), .nothing)
        XCTAssertEqual(decide(automatic: true, on: false), .nothing)

        // The terminal's "update", for that version or a newer one.
        XCTAssertEqual(decide(intent: Intent(version: "0.40.0", choice: Intent.update)), .update)
        XCTAssertEqual(decide(intent: Intent(version: "0.41.0", choice: Intent.update)), .update)
        XCTAssertEqual(decide(intent: Intent(version: "0.39.5", choice: Intent.update)), .ask, "said of an older am")
        // The terminal's "skip": quietly, even with the automatic switch on.
        XCTAssertEqual(decide(intent: Intent(version: "0.40.0", choice: Intent.skip)), .note)
        XCTAssertEqual(decide(intent: Intent(version: "0.40.0", choice: Intent.skip), automatic: true), .note)
        XCTAssertEqual(decide(intent: Intent(version: "0.39.5", choice: Intent.skip)), .ask, "said of an older am")
        XCTAssertEqual(decide(intent: Intent(version: "0.39.5", choice: Intent.skip), automatic: true), .update)
        XCTAssertEqual(decide(intent: Intent(version: "0.40.0", choice: "later")), .ask, "no choice am makes")
        XCTAssertEqual(decide(intent: Intent(version: "dev", choice: Intent.update)), .ask)

        // The automatic switch.
        XCTAssertEqual(decide(automatic: true), .update)
        // Not Now, until a newer am serve comes.
        XCTAssertEqual(decide(declined: "0.40.0"), .note)
        XCTAssertEqual(decide(automatic: true, declined: "0.40.0"), .note)
        XCTAssertEqual(decide(daemon: "0.41.0", declined: "0.40.0"), .ask)
        XCTAssertEqual(decide(daemon: "0.41.0", automatic: true, declined: "0.40.0"), .update)
    }

    // MARK: Updating

    // Yes in the terminal: the update starts at once, in the background,
    // once a launch; the footer says nothing until it is downloaded, then
    // offers the restart, which installs it.
    func testUpdatingAsTheTerminalSaid() throws {
        let updater = FakeUpdater()
        let catchUp = catchUp(updater)
        try terminalChose("update", for: "0.40.0")
        catchUp.daemonReported(daemon("0.40.0"))
        XCTAssertEqual(updater.background, 1)
        XCTAssertEqual(catchUp.started, "0.40.0")
        XCTAssertNil(catchUp.asking)
        XCTAssertNil(catchUp.note, "nothing to say while it downloads")
        catchUp.daemonReported(daemon("0.40.0"))
        XCTAssertEqual(updater.background, 1, "started once")

        updater.status = .ready("0.40.0")
        XCTAssertEqual(catchUp.note, .restart("0.40.0"))
        catchUp.act(on: .restart("0.40.0"))
        XCTAssertEqual(updater.installs, 1)
        XCTAssertEqual(updater.checks, 0, "no window")
    }

    // Only am, in the terminal: no pop-up, a quiet note that opens Sparkle's
    // window.
    func testOnlyAmLeavesAQuietNote() throws {
        let updater = FakeUpdater()
        let catchUp = catchUp(updater)
        try terminalChose("skip", for: "0.40.0")
        catchUp.daemonReported(daemon("0.40.0"))
        XCTAssertNil(catchUp.asking)
        XCTAssertEqual(catchUp.note, .waiting("0.40.0"))
        XCTAssertEqual(updater.background, 0)
        catchUp.act(on: .waiting("0.40.0"))
        XCTAssertEqual(updater.checks, 1)

        // A newer am serve after that is asked about.
        catchUp.daemonReported(daemon("0.41.0"))
        XCTAssertEqual(catchUp.asking, "0.41.0")
    }

    // The first time it asks; Not Now keeps the version, and a quiet note,
    // until a newer am serve comes.
    func testNotNowRemembersTheVersion() {
        let updater = FakeUpdater()
        let catchUp = catchUp(updater)
        catchUp.daemonReported(daemon("0.40.0"))
        XCTAssertEqual(catchUp.asking, "0.40.0")
        XCTAssertNil(catchUp.note)

        catchUp.notNow(automatically: false)
        XCTAssertNil(catchUp.asking)
        XCTAssertEqual(catchUp.declined, "0.40.0")
        XCTAssertFalse(catchUp.automatic)
        XCTAssertEqual(catchUp.note, .waiting("0.40.0"))

        // Next launch, the same am serve: no pop-up.
        let again = self.catchUp(updater)
        again.daemonReported(daemon("0.40.0"))
        XCTAssertNil(again.asking)
        XCTAssertEqual(again.note, .waiting("0.40.0"))
        again.daemonReported(daemon("0.41.0"))
        XCTAssertEqual(again.asking, "0.41.0")
        XCTAssertEqual(updater.checks + updater.background, 0)
    }

    // Update Agentman opens Sparkle's window. With the checkbox on, later
    // ones update without asking; without it, this launch asks no more and
    // the footer keeps a way back.
    func testUpdatingFromThePopUp() {
        let updater = FakeUpdater()
        let catchUp = catchUp(updater)
        catchUp.daemonReported(daemon("0.40.0"))
        catchUp.accept(automatically: false)
        XCTAssertEqual(updater.checks, 1)
        XCTAssertNil(catchUp.asking)
        catchUp.daemonReported(daemon("0.40.0"))
        XCTAssertNil(catchUp.asking, "asked once a launch")
        XCTAssertEqual(catchUp.note, .waiting("0.40.0"))
        XCTAssertFalse(catchUp.automatic)

        let next = self.catchUp(updater)
        next.daemonReported(daemon("0.41.0"))
        next.accept(automatically: true)
        XCTAssertTrue(next.automatic)
        XCTAssertEqual(updater.checks, 2)
        // From then on, newer ones update without asking.
        let later = self.catchUp(updater)
        later.daemonReported(daemon("0.42.0"))
        XCTAssertNil(later.asking)
        XCTAssertEqual(updater.background, 1)
    }

    // Not Now with the checkbox on keeps this version waiting, and updates
    // the next without asking.
    func testNotNowWithTheCheckboxOn() {
        let updater = FakeUpdater()
        let catchUp = catchUp(updater)
        catchUp.daemonReported(daemon("0.40.0"))
        catchUp.notNow(automatically: true)
        XCTAssertTrue(catchUp.automatic)
        catchUp.daemonReported(daemon("0.40.0"))
        XCTAssertEqual(updater.background, 0)
        XCTAssertEqual(catchUp.note, .waiting("0.40.0"))
        catchUp.daemonReported(daemon("0.41.0"))
        XCTAssertEqual(updater.background, 1)
    }

    // Once the app has caught up, or the daemon went back, nothing shows.
    func testNothingShowsOnceMatched() {
        let catchUp = catchUp()
        catchUp.daemonReported(daemon("0.40.0"))
        XCTAssertNotNil(catchUp.asking)
        catchUp.daemonReported(daemon("0.39.0"))
        XCTAssertNil(catchUp.asking)
        XCTAssertNil(catchUp.note)
    }

    // Where Sparkle doesn't run, nothing pops up and nothing updates, even
    // with yes from the terminal and the switch on.
    func testNothingWithoutSparkle() throws {
        let updater = FakeUpdater(status: .off)
        let catchUp = catchUp(updater)
        try terminalChose("update", for: "0.40.0")
        catchUp.automatic = true
        catchUp.daemonReported(daemon("0.40.0"))
        XCTAssertNil(catchUp.asking)
        XCTAssertNil(catchUp.note)
        XCTAssertEqual(updater.checks + updater.background + updater.installs, 0)
    }

    // The store tells it what the daemon is, from the session list.
    func testTheStoreReportsTheDaemon() throws {
        let catchUp = catchUp()
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-catchup.sock"), catchUp: catchUp)
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data("""
        {"type":"sessions","sessions":[],"daemon":{"version":"0.40.0","apiLevel":1,"install":"homebrew"}}
        """.utf8)))
        XCTAssertEqual(catchUp.asking, "0.40.0")
    }

    // The app under test, the tests' host, has Sparkle off and no look at
    // the user's own ~/.agentman.
    func testTheAppUnderTestNeverCatchesUp() {
        let real = AppCatchUp()
        XCTAssertEqual(real.updater.status, .off)
        XCTAssertFalse(real.home.hasPrefix(NSHomeDirectory() + "/"))
        XCTAssertFalse(real.intentPath.hasPrefix(NSHomeDirectory() + "/"))
    }

    // MARK: Pictures

    func testRenderCatchingUp() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let folder = URL(fileURLWithPath: out)
        for dark in [false, true] {
            let look = dark ? "dark" : "light"

            // The pop-up, the first time.
            let asking = store(FakeUpdater())
            asking.catchUp.daemonReported(daemon("0.40.0"))
            XCTAssertEqual(asking.catchUp.asking, "0.40.0")
            try render(CatchUpPrompt(version: "0.40.0"), size: CGSize(width: 720, height: 360), dark: dark, store: asking,
                       to: folder.appendingPathComponent("catchup-prompt-\(look).png"))

            // The footer: an update waiting, and one ready for a restart.
            let waiting = store(FakeUpdater())
            try terminalChose("skip", for: "0.40.0")
            waiting.catchUp.daemonReported(daemon("0.40.0"))
            XCTAssertEqual(waiting.catchUp.note, .waiting("0.40.0"))
            try render(ConnectionFooter(), size: CGSize(width: 300, height: 40), dark: dark, store: waiting,
                       to: folder.appendingPathComponent("catchup-note-waiting-\(look).png"))

            let updater = FakeUpdater()
            let ready = store(updater)
            try terminalChose("update", for: "0.40.0")
            ready.catchUp.daemonReported(daemon("0.40.0"))
            updater.status = .ready("0.40.0")
            XCTAssertEqual(ready.catchUp.note, .restart("0.40.0"))
            try render(ConnectionFooter(), size: CGSize(width: 300, height: 40), dark: dark, store: ready,
                       to: folder.appendingPathComponent("catchup-note-restart-\(look).png"))
            try FileManager.default.removeItem(at: home.appendingPathComponent(".agentman/app-update.json"))
        }
    }

    /// Settings › General › About with Sparkle on, the new switch off and on.
    func testRenderSettingsSwitch() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let folder = URL(fileURLWithPath: out)
        let updater = AppUpdater.shared
        defer {
            updater.status = .off
            updater.lastChecked = nil
            updater.canCheck = false
        }
        updater.status = .idle
        updater.lastChecked = Date().addingTimeInterval(-2 * 3600)
        updater.canCheck = true
        let store = store(FakeUpdater())
        store.statusChanged(.connected)
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data("""
        {"type":"sessions","sessions":[],"daemon":{"version":"0.39.0","apiLevel":1,"install":"managed","latest":"0.39.0","behind":0}}
        """.utf8)))
        for dark in [false, true] {
            for on in [false, true] {
                defaults.set(on, forKey: AppCatchUp.automaticKey)
                try render(SettingsPageView(title: "General") { AboutSection() }, size: CGSize(width: 600, height: 400),
                           dark: dark, store: store,
                           to: folder.appendingPathComponent("catchup-settings-\(on ? "on" : "off")-\(dark ? "dark" : "light").png"))
            }
        }
    }

    private func store(_ updater: FakeUpdater) -> AgentsStore {
        AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-catchup.sock"), catchUp: catchUp(updater))
    }

    private func render<V: View>(_ view: V, size: CGSize, dark: Bool, store: AgentsStore, to url: URL) throws {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentView = NSHostingView(rootView: view.environment(store)
            .defaultAppStorage(UserDefaults(suiteName: suite)!)
            .frame(width: size.width, height: size.height, alignment: .top)
            .background(Palette.paper))
        window.orderFrontRegardless()
        RunLoop.main.run(until: Date().addingTimeInterval(0.6))
        let content = try XCTUnwrap(window.contentView)
        let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
        content.cacheDisplay(in: content.bounds, to: bitmap)
        try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: url)
        window.close()
    }
}

/// An updater that updates nothing: it counts what it was asked, and says
/// where things stand as the test sets it.
@MainActor @Observable
final class FakeUpdater: AppUpdating {
    var status: AppUpdater.Status
    private(set) var checks = 0
    private(set) var background = 0
    private(set) var installs = 0

    init(status: AppUpdater.Status = .idle) {
        self.status = status
    }

    func check() { checks += 1 }
    func updateInBackground() { background += 1 }
    func installNow() { installs += 1 }
}
