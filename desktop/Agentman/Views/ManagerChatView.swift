import SwiftUI

/// The conversation with Agentman: the one place to ask what every agent is
/// doing, read what happened, and be told when something needs you.
struct ManagerChatView: View {
    @Environment(AgentsStore.self) private var store
    @Environment(\.fileMentions) private var mentions
    @Environment(\.detailHeaderLeading) private var headerLeading
    /// In an editor's agent panel: narrow, with no toolbar of its own, as
    /// the panel has its own header.
    var inPanel = false
    @State private var confirmNewChat = false
    @State private var chatAutofocus = ChatAutofocus()
    @State private var draft = ""
    @State private var attachments: [Attachment] = []
    @State private var sending = false
    @State private var dropping = false
    /// Whether the conversation is scrolled to its end, so new messages
    /// follow on; scrolled up, nothing moves what the user is reading.
    @State private var following = Following()
    /// Bumped when the user sends, which always brings them to the end.
    @State private var followRequest = 0

    private var manager: ManagerStore { store.manager }

    private var entry: Bool { Self.showsEntry(manager) }

    /// The entry page: Agentman and one box, with nothing else competing.
    /// Only while there is no conversation; once there is one, it reads as
    /// a conversation with its box below, never with the page under it.
    static func showsEntry(_ manager: ManagerStore) -> Bool {
        manager.entries.isEmpty && !manager.thinking
    }

    var body: some View {
        Group {
            if inPanel {
                conversation
            } else {
                VStack(spacing: 0) {
                    HStack(spacing: 10) {
                        // The sidebar beside it already says Agentman; the page
                        // names itself only while the sidebar is hidden.
                        if headerLeading > 0 {
                            Text("Agentman").font(Fonts.heading(14))
                        }
                        Spacer()
                        if !manager.schedules.isEmpty { SchedulesButton() }
                        if !manager.entries.isEmpty {
                            ChromeButton(symbol: "plus.bubble", help: "Start a new conversation with Agentman") {
                                confirmNewChat = true
                            }
                        }
                    }
                    .padding(.horizontal, 16)
                    .padding(.leading, headerLeading)
                    .frame(height: 32)
                    .windowTopBar()
                    conversation
                }
                    .navigationTitle("")
                    .confirmationDialog("Start a new chat?", isPresented: $confirmNewChat) {
                        Button("New Chat") {
                            Task { manager.problem = await manager.reset() }
                        }
                        Button("Cancel", role: .cancel) {}
                    } message: {
                        Text(Self.newChatMessage)
                    }
            }
        }
        .environment(\.chatAutofocus, chatAutofocus)
    }

    static let newChatMessage = "This conversation is set aside, and Agentman keeps what matters from it in its private notes. Cards still waiting stay."

    /// The space at either side of the conversation: less in a panel.
    private var gutter: CGFloat { inPanel ? 14 : 36 }

    /// The conversation and the box under it, in the window or a panel.
    private var conversation: some View {
        GeometryReader { geometry in
            VStack(spacing: 0) {
                ScrollViewReader { proxy in
                    ScrollView(showsIndicators: false) {
                        let items = ChatItem.items(from: manager.entries)
                        LazyVStack(alignment: .leading, spacing: 28) {
                            if manager.hasOlder, !manager.entries.isEmpty {
                                LoadingEarlier()
                            }
                            ForEach(items) { item in
                                ChatItemView(item: item)
                                    .id(item.id)
                                    .firstMessage(item.id, if: item.id == items.first?.id)
                            }
                            if manager.thinking, manager.entries.last?.streaming != true {
                                ThinkingRow()
                                    .id("thinking")
                            }
                            if entry {
                                EntryHero(draft: $draft, attachments: $attachments, dropping: $dropping, sending: sending) {
                                    send(draft)
                                }
                                // The whole visible height, the title bar's
                                // too, so it sits in the middle of the page.
                                .frame(minHeight: geometry.size.height)
                                .id("entry")
                            } else {
                                // The end of the conversation, below the last
                                // message, so following it keeps room above
                                // the composer.
                                Color.clear.frame(height: 1).id("end")
                            }
                        }
                        .padding(.horizontal, gutter)
                        .padding(.top, entry ? 0 : 28)
                        .padding(.bottom, entry ? 0 : 6)
                        .frame(maxWidth: AgentThreadView.column)
                        .frame(maxWidth: .infinity)
                    }
                    .modifier(FollowsEnd(following: following, earlier: manager.hasOlder ? { await manager.loadOlder() } : nil))
                    .scrollIndicators(.never)
                    .modifier(TopFade())
                    .overlay(alignment: .bottom) {
                        JumpToLatestOverlay(following: following, hidden: entry) {
                            withAnimation(.easeOut(duration: 0.25)) { proxy.scrollTo("end", anchor: .bottom) }
                        }
                    }
                    .onChange(of: scrollKey) { old, _ in
                        guard !entry, following.atEnd else { return }
                        if old.hasPrefix("0-") {
                            // The first page: its end is only laid out once
                            // this update is done, and scrolling to it before
                            // lands at the top.
                            DispatchQueue.main.async { proxy.scrollTo("end", anchor: .bottom) }
                            return
                        }
                        withAnimation(.easeOut(duration: 0.18)) {
                            proxy.scrollTo("end", anchor: .bottom)
                        }
                    }
                    .onChange(of: followRequest) { _, _ in
                        following.atEnd = true
                        withAnimation(.easeOut(duration: 0.18)) {
                            proxy.scrollTo("end", anchor: .bottom)
                        }
                    }
                }

                if !entry {
                    VStack(spacing: 8) {
                        if store.status != .connected || manager.needsKey {
                            // Agentman can't take a message yet: the box
                            // says why, as on the entry page.
                            ManagerBox(draft: $draft, attachments: $attachments, dropping: $dropping, sending: sending) {
                                send(draft)
                            }
                        } else {
                            ManagerComposer(text: $draft, attachments: $attachments, thinking: manager.thinking, sending: sending,
                                            onDropTargeted: { dropping = $0 }) {
                                send(draft)
                            } onStop: {
                                manager.stop()
                            }
                            .modifier(ComposerFeedback(message: manager.problem, radius: 22) {
                                manager.problem = nil
                            })
                        }
                    }
                    .padding(.horizontal, gutter)
                    .padding(.top, 4)
                    .padding(.bottom, 22)
                    .frame(maxWidth: AgentThreadView.column)
                    .frame(maxWidth: .infinity)
                }
            }
        }
        // Images dropped anywhere on the conversation wait in the box.
        .onDrop(of: [.fileURL, .image], isTargeted: $dropping) { providers in
            Task { @MainActor in
                let found = await Attachments.from(providers: providers)
                let room = Attachments.maxCount - attachments.count
                attachments.append(contentsOf: found.prefix(max(0, room)))
            }
            return true
        }
        .overlay {
            if dropping { DropTarget(label: "Drop images for Agentman") }
        }
        .task {
            // For screenshots and tests: images and words waiting in the box.
            let environment = ProcessInfo.processInfo.environment
            if let paths = environment["AGENTMAN_ATTACH"], attachments.isEmpty {
                attachments = paths.split(separator: ":").compactMap { Attachments.make(file: URL(fileURLWithPath: String($0))) }
            }
            if let text = environment["AGENTMAN_DRAFT"], draft.isEmpty {
                draft = text.replacingOccurrences(of: "\\n", with: "\n")
            }
        }
    }

    /// Changes whenever the conversation grows, so it can follow along.
    private var scrollKey: String {
        "\(manager.entries.count)-\(manager.entries.last?.text.count ?? 0)-\(manager.thinking)"
    }

    private func send(_ text: String) {
        var message = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !message.isEmpty || !attachments.isEmpty, !sending else { return }
        // Files named with @ in the editor, by their full paths, for
        // Agentman to pass on.
        if let mentions { message = mentions.resolve(message, folder: nil) }
        sending = true
        followRequest += 1
        let images = attachments
        Task {
            if await manager.send(message, images: images) {
                draft = ""
                attachments = []
            }
            sending = false
        }
    }
}

/// Agentman's name and the one box to talk to it, while there is no
/// conversation yet.
private struct EntryHero: View {
    @Environment(AgentsStore.self) private var store
    @Binding var draft: String
    @Binding var attachments: [Attachment]
    @Binding var dropping: Bool
    var sending: Bool
    var send: () -> Void

    var body: some View {
        VStack(spacing: 0) {
            Spacer(minLength: 48)
            // The website's Agentman mascot on its dots, over the name in
            // Rubik; the badge in the Classic theme.
            VStack(spacing: Look.site ? 4 : 16) {
                if Look.site {
                    StickerOnDots(name: .mascot, size: 150)
                } else {
                    AgentmanBadge(size: 46)
                }
                Text("Agentman")
                    .font(TextStyle.wordmark)
                    .foregroundStyle(Palette.text)
            }
            .padding(.bottom, Look.site ? 24 : 28)

            ManagerBox(draft: $draft, attachments: $attachments, dropping: $dropping, sending: sending, large: true, send: send)
                .frame(maxWidth: 620)

            // A few questions to start with, once there is someone to ask.
            if store.status == .connected, !store.manager.needsKey {
                FlowLayout(spacing: 8) {
                    ForEach(Self.starters, id: \.self) { question in
                        StarterChip(text: question) {
                            draft = question
                            send()
                        }
                    }
                }
                .frame(maxWidth: 620)
                .padding(.top, 6)
            }

            Spacer(minLength: 48)
            Spacer(minLength: 24)
        }
        .frame(maxWidth: .infinity)
    }
}

extension EntryHero {
    static let starters = ["What needs me?", "What happened while I was away?", "What is everyone working on?"]
}

/// A question to start the conversation with, asked when clicked.
private struct StarterChip: View {
    let text: String
    let ask: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: ask) {
            Text(text)
                .font(TextStyle.caption)
                .foregroundStyle(hovering ? Palette.text : Palette.textSecondary)
                .padding(.horizontal, 12)
                .frame(height: 28)
                // The site's chips: cream, edged, on a hard shadow.
                .look {
                    $0.edged(14, fill: hovering ? Palette.cream.mix(with: Palette.edge, by: 0.06) : Palette.cream, depth: 2)
                        .padding(.bottom, 2)
                } classic: {
                    $0.background(hovering ? Palette.fill : Palette.surface, in: Capsule())
                        .overlay(Capsule().strokeBorder(Palette.line))
                }
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
    }
}

/// The box to talk to Agentman: a message, its API key before it has one,
/// or a note that am serve isn't running.
struct ManagerBox: View {
    @Environment(AgentsStore.self) private var store
    @Binding var draft: String
    @Binding var attachments: [Attachment]
    @Binding var dropping: Bool
    var sending: Bool
    var large = false
    /// The room kept under it for a word about the connection or the key,
    /// which the Board only gives when there is one.
    var keepsFooter = true
    var send: () -> Void

    var body: some View {
        let manager = store.manager
        VStack(spacing: 0) {
            Group {
                if store.status != .connected {
                    ManagerComposer(text: .constant(""), thinking: false, sending: true, large: large,
                                    placeholder: "Waiting for Agentman to connect…", enabled: false, onSend: {}, onStop: {})
                } else if manager.needsKey {
                    KeyBox()
                } else {
                    ManagerComposer(text: $draft, attachments: $attachments, thinking: false, sending: sending, large: large,
                                    onDropTargeted: { dropping = $0 }, onSend: send, onStop: {})
                }
            }
            .modifier(ComposerFeedback(message: manager.problem, radius: large ? 26 : 22) {
                manager.problem = nil
            })

            if keepsFooter || store.status != .connected || manager.needsKey {
            Group {
                if case .unavailable = store.status {
                    Text("Not connected.")
                } else if store.status == .connecting {
                    Text("Connecting…")
                } else if manager.needsKey {
                    HStack(spacing: 4) {
                        // The card above says what the key is for.
                        Text("Don't have one?")
                        if manager.provider == "gemini" {
                            Link("Get a Gemini key", destination: URL(string: "https://aistudio.google.com/apikey")!)
                        } else {
                            Link("Get an Anthropic key", destination: URL(string: "https://console.anthropic.com/settings/keys")!)
                        }
                    }
                }
            }
            .font(TextStyle.caption)
            .foregroundStyle(Palette.muted)
            .padding(.top, 14)
            .frame(height: 34, alignment: .top)
            }
        }
    }
}

/// What the conversation shows: entries, with runs of Agentman's activity
/// folded into one line, and images it looked at shown on their own.
enum ChatItem: Identifiable {
    case entry(ManagerEntry)
    case activities([ManagerEntry])
    case image(ManagerEntry)

    var id: String {
        switch self {
        case .entry(let entry), .image(let entry): entry.id
        case .activities(let entries): "acts-" + (entries.first?.id ?? "")
        }
    }

    static func items(from entries: [ManagerEntry]) -> [ChatItem] {
        var items: [ChatItem] = []
        var run: [ManagerEntry] = []
        func flush() {
            if !run.isEmpty {
                items.append(.activities(run))
                run = []
            }
        }
        for entry in entries {
            // A card answered in the agent itself, or put away, is gone, as
            // is anything the user closed.
            if entry.card?.isGone == true || entry.closed == true { continue }
            if entry.role == "activity" {
                if entry.path != nil {
                    flush()
                    items.append(.image(entry))
                } else {
                    run.append(entry)
                }
            } else {
                flush()
                items.append(.entry(entry))
            }
        }
        flush()
        return items
    }
}

private struct ChatItemView: View {
    @Environment(AgentsStore.self) private var store
    let item: ChatItem

    var body: some View {
        switch item {
        case .entry(let entry):
            switch entry.role {
            case "user":
                VStack(alignment: .trailing, spacing: 8) {
                    if let images = entry.images, !images.isEmpty {
                        SentImages(names: images)
                    }
                    if !entry.text.isEmpty {
                        // Files are named to Agentman by their full paths.
                        TaggedText(text: entry.text, folder: nil)
                            .padding(.horizontal, 16)
                            .padding(.vertical, 12)
                            .background(Palette.bubble, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
                    }
                }
                .padding(.leading, 80)
                .frame(maxWidth: .infinity, alignment: .trailing)
            case "assistant":
                AssistantReply(entry: entry)
            case "brief", "agent":
                BriefCard(entry: entry)
                    .swipeToDismiss { await store.manager.close(entry) == nil }
            case "scheduled":
                ScheduledLine(entry: entry)
            case "card":
                if let card = entry.card {
                    if card.kind == "note" {
                        if card.isOpen {
                            NoteSuggestionCard(card: card)
                        } else {
                            NoteReceipt(card: card)
                                .modifier(ClosesOnHover(entry: entry))
                        }
                    } else if card.isOpen {
                        CompactCard(card: card) { await store.manager.dismiss(card) }
                            .edged(14, depth: 3)
                            .swipeToDismiss { await store.manager.dismiss(card) == nil }
                    } else {
                        CardReceipt(card: card)
                            .modifier(ClosesOnHover(entry: entry))
                    }
                }
            case "error":
                Label(entry.text, systemImage: "exclamationmark.circle")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.errorText)
            case "compacted":
                CompactedDivider(text: entry.text)
            default:
                Text(entry.text)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.faint)
            }
        case .activities(let entries):
            ActivityRun(entries: entries)
        case .image(let entry):
            ImageActivity(entry: entry)
        }
    }
}

private struct AssistantReply: View {
    let entry: ManagerEntry

    var body: some View {
        HStack(alignment: .top, spacing: 14) {
            AgentmanBadge(size: 22)
                .padding(.top, 1)
            VStack(alignment: .leading, spacing: 6) {
                if entry.text.isEmpty {
                    ThinkingDots()
                } else {
                    MarkdownView(entry.text)
                }
            }
        }
    }
}

/// Agentman's tool calls, folded into one quiet line until opened, the way
/// Claude shows what it examined.
private struct ActivityRun: View {
    let entries: [ManagerEntry]
    @State private var open = false

    var body: some View {
        // In the same soft card as an agent's tool calls: one line for one
        // thing Agentman did, a line that opens for several.
        VStack(alignment: .leading, spacing: 6) {
            if entries.count == 1, let entry = entries.first {
                row(entry)
            } else {
                Button {
                    withAnimation(.easeOut(duration: 0.15)) { open.toggle() }
                } label: {
                    HStack(spacing: 7) {
                        Image(systemName: Self.icon(entries.last?.tool))
                            .font(.system(size: 10, weight: .semibold))
                            .frame(width: 12)
                        Text(entries.last?.text ?? "")
                            .lineLimit(1)
                        // Apart from the line, so a long one never cuts it off.
                        Text("and \(entries.count - 1) more")
                            .foregroundStyle(Palette.faint)
                            .fixedSize()
                        Image(systemName: "chevron.right")
                            .font(.system(size: 9, weight: .semibold))
                            .rotationEffect(.degrees(open ? 90 : 0))
                    }
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                if open {
                    Rectangle()
                        .fill(Palette.toolCardEdge)
                        .frame(height: 1)
                        .padding(.vertical, 3)
                    VStack(alignment: .leading, spacing: 8) {
                        ForEach(entries) { entry in
                            row(entry)
                        }
                    }
                    .padding(.leading, 18)
                    .padding(.bottom, 2)
                }
            }
        }
        .toolCard()
        .padding(.leading, 34)
    }

    private func row(_ entry: ManagerEntry) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 7) {
            Image(systemName: Self.icon(entry.tool))
                .font(.system(size: 9.5, weight: .semibold))
                .foregroundStyle(Palette.faint)
                .frame(width: 12)
            Text(entry.text)
                .foregroundStyle(Palette.textSecondary)
                .lineLimit(2)
                .fixedSize(horizontal: false, vertical: true)
        }
        .font(TextStyle.caption)
    }

    /// An icon for what Agentman did.
    static func icon(_ tool: String?) -> String {
        switch tool {
        case "send_to_agent": "paperplane"
        case "answer_question": "checkmark.bubble"
        case "approve": "checkmark.shield"
        case "interrupt": "stop.circle"
        case "dismiss_card": "xmark.circle"
        case "give_key": "key"
        case "search_knowledge", "read_knowledge", "save_knowledge": "book"
        case "update_instructions": "doc.text"
        case "view_image": "photo"
        default: "eye"
        }
    }
}

/// An image Agentman looked at, shown to the user as well: a short label
/// and a thumbnail that opens full window.
private struct ImageActivity: View {
    let entry: ManagerEntry

    var body: some View {
        if let sessionId = entry.sessionId, let path = entry.path {
            ImageThumbnail(sessionId: sessionId, path: path, label: entry.text, carded: true)
                .padding(.leading, 34)
        }
    }
}

/// News from one agent, as Agentman put it, with the way to that agent.
private struct BriefCard: View {
    @Environment(AgentsStore.self) private var store
    let entry: ManagerEntry

    var body: some View {
        let session = entry.sessionId.flatMap { store.session($0) }
        let brief = entry.sessionId.flatMap { store.manager.briefs[$0] }
        HStack(alignment: .top, spacing: 12) {
            AgentMark(kind: session?.kind ?? "", size: 20)
                .padding(.top, 2)
            VStack(alignment: .leading, spacing: 4) {
                Text(session?.name ?? brief?.name ?? "An agent")
                    .font(TextStyle.bodyMedium)
                    .foregroundStyle(Palette.text)
                Text(entry.text)
                    .font(TextStyle.body)
                    .foregroundStyle(Palette.textSecondary)
                    .lineSpacing(5)
                    .textSelection(.enabled)
                if let needs = brief?.needsYou, !needs.isEmpty {
                    Label(needs, systemImage: "hand.raised")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.needsYouText)
                }
            }
            Spacer(minLength: 8)
            if let session {
                Button("Open agent") { store.selection = .agent(session.id) }
                    .buttonStyle(.borderless)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
            }
            CloseButton(help: "Close (or swipe it aside)") {
                Task { _ = await store.manager.close(entry) }
            }
        }
        .padding(.horizontal, 18)
        .padding(.vertical, 16)
        .edged(16, depth: 3)
    }
}

/// The small × that takes a card out of the conversation.
private struct CloseButton: View {
    var help: String
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            Image(systemName: "xmark")
                .font(.system(size: 10, weight: .semibold))
                .foregroundStyle(Palette.muted)
                .frame(width: 20, height: 20)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(help)
        .accessibilityLabel("Close")
    }
}

/// A one-line receipt that offers its × only under the pointer, so a run
/// of them stays quiet.
private struct ClosesOnHover: ViewModifier {
    @Environment(AgentsStore.self) private var store
    let entry: ManagerEntry
    @State private var hovering = false

    func body(content: Content) -> some View {
        HStack(spacing: 6) {
            content
            CloseButton(help: "Close") {
                Task { _ = await store.manager.close(entry) }
            }
            .opacity(hovering ? 1 : 0)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .contentShape(Rectangle())
        .onHover { hovering = $0 }
    }
}

/// A card the user answered here, folded to what they chose.
private struct CardReceipt: View {
    @Environment(AgentsStore.self) private var store
    let card: ManagerCard

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "checkmark")
                .font(.system(size: 10, weight: .bold))
                .foregroundStyle(Palette.ok)
            Text("\(store.session(card.sessionId)?.name ?? card.agentName) · \(card.outcome ?? "Answered")")
                .lineLimit(1)
            if store.session(card.sessionId) != nil {
                Button("Open") { store.selection = .agent(card.sessionId) }
                    .buttonStyle(.link)
            }
        }
        .font(TextStyle.caption)
        .foregroundStyle(Palette.muted)
        .padding(.leading, 34)
    }
}

private struct ThinkingRow: View {
    var body: some View {
        HStack(spacing: 12) {
            AgentmanBadge(size: 22)
            ThinkingDots()
        }
        .padding(.bottom, 8)
    }
}

/// Three dots that breathe while Agentman works.
private struct ThinkingDots: View {
    var body: some View {
        TimelineView(.animation(minimumInterval: 1 / 30)) { context in
            let phase = context.date.timeIntervalSinceReferenceDate
            HStack(spacing: 4) {
                ForEach(0..<3) { index in
                    Circle()
                        .fill(Palette.faint)
                        .frame(width: 6, height: 6)
                        .opacity(0.35 + 0.65 * max(0, sin(phase * 4 - Double(index) * 0.7)))
                }
            }
            .frame(height: 18)
        }
        .accessibilityLabel("Agentman is thinking")
    }
}

/// The composer, a soft pill whose button stops Agentman while it is
/// replying. Larger on the entry page. Images can be attached with the
/// button, pasted or dropped, and wait above the words until sent. Return
/// sends; Shift-Return starts a new line; the box grows to ten lines.
private struct ManagerComposer: View {
    @Environment(AgentsStore.self) private var store
    @Environment(\.fileMentions) private var mentions
    @Binding var text: String
    var attachments: Binding<[Attachment]> = .constant([])
    var thinking: Bool
    var sending: Bool
    var large = false
    var placeholder = "Ask Agentman about your agents"
    var enabled = true
    /// Images dragged over the box, so the conversation shows where they
    /// will land.
    var onDropTargeted: ((Bool) -> Void)? = nil
    var onSend: () -> Void
    var onStop: () -> Void
    @State private var focused = false

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            if !attachments.wrappedValue.isEmpty {
                AttachmentStrip(attachments: attachments)
            }
            HStack(alignment: .bottom, spacing: 10) {
                Button(action: attach) {
                    Image(systemName: "plus")
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundStyle(Palette.muted)
                        .frame(width: large ? 32 : 28, height: large ? 32 : 28)
                        .background(Palette.fill, in: Circle())
                }
                .buttonStyle(.plain)
                .disabled(attachments.wrappedValue.count >= Attachments.maxCount)
                .help("Attach images (or paste or drop them)")

                MessageField(
                    text: $text,
                    placeholder: placeholder,
                    fontSize: large ? 15 : 14,
                    enabled: enabled,
                    focused: $focused,
                    autofocus: true,
                    onSubmit: { if !thinking, canSend { onSend() } },
                    onImages: { add($0) },
                    onDropTargeted: onDropTargeted,
                    mentions: mentions
                )
                .padding(.vertical, large ? 9 : 6)

                VoiceButton(size: large ? 32 : 28, onText: { spoken in
                    text = text.adding(spoken: spoken)
                    focused = true
                }, onProblem: { problem in
                    store.manager.problem = problem
                })

                Button {
                    thinking ? onStop() : onSend()
                } label: {
                    Image(systemName: thinking ? "stop.fill" : "arrow.up")
                        .font(.system(size: thinking ? 11 : 13, weight: .bold))
                        .foregroundStyle(Look.site ? (canSend || thinking ? Color.white : Palette.muted) : Palette.onInverse)
                        .frame(width: large ? 32 : 28, height: large ? 32 : 28)
                        .background(Look.site ? (thinking ? Palette.inverse : canSend ? Palette.flood : Palette.fillStrong) : Palette.inverse.opacity(canSend || thinking ? 1 : 0.25), in: Circle())
                }
                .buttonStyle(.plain)
                .disabled(!thinking && !canSend)
                .help(thinking ? "Stop" : "Send")
            }
        }
        .padding(.leading, large ? 12 : 10)
        .padding(.trailing, 8)
        .padding(.vertical, large ? 9 : 7)
        // A click on the card around the text, not on a control, goes to
        // the text.
        .background {
            RoundedRectangle(cornerRadius: large ? 26 : 22, style: .continuous).hardShadowFill(Palette.surface, shadow: Palette.edge, depth: 3)
                .onTapGesture { focused = true }
        }
        .overlay(
            RoundedRectangle(cornerRadius: large ? 26 : 22, style: .continuous)
                .strokeBorder(Look.site ? Palette.edge : (focused ? Palette.fillStrong : Palette.line), lineWidth: Palette.edgeWidth)
        )
        .classicShadow(color: .black.opacity(large ? 0.06 : 0.04), radius: large ? 18 : 8, y: large ? 6 : 2)
        // The files for `@`, just above the box.
        .overlay(alignment: .top) {
            if let mentions, mentions.isOpen {
                // Hung from the box's top edge, rising from it.
                MentionList(mentions: mentions)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.bottom, 6)
                    .frame(height: 0, alignment: .bottom)
            }
        }
    }

    private var canSend: Bool {
        !sending && (!text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !attachments.wrappedValue.isEmpty)
    }

    private func add(_ found: [Attachment]) {
        let room = Attachments.maxCount - attachments.wrappedValue.count
        withAnimation(.easeOut(duration: 0.12)) {
            attachments.wrappedValue.append(contentsOf: found.prefix(max(0, room)))
        }
    }

    private func attach() {
        let panel = NSOpenPanel()
        panel.allowedContentTypes = [.image]
        panel.allowsMultipleSelection = true
        panel.message = "Choose images for Agentman"
        if panel.runModal() == .OK {
            add(panel.urls.compactMap(Attachments.make(file:)))
        }
    }
}

/// Where the API key goes on the entry page, in place of the message box
/// until there is one.
private struct KeyBox: View {
    @Environment(AgentsStore.self) private var store
    @State private var key = ""
    @State private var saving = false
    @State private var problem: String?

    private var gemini: Bool { store.manager.provider == "gemini" }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            // What Agentman is and why it needs a key, before the key.
            VStack(alignment: .leading, spacing: 5) {
                Text("Agentman needs an API key")
                    .font(TextStyle.bodyMedium)
                    .foregroundStyle(Palette.text)
                Text("It reads what your agents are doing, answers their routine requests, and tells you what needs you. "
                     + "It runs on your own \(gemini ? "Gemini" : "Anthropic") key, kept in your Mac's Keychain and only sent to \(gemini ? "Google" : "Anthropic").")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .padding(.horizontal, 6)
            HStack(spacing: 10) {
                Image(systemName: "key")
                    .foregroundStyle(Palette.muted)
                SecureField(gemini ? "Paste your Gemini API key" : "Paste your Anthropic API key", text: $key)
                    .textFieldStyle(.plain)
                    .font(Fonts.sans(15))
                    .onSubmit(save)
                    .padding(.vertical, 9)
                Button(action: save) {
                    Image(systemName: "arrow.right")
                        .font(.system(size: 13, weight: .bold))
                        .foregroundStyle(Palette.onInverse)
                        .frame(width: 32, height: 32)
                        .background(Palette.inverse.opacity(key.isEmpty ? 0.25 : 1), in: Circle())
                }
                .buttonStyle(.plain)
                .disabled(key.trimmingCharacters(in: .whitespaces).isEmpty || saving)
            }
            .padding(.leading, 20)
            .padding(.trailing, 8)
            .padding(.vertical, 9)
            .edged(26, depth: 3)
            .classicShadow(color: .black.opacity(0.06), radius: 18, y: 6)
            if let problem {
                Text(problem)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.errorText)
            }
        }
    }

    private func save() {
        let value = key.trimmingCharacters(in: .whitespaces)
        guard !value.isEmpty else { return }
        saving = true
        Task {
            problem = await store.manager.setKey(value, provider: store.manager.provider)
            if problem == nil { key = "" }
            saving = false
        }
    }
}

/// One of Agentman's own reminders or watches waking it: a quiet line above
/// the turn it takes.
private struct ScheduledLine: View {
    let entry: ManagerEntry

    var body: some View {
        HStack(spacing: 7) {
            Image(systemName: entry.tool == "watch" ? "eye" : "bell")
                .font(.system(size: 10, weight: .medium))
                .frame(width: 12)
            Text(entry.text)
                .lineLimit(2)
            Text(Date(timeIntervalSince1970: TimeInterval(entry.ts) / 1000), format: .dateTime.hour().minute())
                .foregroundStyle(Palette.faint)
        }
        .font(TextStyle.caption)
        .foregroundStyle(Palette.muted)
        .padding(.leading, 34)
    }
}

/// The clock in the title bar while Agentman has something scheduled, with
/// the list to see and cancel it.
struct SchedulesButton: View {
    @Environment(AgentsStore.self) private var store
    @State private var open = false

    var body: some View {
        Button {
            open.toggle()
        } label: {
            Label("\(store.manager.schedules.count) scheduled", systemImage: "clock")
                .labelStyle(.titleAndIcon)
                .font(TextStyle.caption)
        }
        .help("What Agentman has scheduled for itself")
        .popover(isPresented: $open, arrowEdge: .bottom) {
            SchedulesList()
                .environment(store)
        }
    }
}

/// Agentman's reminders and watches: what each is for, when it comes due,
/// and a button to cancel it.
private struct SchedulesList: View {
    @Environment(AgentsStore.self) private var store
    @State private var problem: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Scheduled")
                .font(TextStyle.title)
                .foregroundStyle(Palette.text)
            if store.manager.schedules.isEmpty {
                Text("Nothing scheduled. Ask Agentman to remind itself, or to watch an agent until it's done.")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
                    .fixedSize(horizontal: false, vertical: true)
            }
            ForEach(store.manager.schedules) { item in
                HStack(alignment: .top, spacing: 10) {
                    Image(systemName: item.kind == "watch" ? "eye" : (item.every ?? 0) > 0 ? "repeat" : "bell")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundStyle(Palette.working)
                        .frame(width: 16)
                        .padding(.top, 2)
                    VStack(alignment: .leading, spacing: 3) {
                        Text(title(item))
                            .font(TextStyle.body)
                            .foregroundStyle(Palette.text)
                            .lineSpacing(3)
                            .fixedSize(horizontal: false, vertical: true)
                        Text(when(item))
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.muted)
                    }
                    Spacer(minLength: 8)
                    Button {
                        Task { problem = await store.manager.cancel(item) }
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .font(.system(size: 14))
                            .foregroundStyle(Palette.faint)
                    }
                    .buttonStyle(.plain)
                    .help("Cancel")
                }
            }
            if let problem {
                Text(problem)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.errorText)
            }
        }
        .padding(18)
        .frame(width: 360)
    }

    private func title(_ item: ManagerSchedule) -> String {
        guard item.kind == "watch" else { return item.text }
        let name = item.agentName ?? "An agent"
        let until: String = switch item.condition {
        case "needs_user": "needs you"
        case "idle": "is idle"
        case "ended": "ends"
        default: "finishes its turn"
        }
        return "When \(name) \(until)" + (item.text.isEmpty ? "" : ": \(item.text)")
    }

    private func when(_ item: ManagerSchedule) -> String {
        let time = item.dueDate.formatted(date: .omitted, time: .shortened)
        if item.kind == "watch" { return "Gives up at \(time)" }
        var text = "Next at \(time)"
        if let every = item.every, every > 0 {
            let minutes = every / 60
            text += minutes % 60 == 0 && minutes >= 60 ? " · every \(minutes / 60) h" : " · every \(minutes) min"
            if let times = item.times, times > 0 {
                text += " · \(times - (item.fired ?? 0)) more"
            } else if let until = item.until, until > 0 {
                text += " · until " + Date(timeIntervalSince1970: TimeInterval(until) / 1000).formatted(date: .omitted, time: .shortened)
            }
        }
        return text
    }
}

/// Where the conversation was compacted: before it, the model now has a
/// summary; the conversation itself is shown as it was.
private struct CompactedDivider: View {
    let text: String

    var body: some View {
        HStack(spacing: 10) {
            Rectangle().fill(Palette.line).frame(height: 1)
            Text(text)
                .font(TextStyle.caption)
                .foregroundStyle(Palette.faint)
                .fixedSize()
            Rectangle().fill(Palette.line).frame(height: 1)
        }
        .help("Agentman summarised the conversation before this point, to keep it quick and inexpensive.")
    }
}
