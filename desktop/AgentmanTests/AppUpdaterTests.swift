import AppKit
import Sparkle
import SwiftUI
import XCTest
@testable import Agentman

/// The app updating itself: the Info.plist Sparkle reads, the delegate calls
/// it makes actually reaching the updater, what Settings › General › About
/// says at each step, and pictures of that row, which run with
/// TEST_RUNNER_AGENTMAN_SHOTS=<folder> set.
@MainActor
final class AppUpdaterTests: XCTestCase {
    // Sparkle refuses a feed over plain HTTP, and an update not signed with
    // this key; a key that is not 32 bytes of base64 is a broken paste.
    func testTheInfoPlistNamesTheFeedAndTheKey() throws {
        let info = try XCTUnwrap(Bundle.main.infoDictionary)
        XCTAssertEqual(info["SUFeedURL"] as? String, "https://agentman-nu.vercel.app/appcast.xml")
        let key = try XCTUnwrap(info["SUPublicEDKey"] as? String)
        XCTAssertEqual(Data(base64Encoded: key)?.count, 32)
        XCTAssertNil(info["SUEnableAutomaticChecks"], "Sparkle asks once whether to check by itself")
    }

    // The delegate's methods are optional, so a misspelt one compiles and is
    // never called. Each selector here is the protocol's own.
    func testTheDelegateMethodsAreTheProtocols() {
        let updater = AppUpdater.shared
        let selectors: [Selector] = [
            #selector(SPUUpdaterDelegate.updater(_:mayPerform:)),
            #selector(SPUUpdaterDelegate.updater(_:didFindValidUpdate:)),
            #selector(SPUUpdaterDelegate.updaterDidNotFindUpdate(_:error:)),
            #selector(SPUUpdaterDelegate.updater(_:userDidMake:forUpdate:state:)),
            #selector(SPUUpdaterDelegate.updater(_:willDownloadUpdate:with:)),
            #selector(SPUUpdaterDelegate.userDidCancelDownload(_:)),
            #selector(SPUUpdaterDelegate.updater(_:failedToDownloadUpdate:error:)),
            #selector(SPUUpdaterDelegate.updater(_:willInstallUpdateOnQuit:immediateInstallationBlock:)),
            #selector(SPUUpdaterDelegate.updater(_:didAbortWithError:)),
            #selector(SPUUpdaterDelegate.updater(_:didFinishUpdateCycleFor:error:)),
        ]
        for selector in selectors {
            XCTAssertTrue(updater.responds(to: selector), "\(selector)")
        }
    }

    // Under tests (and in any Debug build) Sparkle never starts, so nothing
    // checks the network or offers to replace the test host.
    func testItStaysOffUnderTests() {
        AppUpdater.shared.start()
        XCTAssertEqual(AppUpdater.shared.status, .off)
        XCTAssertFalse(AppUpdater.shared.canCheck)
    }

    func testWhatTheAboutRowSays() {
        let now = Date(timeIntervalSince1970: 1_761_955_200)
        func detail(_ status: AppUpdater.Status, _ lastChecked: Date? = nil, automatic: Bool = true) -> String {
            AppUpdater.detail(status, lastChecked: lastChecked, automatic: automatic, now: now)
        }
        XCTAssertEqual(detail(.idle, now.addingTimeInterval(-2 * 3600)), "Last checked for updates 2 hours ago.")
        XCTAssertEqual(detail(.idle), "Checks for updates every day.")
        XCTAssertEqual(detail(.idle, automatic: false), "Doesn't check for updates by itself.")
        XCTAssertEqual(detail(.checking), "Checking for updates…")
        XCTAssertEqual(detail(.upToDate), "Up to date.")
        XCTAssertEqual(detail(.available("0.39.0")), "Agentman 0.39.0 is available.")
        XCTAssertEqual(detail(.downloading("0.39.0")), "Downloading Agentman 0.39.0…")
        XCTAssertEqual(detail(.ready("0.39.0")), "Agentman 0.39.0 installs when you quit.")
        XCTAssertEqual(detail(.failed("The feed could not be read.")), "The feed could not be read.")
        XCTAssertEqual(detail(.off), "Updates are off in development builds.")

        XCTAssertEqual(AppUpdater.action(.upToDate), "Check Now")
        XCTAssertEqual(AppUpdater.action(.available("0.39.0")), "Update…")
        XCTAssertEqual(AppUpdater.action(.ready("0.39.0")), "Restart to Update")
    }

    // "Nothing new" and the user's own cancelling end a check without it
    // having gone wrong.
    func testOnlyRealProblemsAreShown() {
        func error(_ code: SUError) -> NSError { NSError(domain: SUSparkleErrorDomain, code: Int(code.rawValue)) }
        XCTAssertFalse(AppUpdater.shows(error(.noUpdateError)))
        XCTAssertFalse(AppUpdater.shows(error(.installationCanceledError)))
        XCTAssertFalse(AppUpdater.shows(error(.installationAuthorizeLaterError)))
        XCTAssertTrue(AppUpdater.shows(error(.signatureError)))
        XCTAssertTrue(AppUpdater.shows(error(.downloadError)))
        XCTAssertTrue(AppUpdater.shows(NSError(domain: NSURLErrorDomain, code: NSURLErrorNotConnectedToInternet)))
    }

    // The app menu carries Check for Updates… right under About.
    func testTheAppMenuChecksForUpdates() throws {
        let appMenu = try XCTUnwrap(NSApp.mainMenu?.items.first?.submenu)
        let titles = appMenu.items.map(\.title)
        let about = try XCTUnwrap(titles.firstIndex { $0.hasPrefix("About") })
        XCTAssertEqual(titles.firstIndex(of: "Check for Updates…"), about + 1, "\(titles)")
    }

    // MARK: Pictures

    func testRenderAbout() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let folder = URL(fileURLWithPath: out)
        let updater = AppUpdater.shared
        defer {
            updater.status = .off
            updater.lastChecked = nil
            updater.canCheck = false
            updater.checksAutomatically = false
        }
        updater.checksAutomatically = true
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-updater.sock"))
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data("""
        {"type":"sessions","sessions":[],"daemon":{"version":"0.38.0","latest":"0.38.0"}}
        """.utf8)))
        let states: [(String, AppUpdater.Status, Date?)] = [
            ("idle", .idle, Date().addingTimeInterval(-2 * 3600)),
            ("checking", .checking, nil),
            ("uptodate", .upToDate, Date()),
            ("available", .available("0.39.0"), Date()),
            ("downloading", .downloading("0.39.0"), Date()),
            ("ready", .ready("0.39.0"), Date()),
            ("failed", .failed("An error occurred in retrieving update information. Please try again later."), nil),
            ("off", .off, nil),
        ]
        for dark in [false, true] {
            for (name, status, lastChecked) in states {
                updater.status = status
                updater.lastChecked = lastChecked
                updater.canCheck = status != .checking
                try render(SettingsPageView(title: "General") { AboutSection() }, size: CGSize(width: 600, height: 330),
                           dark: dark, store: store,
                           to: folder.appendingPathComponent("about-\(name)-\(dark ? "dark" : "light").png"))
            }
        }
    }

    private func render<V: View>(_ view: V, size: CGSize, dark: Bool, store: AgentsStore, to url: URL) throws {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentView = NSHostingView(rootView: view.environment(store).frame(width: size.width, height: size.height, alignment: .top)
            .background(Palette.paper))
        window.orderFrontRegardless()
        RunLoop.main.run(until: Date().addingTimeInterval(0.5))
        let content = try XCTUnwrap(window.contentView)
        let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
        content.cacheDisplay(in: content.bounds, to: bitmap)
        try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: url)
        window.close()
    }
}
