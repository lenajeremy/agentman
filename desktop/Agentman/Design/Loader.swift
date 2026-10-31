import SwiftUI

/// What an agent shows while it works: a ring chasing round, its tail
/// fading and its length breathing, its colours flowing along it. Each
/// colour rests on the whole ring a moment, then the next comes in at the
/// head and spreads back to the tail. Still when the Mac is asked to
/// reduce motion.
struct Loader: View {
    var size: CGFloat = 11
    var colors = Palette.loader
    /// A fixed moment instead of the clock, for pictures of it.
    var at: TimeInterval?
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    /// Seconds each colour has before the next has taken over.
    static let hold = 1.2
    /// How much of a turn from one colour to the next the ring spans at once.
    private static let spread = 0.35
    /// The part of its turn a colour rests before the next comes in.
    private static let rest = 0.6

    var body: some View {
        TimelineView(.animation(paused: reduceMotion || at != nil)) { context in
            ring(at ?? context.date.timeIntervalSinceReferenceDate)
        }
        .frame(width: size, height: size)
        .accessibilityElement()
        .accessibilityLabel("Working")
    }

    private func ring(_ time: TimeInterval) -> some View {
        let width = max(1.6, size * 0.18)
        // The tail starts a little in, so its round cap stays in the fade.
        let start = 0.04
        let end = start + 0.5 + 0.22 * sin(time * .pi * 2 / 1.44)
        let turn = (time * 400).truncatingRemainder(dividingBy: 360)
        return Circle()
            .trim(from: start, to: end)
            .stroke(AngularGradient(stops: stops(time), center: .center, startAngle: .zero, endAngle: .degrees(360 * end)),
                    style: StrokeStyle(lineWidth: width, lineCap: .round))
            .rotationEffect(.degrees(turn))
            .padding(width / 2)
    }

    /// The colours from tail to head at a moment; the head is ahead.
    private func stops(_ time: TimeInterval) -> [Gradient.Stop] {
        let samples = 12
        return (0...samples).map { index in
            let along = Double(index) / Double(samples)
            let color = color(time / Self.hold + along * Self.spread)
            return .init(color: color.opacity(min(1, along / 0.25)), location: along)
        }
    }

    private func color(_ phase: Double) -> Color {
        guard colors.count > 1 else { return colors.first ?? Palette.working }
        let step = phase.rounded(.down)
        let index = Int(step) % colors.count
        let x = min(1, max(0, (phase - step - Self.rest) / (1 - Self.rest)))
        return colors[index].mix(with: colors[(index + 1) % colors.count], by: x * x * (3 - 2 * x))
    }
}
