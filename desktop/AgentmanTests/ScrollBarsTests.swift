import AppKit
import XCTest
@testable import Agentman

final class ScrollBarsTests: XCTestCase {
    func testThinBarsUnlessTheSystemSaysAlways() {
        XCTAssertEqual(ScrollBars.arguments([:], system: nil)?[ScrollBars.key] as? String, "WhenScrolling")
        XCTAssertEqual(ScrollBars.arguments([:], system: "Automatic")?[ScrollBars.key] as? String, "WhenScrolling")
        XCTAssertEqual(ScrollBars.arguments([:], system: "WhenScrolling")?[ScrollBars.key] as? String, "WhenScrolling")
        XCTAssertNil(ScrollBars.arguments([:], system: "Always"))
    }

    func testLaunchArgumentsKept() {
        let changed = ScrollBars.arguments(["NSQuitAlwaysKeepsWindows": false], system: nil)
        XCTAssertEqual(changed?["NSQuitAlwaysKeepsWindows"] as? Bool, false)
        // One given at launch is left as it is.
        XCTAssertNil(ScrollBars.arguments([ScrollBars.key: "Always"], system: nil))
    }

    /// The app asked at launch, so scroll views are thin here unless this
    /// Mac's own setting is "Always".
    func testTheAppsScrollViewsAreThin() {
        let system = UserDefaults.standard.persistentDomain(forName: UserDefaults.globalDomain)?[ScrollBars.key] as? String
        let expected: NSScroller.Style = system == "Always" ? .legacy : .overlay
        XCTAssertEqual(NSScroller.preferredScrollerStyle, expected)
        XCTAssertEqual(NSScrollView(frame: NSRect(x: 0, y: 0, width: 200, height: 200)).scrollerStyle, expected)
    }
}
