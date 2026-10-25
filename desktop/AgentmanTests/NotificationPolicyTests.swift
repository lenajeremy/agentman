import XCTest
@testable import Agentman

final class NotificationPolicyTests: XCTestCase {
    private var defaults: UserDefaults!
    private let suite = "NotificationPolicyTests"

    override func setUp() {
        defaults = UserDefaults(suiteName: suite)
        defaults.removePersistentDomain(forName: suite)
    }

    override func tearDown() {
        defaults.removePersistentDomain(forName: suite)
    }

    private func at(_ hour: Int, _ minute: Int = 0) -> Date {
        Calendar.current.date(bySettingHour: hour, minute: minute, second: 0, of: Date())!
    }

    func testEverythingIsOnUntilSwitchedOff() {
        XCTAssertTrue(NotificationPolicy.allows(.finished, defaults: defaults))
        XCTAssertTrue(NotificationPolicy.allows(.agentman, defaults: defaults))
        defaults.set(false, forKey: NotificationPolicy.Key.finished)
        XCTAssertFalse(NotificationPolicy.allows(.finished, defaults: defaults))
        XCTAssertTrue(NotificationPolicy.allows(.needsYou, defaults: defaults))
    }

    func testAMutedFolderKeepsItsAgentsAndThoseBelowQuiet() {
        NotificationPolicy.setMuted("/work/app", true, defaults: defaults)
        XCTAssertFalse(NotificationPolicy.allows(.finished, folder: "/work/app", defaults: defaults))
        XCTAssertFalse(NotificationPolicy.allows(.needsYou, folder: "/work/app/mobile", defaults: defaults))
        XCTAssertTrue(NotificationPolicy.allows(.finished, folder: "/work/application", defaults: defaults))
        NotificationPolicy.setMuted("/work/app", false, defaults: defaults)
        XCTAssertTrue(NotificationPolicy.allows(.finished, folder: "/work/app", defaults: defaults))
    }

    func testQuietHoursRunPastMidnightAndLetUrgentThrough() {
        defaults.set(true, forKey: NotificationPolicy.Key.quiet)
        defaults.set(22 * 60, forKey: NotificationPolicy.Key.quietStart)
        defaults.set(8 * 60, forKey: NotificationPolicy.Key.quietEnd)
        XCTAssertFalse(NotificationPolicy.allows(.finished, at: at(23), defaults: defaults))
        XCTAssertFalse(NotificationPolicy.allows(.agentman, at: at(3), defaults: defaults))
        XCTAssertTrue(NotificationPolicy.allows(.needsYou, at: at(3), defaults: defaults))
        XCTAssertTrue(NotificationPolicy.allows(.finished, at: at(9), defaults: defaults))
        defaults.set(false, forKey: NotificationPolicy.Key.quietLetsNeedsYou)
        XCTAssertFalse(NotificationPolicy.allows(.needsYou, at: at(3), defaults: defaults))
    }

    func testDaytimeQuietHours() {
        defaults.set(true, forKey: NotificationPolicy.Key.quiet)
        defaults.set(13 * 60, forKey: NotificationPolicy.Key.quietStart)
        defaults.set(14 * 60 + 30, forKey: NotificationPolicy.Key.quietEnd)
        XCTAssertTrue(NotificationPolicy.inQuietHours(at: at(14), defaults: defaults))
        XCTAssertFalse(NotificationPolicy.inQuietHours(at: at(14, 30), defaults: defaults))
        XCTAssertFalse(NotificationPolicy.inQuietHours(at: at(12, 59), defaults: defaults))
    }
}
