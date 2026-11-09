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

    func makeNSView(context: Context) -> NSView {
        let host = TerminalContainer()
        session.view.removeFromSuperview()
        host.addSubview(session.view)
        return host
    }

    func updateNSView(_ host: NSView, context: Context) {
        let view = session.view
        if view.superview !== host {
            view.removeFromSuperview()
            host.addSubview(view)
        }
        let wasHidden = host.isHidden
        host.isHidden = !visible
        if visible && (wasHidden || context.coordinator.first) {
            context.coordinator.first = false
            // The keyboard goes to the shell once it is in a window. A shell
            // started by the same click that shows the terminal isn't in one
            // yet a moment later, and the keyboard used to stay where it was
            // until the terminal was hidden and shown again.
            let container = host as? TerminalContainer
            container?.wantsKeyboard = true
            DispatchQueue.main.async { container?.takeKeyboardIfWanted() }
        }
    }

    func makeCoordinator() -> Coordinator { Coordinator() }

    final class Coordinator {
        var first = true
    }
}

/// Sizes the shell's view to itself, and only once it has a size of its
/// own.
private final class TerminalContainer: NSView {
    /// The shell takes the keyboard as soon as it is in a window and shown.
    var wantsKeyboard = false

    func takeKeyboardIfWanted() {
        guard wantsKeyboard, !isHidden, let window, let view = subviews.first else { return }
        wantsKeyboard = false
        window.makeFirstResponder(view)
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        takeKeyboardIfWanted()
    }

    override func resizeSubviews(withOldSize oldSize: NSSize) {
        for view in subviews where bounds.width > 0 && bounds.height > 0 {
            view.frame = bounds
        }
    }

    override func didAddSubview(_ subview: NSView) {
        super.didAddSubview(subview)
        if bounds.width > 0 && bounds.height > 0 { subview.frame = bounds }
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
