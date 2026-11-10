import SwiftUI

/// The two looks the app wears: today's (Classic, the default) and the
/// website's (New), switched in Settings › General while New is refined.
enum AppTheme: String, CaseIterable, Identifiable, Sendable {
    case classic, site

    var id: String { rawValue }
    var title: String { self == .classic ? "Classic" : "New" }
}

/// The theme chosen, kept in the app's defaults. Views read it through
/// Look.site, and the colours, fonts and edges below follow it, so a change
/// redraws whatever read them.
@Observable
final class ThemeChoice: @unchecked Sendable {
    nonisolated(unsafe) static let shared = ThemeChoice()
    static let key = "appTheme"

    private var stored: AppTheme

    var theme: AppTheme {
        get { stored }
        set {
            stored = newValue
            UserDefaults.standard.set(newValue.rawValue, forKey: Self.key)
        }
    }

    init() {
        // A picture test asks for a theme without touching the app's
        // defaults.
        let asked = ProcessInfo.processInfo.environment["AGENTMAN_THEME"] ?? UserDefaults.standard.string(forKey: Self.key)
        stored = AppTheme(rawValue: asked ?? "") ?? .classic
    }
}

enum Look {
    /// The website's look is on. Everything it changes keeps today's look
    /// exactly while it is off.
    static var site: Bool { ThemeChoice.shared.theme == .site }
}

/// The website's look, as the phone has it: Rubik for titles, the site's
/// mono labels, a warm dark edge on cards and controls with a hard shadow
/// under the ones that stand out, cream for Agentman's own things, and the
/// site's orange for the one thing to tap. What a person reads (the agent's
/// words, diffs) stays plain; the frames round it (steps, your messages,
/// code) take the edge, as the site's cards do. Each colour that replaced
/// one of today's goes back to it in the Classic theme.
extension Palette {
    /// The website's orange, and the white set on it.
    static let flood = Color(light: 0xFF5F1F, dark: 0xFF5F1F)
    static let onFlood = Color(light: 0xFFFFFF, dark: 0xFFFFFF)
    /// The site's cream: Agentman's card, notes.
    static let cream = Color(light: 0xFFF3E3, dark: 0x16110D)
    /// Your own messages' bubble: cream, or today's grey.
    static var bubble: Color { Look.site ? siteBubble : fill }
    private static let siteBubble = Color(light: 0xFFF3E3, dark: 0x2A1F18)
    /// The cream cards' dots.
    static let creamDots = Color(light: 0x160D08, dark: 0xFFF3E3).opacity(0.1)
    /// The site's ink edge, softened to a warm dark grey: ink on white was
    /// too much contrast. Also the hard shadow's colour. Today's hairline in
    /// the Classic theme, at its width.
    static var edge: Color { Look.site ? siteEdge : line }
    static var edgeWidth: CGFloat { Look.site ? 1.5 : 1 }
    private static let siteEdge = Color(light: 0x4A423C, dark: 0xA69B92)
    /// The hard shadow under an ink (inverse) button.
    static let inverseShadow = Color(light: 0x000000, dark: 0xFFFFFF).opacity(0.25)
    /// The $ before a command, and the icons on Agentman's cream.
    static let prompt = Color(light: 0xE5480A, dark: 0xFF8A55)
    /// The Board's ground in New: the page with a tint of the site's orange.
    static let boardTint = Color(light: 0xFFF3E8, dark: 0x140E0A)
    /// The site's ink, as a strip over a code block, and the cream set on it.
    static let ink = Color(light: 0x160D08, dark: 0x2A211B)
    static let onInk = Color(light: 0xFFF3E3, dark: 0xFFF3E3)
}

extension Fonts {
    /// Rubik, the website's headline face, for titles and headings only:
    /// Bold for a screen's title, SemiBold for the rest. In the Classic
    /// theme, the face the heading had: `classic`, or the system font
    /// semibold at the size.
    static func display(_ size: CGFloat, classic: Font? = nil) -> Font {
        Look.site ? .custom("Rubik-Bold", size: size * scale) : (classic ?? sans(size, .semibold))
    }

    static func heading(_ size: CGFloat, classic: Font? = nil) -> Font {
        Look.site ? .custom("Rubik-SemiBold", size: size * scale) : (classic ?? sans(size, .semibold))
    }

    /// The site's small labels: mono, upper case (with .eyebrow()).
    static let eyebrow = mono(10.5, .medium)
}

extension View {
    /// The site's edge round a shape, on its fill, with a hard shadow of
    /// `depth` points straight below when it should stand out. In the
    /// Classic theme, today's card: the fill with a hairline round it.
    func edged(_ radius: CGFloat, fill: Color = Palette.surface, depth: CGFloat = 0, lineWidth: CGFloat = 1.5) -> some View {
        background(
            RoundedRectangle(cornerRadius: radius, style: .continuous)
                .hardShadowFill(fill, shadow: depth > 0 ? Palette.edge : .clear, depth: depth)
        )
        .overlay(
            RoundedRectangle(cornerRadius: radius, style: .continuous)
                .strokeBorder(Palette.edge, lineWidth: Look.site ? lineWidth : 1)
        )
    }

    /// A soft shadow that only the Classic theme has (the website's look
    /// replaced it with a hard one).
    func classicShadow(color: Color, radius: CGFloat, y: CGFloat) -> some View {
        shadow(color: Look.site ? .clear : color, radius: radius, x: 0, y: y)
    }

    /// One look or the other, where the website's look changes more than a
    /// colour or a font: `classic` is today's code as it was.
    @ViewBuilder
    func look<Site: View, Classic: View>(site: (Self) -> Site, classic: (Self) -> Classic) -> some View {
        if Look.site { site(self) } else { classic(self) }
    }
}

extension Shape {
    /// The shape filled, over a copy of itself in `shadow` moved `depth`
    /// points down: the site's hard shadow. Drawn rather than cast, because
    /// a cast shadow's offset comes out upside down in some AppKit-hosted
    /// lists (a thread's).
    func hardShadowFill(_ fill: Color, shadow: Color, depth: CGFloat) -> some View {
        let depth = Look.site ? depth : 0
        return ZStack {
            if depth > 0 {
                self.fill(shadow).offset(y: depth)
            }
            self.fill(fill)
        }
    }
}

extension Text {
    /// A section's heading in the site's mono label style; `classic` in the
    /// Classic theme.
    func eyebrow(_ color: Color = Palette.muted, classic: Font = TextStyle.captionMedium) -> some View {
        font(Look.site ? Fonts.eyebrow : classic)
            .tracking(Look.site ? 1.2 : 0)
            .textCase(Look.site ? .uppercase : nil)
            .foregroundStyle(color)
    }
}

/// The website's orange band: the flood with a film grain, and the site's
/// wavy edge at its foot in the colour of what comes after it.
struct FloodBand<Content: View>: View {
    var edge: Color = Palette.surface
    @ViewBuilder var content: Content

    var body: some View {
        content
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.bottom, 10)
            .background {
                ZStack(alignment: .bottom) {
                    Palette.flood
                    if let grain = brandGrain {
                        Rectangle()
                            .fill(ImagePaint(image: Image(nsImage: grain), scale: 1 / 3))
                            .blendMode(.overlay)
                            .opacity(0.2)
                    }
                    WaveEdge()
                        .fill(edge)
                        .frame(height: 10)
                        .offset(y: 1)
                }
            }
    }
}

/// The film grain over the orange band, read once.
@MainActor private let brandGrain: NSImage? = Bundle.main.url(forResource: "grain", withExtension: "png").flatMap(NSImage.init(contentsOf:))

/// The site's wavy edge: a 22-point wave, filled below the curve.
struct WaveEdge: Shape {
    func path(in rect: CGRect) -> Path {
        var path = Path()
        let period: CGFloat = 22, mid = rect.minY + rect.height / 2
        path.move(to: CGPoint(x: rect.minX, y: mid))
        var x = rect.minX
        while x < rect.maxX {
            path.addQuadCurve(to: CGPoint(x: x + period / 2, y: mid), control: CGPoint(x: x + period / 4, y: rect.minY))
            path.addQuadCurve(to: CGPoint(x: x + period, y: mid), control: CGPoint(x: x + period * 3 / 4, y: rect.maxY))
            x += period
        }
        path.addLine(to: CGPoint(x: x, y: rect.maxY))
        path.addLine(to: CGPoint(x: rect.minX, y: rect.maxY))
        path.closeSubpath()
        return path
    }
}

/// A word set as an outline, as the site sets one in its headlines: the
/// word in the colour behind it, ringed in `stroke`.
struct OutlinedWord: View {
    let text: String
    let font: Font
    var stroke: Color = Palette.onFlood
    var fill: Color = Palette.flood
    var width: CGFloat = 1.6

    var body: some View {
        ZStack {
            ForEach(0..<12, id: \.self) { step in
                let angle = Double(step) / 12 * 2 * .pi
                Text(text).font(font).foregroundStyle(stroke)
                    .offset(x: cos(angle) * width, y: sin(angle) * width)
            }
            Text(text).font(font).foregroundStyle(fill)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(text)
    }
}

/// A sheet's or a card's actions: the site's buttons (ink on a hard shadow
/// for the one to press, white with the edge for the others), or the
/// system's in the Classic theme.
struct BrandButtonStyle: PrimitiveButtonStyle {
    var prominent = true

    @ViewBuilder
    func makeBody(configuration: Configuration) -> some View {
        if Look.site {
            Button(configuration).buttonStyle(SiteButtonStyle(prominent: prominent))
        } else if prominent {
            Button(configuration).buttonStyle(.borderedProminent)
        } else {
            Button(configuration).buttonStyle(.automatic)
        }
    }
}

private struct SiteButtonStyle: ButtonStyle {
    var prominent: Bool

    func makeBody(configuration: Configuration) -> some View {
        BrandButtonBody(configuration: configuration, prominent: prominent)
    }
}

private struct BrandButtonBody: View {
    let configuration: ButtonStyleConfiguration
    let prominent: Bool
    @Environment(\.isEnabled) private var enabled
    @Environment(\.controlSize) private var controlSize
    @State private var hovering = false

    var body: some View {
        let large = controlSize == .large
        let shape = RoundedRectangle(cornerRadius: 8, style: .continuous)
        configuration.label
            .font(Fonts.sans(large ? 13 : 12.5, .medium))
            .foregroundStyle(prominent ? Palette.onInverse : Palette.text)
            .padding(.horizontal, large ? 14 : 11)
            .frame(minHeight: large ? 30 : 26)
            .background(
                shape.hardShadowFill(
                    prominent ? Palette.inverse.opacity(hovering ? 0.88 : 1) : (hovering ? Palette.fill : Palette.surface),
                    shadow: prominent ? Palette.inverseShadow : Palette.edge,
                    depth: configuration.isPressed ? 0 : 2)
            )
            .overlay(shape.strokeBorder(prominent ? Color.clear : Palette.edge, lineWidth: 1.5))
            .offset(y: configuration.isPressed ? 2 : 0)
            .padding(.bottom, 2)
            .opacity(enabled ? 1 : 0.45)
            .contentShape(shape)
            .onHover { hovering = $0 }
    }
}

extension PrimitiveButtonStyle where Self == BrandButtonStyle {
    /// Ink on a hard shadow: the action to take.
    static var brand: BrandButtonStyle { BrandButtonStyle() }
    /// White with the site's edge: the others beside it.
    static var brandSecondary: BrandButtonStyle { BrandButtonStyle(prominent: false) }
}
