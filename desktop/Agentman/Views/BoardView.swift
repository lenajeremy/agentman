import AppKit
import SwiftUI

/// Agentman's page: every agent as a card, in a column for what it wants
/// from the user, with Agentman's brief above and a box to ask it anything
/// floating below. Its conversation opens beside the columns. Quiet on
/// purpose: one size of type on a card, and colour only where a state is.
struct BoardView: View {
    /// Agentman's conversation, beside the columns.
    static let chatWidth: CGFloat = 380
    private static let margin: CGFloat = 20
    private static let gap: CGFloat = 10
    /// Columns stretch between these, and scroll sideways rather than get
    /// too narrow to read.
    private static let narrowest: CGFloat = 264
    private static let widest: CGFloat = 400
    /// What the columns' last cards keep clear of the floating box.
    private static let askClearance: CGFloat = 92

    @Environment(AgentsStore.self) private var store
    @Environment(\.detailHeaderLeading) private var headerLeading
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var chatAutofocus = ChatAutofocus()
    @State private var draft = ""
    @State private var attachments: [Attachment] = []
    @State private var sending = false
    @State private var dropping = false
    @State private var confirmNewChat = false

    private var manager: ManagerStore { store.manager }

    var body: some View {
        let sections = store.board
        let outlined = store.boardChatOpen ? BoardSections.mentioned(in: manager.entries, sessions: store.sessions) : []
        HStack(spacing: 0) {
            VStack(spacing: 0) {
                topBar
                GeometryReader { geometry in
                    board(sections, outlined: outlined, room: geometry.size)
                }
            }
            .frame(minWidth: 0, maxWidth: .infinity)
            .overlay(alignment: .bottom) {
                if !store.boardChatOpen {
                    ask.transition(.opacity)
                }
            }
            if store.boardChatOpen {
                chat
                    .frame(width: Self.chatWidth)
                    .transition(.move(edge: .trailing))
            }
        }
        .animation(reduceMotion ? nil : .easeOut(duration: 0.2), value: store.boardChatOpen)
        .environment(\.chatAutofocus, chatAutofocus)
        .confirmationDialog("Start a new chat?", isPresented: $confirmNewChat) {
            Button("New Chat") {
                Task { manager.problem = await manager.reset() }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text(ManagerChatView.newChatMessage)
        }
    }

    private var topBar: some View {
        HStack(spacing: 4) {
            if headerLeading > 0 {
                Text("Agentman")
                    .font(Fonts.sans(13, .medium))
                    .foregroundStyle(Palette.textSecondary)
            }
            Spacer()
            HandledButton()
            folderMenu
                .padding(.trailing, 4)
            if !manager.schedules.isEmpty { SchedulesButton() }
            ChromeButton(symbol: "square.and.pencil", help: "New agent") { store.selection = .newAgent }
            if !store.boardChatOpen {
                ChromeButton(symbol: "bubble.left.and.bubble.right", help: "Open the conversation with Agentman") {
                    store.boardChatOpen = true
                }
                .overlay(alignment: .topTrailing) {
                    if !manager.unread.isEmpty {
                        Circle()
                            .fill(Palette.needsYou)
                            .frame(width: 7, height: 7)
                            .padding(4)
                            .accessibilityLabel("New from Agentman")
                    }
                }
            }
        }
        .padding(.leading, 16 + headerLeading)
        .padding(.trailing, 12)
        .frame(height: 32)
        .windowTopBar()
    }

    private var folderMenu: some View {
        Menu {
            Button("All Folders") { store.folderFilter = nil }
            Divider()
            ForEach(store.folders) { folder in
                Button((folder.path as NSString).lastPathComponent) { store.folderFilter = folder.path }
            }
        } label: {
            HStack(spacing: 5) {
                Text(store.folderFilter.map { ($0 as NSString).lastPathComponent } ?? "All folders")
                    .lineLimit(1)
                Image(systemName: "chevron.down")
                    .font(.system(size: 8, weight: .semibold))
                    .foregroundStyle(Palette.muted)
            }
            .font(Fonts.sans(12))
            .foregroundStyle(Palette.textSecondary)
            .padding(.horizontal, 8)
            .frame(height: 24)
            .background(Palette.surface, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 6, style: .continuous).strokeBorder(Palette.line))
            .contentShape(RoundedRectangle(cornerRadius: 6, style: .continuous))
        }
        .menuStyle(.button)
        .buttonStyle(.plain)
        .menuIndicator(.hidden)
        .fixedSize()
        .help("Show one folder's agents")
        .task { await store.loadFolders() }
    }

    /// The brief, and the columns under it, sharing their edges.
    private func board(_ sections: BoardSections, outlined: Set<String>, room: CGSize) -> some View {
        let column = max(Self.narrowest, min(Self.widest, (room.width - Self.margin * 2 - Self.gap * 2) / 3))
        let width = column * 3 + Self.gap * 2
        let scrolls = width + Self.margin * 2 > room.width
        return VStack(alignment: .leading, spacing: 0) {
            brief(sections)
                .frame(width: min(width, room.width - Self.margin * 2), alignment: .leading)
                .frame(maxWidth: .infinity)
            ScrollView(.horizontal) {
                HStack(alignment: .top, spacing: Self.gap) {
                    BoardColumn(kind: .needsYou, sessions: sections.needsYou, outlined: outlined, offers: manager.offers, clearance: clearance)
                        .frame(width: column)
                    BoardColumn(kind: .working, sessions: sections.working, outlined: outlined, clearance: clearance)
                        .frame(width: column)
                    BoardColumn(kind: .idle, sessions: sections.idle, outlined: outlined, hidden: sections.hidden, clearance: clearance)
                        .frame(width: column)
                }
                .padding(.horizontal, Self.margin)
                .frame(minWidth: room.width)
                // Cards leave one column and arrive in the next as agents move.
                .animation(reduceMotion ? nil : .spring(duration: 0.35), value: sections.layout)
            }
            .scrollIndicators(.never)
            .scrollDisabled(!scrolls)
        }
    }

    private var clearance: CGFloat { store.boardChatOpen ? 12 : Self.askClearance }

    private func brief(_ sections: BoardSections) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(sections.headline(connected: store.status == .connected && store.sessionsLoaded))
                .font(Fonts.sans(20, .semibold))
                .tracking(-0.2)
                .foregroundStyle(Palette.text)
                .lineLimit(1)
                .minimumScaleFactor(0.85)
            HStack(spacing: 8) {
                Text(detail(sections))
                    .font(Fonts.sans(13))
                    .foregroundStyle(Palette.muted)
                    .lineLimit(1)
                    .truncationMode(.tail)
                if showsStart(sections) {
                    BoardLink(link: .init(title: "New agent", symbol: "plus") { store.selection = .newAgent })
                }
            }
        }
        .padding(.horizontal, 6)
        .padding(.top, 10)
        .padding(.bottom, 16)
        .accessibilityElement(children: .combine)
    }

    /// With nothing on the Board at all, the way to put something there.
    private func showsStart(_ sections: BoardSections) -> Bool {
        sections.shown == 0 && sections.hidden.isEmpty && store.status == .connected && store.sessionsLoaded
    }

    private func detail(_ sections: BoardSections) -> String {
        if showsStart(sections) { return "Start one and it shows up here." }
        return sections.detail.isEmpty ? " " : sections.detail
    }

    /// One box to ask Agentman, floating over the columns' foot.
    private var ask: some View {
        ManagerBox(draft: $draft, attachments: $attachments, dropping: $dropping, sending: sending, keepsFooter: false, send: send)
            .frame(maxWidth: 620)
            .shadow(color: .black.opacity(0.07), radius: 18, y: 6)
            // Agentman's toasts keep clear of it.
            .anchorPreference(key: MessageBoxFrame.self, value: .bounds) { $0 }
            .frame(maxWidth: .infinity)
            .padding(.horizontal, Self.margin)
            .padding(.bottom, 16)
    }

    private func send() {
        let message = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !message.isEmpty || !attachments.isEmpty, !sending else { return }
        sending = true
        let images = attachments
        store.boardChatOpen = true
        Task {
            if await manager.send(message, images: images) {
                draft = ""
                attachments = []
            }
            sending = false
        }
    }

    private var chat: some View {
        VStack(spacing: 0) {
            HStack(spacing: 4) {
                Text("Agentman")
                    .font(Fonts.sans(13, .medium))
                    .foregroundStyle(Palette.textSecondary)
                Spacer()
                if !manager.entries.isEmpty {
                    ChromeButton(symbol: "plus.bubble", help: "Start a new conversation with Agentman") {
                        confirmNewChat = true
                    }
                }
                ChromeButton(symbol: "xmark", help: "Close the conversation") {
                    store.boardChatOpen = false
                }
            }
            .padding(.leading, 16)
            .padding(.trailing, 12)
            .frame(height: 32)
            .windowTopBar()
            ManagerChatView(inPanel: true)
        }
        .background(Palette.paper)
        .overlay(alignment: .leading) {
            Rectangle().fill(Palette.line).frame(width: 1)
        }
    }
}

/// What Agentman has approved by itself today, each with the way to have it
/// ask next time.
private struct HandledButton: View {
    @Environment(AgentsStore.self) private var store
    @State private var open = false

    var body: some View {
        let handled = store.manager.handledToday
        if !handled.isEmpty {
            Button { open.toggle() } label: {
                HStack(spacing: 4) {
                    Image(systemName: "checkmark.circle")
                        .font(.system(size: 11))
                    Text("\(handled.count) handled")
                        .font(Fonts.sans(12))
                        .monospacedDigit()
                }
                .foregroundStyle(Palette.muted)
                .padding(.horizontal, 8)
                .frame(height: 24)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("What Agentman approved for you today")
            .popover(isPresented: $open, arrowEdge: .bottom) {
                ScrollView {
                    VStack(alignment: .leading, spacing: 0) {
                        Text("Approved for you today")
                            .font(Fonts.sans(13, .medium))
                            .foregroundStyle(Palette.text)
                            .padding(.bottom, 8)
                        ForEach(handled) { entry in
                            HandledRow(entry: entry)
                        }
                    }
                    .padding(14)
                }
                .frame(width: 400)
                .frame(maxHeight: 420)
            }
        }
    }
}

private struct HandledRow: View {
    @Environment(AgentsStore.self) private var store
    let entry: ManagerEntry
    @State private var asked = false

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Text(CompactCard.inline(entry.text))
                .font(Fonts.sans(12.5))
                .foregroundStyle(Palette.textSecondary)
                .lineLimit(2)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 8)
            if let approved = entry.approved {
                Button(asked ? "Will ask" : "Ask next time") {
                    Task { asked = await store.manager.alwaysAsk(about: approved) == nil }
                }
                .buttonStyle(.link)
                .font(Fonts.sans(12))
                .disabled(asked)
            }
        }
        .padding(.vertical, 5)
    }
}

/// What a column holds, and how it says so.
private enum BoardColumnKind {
    case needsYou, working, idle

    var title: String {
        switch self {
        case .needsYou: "Needs you"
        case .working: "Working"
        case .idle: "Idle"
        }
    }

    var empty: String {
        switch self {
        case .needsYou: "Nothing needs you"
        case .working: "Nothing running"
        case .idle: "No idle agents"
        }
    }
}

private struct BoardColumn: View {
    @Environment(AgentsStore.self) private var store
    let kind: BoardColumnKind
    let sessions: [Session]
    let outlined: Set<String>
    var hidden: [Session] = []
    /// What Agentman offers the user, above the agents waiting on them.
    var offers: [ManagerCard] = []
    let clearance: CGFloat

    var body: some View {
        VStack(spacing: 0) {
            header
            ScrollView {
                LazyVStack(spacing: 6) {
                    ForEach(offers) { offer in
                        CompactCard(card: offer) { await store.manager.dismiss(offer) }
                            .environment(\.roomyChoices, true)
                            .background(Palette.surface, in: RoundedRectangle(cornerRadius: 9, style: .continuous))
                            .overlay(RoundedRectangle(cornerRadius: 9, style: .continuous).strokeBorder(Palette.line))
                            .transition(.scale(scale: 0.97).combined(with: .opacity))
                    }
                    ForEach(sessions) { session in
                        BoardCard(session: session, kind: kind, outlined: outlined.contains(session.id))
                            .transition(.scale(scale: 0.97).combined(with: .opacity))
                    }
                    if sessions.isEmpty, offers.isEmpty {
                        Text(kind.empty)
                            .font(Fonts.sans(12))
                            .foregroundStyle(Palette.boardMuted)
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 20)
                    }
                }
                .padding(.horizontal, 6)
                .padding(.top, 2)
            }
            .contentMargins(.bottom, clearance, for: .scrollContent)
            .scrollIndicators(.never)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
        .background(Palette.boardWell, in: UnevenRoundedRectangle(topLeadingRadius: 10, topTrailingRadius: 10, style: .continuous))
    }

    private var header: some View {
        HStack(spacing: 7) {
            glyph.frame(width: 14, height: 14)
            Text(kind.title)
                .font(Fonts.sans(13, .medium))
                .foregroundStyle(Palette.text)
            Text("\(sessions.count + offers.count)")
                .font(Fonts.sans(13))
                .monospacedDigit()
                .foregroundStyle(Palette.boardMuted)
            Spacer()
            if !hidden.isEmpty { hiddenMenu }
        }
        .padding(.horizontal, 12)
        .frame(height: 38)
    }

    /// Colour and motion only while the column holds something.
    @ViewBuilder private var glyph: some View {
        switch (kind, sessions.isEmpty && offers.isEmpty) {
        case (.needsYou, false):
            Image(systemName: "exclamationmark.circle.fill")
                .font(.system(size: 13))
                .foregroundStyle(Palette.needsYou)
        case (.working, false):
            Loader(size: 11)
        default:
            Image(systemName: "circle")
                .font(.system(size: 13, weight: .medium))
                .foregroundStyle(Palette.faint)
        }
    }

    private var hiddenMenu: some View {
        Menu {
            ForEach(hidden) { session in
                Button(session.name) { store.showOnBoard(session.id) }
            }
            Divider()
            Button("Show All") { store.showAllOnBoard() }
        } label: {
            HStack(spacing: 4) {
                Image(systemName: "eye.slash")
                    .font(.system(size: 10))
                Text("\(hidden.count) hidden")
                    .font(Fonts.sans(12))
            }
            .foregroundStyle(Palette.boardMuted)
            .frame(height: 22)
            .contentShape(Rectangle())
        }
        .menuStyle(.button)
        .buttonStyle(.plain)
        .menuIndicator(.hidden)
        .fixedSize()
        .help("Agents you hid. They come back by themselves when they need you.")
    }
}

/// One agent: where it is, what it is called, and what it wants, answerable
/// or followable on the spot. Clicking anywhere else opens its conversation.
private struct BoardCard: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    let kind: BoardColumnKind
    let outlined: Bool
    @State private var hovering = false
    @State private var git: GitState?

    private static let shape = RoundedRectangle(cornerRadius: 9, style: .continuous)

    private var brief: Brief? { store.manager.briefs[session.id] }

    var body: some View {
        let digest = BoardDigest.make(store.messages[session.id] ?? [])
        VStack(alignment: .leading, spacing: 9) {
            VStack(alignment: .leading, spacing: 5) {
                meta(digest)
                Text(session.name)
                    .font(Fonts.sans(13.5, .medium))
                    .foregroundStyle(Palette.text)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
                    .help(session.name)
            }
            switch kind {
            case .needsYou: needsYou(digest)
            case .working: working(digest)
            case .idle: idle(digest)
            }
            if let problem = store.actionError[session.id] {
                Text(problem)
                    .font(Fonts.sans(12))
                    .foregroundStyle(Palette.errorText)
                    .lineLimit(2)
            }
        }
        .padding(11)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Palette.surface, in: Self.shape)
        .overlay(Self.shape.strokeBorder(hovering ? Palette.fillStrong : Palette.line))
        .overlay {
            // The cards Agentman's answer is about.
            if outlined { Self.shape.strokeBorder(Palette.working, lineWidth: 1.5) }
        }
        .shadow(color: .black.opacity(0.025), radius: 1.5, y: 1)
        .contentShape(Self.shape)
        .onTapGesture(perform: open)
        .onHover { hovering = $0 }
        .animation(.easeOut(duration: 0.12), value: hovering)
        .contextMenu { menuItems }
        .onAppear { store.watch(session.id) }
        .onDisappear { store.unwatch(session.id) }
        // What is left to commit, read again whenever it did something.
        .task(id: kind == .idle ? session.lastActivityAt : nil) {
            guard kind == .idle else { return }
            git = try? await store.git(.gitStatus, session: session.id)
        }
        .accessibilityElement(children: .contain)
        .accessibilityAction(named: "Open conversation", open)
    }

    private func open() {
        store.selection = .agent(session.id)
    }

    private func meta(_ digest: BoardDigest) -> some View {
        HStack(spacing: 5) {
            FolderMark(session: session, size: 14)
            Text(Worktrees.label(session.cwd))
                .lineLimit(1)
            Spacer(minLength: 4)
            if let when = kind == .working ? digest.turnStarted : session.lastActivity {
                Text(Relative.short(when)).monospacedDigit()
            }
            // The menu takes the mark's place under the pointer, so the
            // mark sits at the card's edge the rest of the time.
            ZStack {
                AgentMark(kind: session.kind, size: 13)
                    .opacity(hovering ? 0 : 1)
                Menu { menuItems } label: {
                    Image(systemName: "ellipsis")
                        .font(.system(size: 12, weight: .semibold))
                        .frame(width: 16, height: 16)
                        .contentShape(Rectangle())
                }
                .menuStyle(.button)
                .buttonStyle(.plain)
                .menuIndicator(.hidden)
                .fixedSize()
                .opacity(hovering ? 1 : 0)
                .help("More for this agent")
                .accessibilityLabel("More for \(session.name)")
            }
        }
        .font(Fonts.sans(12))
        .foregroundStyle(Palette.muted)
    }

    @ViewBuilder private var menuItems: some View {
        Button("Open Conversation", action: open)
        if kind != .needsYou {
            Button("Hide from the Board") { store.hideFromBoard(session.id) }
        }
        Menu("Trust This Folder") {
            let level = FolderTrust.level(for: session.cwd, in: store.manager.settings)
            ForEach(FolderTrust.levels, id: \.id) { option in
                Button {
                    Task { await store.manager.setTrust(option.id, for: session.cwd) }
                } label: {
                    if option.id == level {
                        Label(option.title, systemImage: "checkmark")
                    } else {
                        Text(option.title)
                    }
                }
            }
        }
        if kind == .idle {
            Divider()
            Button("Archive") { store.archive(session.id) }
        }
    }

    // MARK: - Needs you

    @ViewBuilder private func needsYou(_ digest: BoardDigest) -> some View {
        let card = store.manager.openCards.first { $0.sessionId == session.id && ($0.kind == "question" || $0.kind == "needs_you") }
        if let question = session.question {
            line(question.title ?? "Needs your answer", lines: 3)
            if let detail = question.detail, !detail.isEmpty {
                CodeBox(text: detail, compact: true)
            }
            if !question.prompt.isEmpty, !question.promptRepeatsDetail {
                line(question.prompt, lines: 4)
            }
            QuestionChoices(question: question, suggested: card?.questionId == question.id ? card?.suggested : nil) { reply in
                await store.answer(session.id, question: question, reply: reply)
            }
            .environment(\.compactChoices, true)
            .environment(\.roomyChoices, true)
            .id(question.id ?? question.prompt)
            .padding(.horizontal, -5)
        } else if let card {
            line(card.title, lines: 3)
            if let question = card.question {
                QuestionChoices(question: question, suggested: card.suggested, typedPrompt: "Or reply to Agentman") { reply in
                    await store.manager.reply(to: card, with: reply) == nil
                }
                .environment(\.compactChoices, true)
                .environment(\.roomyChoices, true)
                .id(card.id)
                .padding(.horizontal, -5)
            } else {
                BoardReply(placeholder: "Reply to Agentman") { text in
                    await store.manager.reply(to: card, with: QuestionReply(text: text)) == nil
                }
            }
        } else {
            line(brief?.needsYou ?? digest.said ?? "Waiting for your answer", lines: 3)
            if session.canReceive {
                BoardReply(placeholder: "Reply to \(session.name)") { text in
                    await store.send(text, to: session.id)
                }
            }
        }
    }

    // MARK: - Working

    @ViewBuilder private func working(_ digest: BoardDigest) -> some View {
        if let now = brief?.now, !now.isEmpty {
            line(now, lines: 2)
        }
        if !digest.steps.isEmpty {
            VStack(alignment: .leading, spacing: 4) {
                ForEach(digest.steps) { step in
                    BoardStep(step: step)
                }
            }
        }
        if let progress = digest.progress {
            BoardProgress(progress: progress)
        }
        if let command = digest.command {
            Text(command)
                .font(Fonts.mono(11.5))
                .foregroundStyle(Palette.textSecondary)
                .lineLimit(1)
                .truncationMode(.middle)
                .padding(.horizontal, 8)
                .padding(.vertical, 6)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Palette.fill.opacity(0.6), in: RoundedRectangle(cornerRadius: 6, style: .continuous))
                .help(command)
        }
    }

    // MARK: - Idle

    @ViewBuilder private func idle(_ digest: BoardDigest) -> some View {
        if let text = brief?.summary ?? digest.said, !text.isEmpty {
            line(text, lines: 2)
        }
        if let check = store.manager.verifications[session.id] {
            BoardCheck(check: check)
        }
        let links = links
        if !links.isEmpty {
            FlowLayout(spacing: 5) {
                ForEach(links) { link in
                    BoardLink(link: link)
                }
            }
        }
    }

    private var links: [BoardLink.Item] {
        var links: [BoardLink.Item] = []
        let id = session.id
        if let changes = git?.changes, changes > 0 {
            links.append(.init(title: changes == 1 ? "Review 1 file" : "Review \(changes) files", symbol: "plus.forwardslash.minus") { [store] in
                store.selection = .agent(id)
                RightPanel.show(.diff)
            })
        }
        let action = GitAction.quick(git)
        if git != nil, !action.isIdle {
            links.append(.init(title: action.label, symbol: action.symbol) { [store] in
                store.selection = .agent(id)
                store.gitActionRequest = id
            })
        }
        if let documents = session.artifactsToReview, documents > 0 {
            links.append(.init(title: documents == 1 ? "Review document" : "Review \(documents) documents", symbol: "doc.text") { [store] in
                store.selection = .agent(id)
            })
        }
        for server in (session.servers ?? []).prefix(2) {
            if let url = URL(string: "http://localhost:\(server.port)") {
                links.append(.init(title: "localhost:\(server.port)", symbol: "globe") { NSWorkspace.shared.open(url) })
            }
        }
        return links
    }

    /// A card's one size of running text.
    private func line(_ text: String, lines: Int) -> some View {
        Text(CompactCard.inline(text))
            .font(Fonts.sans(12.5))
            .foregroundStyle(Palette.textSecondary)
            .lineSpacing(1.5)
            .lineLimit(lines)
            .fixedSize(horizontal: false, vertical: true)
    }
}

extension GitAction {
    fileprivate var isIdle: Bool {
        if case .idle = self { true } else { false }
    }
}

/// What Agentman found checking the agent's finished work.
private struct BoardCheck: View {
    let check: Verification

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline, spacing: 6) {
                icon.frame(width: 13)
                Text(CompactCard.inline(check.summary ?? fallback))
                    .font(Fonts.sans(12.5, .medium))
                    .foregroundStyle(tint)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
            }
            ForEach(check.issues ?? [], id: \.self) { issue in
                Text(CompactCard.inline(issue))
                    .font(Fonts.sans(12.5))
                    .foregroundStyle(Palette.textSecondary)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.leading, 19)
            }
            if check.sentBack == true {
                Text("Sent back for it to fix")
                    .font(Fonts.sans(12))
                    .foregroundStyle(Palette.muted)
                    .padding(.leading, 19)
            }
        }
        .help(check.output ?? check.summary ?? fallback)
        .accessibilityElement(children: .combine)
    }

    private var fallback: String {
        switch check.state {
        case "verified": "Verified"
        case "passed": "Tests pass"
        case "concerns": "Something looks off"
        case "failed": "Tests fail"
        default: "Checking its work"
        }
    }

    @ViewBuilder private var icon: some View {
        switch check.state {
        case "verified", "passed":
            Image(systemName: "checkmark.seal.fill").font(.system(size: 11)).foregroundStyle(Palette.ok)
        case "concerns":
            Image(systemName: "exclamationmark.triangle.fill").font(.system(size: 10)).foregroundStyle(Palette.needsYou)
        case "failed":
            Image(systemName: "xmark.circle.fill").font(.system(size: 11)).foregroundStyle(Palette.error)
        default:
            Loader(size: 10)
        }
    }

    private var tint: Color {
        switch check.state {
        case "concerns": Palette.needsYouText
        case "failed": Palette.errorText
        case "verified", "passed": Palette.text
        default: Palette.muted
        }
    }
}

/// One step of the turn an agent is on.
private struct BoardStep: View {
    let step: BoardDigest.Step

    var body: some View {
        HStack(spacing: 7) {
            icon.frame(width: 12, height: 12)
            Text(step.text)
                .font(Fonts.sans(12.5))
                .foregroundStyle(color)
                .lineLimit(1)
                .truncationMode(.tail)
                .help(step.text)
        }
    }

    @ViewBuilder private var icon: some View {
        switch step.state {
        case .done:
            Image(systemName: "checkmark").font(.system(size: 9, weight: .semibold)).foregroundStyle(Palette.faint)
        case .running:
            Loader(size: 10)
        case .failed:
            Image(systemName: "xmark").font(.system(size: 9, weight: .semibold)).foregroundStyle(Palette.errorText)
        }
    }

    private var color: Color {
        switch step.state {
        case .done: Palette.muted
        case .running: Palette.text
        case .failed: Palette.errorText
        }
    }
}

/// How far through its checklist an agent is.
private struct BoardProgress: View {
    let progress: BoardDigest.Progress

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            GeometryReader { geometry in
                ZStack(alignment: .leading) {
                    Capsule().fill(Palette.fill)
                    Capsule()
                        .fill(Palette.working)
                        .frame(width: geometry.size.width * CGFloat(progress.done) / CGFloat(max(1, progress.total)))
                }
            }
            .frame(height: 4)
            Text(line)
                .font(Fonts.sans(12.5))
                .foregroundStyle(Palette.muted)
                .lineLimit(1)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(line)
    }

    private var line: String {
        let count = "\(progress.done) of \(progress.total)"
        return progress.now.map { "\(count) · \($0)" } ?? count
    }
}

/// Something to do with an idle agent, a click away, as a small chip.
private struct BoardLink: View {
    struct Item: Identifiable {
        let title: String
        let symbol: String
        let action: () -> Void
        var id: String { title }
    }

    let link: Item
    @State private var hovering = false
    private static let shape = RoundedRectangle(cornerRadius: 6, style: .continuous)

    var body: some View {
        Button(action: link.action) {
            HStack(spacing: 4) {
                Image(systemName: link.symbol)
                    .font(.system(size: 10))
                    .foregroundStyle(Palette.muted)
                Text(link.title)
                    .font(Fonts.sans(12))
                    .foregroundStyle(hovering ? Palette.text : Palette.textSecondary)
            }
            .padding(.horizontal, 7)
            .frame(height: 22)
            .background(hovering ? Palette.fill : .clear, in: Self.shape)
            .overlay(Self.shape.strokeBorder(Palette.line))
            .contentShape(Self.shape)
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
    }
}

/// A line back, for an agent waiting with nothing to pick from.
private struct BoardReply: View {
    let placeholder: String
    let send: (String) async -> Bool
    @State private var text = ""
    @State private var busy = false

    private var typed: Bool { !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }

    var body: some View {
        HStack(spacing: 6) {
            TextField(placeholder, text: $text)
                .textFieldStyle(.plain)
                .font(Fonts.sans(12.5))
                .onSubmit(reply)
            Button(action: reply) {
                Image(systemName: "arrow.up")
                    .font(.system(size: 9, weight: .bold))
                    .foregroundStyle(Palette.onInverse)
                    .frame(width: 20, height: 20)
                    .background(Palette.inverse.opacity(typed ? 1 : 0.2), in: RoundedRectangle(cornerRadius: 5, style: .continuous))
            }
            .buttonStyle(.plain)
            .disabled(!typed || busy)
            .accessibilityLabel("Send")
        }
        .padding(.leading, 9)
        .padding(.trailing, 4)
        .frame(height: 28)
        .background(Palette.fill.opacity(0.6), in: RoundedRectangle(cornerRadius: 7, style: .continuous))
        .disabled(busy)
    }

    private func reply() {
        let words = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !words.isEmpty, !busy else { return }
        busy = true
        Task {
            if await send(words) { text = "" }
            busy = false
        }
    }
}
