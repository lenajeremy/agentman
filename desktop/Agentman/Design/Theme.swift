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
    fileprivate convenience init(rgb: UInt32) {
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

    /// The dot beside an agent: what state it is in, at a glance.
    static func dot(_ state: SessionState) -> Color {
        switch state {
        case .waitingInput: needsYou
        case .busy: working
        case .idle, .ended, .unknown: faint
        }
    }
}

/// Geist, bundled with the app, as the phone bundles it.
enum Fonts {
    static func register() {
        let urls = Bundle.main.urls(forResourcesWithExtension: "ttf", subdirectory: nil) ?? []
        for url in urls {
            CTFontManagerRegisterFontsForURL(url as CFURL, .process, nil)
        }
    }

    static func sans(_ size: CGFloat, _ weight: Font.Weight = .regular) -> Font {
        switch weight {
        case .semibold, .bold, .heavy, .black: .custom("Geist-SemiBold", size: size)
        case .medium: .custom("Geist-Medium", size: size)
        default: .custom("Geist-Regular", size: size)
        }
    }

    static func mono(_ size: CGFloat) -> Font {
        .custom("GeistMono-Regular", size: size)
    }
}

/// The type scale, in the phone's sizes adjusted for a desktop's distance.
enum TextStyle {
    static let display = Fonts.sans(30, .semibold)
    static let title = Fonts.sans(17, .semibold)
    static let body = Fonts.sans(14)
    static let bodyMedium = Fonts.sans(14, .medium)
    static let caption = Fonts.sans(12)
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
