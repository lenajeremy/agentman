import SwiftUI

/// What is left of each agent's plan, as T3 Code's Limits tab shows it: for
/// each agent, a card per window with the share left, a bar of it in the
/// agent's colour, a mark where spending evenly would have it, and when it
/// resets. The numbers are each agent's own: Claude Code's /usage, Codex's
/// /status.
struct LimitsPage: View {
    let limits: Limits
    /// Now, for how far through each window the plan is; fixed in pictures.
    var now: Date?

    var body: some View {
        TimelineView(.periodic(from: .now, by: 30)) { context in
            let now = now ?? context.date
            VStack(alignment: .leading, spacing: 34) {
                ForEach(limits.agents, id: \.kind) { agent in
                    LimitsSection(agent: agent, now: now)
                }
                Text(LimitsFormat.footnote)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.faint)
            }
        }
    }
}

/// Nothing to show: no agent here has plan limits to read.
struct NoLimits: View {
    var body: some View {
        VStack(spacing: 8) {
            Image(systemName: "gauge.with.dots.needle.33percent")
                .font(.system(size: 26))
                .foregroundStyle(Palette.faint)
            Text("No plan limits to show")
                .font(TextStyle.title)
                .foregroundStyle(Palette.text)
            Text(LimitsFormat.footnote)
                .font(TextStyle.caption)
                .foregroundStyle(Palette.muted)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// One agent: its name and plan, when the numbers were read, and a card for
/// each window, the five hours first.
private struct LimitsSection: View {
    let agent: Limits.Agent
    let now: Date

    private var windows: [Limits.Window] { LimitsFormat.ordered(agent.windows ?? []) }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 8) {
                AgentMark(kind: agent.kind, size: 16)
                Text(AgentName.of(agent.kind))
                    .font(Fonts.sans(14, .semibold))
                    .foregroundStyle(Palette.text)
                if let plan = agent.plan, !plan.isEmpty {
                    Text(plan)
                        .font(Fonts.sans(11, .medium))
                        .foregroundStyle(Palette.textSecondary)
                        .padding(.horizontal, 7)
                        .frame(height: 18)
                        .background(Palette.fill, in: Capsule())
                }
                Spacer(minLength: 12)
                Text(LimitsFormat.checked(agent, now: now))
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
                    .help(agent.lastTurn == true ? LimitsFormat.lastTurnHelp : "")
            }
            if let problem = agent.problem, !problem.isEmpty {
                Text(problem)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
            }
            VStack(spacing: 10) {
                ForEach(windows) { window in
                    LimitCard(window: window, color: Palette.agent(agent.kind), now: now)
                }
            }
        }
    }
}

/// One window: what is left, big, beside a bar of it.
private struct LimitCard: View {
    let window: Limits.Window
    let color: Color
    let now: Date

    var body: some View {
        let left = LimitsFormat.left(window)
        let pace = LimitsFormat.pace(window, now: now)
        HStack(alignment: .center, spacing: 28) {
            VStack(alignment: .leading, spacing: 2) {
                Text(window.label)
                    .font(Fonts.sans(13, .medium))
                    .foregroundStyle(Palette.textSecondary)
                    .lineLimit(1)
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text("\(left)%")
                        .font(Fonts.sans(28, .semibold))
                        .monospacedDigit()
                        .foregroundStyle(left == 0 ? Palette.errorText : Palette.text)
                    Text("left")
                        .font(Fonts.sans(12.5))
                        .foregroundStyle(Palette.muted)
                }
            }
            .frame(width: 150, alignment: .leading)
            VStack(alignment: .leading, spacing: 8) {
                LimitBar(left: left, even: LimitsFormat.evenLeft(window, now: now), color: color)
                    .frame(height: 22)
                HStack(spacing: 6) {
                    if let resets = LimitsFormat.resets(window, now: now) {
                        Image(systemName: "arrow.clockwise")
                            .font(.system(size: 10, weight: .medium))
                        Text(resets)
                    } else {
                        // Extra usage says no reset; an unused window starts
                        // its clock at the next use.
                        Text(left == 100 ? "Not used yet" : " ")
                    }
                    Spacer(minLength: 10)
                    if let pace {
                        Text(pace.title)
                            .foregroundStyle(pace == .ahead ? Palette.needsYouText : Palette.muted)
                            .help(pace.help)
                    }
                }
                .font(Fonts.sans(12))
                .monospacedDigit()
                .foregroundStyle(Palette.muted)
            }
        }
        .padding(.horizontal, 18)
        .padding(.vertical, 15)
        .background(Palette.surface, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Palette.line))
        .accessibilityElement(children: .combine)
    }
}

/// The share left as a bar in the agent's colour, over a track, with a mark
/// where spending evenly through the window would have it.
private struct LimitBar: View {
    let left: Int
    let even: Int?
    let color: Color

    var body: some View {
        GeometryReader { geometry in
            let width = geometry.size.width
            ZStack(alignment: .leading) {
                RoundedRectangle(cornerRadius: 6, style: .continuous)
                    .fill(Palette.fill)
                RoundedRectangle(cornerRadius: 6, style: .continuous)
                    .fill(color.opacity(0.42))
                    .frame(width: width * CGFloat(left) / 100)
                if let even {
                    Capsule()
                        .fill(Palette.text)
                        .frame(width: 2, height: geometry.size.height - 4)
                        .offset(x: min(max(width * CGFloat(even) / 100 - 1, 1), width - 3))
                        .help("Where spending evenly through the window would leave it: \(even)% left")
                }
            }
        }
    }
}

/// How the Limits tab reads a window: what is left, whether the plan is being
/// spent faster than the window passes, and when it resets.
enum LimitsFormat {
    static let footnote = "Read from Claude Code and Codex on this Mac, as their own /usage and /status show. Other agents don't report plan limits."
    static let lastTurnHelp = "Codex's shared daemon isn't running, so these are the numbers Codex wrote down with its last reply."

    enum Pace: Equatable {
        case ahead, on, under

        var title: String {
            switch self {
            case .ahead: "Ahead of pace"
            case .on: "On pace"
            case .under: "Under pace"
            }
        }

        var help: String {
            switch self {
            case .ahead: "Spending faster than the window passes"
            case .on: "Spending about as fast as the window passes"
            case .under: "Headroom left for the rest of the window"
            }
        }
    }

    /// The five hours, then the weeks, then the months, as the daemon sent
    /// them within each.
    static func ordered(_ windows: [Limits.Window]) -> [Limits.Window] {
        let rank = ["session": 0, "weekly": 1, "monthly": 2]
        return windows.enumerated().sorted {
            (rank[$0.element.kind] ?? 3, $0.offset) < (rank[$1.element.kind] ?? 3, $1.offset)
        }.map(\.element)
    }

    /// The share left, a whole percent.
    static func left(_ window: Limits.Window) -> Int {
        Int((100 - min(max(window.used, 0), 100)).rounded())
    }

    /// How far through its window the plan is, 0 to 1, when the window says
    /// how long it is and when it resets.
    static func elapsed(_ window: Limits.Window, now: Date) -> Double? {
        guard let resets = window.resets, let minutes = window.minutes, minutes > 0 else { return nil }
        let length = Double(minutes) * 60
        return min(max((length - resets.timeIntervalSince(now)) / length, 0), 1)
    }

    /// What would be left by now, spending evenly through the window.
    static func evenLeft(_ window: Limits.Window, now: Date) -> Int? {
        elapsed(window, now: now).map { Int(((1 - $0) * 100).rounded()) }
    }

    /// Ahead when more than five points more is used than the window has
    /// passed, under when five fewer: T3's rule.
    static func pace(_ window: Limits.Window, now: Date) -> Pace? {
        guard let elapsed = elapsed(window, now: now) else { return nil }
        let over = window.used - elapsed * 100
        return over > 5 ? .ahead : over < -5 ? .under : .on
    }

    /// "Resets in 3h 12m", "Resets in 4d 2h", and when it is past or all
    /// but, "Resets now".
    static func resets(_ window: Limits.Window, now: Date) -> String? {
        guard let resets = window.resets else { return nil }
        let seconds = resets.timeIntervalSince(now)
        if seconds < 60 { return "Resets now" }
        return "Resets in " + span(seconds)
    }

    /// 4d 2h, 3h 12m, 12m.
    static func span(_ seconds: TimeInterval) -> String {
        let minutes = Int(seconds / 60)
        let days = minutes / 1440, hours = minutes % 1440 / 60, rest = minutes % 60
        if days > 0 { return "\(days)d \(hours)h" }
        if hours > 0 { return "\(hours)h \(rest)m" }
        return "\(rest)m"
    }

    /// When the numbers were read: "Checked just now", "Checked 3m ago", or
    /// for Codex's records, "As of its last reply, 25m ago".
    static func checked(_ agent: Limits.Agent, now: Date) -> String {
        let ago = now.timeIntervalSince(agent.checked)
        let when = ago < 60 ? "just now" : span(ago) + " ago"
        return agent.lastTurn == true ? "As of its last reply, " + when : "Checked " + when
    }
}
