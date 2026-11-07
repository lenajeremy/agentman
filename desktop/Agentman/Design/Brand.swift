import SwiftUI

/// The website's look, as the phone has it: Rubik for titles, the site's
/// mono labels, a warm dark edge on cards and controls with a hard shadow
/// under the ones that stand out, cream for Agentman's own things, and the
/// site's orange for the one thing to tap. What a person reads (messages,
/// tools, code, diffs) never takes any of it.
extension Palette {
    /// The website's orange, and the white set on it.
    static let flood = Color(light: 0xFF5F1F, dark: 0xFF5F1F)
    static let onFlood = Color(light: 0xFFFFFF, dark: 0xFFFFFF)
    /// The site's cream: Agentman's card, notes.
    static let cream = Color(light: 0xFFF3E3, dark: 0x16110D)
    /// Your own messages' bubble.
    static let bubble = Color(light: 0xFFF3E3, dark: 0x2A1F18)
    /// The cream cards' dots.
    static let creamDots = Color(light: 0x160D08, dark: 0xFFF3E3).opacity(0.1)
    /// The site's ink edge, softened to a warm dark grey: ink on white was
    /// too much contrast. Also the hard shadow's colour.
    static let edge = Color(light: 0x4A423C, dark: 0xA69B92)
    /// The hard shadow under an ink (inverse) button.
    static let inverseShadow = Color(light: 0x000000, dark: 0xFFFFFF).opacity(0.25)
    /// The $ before a command, and the icons on Agentman's cream.
    static let prompt = Color(light: 0xE5480A, dark: 0xFF8A55)
}

extension Fonts {
    /// Rubik, the website's headline face, for titles and headings only:
    /// Bold for a screen's title, SemiBold for the rest.
    static func display(_ size: CGFloat) -> Font {
        .custom("Rubik-Bold", size: size * scale)
    }

    static func heading(_ size: CGFloat) -> Font {
        .custom("Rubik-SemiBold", size: size * scale)
    }

    /// The site's small labels: mono, upper case (with .eyebrow()).
    static let eyebrow = mono(10.5, .medium)
}

extension View {
    /// The site's edge round a shape, on its fill, with a hard shadow of
    /// `depth` points straight below when it should stand out.
    func edged(_ radius: CGFloat, fill: Color = Palette.surface, depth: CGFloat = 0, lineWidth: CGFloat = 1.5) -> some View {
        background(
            RoundedRectangle(cornerRadius: radius, style: .continuous)
                .fill(fill)
                .shadow(color: depth > 0 ? Palette.edge : .clear, radius: 0, x: 0, y: depth)
        )
        .overlay(
            RoundedRectangle(cornerRadius: radius, style: .continuous)
                .strokeBorder(Palette.edge, lineWidth: lineWidth)
        )
    }
}

extension Text {
    /// A section's heading in the site's mono label style.
    func eyebrow(_ color: Color = Palette.muted) -> some View {
        font(Fonts.eyebrow)
            .tracking(1.2)
            .textCase(.uppercase)
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
