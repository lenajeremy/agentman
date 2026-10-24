import SwiftUI

/// One agent's conversation, live, with a box to message it and its question
/// to answer when it has one.
struct AgentThreadView: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    @State private var draft = ""
    @State private var attachments: [Attachment] = []
    @State private var dropping = false
    @State private var sending = false
    @State private var showFiles = false
    @State private var confirmEnd = false
    @State private var resuming = false
    @State private var filesTab: WorkspaceTab = .changes
    /// How many files the agent has changed since the last commit.
    @State private var changed = 0

    /// Claude Code in tmux reads the messages it is holding on Ctrl-X
    /// Ctrl-S, so they can be sent now rather than when the step ends.
    private var canSendNow: Bool { session.kind == "claude" && session.inject == "tmux" }

    var body: some View {
        VStack(spacing: 0) {
            if let waiting = session.artifactsToReview, waiting > 0 {
                ReviewBar(count: waiting, agent: AgentName.of(session.kind)) {
                    filesTab = .plans
                    showFiles = true
                }
                .padding(.horizontal, 36)
                .padding(.top, 10)
                .frame(maxWidth: 820)
                .frame(maxWidth: .infinity)
            }
            if changed > 0 {
                ChangedBar(count: changed) {
                    filesTab = .changes
                    showFiles = true
                }
                .padding(.horizontal, 36)
                .padding(.top, 10)
                .frame(maxWidth: 820)
                .frame(maxWidth: .infinity)
            }
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 24) {
                        if store.hasMore[session.id] == true {
                            Button {
                                Task { await store.loadOlder(session.id) }
                            } label: {
                                Text(store.loadingOlder.contains(session.id) ? "Loading…" : "Load earlier messages")
                                    .font(TextStyle.caption)
                            }
                            .buttonStyle(.link)
                            .frame(maxWidth: .infinity)
                        }
                        ForEach(ThreadItem.items(from: store.messages[session.id] ?? [])) { item in
                            switch item {
                            case .message(let message):
                                MessageRow(message: message)
                                    .id(item.id)
                            case .tools(let calls, let said):
                                ToolRun(calls: calls, said: said)
                                    .id(item.id)
                            case .image(let call, let path):
                                ImageThumbnail(sessionId: session.id, path: path,
                                               label: "Read " + (path as NSString).lastPathComponent)
                                    .id(item.id)
                            }
                        }
                        ForEach(Array((session.queued ?? []).enumerated()), id: \.offset) { _, text in
                            QueuedBubble(text: text, note: "Waiting for the agent to read it",
                                         onSendNow: canSendNow ? { await store.sendQueuedNow(session.id) } : nil)
                        }
                        ForEach(Array((store.queued[session.id] ?? []).enumerated()), id: \.offset) { _, text in
                            QueuedBubble(text: text, note: "Queued: it reaches the agent when its turn ends")
                        }
                        // The question is part of the conversation, so it
                        // scrolls with it and is never squeezed to fit.
                        if let question = session.question {
                            QuestionCard(session: session, question: question)
                                .id("question")
                        }
                        // Below the last message, so following the thread
                        // keeps room above the composer.
                        Color.clear.frame(height: 1).id("end")
                    }
                    .padding(.horizontal, 36)
                    .padding(.top, 24)
                    .padding(.bottom, 6)
                    .frame(maxWidth: 820)
                    .frame(maxWidth: .infinity)
                }
                .defaultScrollAnchor(.bottom)
                .modifier(TopFade())
                .overlay {
                    if isEmpty { EmptyThread(session: session) }
                }
                .overlay {
                    if dropping { DropTarget(label: "Drop images for \(session.name)") }
                }
                .onChange(of: store.messages[session.id]?.last?.id) { _, last in
                    guard let last else { return }
                    withAnimation(.easeOut(duration: 0.2)) {
                        proxy.scrollTo("end", anchor: .bottom)
                    }
                }
                .onChange(of: session.question?.id) { _, id in
                    guard id != nil else { return }
                    withAnimation(.easeOut(duration: 0.2)) {
                        proxy.scrollTo("end", anchor: .bottom)
                    }
                }
            }

            VStack(spacing: 10) {
                if let error = store.actionError[session.id] {
                    Label(error, systemImage: "exclamationmark.triangle")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.errorText)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                if Self.resumable(session) {
                    ResumeBar(session: session, resuming: resuming) {
                        resuming = true
                        Task {
                            store.actionError[session.id] = await store.resume(session.id)
                            resuming = false
                        }
                    }
                } else {
                Composer(
                    text: $draft,
                    attachments: session.canReceive ? $attachments : nil,
                    session: session,
                    placeholder: placeholder,
                    enabled: session.canReceive && !sending && session.question == nil,
                    onDropTargeted: { dropping = $0 },
                    onSend: send
                )
                }
            }
            .padding(.horizontal, 36)
            .padding(.top, 4)
            .padding(.bottom, 22)
            .frame(maxWidth: 820)
            .frame(maxWidth: .infinity)
        }
        .confirmationDialog("End this session?", isPresented: $confirmEnd) {
            Button("End session") {
                Task { store.actionError[session.id] = await store.end(session.id) }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Its terminal closes. The conversation is kept, and you can resume it from its folder.")
        }
        .inspector(isPresented: $showFiles) {
            WorkspacePanel(session: session, tab: $filesTab)
                .inspectorColumnWidth(min: 320, ideal: 480, max: 1100)
        }
        // An unsent draft waits for the user's return, as on the phone.
        .onAppear { draft = store.drafts[session.id] ?? "" }
        .onChange(of: draft) { _, now in store.saveDraft(now, for: session.id) }
        .onAppear {
            // For screenshots and tests: the files panel open on a tab.
            if let tab = ProcessInfo.processInfo.environment["AGENTMAN_FILES"].flatMap(WorkspaceTab.init(rawValue:)) {
                filesTab = tab
                showFiles = true
            }
        }
        // The count is read when the thread opens and after each turn.
        .task(id: session.state == .busy) {
            guard session.state != .busy else { return }
            if let result = try? await store.workspace(.listChanges, session: session.id) {
                changed = result.changes?.count ?? 0
            }
        }
        // Images dropped anywhere on the conversation wait in the box.
        .onDrop(of: [.fileURL, .image], isTargeted: Binding(get: { dropping }, set: { dropping = $0 && session.canReceive })) { providers in
            guard session.canReceive else { return false }
            Task { @MainActor in
                let found = await Attachments.from(providers: providers)
                attachments.append(contentsOf: found.prefix(max(0, Attachments.maxCount - attachments.count)))
            }
            return true
        }
        // The window keeps the name; the toolbar shows ThreadTitle instead,
        // which renames the session when clicked.
        .navigationTitle(session.name)
        .toolbar(removing: .title)
        .toolbar {
            if #available(macOS 26, *) {
                // Plain text on the title bar, as the system title was, with
                // the state at the far end.
                ToolbarItem(placement: .navigation) {
                    ThreadTitle(session: session, subtitle: subtitle)
                }
                .sharedBackgroundVisibility(.hidden)
                ToolbarSpacer(.flexible)
            } else {
                ToolbarItem(placement: .navigation) {
                    ThreadTitle(session: session, subtitle: subtitle)
                }
            }
            ToolbarItemGroup(placement: .primaryAction) {
                if let servers = session.servers, !servers.isEmpty {
                    ServersMenu(session: session, servers: servers)
                }
                Button {
                    showFiles.toggle()
                } label: {
                    Label("Files", systemImage: "sidebar.right")
                }
                .help(showFiles ? "Hide files" : "Files, changes and history")
                .keyboardShortcut("e", modifiers: [.command, .shift])
                if Self.endable(session) {
                    Menu {
                        Button("End session…") { confirmEnd = true }
                    } label: {
                        Label("More", systemImage: "ellipsis")
                    }
                    .menuIndicator(.hidden)
                    .help("More")
                }
                StateChip(state: session.state)
                if session.state == .busy, session.canReceive {
                    Button {
                        store.interrupt(session.id)
                    } label: {
                        Label("Stop", systemImage: "stop.fill")
                    }
                    .help("Stop the agent's current turn")
                }
            }
        }
    }

    private static let resumableKinds: Set<String> = ["claude", "codex", "cursor-cli", "kiro", "antigravity"]

    /// An ended session its agent can reopen, as the phone decides it.
    static func resumable(_ session: Session) -> Bool {
        session.inject == "none" && session.state == .ended && resumableKinds.contains(session.kind)
    }

    /// A session running in a terminal agentman can close and reopen.
    static func endable(_ session: Session) -> Bool {
        session.inject == "tmux" && resumableKinds.contains(session.kind)
    }

    /// Where it works and on which model, under its name in the title bar.
    private var subtitle: String {
        let folder = session.cwd.replacingOccurrences(of: NSHomeDirectory(), with: "~")
        guard let model = session.model, !model.isEmpty else { return folder }
        return folder + " · " + model
    }

    private var isEmpty: Bool {
        (store.messages[session.id] ?? []).isEmpty && (session.queued ?? []).isEmpty &&
            (store.queued[session.id] ?? []).isEmpty && session.question == nil
    }

    private var placeholder: String {
        if !session.canReceive { return "Start this agent with am \(session.kind) to message it from here" }
        if session.question != nil { return "Answer the question above first" }
        if session.inject == "hook" {
            return "Message \(session.name): it reads it when its turn ends (start it with am \(session.kind) to type in directly)"
        }
        return "Message \(session.name)"
    }

    private func send() {
        let text = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty || !attachments.isEmpty else { return }
        sending = true
        let images = Attachments.encoded(attachments)
        Task {
            if await store.send(text, images: images, to: session.id) {
                draft = ""
                attachments = []
            }
            sending = false
        }
    }
}

/// The thread's name and where it works, in the title bar. Clicking the
/// name renames the session in place: Return keeps it, Escape or clicking
/// away leaves it as it was.
private struct ThreadTitle: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    let subtitle: String
    @State private var editing = false
    @State private var name = ""
    @State private var hovering = false
    @FocusState private var focused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            if editing {
                TextField("Name", text: $name)
                    .textFieldStyle(.plain)
                    .font(TextStyle.title)
                    .padding(.horizontal, 5)
                    .padding(.vertical, 1)
                    .frame(width: 320)
                    .background(Palette.surface, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: 6, style: .continuous).strokeBorder(Palette.working.opacity(0.6)))
                    .focused($focused)
                    .onSubmit(save)
                    .onExitCommand { editing = false }
                    .onChange(of: focused) { _, now in
                        if !now { editing = false }
                    }
            } else {
                Button(action: begin) {
                    Text(session.name)
                        .font(TextStyle.title)
                        .foregroundStyle(Palette.text)
                        .lineLimit(1)
                        .padding(.horizontal, 5)
                        .padding(.vertical, 1)
                        .background(hovering ? Palette.fill : .clear, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .onHover { hovering = $0 }
                .help(session.originalName.map { "Click to rename · renamed from “\($0)”" } ?? "Click to rename")
                .accessibilityLabel("Rename \(session.name)")
            }
            Text(subtitle)
                .font(TextStyle.caption)
                .foregroundStyle(Palette.muted)
                .lineLimit(1)
                .truncationMode(.middle)
                .padding(.horizontal, 5)
        }
        .frame(maxWidth: 440, alignment: .leading)
        .contextMenu {
            Button("Rename…", action: begin)
            if let own = session.originalName {
                Button("Use the agent's own name, “\(own)”") {
                    Task { await store.rename(session.id, to: "") }
                }
            }
        }
        .onChange(of: session.id) { _, _ in editing = false }
    }

    private func begin() {
        name = session.name
        editing = true
        focused = true
    }

    private func save() {
        let new = name.trimmingCharacters(in: .whitespacesAndNewlines)
        editing = false
        guard !new.isEmpty, new != session.name else { return }
        Task { await store.rename(session.id, to: new) }
    }
}

/// In place of the message box for a session that has ended.
private struct ResumeBar: View {
    let session: Session
    let resuming: Bool
    let resume: () -> Void

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: "moon.zzz")
                .foregroundStyle(Palette.muted)
            VStack(alignment: .leading, spacing: 2) {
                Text("This session has ended")
                    .font(TextStyle.captionMedium)
                    .foregroundStyle(Palette.text)
                Text("Its conversation is here to read. Resume it to carry on.")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
            }
            Spacer()
            Button(action: resume) {
                if resuming { ProgressView().controlSize(.small) } else { Text("Resume") }
            }
            .buttonStyle(.borderedProminent)
            .disabled(resuming)
        }
        .padding(14)
        .background(Palette.surface, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 16, style: .continuous).strokeBorder(Palette.line))
    }
}

/// The dev servers an agent runs: open one here, share a link to it, or
/// stop it.
private struct ServersMenu: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    let servers: [Server]
    @State private var stopping: Server?

    var body: some View {
        Menu {
            ForEach(servers) { server in
                Section(Self.heading(server)) {
                    Button("Open in browser") {
                        if let url = URL(string: "http://localhost:\(server.port)") { NSWorkspace.shared.open(url) }
                    }
                    if let link = server.link {
                        Button("Copy share link") { copy(link) }
                        Button("Stop sharing") { store.unshareServer(session.id, port: server.port) }
                    } else {
                        Button("Share a link…") { share(server) }
                    }
                    Button("Stop server…", role: .destructive) { stopping = server }
                }
            }
        } label: {
            Label {
                Text(verbatim: servers.count == 1 ? ":" + String(servers[0].port) : "\(servers.count) servers")
            } icon: {
                Image(systemName: "globe")
            }
            .labelStyle(.titleAndIcon)
        }
        .help("Dev servers")
        .confirmationDialog(
            stopping.map { "Stop the server on port " + String($0.port) + "?" } ?? "",
            isPresented: Binding(get: { stopping != nil }, set: { if !$0 { stopping = nil } })
        ) {
            Button("Stop server", role: .destructive) {
                if let server = stopping {
                    Task { store.actionError[session.id] = await store.stopServer(session.id, port: server.port) }
                }
                stopping = nil
            }
            Button("Cancel", role: .cancel) { stopping = nil }
        } message: {
            Text("Its process ends. Agentman can't start it again.")
        }
    }

    /// A server as its menu section names it: its page's title, or its
    /// program, and its port, written as a port rather than a number.
    static func heading(_ server: Server) -> String {
        let port = ":" + String(server.port)
        if let title = server.title, !title.isEmpty { return title + " · " + port }
        if let command = server.command, !command.isEmpty { return port + " · " + command }
        return port
    }

    private func share(_ server: Server) {
        Task {
            do {
                let link = try await store.shareServer(session.id, port: server.port)
                copy(link)
                store.actionError[session.id] = nil
            } catch {
                store.actionError[session.id] = error.localizedDescription
            }
        }
    }

    private func copy(_ link: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(link, forType: .string)
    }
}

/// A plan waiting for the user's review, over the thread.
private struct ReviewBar: View {
    let count: Int
    let agent: String
    let open: () -> Void

    var body: some View {
        Button(action: open) {
            HStack(spacing: 8) {
                Image(systemName: "list.bullet.clipboard")
                    .font(.system(size: 11, weight: .medium))
                    .foregroundStyle(Palette.needsYouText)
                Text(count == 1 ? "\(agent) wants you to review a plan" : "\(agent) wants you to review \(count) documents")
                    .font(TextStyle.captionMedium)
                    .foregroundStyle(Palette.text)
                Spacer()
                Text("Review")
                    .font(TextStyle.captionMedium)
                    .foregroundStyle(Palette.needsYouText)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            .background(Palette.needsYouWash, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Palette.needsYouEdge))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

/// "4 files changed", over a thread, opening the agent's changes.
private struct ChangedBar: View {
    let count: Int
    let open: () -> Void

    var body: some View {
        Button(action: open) {
            HStack(spacing: 8) {
                Image(systemName: "folder")
                    .font(.system(size: 11, weight: .medium))
                    .foregroundStyle(Palette.working)
                Text("\(count) file\(count == 1 ? "" : "s") changed")
                    .font(TextStyle.captionMedium)
                    .foregroundStyle(Palette.text)
                Spacer()
                Image(systemName: "chevron.right")
                    .font(.system(size: 9, weight: .semibold))
                    .foregroundStyle(Palette.faint)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            .background(Palette.surface, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Palette.line))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help("See what changed")
    }
}

/// What a step ran and what came of it: its command, then its output, in a
/// box that scrolls when long.
private struct StepDetail: View {
    let command: String?
    let output: String?
    var failed = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let command, !command.isEmpty {
                Text(command)
                    .font(Fonts.mono(11.5))
                    .foregroundStyle(Palette.textSecondary)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let output = output?.trimmingCharacters(in: .whitespacesAndNewlines), !output.isEmpty, output != "[image]" {
                ScrollView {
                    Text(output)
                        .font(Fonts.mono(11))
                        .foregroundStyle(failed ? Palette.errorText : Palette.muted)
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(10)
                }
                .frame(maxHeight: 220)
                .fixedSize(horizontal: false, vertical: true)
                .background(Palette.fill, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            }
        }
    }
}

/// An agent with nothing to show yet.
private struct EmptyThread: View {
    let session: Session

    var body: some View {
        VStack(spacing: 10) {
            AgentMark(kind: session.kind, size: 28)
                .opacity(0.9)
            Text("No messages yet")
                .font(TextStyle.bodyMedium)
                .foregroundStyle(Palette.textSecondary)
            Text(session.canReceive ? "What you send appears here, then the agent's replies." : "Its conversation appears here as it works.")
                .font(TextStyle.caption)
                .foregroundStyle(Palette.muted)
        }
        .allowsHitTesting(false)
    }
}

struct StateChip: View {
    let state: SessionState

    var body: some View {
        Text(name)
            .font(TextStyle.label)
            .foregroundStyle(foreground)
            .padding(.horizontal, 9)
            .padding(.vertical, 4)
            .background(background, in: Capsule())
    }

    private var name: String {
        switch state {
        case .waitingInput: "Needs you"
        case .busy: "Working"
        case .idle: "Idle"
        case .ended, .unknown: "Ended"
        }
    }

    private var foreground: Color {
        switch state {
        case .waitingInput: Palette.needsYouText
        case .busy: Palette.workingText
        default: Palette.muted
        }
    }

    private var background: Color {
        switch state {
        case .waitingInput: Palette.needsYouWash
        case .busy: Palette.workingWash
        default: Palette.fill
        }
    }
}

/// One entry in a conversation.
struct MessageRow: View {
    let message: Message

    var body: some View {
        switch message.role {
        case .user:
            if let text = message.text, MessageRow.isCompactionSummary(text) {
                FoldedText(label: "Summary of the earlier conversation", text: text)
            } else {
                let sent = SentMessage(message.text ?? "")
                VStack(alignment: .trailing, spacing: 8) {
                    if !sent.images.isEmpty {
                        SentFiles(paths: sent.images)
                    }
                    if !sent.text.isEmpty {
                        HStack {
                            Spacer(minLength: 80)
                            LongText(text: sent.text)
                                .padding(.horizontal, 16)
                                .padding(.vertical, 12)
                                .background(Palette.fill, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
                                .contextMenu {
                                    Button("Copy") { Clipboard.copy(sent.text) }
                                }
                        }
                    }
                }
                .frame(maxWidth: .infinity, alignment: .trailing)
            }
        case .assistant:
            AgentReply(text: message.text ?? "")
                .opacity(message.isSidechain == true ? 0.6 : 1)
        case .tool:
            ToolRun(calls: [message])
        case .system:
            Text(message.text ?? "")
                .font(TextStyle.caption)
                .foregroundStyle(Palette.faint)
                .frame(maxWidth: .infinity)
        }
    }
}

extension MessageRow {
    /// The summary Claude Code writes when it compacts a conversation, which
    /// arrives as a message from the user.
    static func isCompactionSummary(_ text: String) -> Bool {
        text.hasPrefix("This session is being continued from a previous conversation")
    }
}

/// A user's message, clipped to a few lines when it is long, with the way
/// to read the rest.
private struct LongText: View {
    let text: String
    @State private var open = false
    private var long: Bool { text.count > 700 || text.filter(\.isNewline).count > 10 }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(text)
                .font(TextStyle.body)
                .foregroundStyle(Palette.text)
                .lineSpacing(5)
                .textSelection(.enabled)
                .lineLimit(long && !open ? 8 : nil)
            if long {
                Button(open ? "Show less" : "Show more") {
                    withAnimation(.easeOut(duration: 0.15)) { open.toggle() }
                }
                .buttonStyle(.link)
                .font(TextStyle.caption)
            }
        }
    }
}

/// Text that rarely needs reading, folded into one quiet line.
private struct FoldedText: View {
    let label: String
    let text: String
    @State private var open = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Button {
                withAnimation(.easeOut(duration: 0.15)) { open.toggle() }
            } label: {
                HStack(spacing: 7) {
                    Image(systemName: "text.alignleft")
                        .font(.system(size: 10, weight: .medium))
                    Text(label)
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
                Text(text)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.textSecondary)
                    .textSelection(.enabled)
                    .padding(12)
                    .background(Palette.fill, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            }
        }
    }
}

/// What a thread shows: messages, with runs of tool calls folded into one
/// line each.
enum ThreadItem: Identifiable {
    case message(Message)
    /// A run of tool calls, with what the agent said just before it.
    case tools([Message], said: String)
    /// An image the agent read, shown as a thumbnail rather than its path.
    case image(Message, path: String)

    /// The last message's ID, so scrolling to the newest message finds it.
    var id: String {
        switch self {
        case .message(let message), .image(let message, _): message.id
        case .tools(let calls, _): calls.last?.id ?? ""
        }
    }

    static func items(from messages: [Message]) -> [ThreadItem] {
        var items: [ThreadItem] = []
        var run: [Message] = []
        var said = ""
        func flush() {
            if !run.isEmpty {
                items.append(.tools(run, said: said))
                run = []
            }
        }
        for message in messages {
            if message.role == .tool, ToolKind(message.tool?.name) == .read,
               let path = ImageThumbnail.imagePath(in: message.tool?.summary ?? message.text ?? "") {
                flush()
                items.append(.image(message, path: path))
            } else if message.role == .tool {
                run.append(message)
            } else {
                flush()
                if message.role == .assistant { said = message.text ?? "" }
                items.append(.message(message))
            }
        }
        flush()
        return items
    }
}

/// A run of tool calls as one quiet line: what was done, in counts, or what
/// is running now. Opened, one tidy row a call.
struct ToolRun: View {
    @Environment(AgentsStore.self) private var store
    let calls: [Message]
    var said = ""
    @State private var open = false

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            if calls.count == 1, let call = calls.first {
                ToolCallRow(call: call)
            } else {
                Button {
                    withAnimation(.easeOut(duration: 0.15)) { open.toggle() }
                } label: {
                    HStack(spacing: 7) {
                        statusIcon
                        Text(summary)
                            .lineLimit(1)
                        if failures > 0 {
                            Text("· \(failures) failed")
                                .foregroundStyle(Palette.errorText)
                        }
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
                    VStack(alignment: .leading, spacing: 9) {
                        ForEach(calls) { call in
                            ToolCallRow(call: call)
                        }
                    }
                    .padding(.leading, 18)
                    .padding(.top, 4)
                }
            }
        }
        .opacity(calls.allSatisfy { $0.isSidechain == true } ? 0.6 : 1)
        // Once the run has finished, Agentman says what it did and why.
        .task(id: finished ? AgentsStore.toolRunKey(calls) + (store.status == .connected ? "" : "~") : "") {
            guard finished, calls.count > 1 else { return }
            await store.titleTools(calls, said: said)
        }
    }

    private var finished: Bool { !calls.contains { $0.tool?.status == "running" } }

    private var failures: Int { calls.filter { $0.tool?.status == "error" }.count }

    @ViewBuilder private var statusIcon: some View {
        if calls.last?.tool?.status == "running" {
            ProgressView().controlSize(.mini).frame(width: 11, height: 11)
        } else {
            Image(systemName: "wrench.and.screwdriver")
                .font(.system(size: 10, weight: .medium))
        }
    }

    /// What the run did: Agentman's line for it when there is one, its
    /// steps' own words until then, or what is running now.
    private var summary: String {
        if let running = calls.last, running.tool?.status == "running" {
            if let title = running.tool?.title, !title.isEmpty { return title + "…" }
            let kind = ToolKind(running.tool?.name)
            return "\(kind.verb) \(ToolKind.tidy(running.tool?.summary ?? running.text ?? ""))…"
        }
        if let title = store.toolTitles[AgentsStore.toolRunKey(calls)], !title.isEmpty {
            return title
        }
        var counts: [ToolKind: Int] = [:]
        var order: [ToolKind] = []
        for call in calls {
            let kind = ToolKind(call.tool?.name)
            if counts[kind] == nil { order.append(kind) }
            counts[kind, default: 0] += 1
        }
        let phrases = order.map { $0.phrase(count: counts[$0] ?? 1) }
        guard var sentence = phrases.first else { return "Used tools" }
        let rest = phrases.dropFirst().map { $0.prefix(1).lowercased() + $0.dropFirst() }
        if rest.count == 1 {
            sentence += " and " + rest[0]
        } else if rest.count > 1 {
            sentence += ", " + rest.dropLast().joined(separator: ", ") + " and " + rest.last!
        }
        return sentence
    }
}

/// One tool call in a line: what it did in plain words, with the raw
/// command or path a click away.
private struct ToolCallRow: View {
    let call: Message
    @State private var showRaw = false

    var body: some View {
        if let title = call.tool?.title, !title.isEmpty {
            VStack(alignment: .leading, spacing: 5) {
                Button {
                    withAnimation(.easeOut(duration: 0.12)) { showRaw.toggle() }
                } label: {
                    HStack(alignment: .firstTextBaseline, spacing: 7) {
                        statusIcon
                        Text(title)
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.textSecondary)
                            .lineLimit(2)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(showRaw ? "Hide the command" : "Show the command")
                if showRaw {
                    StepDetail(command: call.tool?.summary, output: call.text, failed: call.tool?.status == "error")
                        .padding(.leading, 19)
                }
            }
            .opacity(call.isSidechain == true ? 0.6 : 1)
        } else {
            rawRow
        }
    }

    @ViewBuilder private var statusIcon: some View {
        let kind = ToolKind(call.tool?.name)
        Group {
            switch call.tool?.status {
            case "running":
                ProgressView().controlSize(.mini).frame(width: 11, height: 11)
            case "error":
                Image(systemName: "xmark.circle.fill").foregroundStyle(Palette.error)
            default:
                Image(systemName: kind.icon).foregroundStyle(Palette.faint)
            }
        }
        .font(.system(size: 10, weight: .medium))
        .frame(width: 12)
    }

    /// A call from a daemon too old to describe it: its verb and target.
    private var rawRow: some View {
        let kind = ToolKind(call.tool?.name)
        return HStack(alignment: .firstTextBaseline, spacing: 7) {
            Group {
                switch call.tool?.status {
                case "running":
                    ProgressView().controlSize(.mini).frame(width: 11, height: 11)
                case "error":
                    Image(systemName: "xmark.circle.fill").foregroundStyle(Palette.error)
                default:
                    Image(systemName: kind.icon).foregroundStyle(Palette.faint)
                }
            }
            .font(.system(size: 10, weight: .medium))
            .frame(width: 12)
            Text(kind.verb)
                .font(TextStyle.captionMedium)
                .foregroundStyle(Palette.textSecondary)
            let target = ToolKind.tidy(call.tool?.summary ?? call.text ?? "")
            if !target.isEmpty {
                Text(target)
                    .font(Fonts.mono(11.5))
                    .foregroundStyle(Palette.muted)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .textSelection(.enabled)
                    .help(call.tool?.summary ?? "")
            }
        }
        .opacity(call.isSidechain == true ? 0.6 : 1)
    }
}

/// What kind of thing a tool call does, by the agent's name for the tool.
enum ToolKind: Hashable {
    case command, read, edit, write, search, web, task, plan, other(String)

    init(_ name: String?) {
        let name = (name ?? "").lowercased()
        switch name {
        case "bash", "shell", "exec_command", "run_shell_command", "local_shell", "terminal": self = .command
        case "read", "read_file", "view", "cat", "notebookread": self = .read
        case "edit", "multiedit", "apply_patch", "str_replace", "str_replace_editor", "notebookedit", "replace": self = .edit
        case "write", "create_file", "write_file": self = .write
        case "grep", "glob", "search", "find", "list", "ls", "codebase_search", "file_search": self = .search
        case "webfetch", "fetch", "websearch", "web_search", "web_fetch": self = .web
        case "task", "agent", "subagent": self = .task
        case "todowrite", "update_plan", "todo_write": self = .plan
        default:
            if name.hasPrefix("mcp__") {
                self = .other(String(name.split(separator: "_").last ?? "tool"))
            } else {
                self = .other(name.isEmpty ? "tool" : name)
            }
        }
    }

    var verb: String {
        switch self {
        case .command: "Ran"
        case .read: "Read"
        case .edit: "Edited"
        case .write: "Wrote"
        case .search: "Searched"
        case .web: "Fetched"
        case .task: "Delegated"
        case .plan: "Planned"
        case .other(let name): "Used " + name
        }
    }

    var icon: String {
        switch self {
        case .command: "terminal"
        case .read: "doc.text"
        case .edit, .write: "pencil"
        case .search: "magnifyingglass"
        case .web: "globe"
        case .task: "person.2"
        case .plan: "checklist"
        case .other: "wrench"
        }
    }

    func phrase(count: Int) -> String {
        let one = count == 1
        switch self {
        case .command: return one ? "Ran a command" : "Ran \(count) commands"
        case .read: return one ? "Read a file" : "Read \(count) files"
        case .edit: return one ? "Edited a file" : "Edited \(count) files"
        case .write: return one ? "Wrote a file" : "Wrote \(count) files"
        case .search: return one ? "Searched once" : "Searched \(count) times"
        case .web: return one ? "Fetched a page" : "Fetched \(count) pages"
        case .task: return one ? "Delegated a task" : "Delegated \(count) tasks"
        case .plan: return "Updated the plan"
        case .other: return one ? "Used a tool" : "Used \(count) tools"
        }
    }

    /// A target made readable: home as ~, an agent's own temporary folder
    /// left out (scratchpad/…, tasks/…), and whitespace on one line.
    static func tidy(_ text: String) -> String {
        var text = text.replacingOccurrences(of: NSHomeDirectory(), with: "~")
        if let session = try? Regex(#"/(?:private/)?tmp/claude-\d+/[^/\s]+/[0-9a-f-]{36}/"#) {
            text = text.replacing(session, with: "")
        }
        return text.split(whereSeparator: \.isNewline).joined(separator: " ").trimmingCharacters(in: .whitespaces)
    }
}

/// A message the agent has not read yet: held until its current step ends,
/// or queued until its turn does.
private struct QueuedBubble: View {
    let text: String
    let note: String
    /// Hurries the agent's queue, where the agent can be: Claude Code in
    /// tmux, which reads what it holds on Ctrl-X Ctrl-S.
    var onSendNow: (() async -> Bool)?
    @State private var sending = false

    var body: some View {
        HStack {
            Spacer(minLength: 80)
            VStack(alignment: .trailing, spacing: 4) {
                Text(text)
                    .font(TextStyle.body)
                    .foregroundStyle(Palette.textSecondary)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 10)
                    .background(
                        RoundedRectangle(cornerRadius: 16, style: .continuous)
                            .strokeBorder(Palette.line, style: StrokeStyle(lineWidth: 1, dash: [4, 3]))
                    )
                HStack(spacing: 8) {
                    Text(note)
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.faint)
                    if let onSendNow {
                        Button(sending ? "Sending…" : "Send now") {
                            sending = true
                            Task {
                                _ = await onSendNow()
                                sending = false
                            }
                        }
                        .buttonStyle(.link)
                        .font(TextStyle.caption)
                        .disabled(sending)
                        .help("Sends every waiting message now instead of when this step ends")
                    }
                }
            }
        }
    }
}

/// The box at the foot of a chat. Return sends; Shift-Return starts a new
/// line; it grows to ten lines. With attachments, images can be attached
/// with the button, pasted or dropped, and wait above the words until sent.
struct Composer: View {
    @Environment(AgentsStore.self) private var store
    @Binding var text: String
    /// Nil for an agent that can't be messaged, so nothing offers images.
    var attachments: Binding<[Attachment]>?
    /// The agent being written to, for its mode and model.
    var session: Session? = nil
    var placeholder: String
    var enabled: Bool
    var onDropTargeted: ((Bool) -> Void)? = nil
    var onSend: () -> Void
    @State private var focused = false
    @State private var confirming: String?

    private var canSend: Bool {
        enabled && (!text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !(attachments?.wrappedValue.isEmpty ?? true))
    }

    /// What the agent can be switched to, when it offers anything. A model
    /// only with a scope: the agent has to say what a switch touches.
    private var modes: [String] { session?.modes ?? [] }
    private var models: [String] { session?.modelScope == nil ? [] : (session?.models ?? []) }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            if let attachments, !attachments.wrappedValue.isEmpty {
                AttachmentStrip(attachments: attachments)
                    .padding(.leading, 4)
            }
            MessageField(
                text: $text,
                placeholder: placeholder,
                enabled: enabled,
                focused: $focused,
                onSubmit: { if canSend { onSend() } },
                onImages: attachments == nil ? nil : { add($0) },
                onDropTargeted: onDropTargeted
            )
            .opacity(enabled ? 1 : 0.6)
            .padding(.horizontal, 6)
            .padding(.top, 4)
            // The things to do with a message, under it: attach on the left,
            // what the agent runs as, then send on the right.
            HStack(spacing: 6) {
                if let attachments {
                    Button(action: attach) {
                        Image(systemName: "plus")
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundStyle(Palette.muted)
                            .frame(width: 28, height: 28)
                            .background(Palette.fill, in: Circle())
                    }
                    .buttonStyle(.plain)
                    .disabled(!enabled || attachments.wrappedValue.count >= Attachments.maxCount)
                    .help("Attach images (or paste or drop them)")
                }
                if !modes.isEmpty, let session {
                    switchMenu(values: modes, current: session.mode, label: SessionLabels.mode, help: "Switch the agent's mode") { value in
                        Task { await apply(model: false, value) }
                    }
                }
                Spacer(minLength: 8)
                if !models.isEmpty, let session {
                    switchMenu(values: models, current: session.model, label: SessionLabels.model, help: "Switch the agent's model") { value in
                        if session.modelScope == "default" {
                            confirming = value
                        } else {
                            Task { await apply(model: true, value) }
                        }
                    }
                }
                sendButton
            }
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 8)
        .confirmationDialog(
            confirming.map { "Switch to \(SessionLabels.model($0))?" } ?? "",
            isPresented: Binding(get: { confirming != nil }, set: { if !$0 { confirming = nil } })
        ) {
            Button("Switch") {
                if let value = confirming { Task { await apply(model: true, value) } }
                confirming = nil
            }
            Button("Cancel", role: .cancel) { confirming = nil }
        } message: {
            if let value = confirming, let session {
                Text("\(SessionLabels.model(value)) also becomes the default model for new \(AgentName.of(session.kind)) sessions on your Mac.")
            }
        }
        .background(Palette.surface, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 20, style: .continuous)
                .strokeBorder(focused ? Palette.fillStrong : Palette.line, lineWidth: 1)
        )
        .shadow(color: .black.opacity(0.04), radius: 8, y: 2)
    }

    private func switchMenu(values: [String], current: String?, label: @escaping (String) -> String,
                            help: String, choose: @escaping (String) -> Void) -> some View {
        Menu {
            ForEach(values, id: \.self) { value in
                Button {
                    if value != current { choose(value) }
                } label: {
                    if value == current {
                        Label(label(value), systemImage: "checkmark")
                    } else {
                        Text(label(value))
                    }
                }
            }
        } label: {
            HStack(spacing: 4) {
                Text(current.map(label) ?? "Choose")
                Image(systemName: "chevron.down")
                    .font(.system(size: 8, weight: .bold))
            }
            .font(TextStyle.caption)
            .foregroundStyle(Palette.textSecondary)
            .padding(.horizontal, 10)
            .frame(height: 28)
            .background(Palette.fill, in: Capsule())
        }
        .menuStyle(.button)
        .buttonStyle(.plain)
        .menuIndicator(.hidden)
        .fixedSize()
        .disabled(!enabled)
        .help(help)
    }

    private func apply(model: Bool, _ value: String) async {
        guard let session else { return }
        let problem = await store.switchSession(session.id, model: model, to: value)
        store.actionError[session.id] = problem
    }

    private func add(_ found: [Attachment]) {
        guard let attachments else { return }
        let room = Attachments.maxCount - attachments.wrappedValue.count
        withAnimation(.easeOut(duration: 0.12)) {
            attachments.wrappedValue.append(contentsOf: found.prefix(max(0, room)))
        }
    }

    private func attach() {
        let panel = NSOpenPanel()
        panel.allowedContentTypes = [.image]
        panel.allowsMultipleSelection = true
        panel.message = "Choose images to send"
        if panel.runModal() == .OK {
            add(panel.urls.compactMap(Attachments.make(file:)))
        }
    }

    private var sendButton: some View {
            Button(action: onSend) {
                Image(systemName: "arrow.up")
                    .font(.system(size: 13, weight: .bold))
                    .foregroundStyle(Palette.onInverse)
                    .frame(width: 28, height: 28)
                    .background(Palette.inverse, in: Circle())
            }
            .buttonStyle(.plain)
            .disabled(!canSend)
            .opacity(enabled ? 1 : 0.4)
            .keyboardShortcut(.return, modifiers: .command)
            .help("Send")
    }
}

/// An agent's reply, with Copy under it when the pointer is over it, as
/// ChatGPT and Claude offer, and in its context menu.
private struct AgentReply: View {
    let text: String
    @State private var hovering = false
    @State private var copied = false

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            MarkdownView(text)
            Button {
                Clipboard.copy(text)
                copied = true
                Task {
                    try? await Task.sleep(for: .seconds(1.5))
                    copied = false
                }
            } label: {
                Label(copied ? "Copied" : "Copy", systemImage: copied ? "checkmark" : "doc.on.doc")
                    .font(TextStyle.caption)
                    .foregroundStyle(copied ? Palette.ok : Palette.muted)
            }
            .buttonStyle(.plain)
            .opacity(hovering || copied ? 1 : 0)
            .help("Copy the reply")
        }
        .contentShape(Rectangle())
        .onHover { hovering = $0 }
        .contextMenu {
            Button("Copy") { Clipboard.copy(text) }
        }
    }
}

enum Clipboard {
    @MainActor static func copy(_ text: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
    }
}
