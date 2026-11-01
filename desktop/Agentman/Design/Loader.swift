import AppKit
import QuartzCore
import SwiftUI

/// What an agent shows while it works: a ring chasing round, its tail
/// fading and its length breathing, its colours flowing along it. Each
/// colour rests on the whole ring a moment, then the next comes in at the
/// head and spreads back to the tail. Still when the Mac is asked to
/// reduce motion.
///
/// Core Animation moves it: its turn, its breathing and its colours are
/// animations handed to the window server once, so it costs the app nothing
/// from one frame to the next, and nothing at all while the window is
/// hidden or covered. Drawn by SwiftUI at every frame, its colours resolved
/// from the palette each time and its gradient painted on the main thread,
/// a few agents working took most of the app's CPU. A still one, for a
/// picture or with reduced motion, is drawn by SwiftUI from the same
/// figures.
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
    /// The tail starts a little in, so its round cap stays in the fade.
    static let start = 0.04
    /// How fast it turns, and so the seconds for one turn; the seconds for
    /// one breath.
    private static let degreesPerSecond = 400.0
    static let turnPeriod = 360 / degreesPerSecond
    static let breathPeriod = 1.44
    /// How many points along the ring its colours are given at.
    static let samples = 12

    var body: some View {
        Group {
            if at == nil, !reduceMotion {
                LoaderLayers(size: size, colors: colors)
            } else {
                TimelineView(.animation(paused: true)) { context in
                    ring(at ?? context.date.timeIntervalSinceReferenceDate)
                }
            }
        }
        .frame(width: size, height: size)
        .accessibilityElement()
        .accessibilityLabel("Working")
    }

    private func ring(_ time: TimeInterval) -> some View {
        let width = Self.lineWidth(size)
        let end = Self.end(time)
        return Circle()
            .trim(from: Self.start, to: end)
            .stroke(AngularGradient(stops: Self.stops(time, colors: colors), center: .center, startAngle: .zero, endAngle: .degrees(360 * end)),
                    style: StrokeStyle(lineWidth: width, lineCap: .round))
            .rotationEffect(.degrees(Self.turn(time)))
            .padding(width / 2)
    }

    static func lineWidth(_ size: CGFloat) -> CGFloat { max(1.6, size * 0.18) }

    /// Where the head is, as a part of the whole circle.
    static func end(_ time: TimeInterval) -> Double {
        start + 0.5 + 0.22 * sin(time * .pi * 2 / breathPeriod)
    }

    /// How far round the ring has turned, in degrees.
    static func turn(_ time: TimeInterval) -> Double {
        (time * degreesPerSecond).truncatingRemainder(dividingBy: 360)
    }

    /// The colours from tail to head at a moment; the head is ahead.
    static func stops(_ time: TimeInterval, colors: [Color]) -> [Gradient.Stop] {
        (0...samples).map { index in
            let along = Double(index) / Double(samples)
            let color = color(time / hold + along * spread, colors: colors)
            return .init(color: color.opacity(min(1, along / 0.25)), location: along)
        }
    }

    private static func color(_ phase: Double, colors: [Color]) -> Color {
        guard colors.count > 1 else { return colors.first ?? Palette.working }
        let step = phase.rounded(.down)
        let index = Int(step) % colors.count
        let x = min(1, max(0, (phase - step - rest) / (1 - rest)))
        return colors[index].mix(with: colors[(index + 1) % colors.count], by: x * x * (3 - 2 * x))
    }
}

/// The moving ring, in a view of Core Animation layers.
private struct LoaderLayers: NSViewRepresentable {
    let size: CGFloat
    let colors: [Color]

    func makeNSView(context: Context) -> LoaderView { LoaderView() }

    func updateNSView(_ view: LoaderView, context: Context) {
        view.ring.show(size: size, colors: colors, in: context.environment)
    }
}

/// Holds a LoaderLayer, at the window's scale, out of the way of clicks.
final class LoaderView: NSView {
    let ring = LoaderLayer()

    init() {
        super.init(frame: .zero)
        // Hosting a layer of its own, which AppKit leaves alone.
        layer = ring
        wantsLayer = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("init(coder:) is not used") }

    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    override func isAccessibilityElement() -> Bool { false }

    override func viewDidChangeBackingProperties() {
        super.viewDidChangeBackingProperties()
        ring.scale = window?.backingScaleFactor ?? 2
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        ring.scale = window?.backingScaleFactor ?? 2
    }
}

/// The Loader's ring as layers: a conic gradient, masked to the arc of a
/// circle, in a layer that turns. Each motion is an animation that repeats
/// on its own period, in step with the clock as the drawn ring is, so
/// every loader on screen turns together.
final class LoaderLayer: CALayer {
    private let spin = CALayer()
    private let gradient = CAGradientLayer()
    private let arc = CAShapeLayer()
    private var shown: (size: CGFloat, colors: [Color.Resolved])?
    /// The colours it shows in, as resolved for its appearance.
    var colors: [Color.Resolved] { shown?.colors ?? [] }

    /// The screen's pixels to a point, for the arc and gradient to be drawn at.
    var scale: CGFloat = 2 {
        didSet {
            guard scale != oldValue else { return }
            for layer in [self, spin, gradient, arc] as [CALayer] { layer.contentsScale = scale }
        }
    }

    override init() {
        super.init()
        // Laid out as SwiftUI draws: y down, angles clockwise from three
        // o'clock.
        isGeometryFlipped = true
        gradient.type = .conic
        gradient.startPoint = CGPoint(x: 0.5, y: 0.5)
        gradient.endPoint = CGPoint(x: 1, y: 0.5)
        arc.fillColor = nil
        arc.strokeColor = CGColor(gray: 0, alpha: 1)
        arc.lineCap = .round
        arc.strokeStart = Loader.start
        gradient.mask = arc
        spin.addSublayer(gradient)
        addSublayer(spin)
        for layer in [self, spin, gradient, arc] as [CALayer] { layer.contentsScale = scale }
    }

    override init(layer: Any) {
        super.init(layer: layer)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("init(coder:) is not used") }

    /// Shows the ring at a size, in colours as they are in an environment:
    /// made again only when either changed, as with the appearance. Tests
    /// give it a clock of their own: a moment in media time and the
    /// reference date's time then.
    @MainActor func show(size: CGFloat, colors: [Color], in environment: EnvironmentValues,
              clock: (media: CFTimeInterval, reference: TimeInterval)? = nil) {
        let resolved = colors.map { $0.resolve(in: environment) }
        guard shown?.size != size || shown?.colors != resolved else { return }
        shown = (size, resolved)
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        let width = Loader.lineWidth(size)
        let bounds = CGRect(x: 0, y: 0, width: size, height: size)
        self.bounds = bounds
        spin.frame = bounds
        gradient.frame = bounds
        arc.frame = bounds
        arc.lineWidth = width
        arc.path = CGPath(ellipseIn: bounds.insetBy(dx: width / 2, dy: width / 2), transform: nil)
        animate(Self.frames(resolved, in: environment), period: Loader.hold * Double(max(1, resolved.count)),
                clock: clock ?? (CACurrentMediaTime(), Date.timeIntervalSinceReferenceDate))
        CATransaction.commit()
    }

    /// The three motions, each repeating on its own period: the turn, the
    /// breathing of the arc and its gradient with it, and the colours
    /// flowing, whose round is `period`.
    private func animate(_ colors: [[CGColor]], period: Double, clock: (media: CFTimeInterval, reference: TimeInterval)) {
        let fps = Self.breathsPerSecond
        // Begun as long before now as the clock is into each period, so the
        // ring is where the drawn one would be.
        func repeating(_ animation: CAAnimation, period: Double) -> CAAnimation {
            animation.duration = period
            animation.repeatCount = .infinity
            animation.isRemovedOnCompletion = false
            animation.beginTime = clock.media - clock.reference.truncatingRemainder(dividingBy: period)
            return animation
        }

        let turn = CABasicAnimation(keyPath: "transform.rotation.z")
        turn.fromValue = 0
        turn.toValue = Double.pi * 2
        turn.timingFunction = CAMediaTimingFunction(name: .linear)
        spin.add(repeating(turn, period: Loader.turnPeriod), forKey: "turn")

        let breaths = (0...Int((Loader.breathPeriod * fps).rounded())).map { Double($0) / fps }
        let ends = breaths.map { Loader.end($0) }
        let head = CAKeyframeAnimation(keyPath: "strokeEnd")
        head.values = ends
        arc.add(repeating(head, period: Loader.breathPeriod), forKey: "breath")
        // The gradient spans the arc, tail to head, as it breathes.
        let spans = CAKeyframeAnimation(keyPath: "locations")
        spans.values = ends.map { end in
            (0...Loader.samples).map { NSNumber(value: Double($0) / Double(Loader.samples) * end) }
        }
        gradient.add(repeating(spans, period: Loader.breathPeriod), forKey: "breath")

        let flow = CAKeyframeAnimation(keyPath: "colors")
        flow.values = colors
        gradient.add(repeating(flow, period: period), forKey: "colors")
        // Still values under the animations, for anything that reads them.
        arc.strokeEnd = ends[0]
        gradient.locations = spans.values?.first as? [NSNumber]
        gradient.colors = colors.first
    }

    /// Frames a second the breathing and the colours are given at; the
    /// window server goes in straight lines from one to the next, which on
    /// curves this gentle is within a fraction of a degree and of a shade.
    /// Each frame is work for every ring that appears: the colours most, as
    /// each is converted for the screen, and a new step in a conversation
    /// brings a new ring. At 60 a second, that was most of what a step cost.
    static let breathsPerSecond = 30.0
    static let coloursPerSecond = 20.0

    /// The stops' colours through one round of every colour, a frame at a
    /// time; worked out once for each set of colours, as resolving and
    /// mixing them was most of what drawing the ring cost.
    @MainActor private static var made: [[Color.Resolved]: [[CGColor]]] = [:]

    @MainActor private static func frames(_ colors: [Color.Resolved], in environment: EnvironmentValues) -> [[CGColor]] {
        if let known = made[colors] { return known }
        let palette = colors.map { Color($0) }
        let period = Loader.hold * Double(max(1, colors.count))
        let frames = (0...Int((period * coloursPerSecond).rounded())).map { frame in
            Loader.stops(Double(frame) / coloursPerSecond, colors: palette).map { $0.color.resolve(in: environment).cgColor }
        }
        if made.count > 8 { made.removeAll() }
        made[colors] = frames
        return frames
    }
}
