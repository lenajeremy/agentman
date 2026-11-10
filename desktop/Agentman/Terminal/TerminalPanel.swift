import AppKit
import SwiftUI

/// The terminal under the editor, in the manner of VS Code's and Bolt's
/// panels: a row of shells, each a tab, with new, clear, stop and hide at
/// its end, over the shell in front.
struct TerminalPanel: View {
    let store: EditorStore
    /// Hides it: under the editor, and under the conversation. In the main
    /// window's side panel it closes like the panel's other tabs instead.
    var hide: (() -> Void)?
    /// Moves it, in the main window, between under the conversation and the
    /// side panel.
    var move: TerminalMove?

    var body: some View {
        panel.modifier(BrutalCard())
    }

    private var panel: some View {
        VStack(spacing: 0) {
            HStack(spacing: 2) {
                Text("TERMINAL")
                    .font(Look.site ? Fonts.heading(12.5) : TextStyle.label)
                    .foregroundStyle(Look.site ? Palette.prompt : Palette.faint)
                    .padding(.trailing, 8)
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 2) {
                        ForEach(store.terminals.filter { !store.isSplitHalf($0.id) }) { session in
                            TerminalTab(store: store, session: session, active: session.id == store.activeTerminalID,
                                        split: store.splitPartner(of: session.id) != nil)
                        }
                    }
                }
                Spacer(minLength: 8)
                if let session = focused, session.exited {
                    Button("Restart") { session.restart() }
                        .controlSize(.small)
                }
                iconButton("plus", help: "New terminal (⌃⇧`)") { store.newTerminal() }
                if store.splitPartner(of: store.activeTerminalID) == nil {
                    iconButton("square.split.2x1", help: "Split the terminal: a new shell beside this one") { store.splitTerminal() }
                        .disabled(store.activeTerminal == nil)
                } else {
                    iconButton("square.split.2x1.fill", help: "Close the shell on the right") { store.unsplitTerminal() }
                }
                iconButton("eraser", help: "Clear (⌘K in the terminal)") { focused?.clear() }
                    .disabled(store.activeTerminal == nil)
                iconButton("trash", help: "Stop this shell and close it") {
                    if let session = focused { store.closeTerminal(session) }
                }
                .disabled(store.activeTerminal == nil)
                if let move {
                    iconButton(move.symbol, help: move.help, action: move.action)
                }
                if let hide {
                    iconButton("chevron.down", help: "Hide the terminal (⌃`)", action: hide)
                }
            }
            .padding(.horizontal, 12)
            .frame(height: Look.site ? 34 : 32)
            .background(Look.site ? Palette.cream : Palette.paper)
            .overlay(alignment: Look.site ? .bottom : .top) {
                Rectangle().fill(Look.site ? Palette.edge : Palette.line).frame(height: Look.site ? 1.5 : 1)
            }

            // Every shell keeps its place here, shown or not, so none is
            // made again when tabs change; a split tab shows two side by side.
            GeometryReader { geometry in
                let right = store.splitPartner(of: store.activeTerminalID)?.id
                let half = right == nil ? geometry.size.width : (geometry.size.width - 1) / 2
                ZStack(alignment: .topLeading) {
                    Look.site ? TerminalColors.page : Palette.paper
                    ForEach(store.terminals) { session in
                        let isRight = session.id == right
                        TerminalHost(session: session, visible: session.id == store.activeTerminalID || isRight)
                            .padding(.leading, 10)
                            .padding(.top, 6)
                            // Clear of the card's rounded corners, which
                            // don't clip a shell's own view.
                            .padding(.trailing, Look.site ? 8 : 0)
                            .padding(.bottom, Look.site ? 8 : 0)
                            .frame(width: half, height: geometry.size.height)
                            .offset(x: isRight ? half + 1 : 0)
                    }
                    if right != nil {
                        Rectangle().fill(Palette.line).frame(width: 1, height: geometry.size.height).offset(x: half)
                    }
                }
            }
        }
    }

    /// The shell with the keyboard, of the one or two showing; else the
    /// one in front.
    private var focused: TerminalSession? {
        let showing = [store.activeTerminal, store.splitPartner(of: store.activeTerminalID)].compactMap { $0 }
        return showing.first { $0.view.window?.firstResponder === $0.view } ?? store.activeTerminal
    }

    private func iconButton(_ symbol: String, help: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: 11, weight: .medium))
                .foregroundStyle(Palette.muted)
                .frame(width: 24, height: 24)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(help)
    }
}

/// The page behind a shell in the New theme, the shell's own background.
private enum TerminalColors {
    static let page = Color(light: 0xFFFFFF, dark: 0x000000)
}

/// The New theme's terminal: a card with the theme's edge and a small hard
/// shadow, down and to the right. Today's look, untouched, in Classic.
private struct BrutalCard: ViewModifier {
    func body(content: Content) -> some View {
        if Look.site {
            let shape = RoundedRectangle(cornerRadius: 10, style: .continuous)
            content
                .clipShape(shape)
                .overlay(shape.strokeBorder(Palette.edge, lineWidth: 1.5))
                .background(shape.fill(Palette.edge).offset(x: 3, y: 3))
                .padding(.leading, 10)
                .padding(.top, 10)
                .padding(.trailing, 13)
                .padding(.bottom, 13)
                .background(Palette.paper)
        } else {
            content
        }
    }
}

/// Where a terminal in the main window can go instead, and how.
struct TerminalMove {
    let symbol: String
    let help: String
    let action: () -> Void

    @MainActor static let toBottom = TerminalMove(symbol: "rectangle.bottomhalf.inset.filled", help: "Move the terminal under the conversation") {
        RightPanel.dockTerminal(.bottom)
    }
    @MainActor static let toSide = TerminalMove(symbol: "rectangle.righthalf.inset.filled", help: "Move the terminal into the side panel") {
        RightPanel.dockTerminal(.side)
    }
}

private struct TerminalTab: View {
    let store: EditorStore
    let session: TerminalSession
    let active: Bool
    /// Split in two, a shell beside it.
    var split = false
    @State private var hovering = false

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: split ? "square.split.2x1" : "terminal")
                .font(.system(size: 10))
                .foregroundStyle(session.exited ? Palette.faint : Palette.muted)
            Text(session.title)
                .font(Fonts.sans(12, active ? .medium : .regular))
                .foregroundStyle(active ? Palette.text : Palette.muted)
                .lineLimit(1)
            if session.exited {
                Text("ended")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.faint)
            }
            Button {
                // A split tab closes whole, both its shells.
                if let right = store.splitPartner(of: session.id) { store.closeTerminal(right) }
                store.closeTerminal(session)
            } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 8, weight: .bold))
                    .foregroundStyle(Palette.muted)
                    .frame(width: 14, height: 14)
            }
            .buttonStyle(.plain)
            .opacity(hovering || active ? 1 : 0)
            .help("Stop this shell and close it")
        }
        .padding(.horizontal, 9)
        .frame(height: 24)
        .modifier(TerminalTabLook(active: active, hovering: hovering))
        .contentShape(Rectangle())
        .onTapGesture { store.activeTerminalID = session.id }
        .onHover { hovering = $0 }
    }
}

/// A terminal tab's ground: today's quiet fill, or in the New theme the open
/// tab as a small card on the bar.
private struct TerminalTabLook: ViewModifier {
    let active: Bool
    let hovering: Bool

    func body(content: Content) -> some View {
        let shape = RoundedRectangle(cornerRadius: 6, style: .continuous)
        if Look.site {
            content
                .background {
                    if active {
                        ZStack {
                            shape.fill(Palette.edge).offset(x: 1.5, y: 1.5)
                            shape.fill(Palette.surface)
                            shape.strokeBorder(Palette.edge, lineWidth: 1.5)
                        }
                    } else if hovering {
                        shape.fill(Palette.fill.opacity(0.5))
                    }
                }
                .padding(.trailing, active ? 3 : 0)
        } else {
            content.background(active ? Palette.fill : .clear, in: shape)
        }
    }
}

/// Holds a shell's view, which outlives any one window: closing the editor
/// and opening it again moves the same view, screen and scrollback into the
/// new window.
private struct TerminalHost: NSViewRepresentable {
    let session: TerminalSession
    let visible: Bool

    func makeNSView(context: Context) -> TerminalContainer {
        let host = TerminalContainer(session: session)
        host.arrive()
        return host
    }

    func updateNSView(_ host: TerminalContainer, context: Context) {
        host.adoptIfNobodyHas()
        let wasHidden = host.isHidden
        host.isHidden = !visible
        if visible && (wasHidden || context.coordinator.first) {
            context.coordinator.first = false
            // The keyboard goes to the shell once it is in a window. A shell
            // started by the same click that shows the terminal isn't in one
            // yet a moment later, and the keyboard used to stay where it was
            // until the terminal was hidden and shown again.
            host.wantsKeyboard = true
            DispatchQueue.main.async { host.takeKeyboardIfWanted() }
        }
    }

    static func dismantleNSView(_ host: TerminalContainer, coordinator: Coordinator) {
        host.release()
    }

    func makeCoordinator() -> Coordinator { Coordinator() }

    final class Coordinator {
        var first = true
    }
}

/// A place showing a shell, without keeping that place alive.
struct TerminalHostReference {
    weak var container: TerminalContainer?
}

/// Sizes the shell's view to itself, and only once it has a size of its
/// own.
///
/// A shell's view can only be in one place, and the main window and the
/// editor can both have the same shell open. It goes with you: the window you
/// switch to takes every shell it has open, and the keyboard too if that's
/// where you left it. Each used to take a shell back whenever it redrew, so a
/// terminal opened in the editor could be empty until it was hidden and shown
/// again. A window you aren't in says where the shell went; when the place
/// holding it goes, the one before it gets it.
final class TerminalContainer: NSView {
    let session: TerminalSession
    /// The shell takes the keyboard as soon as it is in a window and shown.
    var wantsKeyboard = false

    private lazy var elsewhere: NSTextField = {
        let label = NSTextField(labelWithString: "Open in another window. Click to show it here.")
        label.font = .systemFont(ofSize: 12)
        label.textColor = .secondaryLabelColor
        label.alignment = .center
        label.isHidden = true
        addSubview(label)
        return label
    }()

    init(session: TerminalSession) {
        self.session = session
        super.init(frame: .zero)
    }

    required init?(coder: NSCoder) { fatalError("not used") }

    /// Whether the shell is here.
    var owns: Bool { session.view.superview === self }

    /// New here. Takes the shell, unless it is in the window being used:
    /// a shell started there also appears in the other window, and stays.
    func arrive() {
        guard session.view.window?.isKeyWindow == true else { return claim() }
        session.hosts.removeAll { $0.container == nil || $0.container === self }
        session.hosts.insert(TerminalHostReference(container: self), at: max(session.hosts.count - 1, 0))
        showWhereTheShellIs()
    }

    /// Takes the shell.
    func claim() {
        session.hosts.removeAll { $0.container == nil || $0.container === self }
        session.hosts.append(TerminalHostReference(container: self))
        adopt()
    }

    /// This place is going: the one before it gets the shell.
    func release() {
        let held = owns
        session.hosts.removeAll { $0.container == nil || $0.container === self }
        stopWatchingWindow()
        if held { session.view.removeFromSuperview() }
        let open = session.hosts.compactMap(\.container)
        if held { (open.last { $0.window != nil } ?? open.last)?.adopt() }
    }

    func adoptIfNobodyHas() {
        if session.view.superview == nil { adopt() }
    }

    private func adopt() {
        if !owns { addSubview(session.view) }
        if bounds.width > 0 && bounds.height > 0 { session.view.frame = bounds }
        for host in session.hosts.compactMap(\.container) { host.showWhereTheShellIs() }
        showWhereTheShellIs()
    }

    private func showWhereTheShellIs() {
        elsewhere.isHidden = owns
        placeNote()
    }

    private func placeNote() {
        elsewhere.frame = NSRect(x: 12, y: (bounds.height - 18) / 2, width: max(bounds.width - 24, 0), height: 18)
    }

    /// Showing the note, a click brings the shell here.
    func bringHere() {
        claim()
        wantsKeyboard = true
        takeKeyboardIfWanted()
    }

    override func mouseDown(with event: NSEvent) {
        if owns { super.mouseDown(with: event) } else { bringHere() }
    }

    func takeKeyboardIfWanted() {
        guard wantsKeyboard, !isHidden, owns, let window else { return }
        wantsKeyboard = false
        window.makeFirstResponder(session.view)
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        watchWindow()
        guard let window else { return }
        if window.isKeyWindow && !owns { claim() }
        takeKeyboardIfWanted()
        // Opened here, but the shell is in the window being used.
        if !owns { wantsKeyboard = false }
    }

    private var windowWatchers: [NSObjectProtocol] = []
    /// The shell had the keyboard when this window was left.
    private var hadKeyboard = false

    private func stopWatchingWindow() {
        windowWatchers.forEach(NotificationCenter.default.removeObserver)
        windowWatchers = []
    }

    private func watchWindow() {
        stopWatchingWindow()
        guard let window else { return }
        let center = NotificationCenter.default
        windowWatchers = [
            center.addObserver(forName: NSWindow.didBecomeKeyNotification, object: window, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.windowBecameKey() }
            },
            center.addObserver(forName: NSWindow.didResignKeyNotification, object: window, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.windowLostKey() }
            },
        ]
    }

    private func windowBecameKey() {
        if !owns { claim() }
        if hadKeyboard {
            hadKeyboard = false
            wantsKeyboard = true
            takeKeyboardIfWanted()
        }
    }

    private func windowLostKey() {
        hadKeyboard = owns && window?.firstResponder === session.view
    }

    override func resizeSubviews(withOldSize oldSize: NSSize) {
        guard bounds.width > 0 && bounds.height > 0 else { return }
        if owns { session.view.frame = bounds }
        placeNote()
    }

    override func didAddSubview(_ subview: NSView) {
        super.didAddSubview(subview)
        if subview === session.view, bounds.width > 0 && bounds.height > 0 { subview.frame = bounds }
    }
}

/// The line over the terminal, dragged to make it taller or shorter.
struct PanelDivider: View {
    @Binding var height: Double
    let maximum: Double
    @State private var start: Double?

    var body: some View {
        Rectangle()
            .fill(Color.clear)
            .frame(height: 6)
            .contentShape(Rectangle())
            .onHover { inside in
                if inside { NSCursor.resizeUpDown.push() } else { NSCursor.pop() }
            }
            .gesture(
                DragGesture(minimumDistance: 1)
                    .onChanged { drag in
                        let from = start ?? height
                        start = from
                        height = min(max(from - drag.translation.height, 120), max(maximum, 160))
                    }
                    .onEnded { _ in start = nil }
            )
    }
}
