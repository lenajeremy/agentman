import AppKit
import SwiftUI

/// The terminal under the editor, in the manner of VS Code's and Bolt's
/// panels: a row of shells, each a tab, with new, clear, stop and hide at
/// its end, over the shell in front.
struct TerminalPanel: View {
    let store: EditorStore

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 2) {
                Text("TERMINAL")
                    .font(TextStyle.label)
                    .foregroundStyle(Palette.faint)
                    .padding(.trailing, 8)
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 2) {
                        ForEach(store.terminals) { session in
                            TerminalTab(store: store, session: session, active: session.id == store.activeTerminalID)
                        }
                    }
                }
                Spacer(minLength: 8)
                if let session = store.activeTerminal, session.exited {
                    Button("Restart") { session.restart() }
                        .controlSize(.small)
                }
                iconButton("plus", help: "New terminal (⌃⇧`)") { store.newTerminal() }
                iconButton("eraser", help: "Clear (⌘K in the terminal)") { store.activeTerminal?.clear() }
                    .disabled(store.activeTerminal == nil)
                iconButton("trash", help: "Stop this shell and close it") {
                    if let session = store.activeTerminal { store.closeTerminal(session) }
                }
                .disabled(store.activeTerminal == nil)
                iconButton("chevron.down", help: "Hide the terminal (⌃`)") { store.showTerminal = false }
            }
            .padding(.horizontal, 12)
            .frame(height: 32)
            .background(Palette.paper)
            .overlay(alignment: .top) { Rectangle().fill(Palette.line).frame(height: 1) }

            ZStack {
                Palette.surface
                ForEach(store.terminals) { session in
                    TerminalHost(session: session, visible: session.id == store.activeTerminalID)
                        .padding(.leading, 10)
                        .padding(.top, 6)
                }
            }
        }
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

private struct TerminalTab: View {
    let store: EditorStore
    let session: TerminalSession
    let active: Bool
    @State private var hovering = false

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "terminal")
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
        .background(active ? Palette.fill : .clear, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
        .contentShape(Rectangle())
        .onTapGesture { store.activeTerminalID = session.id }
        .onHover { hovering = $0 }
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
            DispatchQueue.main.async { view.window?.makeFirstResponder(view) }
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
