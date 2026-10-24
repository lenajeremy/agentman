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
            ThreadHeader(session: session)
            Divider().overlay(Palette.line)
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 16) {
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
                        ForEach(store.messages[session.id] ?? []) { message in
                            MessageRow(message: message)
                                .id(message.id)
                        }
                        ForEach(Array((session.queued ?? []).enumerated()), id: \.offset) { _, text in
                            QueuedBubble(text: text, note: "Waiting for the agent to read it",
                                         onSendNow: canSendNow ? { await store.sendQueuedNow(session.id) } : nil)
                        }
                        ForEach(Array((store.queued[session.id] ?? []).enumerated()), id: \.offset) { _, text in
                            QueuedBubble(text: text, note: "Queued: it reaches the agent when its turn ends")
                        }
                    }
                    .padding(.horizontal, 32)
                    .padding(.vertical, 24)
                    .frame(maxWidth: 820)
                    .frame(maxWidth: .infinity)
                }
                .defaultScrollAnchor(.bottom)
                .onChange(of: store.messages[session.id]?.last?.id) { _, last in
                    guard let last else { return }
                    withAnimation(.easeOut(duration: 0.2)) {
                        proxy.scrollTo(last, anchor: .bottom)
                    }
                }
            }

            VStack(spacing: 10) {
                if let question = session.question {
                    QuestionCard(session: session, question: question)
                }
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
            .padding(.horizontal, 32)
            .padding(.bottom, 18)
            .frame(maxWidth: 820)
            .frame(maxWidth: .infinity)
        }
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

private struct ThreadHeader: View {
    @Environment(AgentsStore.self) private var store
    let session: Session

    var body: some View {
        HStack(spacing: 12) {
            AgentMark(kind: session.kind, size: 26)
            VStack(alignment: .leading, spacing: 2) {
                Text(session.name)
                    .font(TextStyle.title)
                    .foregroundStyle(Palette.text)
                HStack(spacing: 6) {
                    Text(session.cwd.replacingOccurrences(of: NSHomeDirectory(), with: "~"))
                    if let model = session.model {
                        Text("·")
                        Text(model)
                    }
                }
                .font(TextStyle.caption)
                .foregroundStyle(Palette.muted)
                .lineLimit(1)
            }
            Spacer()
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
        .padding(.horizontal, 24)
        .padding(.vertical, 14)
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
            HStack {
                Spacer(minLength: 80)
                Text(message.text ?? "")
                    .font(TextStyle.body)
                    .foregroundStyle(Palette.text)
                    .textSelection(.enabled)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 10)
                    .background(Palette.fill, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
            }
        case .assistant:
            MarkdownView(message.text ?? "")
                .opacity(message.isSidechain == true ? 0.6 : 1)
        case .tool:
            ToolRow(tool: message.tool, fallback: message.text)
                .opacity(message.isSidechain == true ? 0.6 : 1)
        case .system:
            Text(message.text ?? "")
                .font(TextStyle.caption)
                .foregroundStyle(Palette.faint)
                .frame(maxWidth: .infinity)
        }
    }
}

private struct ToolRow: View {
    let tool: ToolCall?
    let fallback: String?

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Group {
                switch tool?.status {
                case "running":
                    ProgressView().controlSize(.mini)
                case "error":
                    Image(systemName: "xmark.circle.fill").foregroundStyle(Palette.error)
                default:
                    Image(systemName: "checkmark.circle").foregroundStyle(Palette.faint)
                }
            }
            .font(.system(size: 11))
            .frame(width: 14)
            Text(tool?.name ?? "Tool")
                .font(TextStyle.label)
                .foregroundStyle(Palette.textSecondary)
            if let summary = tool?.summary ?? fallback, !summary.isEmpty {
                Text(summary)
                    .font(TextStyle.code)
                    .foregroundStyle(Palette.muted)
                    .lineLimit(2)
                    .truncationMode(.middle)
                    .textSelection(.enabled)
            }
        }
        .padding(.vertical, 2)
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

/// The box at the foot of a chat. Return sends; Option-Return starts a line.
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
                .lineLimit(1...8)
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
