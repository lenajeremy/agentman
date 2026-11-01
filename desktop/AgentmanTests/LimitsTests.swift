import AppKit
import SwiftUI
import XCTest
@testable import Agentman

/// The Limits tab: the daemon's answer read, each window's pace and reset
/// written as the tab shows them, and pictures of it, which run with
/// TEST_RUNNER_AGENTMAN_SHOTS=<folder> set.
@MainActor
final class LimitsTests: XCTestCase {
    /// An answer as the daemon gives it, its resets measured from now.
    static func sample(now: Date = Date()) -> String {
        func at(_ hours: Double) -> Int64 { Int64((now.timeIntervalSince1970 + hours * 3600) * 1000) }
        let checked = Int64(now.timeIntervalSince1970 * 1000)
        return """
        {"type":"limits","limits":{"agents":[
         {"kind":"claude","plan":"Max","checkedAt":\(checked - 90_000),"windows":[
          {"id":"five_hour","kind":"session","label":"Session","used":38,"resetsAt":\(at(2.5)),"minutes":300},
          {"id":"seven_day","kind":"weekly","label":"Weekly","used":56,"resetsAt":\(at(5 * 24 + 16)),"minutes":10080},
          {"id":"seven_day_fable","kind":"weekly","label":"Weekly · Fable","used":4,"resetsAt":\(at(5 * 24 + 16)),"minutes":10080},
          {"id":"extra_usage","kind":"monthly","label":"Extra usage","used":25}]},
         {"kind":"codex","plan":"Plus","checkedAt":\(checked - 25 * 60_000),"lastTurn":true,"windows":[
          {"id":"secondary","kind":"weekly","label":"Weekly","used":85,"resetsAt":\(at(2 * 24 + 2)),"minutes":10080},
          {"id":"primary","kind":"session","label":"Session","used":100,"resetsAt":\(at(1.2)),"minutes":300}]}
        ]}}
        """
    }

    private func limits(_ text: String = sample()) throws -> Limits {
        try XCTUnwrap(JSONDecoder().decode(DaemonEvent.self, from: Data(text.utf8)).limits)
    }

    func testReadsTheDaemonsAnswer() throws {
        let limits = try limits()
        XCTAssertEqual(limits.agents.map(\.kind), ["claude", "codex"])
        let claude = limits.agents[0], codex = limits.agents[1]
        XCTAssertEqual(claude.plan, "Max")
        XCTAssertNil(claude.lastTurn)
        XCTAssertEqual(claude.windows?.count, 4)
        XCTAssertNil(claude.windows?.last?.resets, "a window that doesn't say when it resets")
        XCTAssertEqual(codex.lastTurn, true)
        XCTAssertEqual(LimitsFormat.ordered(codex.windows ?? []).map(\.id), ["primary", "secondary"], "the five hours first")

        let bare = try self.limits(#"{"type":"limits","limits":{"agents":[{"kind":"claude","checkedAt":1,"problem":"No plan."}]}}"#)
        XCTAssertNil(bare.agents[0].windows)
        XCTAssertEqual(bare.agents[0].problem, "No plan.")
    }

    func testAskingSaysWhetherToReadAgain() throws {
        var request = DaemonRequest(.limits)
        request.force = true
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(request)) as? [String: Any])
        XCTAssertEqual(json["type"] as? String, "limits")
        XCTAssertEqual(json["force"] as? Bool, true)
    }

    // Pace as T3 has it: five points either side of how far through the
    // window the plan is.
    func testPaceAndResets() {
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        func window(used: Double, hoursLeft: Double, minutes: Int? = 300) -> Limits.Window {
            Limits.Window(id: "w", kind: "session", label: "Session", used: used,
                          resetsAt: Int64((now.timeIntervalSince1970 + hoursLeft * 3600) * 1000), minutes: minutes)
        }
        // Halfway through five hours.
        XCTAssertEqual(LimitsFormat.elapsed(window(used: 0, hoursLeft: 2.5), now: now) ?? -1, 0.5, accuracy: 1e-9)
        XCTAssertEqual(LimitsFormat.evenLeft(window(used: 0, hoursLeft: 2.5), now: now), 50)
        XCTAssertEqual(LimitsFormat.pace(window(used: 56, hoursLeft: 2.5), now: now), .ahead)
        XCTAssertEqual(LimitsFormat.pace(window(used: 54, hoursLeft: 2.5), now: now), .on)
        XCTAssertEqual(LimitsFormat.pace(window(used: 44, hoursLeft: 2.5), now: now), .under)
        XCTAssertNil(LimitsFormat.pace(window(used: 44, hoursLeft: 2.5, minutes: nil), now: now), "no length, no pace")

        XCTAssertEqual(LimitsFormat.left(window(used: 37.6, hoursLeft: 1)), 62)
        XCTAssertEqual(LimitsFormat.left(window(used: 140, hoursLeft: 1)), 0)
        XCTAssertEqual(LimitsFormat.resets(window(used: 0, hoursLeft: 3.2), now: now), "Resets in 3h 12m")
        XCTAssertEqual(LimitsFormat.resets(window(used: 0, hoursLeft: 98), now: now), "Resets in 4d 2h")
        XCTAssertEqual(LimitsFormat.resets(window(used: 0, hoursLeft: 0.2), now: now), "Resets in 12m")
        XCTAssertEqual(LimitsFormat.resets(window(used: 0, hoursLeft: -1), now: now), "Resets now")

        let claude = Limits.Agent(kind: "claude", checkedAt: Int64((now.timeIntervalSince1970 - 180) * 1000))
        XCTAssertEqual(LimitsFormat.checked(claude, now: now), "Checked 3m ago")
        var codex = Limits.Agent(kind: "codex", checkedAt: Int64(now.timeIntervalSince1970 * 1000), lastTurn: true)
        XCTAssertEqual(LimitsFormat.checked(codex, now: now), "As of its last reply, just now")
        codex.checkedAt -= 2 * 3_600_000
        XCTAssertEqual(LimitsFormat.checked(codex, now: now), "As of its last reply, 2h 0m ago")
    }

    // MARK: Pictures

    func testRenderLimits() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let folder = URL(fileURLWithPath: out)
        let limits = try limits()
        let usage = try XCTUnwrap(JSONDecoder().decode(DaemonEvent.self, from: Data(UsageTests.sample.utf8)).usage)
        let defaults = UserDefaults.standard
        defer { ["usageDays", "usageMetric"].forEach(defaults.removeObject) }
        defaults.set(30, forKey: "usageDays")
        defaults.set("limits", forKey: "usageMetric")
        for dark in [false, true] {
            let look = dark ? "dark" : "light"
            try render(UsageView(showing: [30: usage], limits: limits).background(Palette.paper),
                       size: CGSize(width: 1240, height: 980), dark: dark,
                       to: folder.appendingPathComponent("limits-\(look).png"))
            try render(UsageView(showing: [30: usage], limits: limits).background(Palette.paper),
                       size: CGSize(width: 760, height: 1100), dark: dark,
                       to: folder.appendingPathComponent("limits-narrow-\(look).png"))
            let none = Limits(agents: [])
            try render(UsageView(showing: [30: usage], limits: none).background(Palette.paper),
                       size: CGSize(width: 1000, height: 520), dark: dark,
                       to: folder.appendingPathComponent("limits-none-\(look).png"))
            let failing = try self.limits(#"""
            {"type":"limits","limits":{"agents":[{"kind":"claude","checkedAt":\#(Int64(Date().timeIntervalSince1970 * 1000)),
              "problem":"Claude Code isn't signed in to a Claude plan, so there are no plan limits."},
             {"kind":"codex","checkedAt":\#(Int64(Date().timeIntervalSince1970 * 1000)),
              "problem":"Codex hasn't said what is left of its plan in the past week. It does with each reply."}]}}
            """#)
            try render(UsageView(showing: [30: usage], limits: failing).background(Palette.paper),
                       size: CGSize(width: 1000, height: 420), dark: dark,
                       to: folder.appendingPathComponent("limits-problems-\(look).png"))
        }
        // The other tabs keep their switch of ranges beside the new one, in
        // a window as narrow as the page gets beside the sidebar too.
        defaults.set("cost", forKey: "usageMetric")
        try render(UsageView(showing: [30: usage], limits: limits).background(Palette.paper),
                   size: CGSize(width: 1240, height: 400), dark: false,
                   to: folder.appendingPathComponent("limits-cost-header-light.png"))
        try render(UsageView(showing: [30: usage], limits: limits).background(Palette.paper),
                   size: CGSize(width: 640, height: 300), dark: false,
                   to: folder.appendingPathComponent("limits-cost-header-narrow-light.png"))
    }

    private func render<V: View>(_ view: V, size: CGSize, dark: Bool, to url: URL) throws {
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-limits.sock"))
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
