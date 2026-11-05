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
                    .font(Fonts.sans(14, .semibold))
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
                    LimitsPage(limits: limits)
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
