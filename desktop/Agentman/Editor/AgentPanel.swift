import SwiftUI

/// What an editor's agent panel shows: every agent and Agentman to pick
/// from, Agentman's conversation, or one agent's thread.
enum PanelRoute: Hashable {
    case agents
    case agentman
    case agent(String)
}

/// Agentman at the side of the code, in the manner of Cursor's and
/// Copilot's chat panes: it opens on the agent working in the editor's
/// folder; Back lists every agent and Agentman itself, and any of them can
/// be read and written to from here. `@` in its message box names files in
/// the folder.
struct AgentPanel: View {
    @Environment(AgentsStore.self) private var agents
    let editor: EditorStore

    var body: some View {
        let route = editor.panelRoute ?? .agents
        VStack(spacing: 0) {
            PanelHeader(editor: editor, route: route)
            Group {
                switch route {
                case .agents:
                    PanelAgentList(folder: editor.root.path) { editor.panelRoute = $0 }
                case .agentman:
                    ManagerChatView(inPanel: true)
                        .onAppear { agents.showInPanel(nil) }
                        .onDisappear { agents.hideFromPanel(nil) }
                case .agent(let id):
                    if let next = agents.successor(of: id) {
                        // Cleared or switched in its terminal: follow it.
                        Color.clear.onAppear { editor.panelRoute = .agent(next) }
                    } else if let session = agents.session(id) {
                        AgentThreadView(session: session, inPanel: true)
                            .id(id)
                            .onAppear { agents.showInPanel(id) }
                            .onDisappear { agents.hideFromPanel(id) }
                    } else {
                        PanelGone { editor.panelRoute = .agents }
                    }
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .background(Palette.paper)
        .environment(\.fileMentions, editor.mentions)
        // Images open full size over the editor, not Agentman's window.
        .environment(\.lightboxPlace, editor.root.path)
        .onAppear {
            // For screenshots and tests: the list, Agentman, or an agent.
            if editor.panelRoute == nil, let shown = ProcessInfo.processInfo.environment["AGENTMAN_PANEL"] {
                editor.panelRoute = shown == "agents" ? .agents : shown == "agentman" ? .agentman : .agent(shown)
            }
            if editor.panelRoute == nil {
                editor.panelRoute = Self.agent(for: editor.root.path, in: agents.sessions).map { .agent($0.id) } ?? .agents
            }
        }
    }

    /// The agent working on a folder: one running in it, in a folder inside
    /// it, or in one around it; one that needs the user first, then one at
    /// work, then the folder's own, then the latest to do anything.
    static func agent(for folder: String, in sessions: [Session]) -> Session? {
        let folder = URL(fileURLWithPath: folder).standardizedFileURL.path
        let related = sessions.filter { session in
            let cwd = URL(fileURLWithPath: session.cwd).standardizedFileURL.path
            return session.state != .ended && (cwd == folder || cwd.hasPrefix(folder + "/") || folder.hasPrefix(cwd + "/"))
        }
        func rank(_ state: SessionState) -> Int {
            switch state {
            case .waitingInput: 0
            case .busy: 1
            case .idle: 2
            default: 3
            }
        }
        return related.min { a, b in
            if rank(a.state) != rank(b.state) { return rank(a.state) < rank(b.state) }
            let aHere = URL(fileURLWithPath: a.cwd).standardizedFileURL.path == folder
            let bHere = URL(fileURLWithPath: b.cwd).standardizedFileURL.path == folder
            if aHere != bHere { return aHere }
            return (a.lastActivity ?? .distantPast) > (b.lastActivity ?? .distantPast)
        }
    }
}

/// The panel's title row, as tall as the tab strip beside it: Back to the
/// list, what is open, and the ways out.
private struct PanelHeader: View {
    @Environment(AgentsStore.self) private var agents
    @Environment(\.openWindow) private var openWindow
    let editor: EditorStore
    let route: PanelRoute
    @State private var confirmNewChat = false

    var body: some View {
        HStack(spacing: 6) {
            if route != .agents {
                HeaderButton(symbol: "chevron.left", help: "All agents and Agentman") {
                    editor.panelRoute = .agents
                }
            }
            title
                .padding(.leading, route == .agents ? 6 : 0)
            Spacer(minLength: 6)
            switch route {
            case .agentman:
                if !agents.manager.entries.isEmpty {
                    HeaderButton(symbol: "plus.bubble", help: "Start a new conversation with Agentman") { confirmNewChat = true }
                }
                HeaderButton(symbol: "arrow.up.forward.app", help: "Open Agentman in its window") {
                    openWindow(id: "main")
                    agents.selection = .manager
                }
            case .agent(let id):
                HeaderButton(symbol: "arrow.up.forward.app", help: "Open this agent in Agentman's window") {
                    openWindow(id: "main")
                    agents.selection = .agent(id)
                }
            case .agents:
                EmptyView()
            }
            HeaderButton(symbol: "xmark", help: "Close the panel (⌘L)") { editor.showAgentPanel = false }
        }
        .padding(.horizontal, 8)
        .frame(height: 36)
        .background(Palette.paper)
        .overlay(alignment: .bottom) { Rectangle().fill(Palette.line).frame(height: 1) }
        .confirmationDialog("Start a new chat?", isPresented: $confirmNewChat) {
            Button("New Chat") {
                Task { agents.manager.problem = await agents.manager.reset() }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text(ManagerChatView.newChatMessage)
        }
    }

    @ViewBuilder private var title: some View {
        switch route {
        case .agents:
            Text("Agents")
                .font(Fonts.sans(13, .semibold))
                .foregroundStyle(Palette.text)
        case .agentman:
            HStack(spacing: 7) {
                AgentmanBadge(size: 18)
                Text("Agentman")
                    .font(Fonts.sans(13, .semibold))
                    .foregroundStyle(Palette.text)
                if agents.manager.thinking {
                    Text("Thinking…")
                        .font(Fonts.sans(11.5))
                        .foregroundStyle(Palette.muted)
                }
            }
        case .agent(let id):
            if let session = agents.session(id) {
                HStack(spacing: 7) {
                    AgentMark(kind: session.kind, size: 15)
                    Text(session.name)
                        .font(Fonts.sans(13, .semibold))
                        .foregroundStyle(Palette.text)
                        .lineLimit(1)
                        .truncationMode(.tail)
                        .layoutPriority(1)
                    Circle()
                        .fill(Palette.dot(session.state))
                        .frame(width: 6, height: 6)
                    Text(PanelAgentList.state(session))
                        .font(Fonts.sans(11.5))
                        .foregroundStyle(session.state == .waitingInput ? Palette.needsYouText : Palette.muted)
                        .lineLimit(1)
                }
                .help("\(session.name) in \((session.cwd as NSString).abbreviatingWithTildeInPath)")
            }
        }
    }
}

/// A small icon button in the panel's header.
private struct HeaderButton: View {
    let symbol: String
    let help: String
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: 11.5, weight: .semibold))
                .foregroundStyle(hovering ? Palette.text : Palette.muted)
                .frame(width: 24, height: 24)
                .background(hovering ? Palette.fill : .clear, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(help)
        .accessibilityLabel(help)
    }
}

/// Agentman, then the agents in the editor's folder, then the rest.
private struct PanelAgentList: View {
    @Environment(AgentsStore.self) private var agents
    let folder: String
    let choose: (PanelRoute) -> Void

    var body: some View {
        let running = agents.sessions.filter { $0.state != .ended }
        let here = running.filter { Self.related($0.cwd, folder) }
        let elsewhere = running.filter { !Self.related($0.cwd, folder) }
        ScrollView {
            VStack(alignment: .leading, spacing: 2) {
                AgentmanRow { choose(.agentman) }
                    .padding(.bottom, 8)
                if !here.isEmpty {
                    heading("This folder")
                    ForEach(here) { row($0) }
                }
                if !elsewhere.isEmpty {
                    heading(here.isEmpty ? "Agents" : "Elsewhere")
                        .padding(.top, here.isEmpty ? 0 : 10)
                    ForEach(elsewhere) { row($0) }
                }
                if running.isEmpty {
                    Text("No agents are running. Ask Agentman, or start one from its window.")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                        .padding(.horizontal, 10)
                        .padding(.top, 6)
                }
            }
            .padding(10)
        }
    }

    private func heading(_ text: String) -> some View {
        Text(text.uppercased())
            .font(TextStyle.label)
            .foregroundStyle(Palette.faint)
            .padding(.horizontal, 10)
            .padding(.bottom, 4)
    }

    private func row(_ session: Session) -> some View {
        PanelRow { choose(.agent(session.id)) } label: {
            AgentRow(session: session)
        }
        .help((session.cwd as NSString).abbreviatingWithTildeInPath)
    }

    static func related(_ cwd: String, _ folder: String) -> Bool {
        let cwd = URL(fileURLWithPath: cwd).standardizedFileURL.path
        let folder = URL(fileURLWithPath: folder).standardizedFileURL.path
        return cwd == folder || cwd.hasPrefix(folder + "/") || folder.hasPrefix(cwd + "/")
    }

    /// An agent's state in a word or two.
    static func state(_ session: Session) -> String {
        switch session.state {
        case .waitingInput: "Needs you"
        case .busy: "Working"
        case .idle: "Idle"
        case .ended: "Ended"
        case .unknown: ""
        }
    }
}

/// Agentman at the top of the list: its mark, what it has to say, and
/// anything new.
private struct AgentmanRow: View {
    @Environment(AgentsStore.self) private var agents
    let action: () -> Void

    var body: some View {
        PanelRow(action: action) {
            HStack(spacing: 10) {
                AgentmanBadge(size: 28)
                VStack(alignment: .leading, spacing: 2) {
                    Text("Agentman")
                        .font(Fonts.sans(13.5, .medium))
                        .foregroundStyle(Palette.text)
                    Text(summary)
                        .font(Fonts.sans(11.5))
                        .foregroundStyle(attention ? Palette.needsYouText : Palette.muted)
                        .lineLimit(1)
                }
                Spacer(minLength: 4)
                let unread = agents.viewingManager ? 0 : agents.manager.unread.count
                if unread > 0 {
                    Text(unread > 99 ? "99+" : String(unread))
                        .font(TextStyle.label)
                        .monospacedDigit()
                        .foregroundStyle(.white)
                        .padding(.horizontal, 6)
                        .frame(minWidth: 18, minHeight: 18)
                        .background(Palette.working, in: Capsule())
                        .accessibilityLabel("\(unread) new")
                }
                Image(systemName: "chevron.right")
                    .font(.system(size: 9, weight: .bold))
                    .foregroundStyle(Palette.faint)
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 9)
        }
        .background(Palette.surface, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(Palette.line))
    }

    private var attention: Bool {
        !agents.manager.openCards.isEmpty || !agents.needsYou.isEmpty
    }

    private var summary: String {
        let cards = agents.manager.openCards.count
        if cards > 0 { return cards == 1 ? "1 card waiting for you" : "\(cards) cards waiting for you" }
        let waiting = agents.needsYou.count
        let working = agents.working.count
        if waiting > 0 { return waiting == 1 ? "1 agent needs you" : "\(waiting) agents need you" }
        if working > 0 { return working == 1 ? "1 agent working" : "\(working) agents working" }
        return "Ask about any of your agents"
    }
}

/// A row that highlights under the pointer and opens what it names.
private struct PanelRow<Label: View>: View {
    let action: () -> Void
    @ViewBuilder var label: Label
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            label
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(hovering ? Palette.fill : .clear, in: RoundedRectangle(cornerRadius: 9, style: .continuous))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
    }
}

/// An agent that ended while its thread was open in the panel.
private struct PanelGone: View {
    let back: () -> Void

    var body: some View {
        VStack(spacing: 10) {
            Text("This agent isn't running any more.")
                .font(TextStyle.body)
                .foregroundStyle(Palette.textSecondary)
            Button("All Agents", action: back)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// The edge between the editor and the panel: drag it to resize the panel.
struct ColumnDivider: View {
    @Binding var width: Double
    let maximum: Double
    @State private var start: Double?

    static let minimum = 300.0

    var body: some View {
        Rectangle()
            .fill(Palette.line)
            .frame(width: 1)
            .padding(.horizontal, 3)
            .contentShape(Rectangle())
            .onHover { inside in
                if inside { NSCursor.resizeLeftRight.push() } else { NSCursor.pop() }
            }
            .gesture(
                DragGesture(minimumDistance: 1)
                    .onChanged { drag in
                        let from = start ?? width
                        start = from
                        width = min(max(from - drag.translation.width, Self.minimum), max(maximum, Self.minimum))
                    }
                    .onEnded { _ in start = nil }
            )
            .padding(.horizontal, -3)
    }
}
