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
            let found = store.sidebarHits
            if !sections.pinned.isEmpty {
                SectionHeading(title: "Pinned")
                ForEach(sections.pinned) { row($0, match: found.bySession[$0.id]) }
            }
            // What waits on you, before what is busy: it never sinks below
            // a long Working list.
            if !sections.needsYou.isEmpty {
                SectionHeading(title: "Needs you", count: sections.needsYou.count, tint: Palette.needsYouText)
                ForEach(sections.needsYou) { row($0, match: found.bySession[$0.id]) }
            }
            if !sections.working.isEmpty {
                SectionHeading(title: "Working", count: sections.working.count, open: $workingOpen)
                if workingOpen {
                    ForEach(sections.working) { row($0, match: found.bySession[$0.id]) }
                }
            }
            if !sections.active.isEmpty, !(sections.pinned.isEmpty && sections.needsYou.isEmpty && sections.working.isEmpty) {
                SectionRule()
            }
            ForEach(sections.active) { row($0, match: found.bySession[$0.id]) }

            // Searching, the conversations whose messages have the words
            // and whose names do not: running or long over.
            if !found.others.isEmpty {
                Section {
                    ForEach(found.others) { hit in
                        if let session = hit.session {
                            AgentButton(session: session, match: hit)
                                .listRowSeparator(.hidden)
                                .listRowInsets(EdgeInsets(top: 2, leading: 10, bottom: 2, trailing: 10))
                        }
                    }
                } header: {
                    Text("In messages")
                        .font(TextStyle.label)
                        .foregroundStyle(Palette.faint)
                        .textCase(nil)
                        .padding(.leading, SidebarMetrics.header)
                }
            }
            if found.searching, found.others.isEmpty, sections.isEmpty {
                Text(found.indexing ? "Reading your conversations…" : "Searching messages…")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.faint)
                    .padding(.leading, SidebarMetrics.header)
                    .listRowSeparator(.hidden)
            }

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
                    // The name, where it fits whole; at the narrowest the
                    // mark alone, rather than "Ag…".
                    ViewThatFits(in: .horizontal) {
                        Text("Agentman").font(Fonts.heading(14)).fixedSize()
                        Color.clear.frame(width: 0, height: 0)
                    }
                    Spacer(minLength: 0)
                    ChromeButton(symbol: "sidebar.left", help: "Hide the sidebar (⌃⌘S)") { hidden = true }
                }
                .padding(.leading, headerLeading)
                .padding(.trailing, 14)
                .frame(height: 32)
                HStack(spacing: 9) {
                    Image(systemName: "magnifyingglass")
                    TextField("Search", text: $store.search)
                        .textFieldStyle(.plain)
                        .accessibilityLabel("Search agents and messages")
                        .onExitCommand { store.search = "" }
                    FilterMenu()
                        .labelStyle(.iconOnly)
                        .fixedSize()
                    Button { store.newAgent(in: store.folderFilter) } label: {
                        Image(systemName: "square.and.pencil")
                    }
                    .help(store.folderFilter.map { "New agent in \(Worktrees.label($0))" } ?? "New agent (⌘N)")
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
        // What the conversations said, a moment after typing stops: the
        // daemon answers in milliseconds, so this only spares it a query
        // per key.
        .task(id: store.search) { await store.refreshMessageHits() }
        // The branch under each agent's title, kept current as agents switch.
        .task {
            while !Task.isCancelled {
                await store.readBranches()
                try? await Task.sleep(for: .seconds(10))
            }
        }
        .overlay {
            if store.sessions.isEmpty, store.status == .connected {
                if Look.site {
                    EmptySidebar { store.selection = .newAgent }
                        .padding(.top, 90)
                } else {
                    ContentUnavailableView {
                        Label("No agents yet", systemImage: "sparkles")
                    } description: {
                        Text("Start one here, or with am claude or am codex in a terminal.")
                    } actions: {
                        SmallButton(title: "New Agent", symbol: "square.and.pencil", primary: true) { store.selection = .newAgent }
                    }
                    .padding(.top, 120)
                }
            } else if Self.foundNothing(query: store.search, sections: store.sidebar, hits: store.sidebarHits) {
                // A search with nothing to show says so, instead of a blank list.
                ContentUnavailableView {
                    Label("Nothing found", systemImage: "magnifyingglass")
                } description: {
                    Text(store.folderFilter != nil || store.stateFilter != nil
                         ? "No agent matches \u{201C}\(store.search.trimmingCharacters(in: .whitespaces))\u{201D} with these filters."
                         : "No agent or message matches \u{201C}\(store.search.trimmingCharacters(in: .whitespaces))\u{201D}.")
                }
                .padding(.top, 60)
            } else if let folder = store.folderFilter, store.stateFilter == nil,
                      store.search.trimmingCharacters(in: .whitespaces).isEmpty,
                      store.sidebar.isEmpty, store.earlierHere.isEmpty {
                // A folder chosen before any agent has worked in it.
                ContentUnavailableView {
                    Label("No agents here yet", systemImage: "folder")
                } description: {
                    Text("Start one to work in \(Worktrees.label(folder)).")
                } actions: {
                    SmallButton(title: "New Agent Here", symbol: "square.and.pencil", primary: true) { store.newAgent(in: folder) }
                }
                .padding(.top, 60)
            }
        }
    }

    /// A search that found no agent and no message, and isn't still looking.
    nonisolated static func foundNothing(query: String, sections: SidebarSections, hits: SidebarHits) -> Bool {
        !query.trimmingCharacters(in: .whitespaces).isEmpty && sections.isEmpty && hits.others.isEmpty
            && !hits.searching && !hits.indexing
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
        // Rows of two lines; three and a half show before it scrolls.
        .frame(maxHeight: min(CGFloat(sessions.count) * 54, 190))
    }

    private func row(_ session: Session, inList: Bool = true, match: SearchHit? = nil) -> some View {
        AgentButton(session: session, inList: inList, match: match)
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
                if AgentThreadView.endable(session) {
                    Button("End Session…") { store.confirmEnd = session.id }
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
    /// What a search found in its conversation, which a click opens at.
    var match: SearchHit?
    @AppStorage(AgentColors.settingKey) private var colorBy: AgentColorBy = .none
    @State private var hovering = false

    var body: some View {
        let tint = AgentColors.color(for: session, by: colorBy)
        AgentRow(session: session, match: match, query: store.messageHits?.query ?? "")
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, inList ? 0 : SidebarMetrics.margin + SidebarMetrics.list)
            .contentShape(Rectangle())
            .onTapGesture { open() }
            .onHover { hovering = $0 }
            .background { if !inList { RowHighlight(selected: selected, hovering: hovering, tint: tint) } }
            .listRowBackground(RowHighlight(selected: selected, hovering: hovering, tint: tint))
            .accessibilityAddTraits(selected ? [.isButton, .isSelected] : .isButton)
            .accessibilityAction { open() }
    }

    private func open() {
        if let match, let query = store.messageHits?.query {
            store.open(match, query: query)
        } else {
            store.selection = .agent(session.id)
        }
    }

    private var selected: Bool { store.selection == .agent(session.id) }
}

/// Behind a row: grey with a hairline edge while it is open, fainter under
/// the pointer, as Agentman's card is. The colour the user picked for its
/// folder or project, if any, is a thin bar on the sidebar's own edge, out
/// of the row's way.
private struct RowHighlight: View {
    var selected: Bool
    var hovering: Bool
    var tint: Color?

    var body: some View {
        RoundedRectangle(cornerRadius: 7, style: .continuous)
            .fill(Color.primary.opacity(selected ? 0.10 : hovering ? 0.05 : 0))
            .overlay(
                RoundedRectangle(cornerRadius: 7, style: .continuous)
                    .strokeBorder(Color.primary.opacity(selected ? 0.10 : 0))
            )
            .padding(.horizontal, 10)
            .padding(.vertical, 1)
            .overlay(alignment: .leading) {
                if let tint {
                    UnevenRoundedRectangle(bottomTrailingRadius: 1, topTrailingRadius: 1, style: .continuous)
                        .fill(tint)
                        .frame(width: 2)
                        .padding(.vertical, 9)
                        .accessibilityHidden(true)
                }
            }
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
struct AgentmanCard: View {
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
            }
            .padding(.horizontal, 11)
            .padding(.vertical, 8)
            .look {
                $0.edged(12, fill: background, depth: selected ? 3 : 2)
            } classic: {
                $0.background(background, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
                    .overlay(
                        RoundedRectangle(cornerRadius: 12, style: .continuous)
                            .strokeBorder(Palette.line.opacity(selected ? 1 : 0))
                    )
            }
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

    /// Agentman's own card, in the site's cream with the edge; a shade
    /// deeper while it is open or under the pointer. In the Classic theme,
    /// grey like a selected row when it is open; otherwise a card of its
    /// own, white on a light sidebar, so it never passes for the selection.
    private var background: Color {
        if Look.site {
            return selected || hovering ? Palette.cream.mix(with: Palette.edge, by: 0.06) : Palette.cream
        }
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

    /// One line, the most pressing thing first: what Agentman needs you to
    /// decide, agents waiting on you, what it said since you last looked,
    /// who is working. One number, in words; there used to be a badge too,
    /// counting something else.
    private var summary: String {
        Self.summary(decisions: store.manager.openCards.count, waiting: store.needsYou.count, unread: unread,
                     working: store.working.count, connected: store.status == .connected)
    }

    nonisolated static func summary(decisions: Int, waiting: Int, unread: Int, working: Int, connected: Bool) -> String {
        if decisions > 0 { return decisions == 1 ? "1 thing needs you" : "\(decisions) things need you" }
        if waiting > 0 { return waiting == 1 ? "1 agent needs you" : "\(waiting) agents need you" }
        if unread > 0 { return unread == 1 ? "1 new message" : "\(unread) new messages" }
        if working > 0 { return working == 1 ? "1 agent working" : "\(working) agents working" }
        // Quiet is only known while connected.
        return connected ? "All quiet" : "Not connected"
    }
}

/// The desktop app's own icon, including its macOS optical inset.
struct AgentmanBadge: View {
    var size: CGFloat
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        Image(nsImage: NSImage(named: NSImage.applicationIconName) ?? NSImage())
            .resizable()
            .interpolation(.high)
            .scaledToFit()
            .frame(width: size, height: size)
            // Its black square sank into a dark window; a faint light edge,
            // following its own shape, keeps it there.
            .shadow(color: .white.opacity(scheme == .dark ? 0.35 : 0), radius: 0.6)
            .accessibilityHidden(true)
    }
}

struct AgentRow: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    /// What a search found in its conversation, shown under it.
    var match: SearchHit?
    var query = ""

    /// The dot's column, and the room after it: the second line starts under
    /// the title.
    private static let dot: CGFloat = 9
    private static let afterDot: CGFloat = 8

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            // What and when: its state, its title, how long since it last
            // did anything.
            HStack(spacing: Self.afterDot) {
                StateDot(state: dotState)
                    .frame(width: Self.dot, height: Self.dot)
                if store.renaming == session.id {
                    RenameField(session: session)
                } else {
                    Text(session.name)
                        .font(Fonts.sans(13.5, .medium))
                        .foregroundStyle(session.state == .ended || store.isArchived(session) ? Palette.muted : Palette.text)
                        .lineLimit(1)
                        .help(session.originalName.map { "Renamed from “\($0)”" } ?? session.name)
                }
                Spacer(minLength: 4)
                when
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
            }
            // Who and where: the agent's logo, then its folder and branch,
            // or what it asks while it needs you.
            HStack(spacing: 6) {
                AgentMark(kind: session.kind, size: 12)
                place
                    .lineLimit(1)
                    .truncationMode(.tail)
            }
            .font(TextStyle.caption)
            .padding(.leading, Self.dot + Self.afterDot)
            if let match {
                Text(SearchWords.snippet(match, query: query))
                    .font(TextStyle.caption)
                    .lineLimit(2)
                    .help(match.snippet)
                    .padding(.leading, Self.dot + Self.afterDot)
            }
        }
        .padding(.vertical, 7)
        .padding(.horizontal, SidebarMetrics.inset)
        .accessibilityElement(children: .combine)
    }

    private var waiting: Bool { AgentsStore.filterState(session) == .waitingInput }

    private var dotState: StateDot.Kind {
        if waiting { return .needsYou }
        return session.state == .busy ? .working : .idle
    }

    /// What it asks, while it needs you.
    private var detail: String {
        session.question?.title ?? session.question?.prompt ?? "Waiting for your answer"
    }

    /// Its folder, then what it asks while it needs you, or its branch and
    /// the dev servers it runs.
    private var place: Text {
        let folder = Worktrees.label(session.cwd)
        if waiting {
            return Text("\(folder) · \(detail)").foregroundStyle(Palette.needsYouText)
        }
        var line = Text(folder)
        if let branch = store.branches[session.cwd] {
            line = line + Text(" · \(branch)")
        }
        if let servers = session.servers, !servers.isEmpty {
            line = line + Text(" · ") + Text(servers.map { ":\($0.port)" }.joined(separator: " ")).font(Fonts.mono(10.5))
        }
        return line.foregroundStyle(Palette.muted)
    }

    /// At the top right: how long since it last did anything, or that it
    /// has ended.
    @ViewBuilder private var when: some View {
        if session.state == .ended, match == nil {
            Text("Ended")
        } else if let last = session.lastActivity {
            Text(Relative.short(last)).monospacedDigit()
        }
    }
}

/// An agent's state, before its title, in the same colours everywhere:
/// orange when it needs you, blue turning while it works, a grey ring
/// while it waits for something to do.
struct StateDot: View {
    enum Kind { case needsYou, working, idle }
    let state: Kind

    var body: some View {
        switch state {
        case .needsYou:
            Circle()
                .fill(Palette.needsYou)
                .frame(width: 8, height: 8)
                .accessibilityLabel("Needs you")
        case .working:
            // Only the working blue: orange is for needing you.
            Loader(size: 9, colors: [Palette.working])
        case .idle:
            Circle()
                .strokeBorder(Palette.faint, lineWidth: 1.5)
                .frame(width: 8, height: 8)
                .accessibilityHidden(true)
        }
    }
}

/// Whether the app can reach `am serve`, and which version it is; while the
/// app sets the daemon up by itself, how that goes; and when am serve is
/// newer than the app, the update that matches it (AppCatchUp), a click
/// away.
struct ConnectionFooter: View {
    @Environment(AgentsStore.self) private var store
    @Environment(\.openSettings) private var openSettings
    @AppStorage("settingsTab") private var settingsTab = "general"

    var body: some View {
        HStack(spacing: 7) {
            if store.setup.note == .updating {
                Loader(size: 8)
                    .frame(width: 8, height: 8)
            } else {
                Circle()
                    .fill(color)
                    .frame(width: 7, height: 7)
            }
            Text(text)
                .font(TextStyle.caption)
                .foregroundStyle(Palette.muted)
                .lineLimit(1)
            Spacer()
            UsageButton()
            AppearanceMenu()
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
        // Its own ground, so the list ends above it instead of running under.
        .background(Palette.sidebar)
        .overlay(alignment: .top) { Rectangle().fill(Palette.line).frame(height: 1) }
        .contentShape(Rectangle())
        // A setup that didn't work is explained in Settings, with what to
        // do; an update to match am serve is Sparkle's to show.
        .onTapGesture {
            if case .failed = store.setup.note {
                settingsTab = SettingsPage.general.rawValue
                openSettings()
            } else if store.setup.note == nil, let note = store.catchUp.note {
                store.catchUp.act(on: note)
            }
        }
        .help(help)
    }

    private var help: String {
        if case .failed(let message) = store.setup.note { return message + " Click for Settings." }
        if store.setup.note == nil, let note = store.catchUp.note {
            switch note {
            case .waiting(let version):
                return "am serve was updated to \(version). Click to update Agentman to match."
            case .restart(let version):
                return "Agentman \(version) is downloaded and installs when you quit. Click to restart into it now."
            }
        }
        return store.socketPath
    }

    private var color: Color {
        switch store.setup.note {
        case .updated: return Palette.ok
        case .failed: return Palette.error
        default: break
        }
        if store.catchUp.note != nil { return Palette.working }
        switch store.status {
        case .connected: return Palette.ok
        case .connecting: return Palette.faint
        case .unavailable: return Palette.error
        }
    }

    private var text: String {
        switch store.setup.note {
        case .updating: return "Updating am serve…"
        case .updated(let version): return version.isEmpty ? "am serve updated" : "am serve updated to \(version)"
        case .failed: return "Couldn't update am serve"
        case nil: break
        }
        switch store.catchUp.note {
        case .waiting(let version): return "Update Agentman to \(version)"
        case .restart: return "Restart to update Agentman"
        case nil: break
        }
        switch store.status {
        case .connected:
            if let version = store.daemonInfo?.version { return "Mac connected · am \(version)" }
            return "Mac connected"
        case .connecting:
            return "Connecting…"
        case .unavailable:
            // The whole story, and the way back, is in the banner over the page.
            return "Not connected"
        }
    }
}

/// Opens the usage page, beside the appearance switch.
private struct UsageButton: View {
    @Environment(AgentsStore.self) private var store

    var body: some View {
        let open = store.selection == .usage
        Button { store.selection = .usage } label: {
            Image(systemName: "chart.xyaxis.line")
                .font(.system(size: 13))
                .foregroundStyle(open ? Palette.text : Palette.muted)
                .frame(width: 22, height: 20)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help("Usage: what the agents spent")
        .accessibilityLabel("Usage")
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
/// folder. It opens the filter panel.
private struct FilterMenu: View {
    @Environment(AgentsStore.self) private var store
    @State private var open = false

    var body: some View {
        Button { open.toggle() } label: {
            Label("Filter", systemImage: "line.3.horizontal.decrease")
                .foregroundStyle(filtering ? Palette.working : Palette.textSecondary)
        }
        .help("Show agents in one state, or in one folder with the sessions that ended there")
        .popover(isPresented: $open, arrowEdge: .bottom) {
            FilterPanel { open = false }
                .environment(store)
        }
    }

    private var filtering: Bool { store.stateFilter != nil || store.folderFilter != nil }
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
    /// The title's colour, where the section calls for attention.
    var tint: Color? = nil
    @State private var hovering = false

    var body: some View {
        HStack(spacing: 6) {
            Text(title)
                .font(Look.site ? Fonts.eyebrow : Fonts.sans(12, .medium))
                .tracking(Look.site ? 1.2 : 0)
                .textCase(Look.site ? .uppercase : nil)
                .foregroundStyle(tint ?? (hovering && open != nil ? Palette.text : Palette.muted))
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

/// Which folder an agent works in: the folder's own logo when the daemon
/// found one, otherwise its first two letters on the brand wash.
struct FolderMark: View {
    let session: Session
    var size: CGFloat = 16

    var body: some View {
        if let image = FolderLogos.image(cwd: session.cwd, icon: session.folderIcon) {
            Image(nsImage: image)
                .resizable()
                .interpolation(.high)
                .scaledToFill()
                .frame(width: size, height: size)
                .clipShape(RoundedRectangle(cornerRadius: size / 4, style: .continuous))
                .accessibilityHidden(true)
        } else {
            Text(String((Worktrees.parts(of: session.cwd)?.repo ?? (session.cwd as NSString).lastPathComponent).prefix(2)).uppercased())
                .font(Fonts.sans(8, .bold))
                .foregroundStyle(Palette.workingText)
                .frame(width: size, height: size)
                .background(Palette.workingWash, in: RoundedRectangle(cornerRadius: size / 4))
        }
    }
}

/// Folder logos, read from disk once each: the daemon names the file, and
/// it is on this Mac. A file that cannot be read is remembered as none.
@MainActor
enum FolderLogos {
    private static var loaded: [String: NSImage?] = [:]

    static func image(cwd: String, icon: String?) -> NSImage? {
        guard let icon, !icon.isEmpty, !icon.contains("..") else { return nil }
        let path = (cwd as NSString).appendingPathComponent(icon)
        if let known = loaded[path] { return known }
        let image = NSImage(contentsOfFile: path)
        loaded[path] = image
        return image
    }
}

/// No agent has run on this Mac yet: the website's terminal sticker on its
/// dots, a Rubik title, the commands set as code so they never break across
/// lines, and the site's orange button.
private struct EmptySidebar: View {
    let newAgent: () -> Void
    @State private var hovering = false

    var body: some View {
        VStack(spacing: 8) {
            StickerOnDots(name: .terminal, size: 118)
            Text("No agents yet")
                .font(Fonts.heading(17))
                .foregroundStyle(Palette.text)
            (Text("Start one here, or with ") + Text("am claude").font(Fonts.mono(11.5, .medium))
                + Text(" or ") + Text("am codex").font(Fonts.mono(11.5, .medium)) + Text(" in a terminal."))
                .font(TextStyle.caption)
                .foregroundStyle(Palette.muted)
                .multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.horizontal, 22)
            Button(action: newAgent) {
                Label("New Agent", systemImage: "square.and.pencil")
                    .font(Fonts.sans(12.5, .semibold))
                    .foregroundStyle(Color(light: 0x160D08, dark: 0x160D08))
                    .padding(.horizontal, 12)
                    .frame(height: 30)
                    .edged(9, fill: Palette.flood.opacity(hovering ? 0.9 : 1), depth: 2)
            }
            .buttonStyle(.plain)
            .onHover { hovering = $0 }
            .padding(.top, 6)
        }
        .frame(maxWidth: .infinity)
    }
}
