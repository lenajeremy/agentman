import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// The usage page: the daemon's summary read, its numbers written as the
/// page shows them, and pictures of it. The pictures run with
/// TEST_RUNNER_AGENTMAN_SHOTS=<folder> set, from the summaries in
/// TEST_RUNNER_AGENTMAN_SHOT_USAGE (usage-<days>.json) when it names a
/// folder, else from a made-up one.
@MainActor
final class UsageTests: XCTestCase {
    static let sample = """
    {"type":"usage","usage":{"days":7,"sessions":12,
     "tokens":{"input":1200,"cacheRead":3400000,"cacheWrite":56000,"output":7800},
     "cost":{"input":0.01,"cacheRead":1.7,"cacheWrite":0.35,"output":0.2},"cacheSavings":15.3,
     "agents":[{"kind":"claude","sessions":9,"tokens":{"input":1000,"cacheRead":3000000,"cacheWrite":56000,"output":7000},"cost":2.1},
               {"kind":"codex","sessions":3,"tokens":{"input":200,"cacheRead":400000,"cacheWrite":0,"output":800},"cost":0.16}],
     "buckets":[{"start":1761868800000,"cost":{"claude":1.5,"codex":0.1},"tokens":{"claude":2000000,"codex":300000}},
                {"start":1761955200000}],
     "models":[{"model":"claude-opus-5-5","kind":"claude","tokens":{"input":1000,"cacheRead":3000000,"cacheWrite":56000,"output":7000},"cost":2.1,"priced":true},
               {"model":"gpt-6-sol","kind":"codex","tokens":{"input":200,"cacheRead":400000,"cacheWrite":0,"output":800},"cost":0,"priced":false}],
     "skipped":2}}
    """

    func testReadsTheDaemonsSummary() throws {
        let event = try JSONDecoder().decode(DaemonEvent.self, from: Data(Self.sample.utf8))
        let usage = try XCTUnwrap(event.usage)
        XCTAssertEqual(usage.days, 7)
        XCTAssertEqual(usage.tokens.total, 1200 + 3_400_000 + 56000 + 7800)
        XCTAssertEqual(usage.cost.total, 2.26, accuracy: 1e-9)
        XCTAssertEqual(usage.agents?.map(\.kind), ["claude", "codex"])
        XCTAssertEqual(usage.buckets?.first?.cost?["claude"], 1.5)
        XCTAssertNil(usage.buckets?.last?.cost, "an empty day leaves its maps out")
        XCTAssertEqual(usage.buckets?.first?.date, Date(timeIntervalSince1970: 1_761_868_800))
        XCTAssertEqual(usage.models?.last?.priced, false)
        XCTAssertEqual(usage.skipped, 2)
    }

    func testAskingSaysHowManyDays() throws {
        var request = DaemonRequest(.usage)
        request.days = 30
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(request)) as? [String: Any])
        XCTAssertEqual(json["type"] as? String, "usage")
        XCTAssertEqual(json["days"] as? Int, 30)
    }

    func testNumbersAreWrittenAsThePageShowsThem() {
        XCTAssertEqual(UsageFormat.money(13021.987), "$13,021.99")
        XCTAssertEqual(UsageFormat.money(0), "$0.00")
        XCTAssertEqual(UsageFormat.money(0.004), "<$0.01")
        XCTAssertEqual(UsageFormat.axisMoney(600, step: 200), "$600")
        XCTAssertEqual(UsageFormat.axisMoney(0.5, step: 0.5), "$0.50")
        XCTAssertEqual(UsageFormat.axisMoney(5, step: 2.5), "$5.00", "steps of $2.50 keep their cents")

        XCTAssertEqual(UsageFormat.tokens(0), "0")
        XCTAssertEqual(UsageFormat.tokens(950), "950")
        XCTAssertEqual(UsageFormat.tokens(12_600), "12.6K")
        XCTAssertEqual(UsageFormat.tokens(410_000_000), "410M")
        XCTAssertEqual(UsageFormat.tokens(2_301_000_000), "2.30B")
        XCTAssertEqual(UsageFormat.tokens(32_400_000_000), "32.4B")
        XCTAssertEqual(UsageFormat.tokens(999_600_000), "1.00B", "rounds up into the next unit, not 1000M")
        XCTAssertEqual(UsageFormat.tokens(999_400_000), "999M")

        XCTAssertEqual(UsageFormat.percent(0), "0%")
        XCTAssertEqual(UsageFormat.percent(0.0003), "<0.1%")
        XCTAssertEqual(UsageFormat.percent(0.077), "7.7%")
        XCTAssertEqual(UsageFormat.sessions(1), "1 session")
        XCTAssertEqual(UsageFormat.sessions(1694), "1,694 sessions")
    }

    // Weeks and months are named as such; a daemon that predates units is
    // read by its hours.
    func testLongerRangesAreNamedByTheirBuckets() throws {
        var usage = try XCTUnwrap(JSONDecoder().decode(DaemonEvent.self, from: Data(Self.sample.utf8)).usage)
        XCTAssertEqual(UsageUnit(usage), .day)
        usage.hourly = true
        XCTAssertEqual(UsageUnit(usage), .hour)
        usage.unit = "week"
        XCTAssertEqual(UsageUnit(usage), .week)
        let monday = try XCTUnwrap(Calendar.current.date(from: DateComponents(year: 2026, month: 10, day: 26)))
        XCTAssertEqual(UsageUnit.week.adjective, "Weekly")
        XCTAssertEqual(UsageUnit.week.noun, "Week")
        XCTAssertTrue(UsageUnit.week.moment(monday).hasPrefix("Week of "))
        XCTAssertTrue(UsageUnit.month.row(monday).contains("2026"))
        XCTAssertTrue(UsageUnit.week.axis(monday, across: 364 * 86_400).contains("2026"), "a year's chart names years")
        XCTAssertFalse(UsageUnit.day.axis(monday, across: 89 * 86_400).contains("2026"))
        XCTAssertEqual(UsageView.ranges.map(UsageView.rangeTitle), ["24h", "7d", "30d", "90d", "6m", "1y", "All"])
    }

    func testAgentmanUsageLinkOpensThePage() throws {
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-usage.sock"))
        store.open(try XCTUnwrap(URL(string: "agentman://usage")))
        XCTAssertEqual(store.selection, .usage)
    }

    // MARK: Pictures

    private func summaries() throws -> [Int: Usage] {
        var out: [Int: Usage] = [:]
        if let folder = ProcessInfo.processInfo.environment["AGENTMAN_SHOT_USAGE"] {
            for days in UsageView.ranges {
                let url = URL(fileURLWithPath: folder).appendingPathComponent("usage-\(days).json")
                if let data = try? Data(contentsOf: url) {
                    out[days] = try JSONDecoder().decode(Usage.self, from: data)
                }
            }
        }
        if out.isEmpty {
            out[30] = try XCTUnwrap(JSONDecoder().decode(DaemonEvent.self, from: Data(Self.sample.utf8)).usage)
        }
        return out
    }

    func testRenderUsage() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let folder = URL(fileURLWithPath: out)
        let summaries = try summaries()
        let defaults = UserDefaults.standard
        defer { ["usageDays", "usageMetric", "usageBreakdown"].forEach(defaults.removeObject) }
        for dark in [false, true] {
            let look = dark ? "dark" : "light"
            for (days, metric) in [(30, "cost"), (90, "tokens"), (1, "cost"), (180, "cost"), (365, "cost"), (0, "tokens")] where summaries[days] != nil {
                defaults.set(days, forKey: "usageDays")
                defaults.set(metric, forKey: "usageMetric")
                try render(UsageView(showing: summaries).background(Palette.paper), size: CGSize(width: 1240, height: 860), dark: dark,
                           to: folder.appendingPathComponent("usage-\(days)-\(metric)-\(look).png"))
            }
            // The whole page, scrolled through, with the chart pointed at.
            if let days = [90, 30].first(where: { summaries[$0] != nil }), let usage = summaries[days] {
                for (metric, breakdown) in [(UsageMetric.cost, UsageBreakdown.model), (.tokens, .day)] {
                    let page = UsagePage(usage: usage, metric: metric, breakdown: .constant(breakdown), width: 1080,
                                         pointing: (usage.buckets?.count ?? 0) * 2 / 3)
                        .padding(36)
                        .frame(width: 1152, alignment: .top)
                        .background(Palette.paper)
                    try render(page, size: CGSize(width: 1152, height: 2000), dark: dark,
                               to: folder.appendingPathComponent("usage-page-\(metric.rawValue)-\(look).png"))
                }
                // Narrow: the chart under the totals.
                let narrow = UsagePage(usage: usage, metric: .cost, breakdown: .constant(.model), width: 620)
                    .padding(28)
                    .frame(width: 676, alignment: .top)
                    .background(Palette.paper)
                try render(narrow, size: CGSize(width: 676, height: 1500), dark: dark,
                           to: folder.appendingPathComponent("usage-narrow-\(look).png"))
            }
        }
    }

    private func render<V: View>(_ view: V, size: CGSize, dark: Bool, to url: URL) throws {
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-usage.sock"))
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentView = NSHostingView(rootView: view.environment(store).frame(width: size.width, height: size.height, alignment: .top))
        window.orderFrontRegardless()
        RunLoop.main.run(until: Date().addingTimeInterval(1.0))
        let content = try XCTUnwrap(window.contentView)
        let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
        content.cacheDisplay(in: content.bounds, to: bitmap)
        try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: url)
        window.close()
    }
}
