import SwiftUI

/// One agent's conversation, live, with a box to message it and its question
/// to answer when it has one.
struct AgentThreadView: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    @State private var draft = ""
    @State private var sending = false

    /// Claude Code in tmux reads the messages it is holding on Ctrl-X
    /// Ctrl-S, so they can be sent now rather than when the step ends.
    private var canSendNow: Bool { session.kind == "claude" && session.inject == "tmux" }

    var body: some View {
        VStack(spacing: 0) {
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
                            case .tools(let calls):
                                ToolRun(calls: calls)
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
                Composer(
                    text: $draft,
                    placeholder: placeholder,
                    enabled: session.canReceive && !sending && session.question == nil,
                    onSend: send
                )
            }
            .padding(.horizontal, 36)
            .padding(.top, 4)
            .padding(.bottom, 22)
            .frame(maxWidth: 820)
            .frame(maxWidth: .infinity)
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
        guard !text.isEmpty else { return }
        sending = true
        Task {
            if await store.send(text, to: session.id) {
                draft = ""
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
                HStack {
                    Spacer(minLength: 80)
                    LongText(text: message.text ?? "")
                        .padding(.horizontal, 16)
                        .padding(.vertical, 12)
                        .background(Palette.fill, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
                }
            }
        case .assistant:
            MarkdownView(message.text ?? "")
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
    case tools([Message])
    /// An image the agent read, shown as a thumbnail rather than its path.
    case image(Message, path: String)

    /// The last message's ID, so scrolling to the newest message finds it.
    var id: String {
        switch self {
        case .message(let message), .image(let message, _): message.id
        case .tools(let calls): calls.last?.id ?? ""
        }
    }

    static func items(from messages: [Message]) -> [ThreadItem] {
        var items: [ThreadItem] = []
        var run: [Message] = []
        func flush() {
            if !run.isEmpty {
                items.append(.tools(run))
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
    let calls: [Message]
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
    }

    private var failures: Int { calls.filter { $0.tool?.status == "error" }.count }

    @ViewBuilder private var statusIcon: some View {
        if calls.last?.tool?.status == "running" {
            ProgressView().controlSize(.mini).frame(width: 11, height: 11)
        } else {
            Image(systemName: "wrench.and.screwdriver")
                .font(.system(size: 10, weight: .medium))
        }
    }

    /// "Ran 3 commands, read 2 files and edited a file", or what is running.
    private var summary: String {
        if let running = calls.last, running.tool?.status == "running" {
            let kind = ToolKind(running.tool?.name)
            return "\(kind.verb) \(ToolKind.tidy(running.tool?.summary ?? running.text ?? ""))…"
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

/// One tool call in a line: what it did, then its target, cut in the middle
/// when long.
private struct ToolCallRow: View {
    let call: Message

    var body: some View {
        let kind = ToolKind(call.tool?.name)
        HStack(alignment: .firstTextBaseline, spacing: 7) {
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
/// line; it grows to ten lines.
struct Composer: View {
    @Binding var text: String
    var placeholder: String
    var enabled: Bool
    var onSend: () -> Void

    var body: some View {
        HStack(alignment: .bottom, spacing: 10) {
            TextField(placeholder, text: $text, axis: .vertical)
                .textFieldStyle(.plain)
                .font(TextStyle.body)
                .lineSpacing(3)
                .lineLimit(1...10)
                .onKeyPress(.return, phases: .down) { press in
                    // Shift-Return is a new line where the cursor is;
                    // Return alone sends, through onSubmit.
                    guard press.modifiers.contains(.shift) else { return .ignored }
                    NSApp.sendAction(#selector(NSResponder.insertNewlineIgnoringFieldEditor(_:)), to: nil, from: nil)
                    return .handled
                }
                .onSubmit(onSend)
                .disabled(!enabled)
                .padding(.vertical, 6)
            Button(action: onSend) {
                Image(systemName: "arrow.up")
                    .font(.system(size: 13, weight: .bold))
                    .foregroundStyle(Palette.onInverse)
                    .frame(width: 28, height: 28)
                    .background(Palette.inverse, in: Circle())
            }
            .buttonStyle(.plain)
            .disabled(!enabled || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            .opacity(enabled ? 1 : 0.4)
            .keyboardShortcut(.return, modifiers: .command)
            .help("Send")
        }
        .padding(.leading, 16)
        .padding(.trailing, 8)
        .padding(.vertical, 8)
        .background(Palette.surface, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 20, style: .continuous)
                .strokeBorder(Palette.line, lineWidth: 1)
        )
        .shadow(color: .black.opacity(0.04), radius: 8, y: 2)
    }
}
