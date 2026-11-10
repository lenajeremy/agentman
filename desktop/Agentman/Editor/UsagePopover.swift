import SwiftUI

/// Usage from the editor's status bar, without leaving the editor: the last
/// 30 days' spend, what is left of each plan, and the way to the full page.
/// Plan limits are read when this opens, and only then: reading Claude's
/// starts Claude Code, which nobody should pay for while coding.
struct UsagePopover: View {
    @Environment(AgentsStore.self) private var agents
    @Environment(\.openWindow) private var openWindow
    let close: () -> Void
    @State private var usage: Usage?
    @State private var limits: Limits?
    @State private var problem: String?
    @State private var reading = true

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(alignment: .firstTextBaseline) {
                Text("Usage")
                    .font(Fonts.heading(14))
                    .foregroundStyle(Palette.text)
                Spacer()
                if let usage {
                    Text("\(UsageFormat.money(usage.cost.total)) in 30 days")
                        .font(TextStyle.caption)
                        .monospacedDigit()
                        .foregroundStyle(Palette.muted)
                }
            }
            Group {
                if let limits, !limits.agents.isEmpty {
                    PopoverLimits(limits: limits)
                } else if reading {
                    HStack(spacing: 8) {
                        Loader(size: 12)
                        Text("Reading what is left of each plan…")
                    }
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
                    .frame(maxWidth: .infinity, minHeight: 80)
                } else {
                    Text(problem ?? "No plan limits to show. " + LimitsFormat.footnote)
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            Divider()
            Button {
                close()
                openWindow(id: "main")
                agents.selection = .usage
            } label: {
                HStack(spacing: 5) {
                    Text("Open Usage")
                    Image(systemName: "arrow.up.right").font(.system(size: 9, weight: .semibold))
                }
            }
            .buttonStyle(.link)
            .font(TextStyle.caption)
        }
        .padding(16)
        // Wide enough for each window's card to say when it resets and how
        // it is pacing in full.
        .frame(width: 560)
        .task { await read() }
    }

    private func read() async {
        reading = true
        defer { reading = false }
        async let spent = try? agents.usage(days: 30)
        do {
            limits = try await agents.limits(force: false)
        } catch is CancellationError {
        } catch {
            problem = error.localizedDescription
        }
        usage = await spent
    }
}

/// What is left of each plan, in a popover: only as tall as it needs, and
/// scrolling once that is more than the screen has room for. With several
/// agents it used to run off the top of the screen, with no way to reach it.
struct PopoverLimits: View {
    let limits: Limits
    var now: Date?
    /// How tall the cards are. A scroll view asks a popover for next to no
    /// height of its own, and the popover gives it just that: a sliver with
    /// half a card in it. Measured, the cards get their own height, up to
    /// the most the screen allows, and scroll past that.
    @State private var height: CGFloat?

    /// The most it may take of a screen this tall, leaving room for the
    /// popover's heading and link, and the status bar it hangs from.
    static func tallest(screen: CGFloat) -> CGFloat { max(screen - 200, 260) }

    var body: some View {
        let tallest = Self.tallest(screen: NSScreen.main?.visibleFrame.height ?? 800)
        ScrollView {
            LimitsPage(limits: limits, now: now)
                // The cards' shadows, which fall to the right and below.
                .padding(.trailing, 4)
                .padding(.bottom, 4)
                .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { height = $0 }
        }
        .scrollBounceBehavior(.basedOnSize)
        .frame(height: height.map { min($0, tallest) })
    }
}
