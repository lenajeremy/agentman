import AppKit
import CoreText
import SwiftUI

// Agentman's look, shared with the phone: the palette and type in
// mobile/lib/theme.ts, so the two apps read as one product. Each colour has a
// light and a dark value, and follows the system appearance.

extension Color {
    fileprivate init(light: UInt32, dark: UInt32) {
        self.init(nsColor: NSColor(name: nil) { appearance in
            let isDark = appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
            return NSColor(rgb: isDark ? dark : light)
        })
    }
}

extension NSColor {
    convenience init(rgb: UInt32) {
        self.init(
            srgbRed: CGFloat((rgb >> 16) & 0xFF) / 255,
            green: CGFloat((rgb >> 8) & 0xFF) / 255,
            blue: CGFloat(rgb & 0xFF) / 255,
            alpha: 1
        )
    }
}

enum Palette {
    static let paper = Color(light: 0xF5F5F2, dark: 0x0E0F11)
    static let surface = Color(light: 0xFFFFFF, dark: 0x17181B)
    static let fill = Color(light: 0xEDECE8, dark: 0x202226)
    static let fillStrong = Color(light: 0xE4E3DE, dark: 0x2C2E33)
    static let line = Color(light: 0xE3E2DD, dark: 0x25272C)
    // Tool calls in a thread: a soft card a step greyer than the page, set
    // apart from the conversation and from the user's own bubbles, with a
    // well inside it for a command's output.
    static let toolCard = Color(light: 0xEEEEEA, dark: 0x17181B)
    static let toolCardEdge = Color(light: 0xE5E4DF, dark: 0x212327)
    static let toolWell = Color(light: 0xF8F8F5, dark: 0x0F1012)
    // Git, in the editor's tree: a file changed.
    static let gitModified = Color(light: 0xA8690F, dark: 0xE2B45C)
    static let text = Color(light: 0x0F1012, dark: 0xF2F3F5)
    static let textSecondary = Color(light: 0x34363B, dark: 0xD2D4D9)
    static let muted = Color(light: 0x6A6D75, dark: 0x9A9DA5)
    static let faint = Color(light: 0x9C9EA5, dark: 0x6E717A)
    static let inverse = Color(light: 0x0F1012, dark: 0xF2F3F5)
    static let onInverse = Color(light: 0xFFFFFF, dark: 0x0E0F11)
    /// An agent is working. Also the brand colour.
    static let working = Color(light: 0x2E4BFF, dark: 0x6B80FF)
    static let workingText = Color(light: 0x1F35C7, dark: 0x9DAAFF)
    static let workingWash = Color(light: 0xEEF1FF, dark: 0x1A1F3D)
    /// An agent needs you.
    static let needsYou = Color(light: 0xFF6A2B, dark: 0xFF7D45)
    static let needsYouText = Color(light: 0xC2451A, dark: 0xFF9A6C)
    static let needsYouWash = Color(light: 0xFFF2EB, dark: 0x2E1B12)
    static let needsYouEdge = Color(light: 0xFFDCCB, dark: 0x4A2818)
    static let ok = Color(light: 0x12A66A, dark: 0x2BC784)
    static let error = Color(light: 0xE5484D, dark: 0xFF6B70)
    static let errorText = Color(light: 0xC4343A, dark: 0xFF8A8E)
    static let errorWash = Color(light: 0xFDEDEE, dark: 0x2C1517)

    // Code: quiet colours that read on paper and ink alike.
    static let codeKeyword = Color(light: 0x8E3BB8, dark: 0xD19AF5)
    static let codeString = Color(light: 0x1C7F4B, dark: 0x7FD3A0)
    static let codeNumber = Color(light: 0xB25A12, dark: 0xF2A867)
    static let codeComment = Color(light: 0x8A8D94, dark: 0x6E727B)
    static let codeType = Color(light: 0x1F5FB8, dark: 0x8DB8F5)
    // Diffs: a line added, a line removed, and where a hunk begins.
    static let diffAdd = Color(light: 0xE8F7EE, dark: 0x13281C)
    static let diffRemove = Color(light: 0xFDECEC, dark: 0x2C1517)
    static let diffHunk = Color(light: 0xEEF1FF, dark: 0x1A1F3D)

    /// The dot beside an agent: what state it is in, at a glance.
    static func dot(_ state: SessionState) -> Color {
        switch state {
        case .waitingInput: needsYou
        case .busy: working
        case .idle, .ended, .unknown: faint
        }
    }
}

/// Google Sans for text and Google Sans Code for code, bundled with the app
/// (both under the SIL Open Font License, in Resources/Fonts). Both are
/// variable fonts: Google Sans has Regular, Medium and Bold faces, Google
/// Sans Code every weight from Light to ExtraBold, and the system picks the
/// optical size for the point size.
enum Fonts {
    static func register() {
        let urls = Bundle.main.urls(forResourcesWithExtension: "ttf", subdirectory: nil) ?? []
        for url in urls {
            CTFontManagerRegisterFontsForURL(url as CFURL, .process, nil)
        }
    }

    static func sans(_ size: CGFloat, _ weight: Font.Weight = .regular) -> Font {
        .custom("Google Sans", size: size).weight(weight)
    }

    static func mono(_ size: CGFloat, _ weight: Font.Weight = .regular) -> Font {
        .custom("Google Sans Code", size: size).weight(weight)
    }
}

/// The type scale, in the phone's sizes adjusted for a desktop's distance.
enum TextStyle {
    /// Agentman's name on the entry page.
    static let wordmark = Fonts.sans(34)
    static let display = Fonts.sans(28, .medium)
    static let title = Fonts.sans(16, .medium)
    static let body = Fonts.sans(14)
    static let bodyMedium = Fonts.sans(14, .medium)
    static let caption = Fonts.sans(12)
    static let captionMedium = Fonts.sans(12, .medium)
    static let label = Fonts.sans(11, .medium)
    static let code = Fonts.mono(12.5)
}

/// An agent's logo, from the asset catalogue (generated from the phone's
/// marks by desktop/scripts/agent-marks.py), or a terminal prompt for an
/// agent without one.
struct AgentMark: View {
    let kind: String
    var size: CGFloat = 18

    var body: some View {
        if NSImage(named: "Agent-\(kind)") != nil {
            Image("Agent-\(kind)")
                .resizable()
                .renderingMode(nil)
                .interpolation(.high)
                .scaledToFit()
                .foregroundStyle(Palette.text)
                .frame(width: size, height: size)
                .accessibilityLabel(kind)
        } else {
            Image(systemName: "terminal")
                .font(.system(size: size * 0.75, weight: .medium))
                .foregroundStyle(Palette.muted)
                .frame(width: size, height: size)
                .accessibilityLabel(kind)
        }
    }
}

/// A conversation fades out as it scrolls up to the title bar, rather than
/// being cut off at its edge.
struct TopFade: ViewModifier {
    func body(content: Content) -> some View {
        content.overlay(alignment: .top) {
            LinearGradient(colors: [Palette.paper, Palette.paper.opacity(0)], startPoint: .top, endPoint: .bottom)
                .frame(height: 20)
                .allowsHitTesting(false)
        }
    }
}

/// The sidebar's own translucent material, for things that sit on it.
struct SidebarMaterial: NSViewRepresentable {
    func makeNSView(context: Context) -> NSVisualEffectView {
        let view = NSVisualEffectView()
        view.material = .sidebar
        view.blendingMode = .behindWindow
        view.state = .followsWindowActiveState
        return view
    }

    func updateNSView(_ view: NSVisualEffectView, context: Context) {}
}
