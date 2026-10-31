import SwiftUI

/// The agents in sections: any pinned on top, then those still working,
/// then those that came back to you (asking something first). The archived
/// fold away at the foot, above Agentman's card.
struct Sidebar: View {
    @Environment(\.windowHeaderLeading) private var headerLeading
    @Environment(AgentsStore.self) private var store
    @AppStorage(Sidebar.hiddenKey) private var hidden = false
    @AppStorage("sidebarWorkingOpen") private var workingOpen = true
    @AppStorage("sidebarArchivedOpen") private var archivedOpen = false

    // Its width as T3 Code's: dragged, kept, double-click for the default;
    // never so wide that the conversation is squeezed.
    static let widthKey = "sidebarWidth"
    static let hiddenKey = "sidebarHidden"
    static let defaultWidth: Double = 256
    static let minWidth: Double = 208

    static func clamp(_ width: Double, window: CGFloat) -> CGFloat {
        CGFloat(min(max(width, minWidth), max(minWidth, Double(window) - 640)))
    }

    var body: some View {
        @Bindable var store = store
        // No selection of the List's own: its highlight is the accent colour,
        // loud beside everything else here. Rows draw a quieter one.
        List {
            if store.folderFilter != nil || store.stateFilter != nil {
                ActiveFilters()
                    .listRowSeparator(.hidden)
            }

            let sections = store.sidebar
            if !sections.pinned.isEmpty {
                SectionHeading(title: "Pinned")
                ForEach(sections.pinned) { row($0) }
            }
            if !sections.working.isEmpty {
                SectionHeading(title: "Working", count: sections.working.count, open: $workingOpen)
                if workingOpen {
                    ForEach(sections.working) { row($0) }
                }
                if !sections.active.isEmpty { SectionRule() }
            } else if !sections.pinned.isEmpty, !sections.active.isEmpty {
                SectionRule()
            }
            ForEach(sections.active) { row($0) }

            // In a folder, the sessions that ran there and ended: read them,
            // or resume one.
            if store.folderFilter != nil, !store.earlierHere.isEmpty {
                Section {
                    ForEach(store.earlierHere) { session in
                        AgentButton(session: session)
                    }
                } header: {
                    Text("Earlier here")
                        .font(TextStyle.label)
                        .foregroundStyle(Palette.faint)
                        .textCase(nil)
                        .padding(.leading, SidebarMetrics.header)
                }
            }
        }
        .listStyle(.plain)
        .environment(\.defaultMinListRowHeight, 18)
        .onMoveCommand(perform: move)
        .scrollContentBackground(.hidden)
        .background(Palette.sidebar)
        .safeAreaInset(edge: .top, spacing: 0) {
            VStack(spacing: 0) {
                HStack(spacing: 8) {
                    AgentmanBadge(size: 20)
                    Text("Agentman").font(Fonts.sans(14, .semibold))
                    Spacer()
                    ChromeButton(symbol: "sidebar.left", help: "Hide the sidebar (⌃⌘S)") { hidden = true }
                }
                .padding(.leading, headerLeading)
                .padding(.trailing, 14)
                .frame(height: 32)
                HStack(spacing: 9) {
                    Image(systemName: "magnifyingglass")
                    TextField("Search agents", text: $store.search)
                        .textFieldStyle(.plain)
                        .accessibilityLabel("Search agents")
                    FilterMenu()
                        .labelStyle(.iconOnly)
                        .fixedSize()
                    Button { store.selection = .newAgent } label: {
                        Image(systemName: "square.and.pencil")
                    }
                    .help("New agent (⌘N)")
                    .accessibilityLabel("New agent")
                }
                .font(TextStyle.caption)
                .foregroundStyle(Palette.muted)
                .buttonStyle(.plain)
                .padding(.horizontal, 16)
                .padding(.vertical, 12)
            }
            .background(Palette.sidebar)
        }
        .safeAreaInset(edge: .bottom, spacing: 0) {
            VStack(spacing: 0) {
                shelves
                AgentmanCard()
                ConnectionFooter()
            }
            .background(Palette.sidebar)
        }
        .task { await store.loadFolders() }
        // The branch under each agent's title, kept current as agents switch.
        .task {
            while !Task.isCancelled {
                await store.readBranches()
                try? await Task.sleep(for: .seconds(10))
            }
        }
        .overlay {
            if store.sessions.isEmpty, store.status == .connected {
                ContentUnavailableView(
                    "No agents yet",
                    systemImage: "sparkles",
                    description: Text("Start one with am claude or am codex in a terminal.")
                )
                .padding(.top, 120)
            }
        }
    }

    /// The archived, at the foot of the list under a heading that folds
    /// them away; open, as many as fit in a part of the sidebar, scrolling
    /// past that.
    @ViewBuilder private var shelves: some View {
        let archived = store.sidebar.archived
        if !archived.isEmpty {
            VStack(spacing: 0) {
                SectionHeading(title: "Archived", count: archived.count, open: $archivedOpen)
                    .padding(.horizontal, SidebarMetrics.margin + SidebarMetrics.list)
                if archivedOpen { shelf(archived) }
            }
            .padding(.top, 4)
        }
    }

    private func shelf(_ sessions: [Session]) -> some View {
        ScrollView {
            VStack(spacing: 0) {
                ForEach(sessions) { session in
                    row(session, inList: false)
                        .padding(.vertical, 2)
                }
            }
        }
        .scrollIndicators(.never)
        .frame(maxHeight: min(CGFloat(sessions.count) * 80, 190))
    }

    private func row(_ session: Session, inList: Bool = true) -> some View {
        AgentButton(session: session, inList: inList)
            .listRowSeparator(.hidden)
            .listRowInsets(EdgeInsets(top: 2, leading: 10, bottom: 2, trailing: 10))
            .contextMenu {
                Button("Rename…") { store.renaming = session.id }
                Button("Show Project") { store.folderFilter = session.cwd }
                if let own = session.originalName {
                    Button("Use the agent's own name, “\(own)”") {
                        Task { await store.rename(session.id, to: "") }
                    }
                }
                Divider()
                if store.isPinned(session.id) {
                    Button("Unpin") { store.unpin(session.id) }
                } else {
                    Button("Pin") { store.pin(session.id) }
                }
                if store.isArchived(session) {
                    Button("Unarchive") { store.unarchive(session.id) }
                } else {
                    Button("Archive") { store.archive(session.id) }
                }
                Divider()
                // Agentman speaks for the agents while it is on;
                // this one can speak for itself as well.
                if store.manager.on {
                    Toggle("Notify Me About This Agent", isOn: Binding(
                        get: { session.alerts == true },
                        set: { on in Task { await store.setAlerts(session.id, on) } }
                    ))
                }
                FolderMuteButton(folder: session.cwd)
            }
    }

    /// Up and down step through the agents, as they would a List's own
    /// selection; up from the first opens Agentman.
    private func move(_ direction: MoveCommandDirection) {
        let ids = store.sidebar.shown(working: workingOpen, archived: archivedOpen).map(\.id)
            + (store.folderFilter == nil ? [] : store.earlierHere.map(\.id))
        guard !ids.isEmpty, direction == .up || direction == .down else { return }
        guard case .agent(let current) = store.selection, let at = ids.firstIndex(of: current) else {
            if direction == .down { store.selection = .agent(ids[0]) }
            return
        }
        if direction == .down {
            if at + 1 < ids.count { store.selection = .agent(ids[at + 1]) }
        } else {
            store.selection = at == 0 ? .manager : .agent(ids[at - 1])
        }
    }
}

/// An agent in the list, opening its thread when clicked.
private struct AgentButton: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    /// In the list, which draws a row's highlight; or at the sidebar's foot,
    /// which doesn't.
    var inList = true
    @State private var hovering = false

    var body: some View {
        AgentRow(session: session)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, inList ? 0 : SidebarMetrics.margin + SidebarMetrics.list)
            .contentShape(Rectangle())
            .onTapGesture { store.selection = .agent(session.id) }
            .onHover { hovering = $0 }
            .background { if !inList { RowHighlight(selected: selected, hovering: hovering) } }
            .listRowBackground(RowHighlight(selected: selected, hovering: hovering))
            .accessibilityAddTraits(selected ? [.isButton, .isSelected] : .isButton)
            .accessibilityAction { store.selection = .agent(session.id) }
    }

    private var selected: Bool { store.selection == .agent(session.id) }
}

/// Behind a row: grey with a hairline edge while it is open, fainter under
/// the pointer, as Agentman's card is.
private struct RowHighlight: View {
    var selected: Bool
    var hovering: Bool

    var body: some View {
        RoundedRectangle(cornerRadius: 7, style: .continuous)
            .fill(Color.primary.opacity(selected ? 0.10 : hovering ? 0.05 : 0))
            .overlay(
                RoundedRectangle(cornerRadius: 7, style: .continuous)
                    .strokeBorder(Color.primary.opacity(selected ? 0.10 : 0))
            )
            .padding(.horizontal, 10)
            .padding(.vertical, 1)
    }
}

/// Every row's mark sits in one column, so every name starts at the same
/// edge, and Agentman's above them.
private enum SidebarMetrics {
    static let mark: CGFloat = 22
    static let gap: CGFloat = 10
    /// Inside the selection's highlight, so a row's contents keep clear of
    /// its edges as they do of its top and bottom.
    static let inset: CGFloat = 5
    /// A section's title starts where the marks do.
    static let header: CGFloat = 7
    /// The list's highlight stops this far from the sidebar's sides, and
    /// Agentman's card does too.
    static let margin: CGFloat = 10
    /// The List's own room beside a row, inside the row's insets. What sits
    /// below the List adds it to line up with the rows.
    static let list: CGFloat = 8
}

/// Agentman, pinned at the top: its mark, what it has to say, and anything
/// new since the user last looked.
private struct AgentmanCard: View {
    @Environment(AgentsStore.self) private var store
    @State private var hovering = false

    var body: some View {
        Button {
            store.selection = .manager
        } label: {
            HStack(spacing: 11) {
                AgentmanBadge(size: 19)
                VStack(alignment: .leading, spacing: 2) {
                    Text("Agentman")
                        .font(Fonts.sans(13, .medium))
                        .foregroundStyle(Palette.text)
                    Text(summary)
                        .font(Fonts.sans(12))
                        .foregroundStyle(attention ? Palette.needsYouText : Palette.muted)
                        .lineLimit(1)
                }
                Spacer(minLength: 4)
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
            }
            .padding(.horizontal, 11)
            .padding(.vertical, 8)
            .background(background, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: 12, style: .continuous)
                    .strokeBorder(Palette.line.opacity(selected ? 1 : 0))
            )
            .contentShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .padding(.horizontal, SidebarMetrics.margin)
        .padding(.top, 6)
        .padding(.bottom, 4)
        .help("Agentman (⌥⌘0)")
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    private var selected: Bool {
        store.selection == .manager || store.selection == nil
    }

    /// Grey like a selected row when it is open; otherwise a card of its
    /// own, white on a light sidebar, so it never passes for the selection.
    private var background: Color {
        if selected { return Palette.fill }
        return hovering ? Palette.fill.opacity(0.6) : .clear
    }

    /// What arrived since the user last looked at the conversation.
    private var unread: Int {
        store.viewingManager ? 0 : store.manager.unread.count
    }

    private var attention: Bool {
        store.manager.openCards.count > 0 || !store.needsYou.isEmpty
    }

    private var summary: String {
        let cards = store.manager.openCards.count
        if cards > 0 { return cards == 1 ? "1 card waiting for you" : "\(cards) cards waiting for you" }
        let waiting = store.needsYou.count
        let working = store.working.count
        if waiting > 0 { return waiting == 1 ? "1 agent needs you" : "\(waiting) agents need you" }
        if working > 0 { return working == 1 ? "1 agent working" : "\(working) agents working" }
        return "All quiet"
    }
}

/// The desktop app's own icon, including its macOS optical inset.
struct AgentmanBadge: View {
    var size: CGFloat

    var body: some View {
        Image(nsImage: NSImage(named: NSImage.applicationIconName) ?? NSImage())
            .resizable()
            .interpolation(.high)
            .scaledToFit()
            .frame(width: size, height: size)
            .accessibilityHidden(true)
    }
}

struct AgentRow: View {
    @Environment(AgentsStore.self) private var store
    let session: Session

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 7) {
                Text(String((session.cwd as NSString).lastPathComponent.prefix(2)).uppercased())
                    .font(Fonts.sans(8, .bold))
                    .foregroundStyle(Palette.workingText)
                    .frame(width: 16, height: 16)
                    .background(Palette.workingWash, in: RoundedRectangle(cornerRadius: 4))
                Text((session.cwd as NSString).lastPathComponent)
                    .lineLimit(1)
                Spacer(minLength: 4)
                status
            }
            .font(TextStyle.caption)
            .foregroundStyle(Palette.muted)
            HStack(spacing: 5) {
                if store.renaming == session.id {
                    RenameField(session: session)
                } else {
                    Text(session.name)
                        .font(Fonts.sans(13.5, session.state == .waitingInput ? .medium : .regular))
                        .foregroundStyle(session.state == .ended || store.isArchived(session) ? Palette.muted : Palette.text)
                        .lineLimit(1)
                        .help(session.originalName.map { "Renamed from “\($0)”" } ?? session.name)
                }
                // With nothing under it, the agent's mark sits by its title.
                if !detailLine {
                    Spacer(minLength: 4)
                    AgentMark(kind: session.kind, size: 14)
                }
            }
            if detailLine {
            HStack(spacing: 5) {
                // What it asks while it needs you; otherwise its branch.
                if waiting {
                    Text(detail).lineLimit(1)
                        .foregroundStyle(Palette.needsYouText)
                } else if let branch = store.branches[session.cwd] {
                    Text(branch)
                        .foregroundStyle(Palette.muted)
                        .lineLimit(1)
                        .truncationMode(.middle)
                        .help("On branch \(branch)")
                }
                Spacer(minLength: 4)
                if let servers = session.servers, !servers.isEmpty {
                    HStack(spacing: 3) {
                        Image(systemName: "globe")
                            .font(.system(size: 9))
                            .foregroundStyle(Palette.ok)
                        Text(servers.map { ":\($0.port)" }.joined(separator: " "))
                            .font(Fonts.mono(10.5))
                            .lineLimit(1)
                    }
                    .help("Dev servers it is running")
                }
                AgentMark(kind: session.kind, size: 14)
            }
            .font(TextStyle.caption)
            }
        }
        .padding(.vertical, 8)
        .padding(.horizontal, SidebarMetrics.inset)
        .accessibilityElement(children: .combine)
    }

    private var waiting: Bool { AgentsStore.filterState(session) == .waitingInput }

    /// A third line, under the title: what it asks, its branch, its servers.
    private var detailLine: Bool {
        waiting || store.branches[session.cwd] != nil || !(session.servers ?? []).isEmpty
    }

    /// What it asks, while it needs you.
    private var detail: String {
        session.question?.title ?? session.question?.prompt ?? "Waiting for your answer"
    }

    /// At the top right, as T3 Code has it: what it is doing while that
    /// matters, otherwise how long since it last did anything.
    @ViewBuilder private var status: some View {
        if waiting {
            HStack(spacing: 4) {
                Circle().fill(Palette.dot(.waitingInput)).frame(width: 5, height: 5)
                Text("Needs you")
            }
            .foregroundStyle(Palette.needsYouText)
        } else if session.state == .busy {
            HStack(spacing: 4) {
                Loader(size: 9)
                Text("Working")
            }
            .foregroundStyle(Palette.workingText)
        } else if session.state == .ended {
            Text("Ended")
        } else if let when = session.lastActivity {
            Text(Relative.short(when)).monospacedDigit()
        }
    }
}

/// Whether the app can reach `am serve`, and which version it is.
private struct ConnectionFooter: View {
    @Environment(AgentsStore.self) private var store

    var body: some View {
        HStack(spacing: 7) {
            Circle()
                .fill(color)
                .frame(width: 7, height: 7)
            Text(text)
                .font(TextStyle.caption)
                .foregroundStyle(Palette.muted)
                .lineLimit(1)
            Spacer()
            AppearanceMenu()
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
        // Its own ground, so the list ends above it instead of running under.
        .background(Palette.sidebar)
        .overlay(alignment: .top) { Rectangle().fill(Palette.line).frame(height: 1) }
        .help(store.socketPath)
    }

    private var color: Color {
        switch store.status {
        case .connected: Palette.ok
        case .connecting: Palette.faint
        case .unavailable: Palette.error
        }
    }

    private var text: String {
        switch store.status {
        case .connected:
            if let version = store.daemonInfo?.version { return "Mac connected · am \(version)" }
            return "Mac connected"
        case .connecting:
            return "Connecting to am serve…"
        case .unavailable(let reason):
            return "\(reason). Run am serve."
        }
    }
}

/// "now", "4m", "3h", "2d": how long ago, in the fewest characters.
enum Relative {
    static func short(_ date: Date, now: Date = .now) -> String {
        let seconds = max(0, Int(now.timeIntervalSince(date)))
        switch seconds {
        case ..<60: return "now"
        case ..<3600: return "\(seconds / 60)m"
        case ..<86_400: return "\(seconds / 3600)h"
        default: return "\(seconds / 86_400)d"
        }
    }
}

/// A session's name, being edited where it shows: Return keeps it, Escape
/// or clicking away leaves it as it was.
private struct RenameField: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    @State private var name = ""
    @FocusState private var focused: Bool

    var body: some View {
        TextField("Name", text: $name)
            .textFieldStyle(.plain)
            .font(Fonts.sans(13.5))
            .padding(.horizontal, 4)
            .padding(.vertical, 1)
            .background(Palette.surface, in: RoundedRectangle(cornerRadius: 5, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 5, style: .continuous).strokeBorder(Palette.working.opacity(0.6)))
            .focused($focused)
            .onSubmit(save)
            .onExitCommand { store.renaming = nil }
            .onChange(of: focused) { _, now in
                if !now, store.renaming == session.id { store.renaming = nil }
            }
            .onAppear {
                name = session.name
                focused = true
            }
    }

    private func save() {
        let new = name.trimmingCharacters(in: .whitespacesAndNewlines)
        store.renaming = nil
        guard new != session.name else { return }
        Task { await store.rename(session.id, to: new) }
    }
}

/// One button for what the list shows: agents in one state, and in one
/// folder, with how many are in each.
private struct FilterMenu: View {
    @Environment(AgentsStore.self) private var store

    var body: some View {
        let counts = store.stateCounts
        Menu {
            Section("Show") {
                option("All agents", on: store.stateFilter == nil) { store.stateFilter = nil }
                option("Needs you", count: counts.needsYou, on: store.stateFilter == .waitingInput) { store.stateFilter = .waitingInput }
                option("Working", count: counts.working, on: store.stateFilter == .busy) { store.stateFilter = .busy }
                option("Idle", count: counts.idle, on: store.stateFilter == .idle) { store.stateFilter = .idle }
            }
            Section("Folder") {
                option("All folders", on: store.folderFilter == nil) { store.folderFilter = nil }
                ForEach(store.folders.prefix(15)) { folder in
                    option((folder.path as NSString).lastPathComponent, count: folder.agents,
                           on: store.folderFilter == folder.path) { store.folderFilter = folder.path }
                }
            }
        } label: {
            Label("Filter", systemImage: "line.3.horizontal.decrease")
                .foregroundStyle(filtering ? Palette.working : Palette.textSecondary)
        }
        .menuIndicator(.hidden)
        .help("Show agents in one state, or one folder with the sessions that ended there")
    }

    private var filtering: Bool { store.stateFilter != nil || store.folderFilter != nil }

    @ViewBuilder
    private func option(_ title: String, count: Int? = nil, on: Bool, action: @escaping () -> Void) -> some View {
        let text = count.map { "\(title)  \($0)" } ?? title
        Button(action: action) {
            if on { Label(text, systemImage: "checkmark") } else { Text(text) }
        }
    }
}

/// What the list is narrowed to, above it, each undone with its ×.
private struct ActiveFilters: View {
    @Environment(AgentsStore.self) private var store

    var body: some View {
        HStack(spacing: 6) {
            if let folder = store.folderFilter {
                token((folder as NSString).lastPathComponent, icon: "folder") { store.folderFilter = nil }
            }
            if let state = store.stateFilter {
                token(name(state), icon: nil) { store.stateFilter = nil }
            }
            Spacer(minLength: 0)
        }
        .padding(.vertical, 2)
        .padding(.horizontal, SidebarMetrics.inset)
    }

    private func name(_ state: SessionState) -> String {
        switch state {
        case .waitingInput: "Needs you"
        case .busy: "Working"
        default: "Idle"
        }
    }

    private func token(_ title: String, icon: String?, clear: @escaping () -> Void) -> some View {
        Button(action: clear) {
            HStack(spacing: 5) {
                if let icon { Image(systemName: icon).font(.system(size: 10, weight: .medium)) }
                Text(title).lineLimit(1)
                Image(systemName: "xmark").font(.system(size: 8, weight: .bold)).foregroundStyle(Palette.workingText.opacity(0.7))
            }
            .font(TextStyle.caption)
            .foregroundStyle(Palette.workingText)
            .padding(.horizontal, 9)
            .frame(height: 24)
            .background(Palette.workingWash, in: Capsule())
        }
        .buttonStyle(.plain)
        .help("Show every agent again")
    }
}

/// A folder's name over its agents; its menu mutes the folder's
/// notifications.
/// Mutes or unmutes the notifications from an agent's folder, as read
/// when the menu opens.
private struct FolderMuteButton: View {
    let folder: String

    var body: some View {
        let muted = NotificationPolicy.isMuted(folder)
        Button(muted ? "Unmute Notifications From This Folder" : "Mute Notifications From This Folder") {
            NotificationPolicy.setMuted(folder, !muted)
        }
    }
}

/// The line between two panes: drag to resize the pane it belongs to,
/// double-click for its default width. The sidebar grows as its trailing
/// edge moves right; the right panel, as its leading edge moves left.
struct PaneResizeHandle: View {
    @Binding var width: Double
    /// 1 when dragging right widens the pane, -1 when it narrows it.
    var grows: CGFloat = 1
    let clamp: (Double) -> CGFloat
    let reset: Double
    @State private var start: Double?

    var body: some View {
        Rectangle()
            .fill(Palette.line)
            .frame(width: 1)
            .frame(maxHeight: .infinity)
            .overlay {
                Color.clear
                    .frame(width: 8)
                    .contentShape(Rectangle())
                    .onHover { inside in
                        if inside { NSCursor.resizeLeftRight.push() } else { NSCursor.pop() }
                    }
                    .gesture(
                        DragGesture(minimumDistance: 1, coordinateSpace: .global)
                            .onChanged { drag in
                                let from = start ?? Double(clamp(width))
                                start = from
                                width = Double(clamp(from + Double(drag.translation.width * grows)))
                            }
                            .onEnded { _ in start = nil }
                    )
                    .onTapGesture(count: 2) { width = reset }
            }
            .accessibilityHidden(true)
    }
}

/// A heading in the list of agents: a section's name, and for one that
/// folds, how many are in it and a chevron that opens and closes it.
private struct SectionHeading: View {
    let title: String
    var count: Int? = nil
    var open: Binding<Bool>? = nil
    @State private var hovering = false

    var body: some View {
        HStack(spacing: 6) {
            Text(title)
                .font(Fonts.sans(12, .medium))
                .foregroundStyle(hovering && open != nil ? Palette.text : Palette.muted)
            Spacer(minLength: 4)
            if let count {
                Text(String(count))
                    .font(TextStyle.label)
                    .monospacedDigit()
                    .foregroundStyle(Palette.faint)
            }
            if let open {
                Image(systemName: "chevron.right")
                    .font(.system(size: 9, weight: .semibold))
                    .foregroundStyle(Palette.faint)
                    .rotationEffect(.degrees(open.wrappedValue ? 90 : 0))
            }
        }
        .padding(.horizontal, SidebarMetrics.inset)
        .padding(.top, 12)
        .padding(.bottom, 4)
        .contentShape(Rectangle())
        .onTapGesture {
            guard let open else { return }
            withAnimation(.easeOut(duration: 0.15)) { open.wrappedValue.toggle() }
        }
        .onHover { hovering = $0 }
        .listRowSeparator(.hidden)
        .listRowInsets(EdgeInsets(top: 0, leading: 10, bottom: 0, trailing: 10))
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(open == nil ? .isHeader : [.isHeader, .isButton])
        .accessibilityValue(open.map { $0.wrappedValue ? "Open" : "Closed" } ?? "")
    }
}

/// A hairline between the agents working and the rest.
private struct SectionRule: View {
    var body: some View {
        Rectangle()
            .fill(Palette.line)
            .frame(height: 1)
            .padding(.horizontal, SidebarMetrics.inset)
            .padding(.vertical, 6)
            .listRowSeparator(.hidden)
            .listRowInsets(EdgeInsets(top: 0, leading: 10, bottom: 0, trailing: 10))
            .accessibilityHidden(true)
    }
}

