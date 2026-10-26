import AppKit
import Observation
import SwiftTerm

/// One shell in the terminal panel: the user's login shell on a real pty,
/// started in the folder, with an emulator view that keeps its screen and
/// scrollback while the panel is hidden or the window is closed.
@MainActor @Observable
final class TerminalSession: Identifiable, @preconcurrency LocalProcessTerminalViewDelegate {
    nonisolated let id = UUID()
    let folder: URL
    @ObservationIgnored let view: AgentmanTerminalView
    /// The shell's name, or the title a program in it sets.
    private(set) var title: String
    /// The shell's exit code once it has ended.
    private(set) var exitCode: Int32?
    private(set) var exited = false
    /// What runs instead of a plain shell: an agent's terminal, attached to
    /// the tmux session it runs in. Closing this leaves the agent running.
    let command: [String]?

    init(folder: URL, number: Int, command: [String]? = nil, title: String? = nil) {
        self.folder = folder
        self.command = command
        let shell = Self.shell
        self.title = title ?? (number > 1 ? "\((shell as NSString).lastPathComponent) \(number)" : (shell as NSString).lastPathComponent)
        view = AgentmanTerminalView(frame: NSRect(x: 0, y: 0, width: 640, height: 240))
        view.processDelegate = self
        view.onClear = { [weak self] in self?.clear() }
        start()
    }

    /// The user's shell, as the system knows it.
    static var shell: String {
        if let entry = getpwuid(getuid()), let shell = entry.pointee.pw_shell {
            let path = String(cString: shell)
            if FileManager.default.isExecutableFile(atPath: path) { return path }
        }
        return ProcessInfo.processInfo.environment["SHELL"] ?? "/bin/zsh"
    }

    /// The app's environment, with what a terminal says about itself, and
    /// without what would confuse a shell started outside tmux.
    static var environment: [String] {
        var values = ProcessInfo.processInfo.environment
        values["TERM"] = "xterm-256color"
        values["COLORTERM"] = "truecolor"
        values["TERM_PROGRAM"] = "Agentman"
        values["LANG"] = values["LANG"] ?? "en_US.UTF-8"
        for key in ["TMUX", "TMUX_PANE", "AGENTMAN_OPEN", "AGENTMAN_SOCKET"] { values[key] = nil }
        return values.map { "\($0.key)=\($0.value)" }
    }

    func start() {
        exited = false
        exitCode = nil
        let shell = Self.shell
        // A leading dash makes it a login shell, as Terminal starts one. A
        // command runs in one too, so it is found on the user's own PATH.
        let args = command.map { ["-c", "exec " + $0.map(Self.quoted).joined(separator: " ")] } ?? []
        view.startProcess(executable: shell, args: args, environment: Self.environment,
                          execName: "-" + (shell as NSString).lastPathComponent,
                          currentDirectory: folder.path)
    }

    /// A word as a shell reads it back unchanged.
    nonisolated static func quoted(_ word: String) -> String {
        "'" + word.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }

    /// A fresh shell in place of one that ended.
    func restart() {
        view.feed(text: "\u{1b}[2J\u{1b}[3J\u{1b}[H")
        start()
    }

    func terminate() {
        if !exited { view.terminate() }
    }

    /// Clears the screen and scrollback, and has the shell draw its prompt
    /// again.
    func clear() {
        view.feed(text: "\u{1b}[2J\u{1b}[3J\u{1b}[H")
        if !exited { view.send(txt: "\u{0c}") }
    }

    // MARK: LocalProcessTerminalViewDelegate

    func sizeChanged(source: LocalProcessTerminalView, newCols: Int, newRows: Int) {}

    func setTerminalTitle(source: LocalProcessTerminalView, title: String) {
        let trimmed = title.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmed.isEmpty { self.title = trimmed }
    }

    func hostCurrentDirectoryUpdate(source: TerminalView, directory: String?) {}

    func processTerminated(source: TerminalView, exitCode: Int32?) {
        exited = true
        self.exitCode = exitCode
        view.feed(text: "\r\n\u{1b}[2m[The shell ended\(exitCode.map { $0 == 0 ? "" : " with code \($0)" } ?? ""). Restart it from the panel.]\u{1b}[0m\r\n")
    }
}

/// The emulator view, in the app's colours and code font, following the
/// Mac between light and dark.
final class AgentmanTerminalView: LocalProcessTerminalView {
    override init(frame: CGRect) {
        super.init(frame: frame)
        // Option types characters, as it does everywhere else on a Mac.
        optionAsMetaKey = false
        applyTheme()
    }

    required init?(coder: NSCoder) { fatalError("not used") }

    /// Moved into a new window or a panel shown again, the view passes
    /// through a zero size; a terminal resized to that cuts every line it
    /// holds, so those sizes are passed over.
    override func setFrameSize(_ newSize: NSSize) {
        guard newSize.width >= 40, newSize.height >= 20 else { return }
        super.setFrameSize(newSize)
    }

    /// Cmd-K clears the screen and scrollback while the terminal has the
    /// keyboard, as it does in Terminal.
    var onClear: (() -> Void)?

    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        if window?.firstResponder === self,
           event.modifierFlags.intersection(.deviceIndependentFlagsMask) == .command,
           event.charactersIgnoringModifiers == "k", let onClear {
            onClear()
            return true
        }
        return super.performKeyEquivalent(with: event)
    }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        applyTheme()
    }

    func applyTheme() {
        let dark = effectiveAppearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
        font = NSFont(descriptor: NSFontDescriptor(fontAttributes: [.family: "Google Sans Code"]), size: 12.5)
            ?? .monospacedSystemFont(ofSize: 12.5, weight: .regular)
        nativeBackgroundColor = NSColor(rgb: dark ? 0x17181B : 0xFFFFFF)
        nativeForegroundColor = NSColor(rgb: dark ? 0xE6E7EA : 0x1E2025)
        caretColor = NSColor(rgb: dark ? 0x6B80FF : 0x2E4BFF)
        selectedTextBackgroundColor = NSColor(rgb: dark ? 0x6B80FF : 0x2E4BFF).withAlphaComponent(0.3)
        installColors((dark ? Self.dark : Self.light).map(Self.color))
    }

    /// The 16 ANSI colours, quiet enough for the page and readable on it.
    static let dark: [UInt32] = [
        0x2A2C31, 0xFF6B70, 0x2BC784, 0xE2B45C, 0x6B80FF, 0xD19AF5, 0x5CC8D8, 0xD2D4D9,
        0x6E717A, 0xFF8A8E, 0x4FDB9C, 0xF2C879, 0x9DAAFF, 0xE0B8FF, 0x7FDCEA, 0xF2F3F5,
    ]
    static let light: [UInt32] = [
        0x1E2025, 0xC4343A, 0x0E8A57, 0xA8690F, 0x1F35C7, 0x8E3BB8, 0x0F7C8C, 0x6A6D75,
        0x9C9EA5, 0xE5484D, 0x12A66A, 0xB7791F, 0x2E4BFF, 0xA855D6, 0x1B98AB, 0x34363B,
    ]

    static func color(_ rgb: UInt32) -> SwiftTerm.Color {
        SwiftTerm.Color(red: UInt16((rgb >> 16) & 0xFF) * 257, green: UInt16((rgb >> 8) & 0xFF) * 257, blue: UInt16(rgb & 0xFF) * 257)
    }
}
