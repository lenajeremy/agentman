import AppKit
import CoreText
import SwiftUI

// Neutral surfaces keep the conversation in focus. Colour is reserved for
// agent state and actions; both appearances use the same hierarchy.

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
    static let paper = Color(light: 0xFAFAFA, dark: 0x000000)
    static let sidebar = Color(light: 0xF5F5F5, dark: 0x050505)
    static let surface = Color(light: 0xFFFFFF, dark: 0x0D0D0D)
    static let fill = Color(light: 0xEFEFEF, dark: 0x202020)
    static let fillStrong = Color(light: 0xE4E4E4, dark: 0x303030)
    static let line = Color(light: 0xE5E5E5, dark: 0x242424)
    // Tool calls in a thread: a soft card a step greyer than the page, set
    // apart from the conversation and from the user's own bubbles, with a
    // well inside it for a command's output.
    static let toolCard = Color(light: 0xF5F5F5, dark: 0x0D0D0D)
    static let toolCardEdge = Color(light: 0xE8E8E8, dark: 0x202020)
    static let toolWell = Color(light: 0xFAFAFA, dark: 0x101010)
    // Git, in the editor's tree: a file changed.
    static let gitModified = Color(light: 0xA8690F, dark: 0xE2B45C)
    static let text = Color(light: 0x101010, dark: 0xF2F2F2)
    static let textSecondary = Color(light: 0x3C3C3C, dark: 0xD0D0D0)
    static let muted = Color(light: 0x737373, dark: 0xA0A0A0)
    static let faint = Color(light: 0x999999, dark: 0x737373)
    static let inverse = Color(light: 0x101010, dark: 0xF2F2F2)
    static let onInverse = Color(light: 0xFFFFFF, dark: 0x000000)
    /// An agent is working. Also the brand colour.
    static let working = Color(light: 0x3268F5, dark: 0x578CFF)
    static let workingText = Color(light: 0x1F35C7, dark: 0x9DAAFF)
    static let workingWash = Color(light: 0xEEF1FF, dark: 0x1A1F3D)
    /// A file named with `@` in a message box.
    static let mention = Color(light: 0x16707E, dark: 0x7FCCDA)
    /// A skill named in a message.
    static let skillTag = Color(light: 0x8A3FB0, dark: 0xD3A2F2)
    /// Each agent's colour on the usage page.
    static func agent(_ kind: String) -> Color {
        switch kind {
        case "claude": Color(light: 0xD97757, dark: 0xE8896A)
        case "codex": Color(light: 0x1A1A1A, dark: 0xE4E4E4)
        case "opencode": Color(light: 0x3E8EBF, dark: 0x6BB3E0)
        case "antigravity": Color(light: 0x7C5CE6, dark: 0xA48BF5)
        case "kiro": Color(light: 0x9A4FE0, dark: 0xBE8BF2)
        case "cursor", "cursor-cli": Color(light: 0x5B6170, dark: 0xB4B8C2)
        default: Color(light: 0x8A8D94, dark: 0x9EA1A8)
        }
    }
    /// The kinds of tokens on the usage page, output the strongest.
    static let tokenInput = Color(light: 0x7D787C, dark: 0x8E8A8D)
    static let tokenCacheRead = Color(light: 0xB9B5B8, dark: 0x4F4C4E)
    static let tokenCacheWrite = Color(light: 0x585256, dark: 0xB4B0B3)
    static let tokenOutput = Color(light: 0x1E1A1D, dark: 0xECE8EB)
    /// The chosen part of a segmented switch, over `fill`.
    static let segment = Color(light: 0xFFFFFF, dark: 0x363636)
    /// The loader's colours, flowing one into the next.
    static let loader = [working, Color(light: 0xF0407F, dark: 0xFF5C9A)]
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
    static let codeFunction = Color(light: 0x8A6100, dark: 0xE8D08A)
    static let codeProperty = Color(light: 0x0E6F8A, dark: 0x86D1E3)
    // Diffs: a line added, a line removed, and where a hunk begins.
    static let diffAdd = Color(light: 0xE8F7EE, dark: 0x13281C)
    static let diffRemove = Color(light: 0xFDECEC, dark: 0x2C1517)
    static let diffHunk = Color(light: 0xEEF1FF, dark: 0x1A1F3D)
    // The words that changed inside a changed line, over its colour.
    static let diffAddWord = Color(light: 0xBFEBD0, dark: 0x1F5134)
    static let diffRemoveWord = Color(light: 0xF8C9CB, dark: 0x5C2427)

    /// The dot beside an agent: what state it is in, at a glance.
    static func dot(_ state: SessionState) -> Color {
        switch state {
        case .waitingInput: needsYou
        case .busy: working
        case .idle, .ended, .unknown: faint
        }
    }
}

/// The Mac's own font for interface text, as T3 Code's app uses on macOS;
/// bundled Google Sans Code for code.
enum Fonts {
    /// Shared scale for interface and code typography.
    static let scale: CGFloat = 1.0

    static func register() {
        let urls = Bundle.main.urls(forResourcesWithExtension: "ttf", subdirectory: nil) ?? []
        for url in urls {
            CTFontManagerRegisterFontsForURL(url as CFURL, .process, nil)
        }
    }

    static func sans(_ size: CGFloat, _ weight: Font.Weight = .regular) -> Font {
        .system(size: size * scale, weight: weight)
    }

    static func mono(_ size: CGFloat, _ weight: Font.Weight = .regular) -> Font {
        .custom("Google Sans Code", size: size * scale).weight(weight)
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
        if let image = NSImage(named: "Agent-\(kind)") {
            // A one-colour mark takes the text colour, so it shows on a dark
            // sidebar as well as a light one.
            Image("Agent-\(kind)")
                .resizable()
                .renderingMode(image.isTemplate ? .template : .original)
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

/// Quiet, square controls shared by the conversation and editor chrome.
struct ChromeButton: View {
    let symbol: String
    let help: String
    var active = false
    let action: () -> Void
    @State private var hovering = false
    var body: some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: 13, weight: .regular))
                .foregroundStyle(active ? Palette.text : Palette.muted)
                .frame(width: 28, height: 28)
                .background(active || hovering ? Palette.fill.opacity(0.55) : .clear, in: RoundedRectangle(cornerRadius: 6))
        }
        .buttonStyle(.plain)
        .help(help)
        .accessibilityLabel(help)
        .onHover { hovering = $0 }
    }
}


extension EnvironmentValues {
    /// Space for native traffic lights in a window, removed in full screen.
    @Entry var windowHeaderLeading: CGFloat = 82
    /// Space before a page's header while the sidebar is hidden: the
    /// window's buttons and the one that shows the sidebar sit there.
    @Entry var detailHeaderLeading: CGFloat = 0
}
