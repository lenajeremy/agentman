import SwiftUI

/// The conversation with Agentman: the one place to ask what every agent is
/// doing, read what happened, and be told when something needs you.
struct ManagerChatView: View {
    @Environment(AgentsStore.self) private var store
    @State private var draft = ""
    @State private var sending = false

    private var manager: ManagerStore { store.manager }

    var body: some View {
        VStack(spacing: 0) {
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 18) {
                        if manager.entries.isEmpty {
                            ManagerWelcome()
                                .padding(.top, 20)
                            if !manager.needsKey {
                                Suggestions { prompt in send(prompt) }
                            }
                        } else {
                            if manager.hasOlder {
                                Button("Earlier messages") {
                                    Task { await manager.loadOlder() }
                                }
                                .buttonStyle(.link)
                                .font(TextStyle.caption)
                                .frame(maxWidth: .infinity)
                            }
                            ForEach(ChatItem.items(from: manager.entries)) { item in
                                ChatItemView(item: item)
                                    .id(item.id)
                            }
                            if manager.thinking, manager.entries.last?.streaming != true {
                                ThinkingRow()
                                    .id("thinking")
                            }
                        }
                    }
                    .padding(.horizontal, 32)
                    .padding(.vertical, 24)
                    .frame(maxWidth: 780)
                    .frame(maxWidth: .infinity)
                }
                .defaultScrollAnchor(.bottom)
                .onChange(of: scrollKey) { _, _ in
                    withAnimation(.easeOut(duration: 0.18)) {
                        proxy.scrollTo(manager.thinking ? "thinking" : manager.entries.last?.id, anchor: .bottom)
                    }
                }
            }

            VStack(spacing: 8) {
                if let problem = manager.problem {
                    Label(problem, systemImage: "exclamationmark.triangle")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.errorText)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                if manager.needsKey {
                    KeySetupCard()
                } else {
                    ManagerComposer(text: $draft, thinking: manager.thinking, sending: sending) {
                        send(draft)
                    } onStop: {
                        manager.stop()
                    }
                }
            }
            .padding(.horizontal, 32)
            .padding(.bottom, 18)
            .frame(maxWidth: 780)
            .frame(maxWidth: .infinity)
        }
    }

    /// Changes whenever the conversation grows, so it can follow along.
    private var scrollKey: String {
        "\(manager.entries.count)-\(manager.entries.last?.text.count ?? 0)-\(manager.thinking)"
    }

    private func send(_ text: String) {
        let message = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !message.isEmpty, !sending else { return }
        sending = true
        Task {
            if await manager.send(message) {
                draft = ""
            }
            sending = false
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
            // A card answered in the agent itself, or put away, is gone.
            if entry.card?.isGone == true { continue }
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
    let item: ChatItem

    var body: some View {
        switch item {
        case .entry(let entry):
            switch entry.role {
            case "user":
                HStack {
                    Spacer(minLength: 80)
                    Text(entry.text)
                        .font(TextStyle.body)
                        .foregroundStyle(Palette.text)
                        .textSelection(.enabled)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 10)
                        .background(Palette.fill, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
                }
            case "assistant":
                AssistantReply(entry: entry)
            case "brief", "agent":
                BriefCard(entry: entry)
            case "card":
                if let card = entry.card {
                    if card.isOpen {
                        ManagerCardView(card: card)
                    } else {
                        CardReceipt(card: card)
                    }
                }
            case "error":
                Label(entry.text, systemImage: "exclamationmark.circle")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.errorText)
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
        HStack(alignment: .top, spacing: 12) {
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
        VStack(alignment: .leading, spacing: 6) {
            Button {
                withAnimation(.easeOut(duration: 0.15)) { open.toggle() }
            } label: {
                HStack(spacing: 6) {
                    Image(systemName: "eye")
                        .font(.system(size: 10, weight: .semibold))
                    Text(summary)
                        .lineLimit(1)
                    Image(systemName: "chevron.right")
                        .font(.system(size: 9, weight: .semibold))
                        .rotationEffect(.degrees(open ? 90 : 0))
                }
                .font(TextStyle.caption)
                .foregroundStyle(Palette.muted)
            }
            .buttonStyle(.plain)
            .padding(.leading, 34)
            if open {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(entries) { entry in
                        Text(entry.text)
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.faint)
                    }
                }
                .padding(.leading, 50)
            }
        }
    }

    private var summary: String {
        if entries.count == 1 { return entries[0].text }
        return "\(entries[0].text) and \(entries.count - 1) more"
    }
}

/// An image Agentman looked at, shown to the user as well.
private struct ImageActivity: View {
    @Environment(AgentsStore.self) private var store
    let entry: ManagerEntry
    @State private var image: NSImage?

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                Image(systemName: "photo")
                    .font(.system(size: 10, weight: .semibold))
                Text(entry.text)
            }
            .font(TextStyle.caption)
            .foregroundStyle(Palette.muted)
            Group {
                if let image {
                    Image(nsImage: image)
                        .resizable()
                        .scaledToFit()
                        .frame(maxWidth: 520, maxHeight: 360, alignment: .leading)
                        .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
                        .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(Palette.line))
                } else {
                    RoundedRectangle(cornerRadius: 12, style: .continuous)
                        .fill(Palette.fill)
                        .frame(width: 240, height: 140)
                        .overlay(ProgressView().controlSize(.small))
                }
            }
        }
        .padding(.leading, 34)
        .task(id: entry.id) {
            guard let sessionId = entry.sessionId, let path = entry.path else { return }
            image = await store.manager.image(sessionId: sessionId, path: path)
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
                    .textSelection(.enabled)
                if let needs = brief?.needsYou, !needs.isEmpty {
                    Label(needs, systemImage: "hand.raised")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.needsYouText)
                }
            }
            Spacer(minLength: 8)
            if let session {
                Button("Open") { store.selection = .agent(session.id) }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
            }
        }
        .padding(14)
        .background(Palette.surface, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 14, style: .continuous).strokeBorder(Palette.line))
    }
}

/// A decision only the user can make, answerable where it stands: the
/// agent's question with its own options, or a box to reply in words.
private struct ManagerCardView: View {
    @Environment(AgentsStore.self) private var store
    let card: ManagerCard
    @State private var reply = ""
    @State private var problem: String?
    @State private var busy = false

    var body: some View {
        let session = store.session(card.sessionId)
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 8) {
                AgentMark(kind: session?.kind ?? "", size: 18)
                Text(card.agentName)
                    .font(TextStyle.bodyMedium)
                    .foregroundStyle(Palette.text)
                Circle().fill(Palette.needsYou).frame(width: 6, height: 6)
                Text("needs you")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.needsYouText)
                Spacer(minLength: 8)
                if session != nil {
                    Button("Open") { store.selection = .agent(card.sessionId) }
                        .buttonStyle(.bordered)
                        .controlSize(.small)
                }
                Button {
                    act { await store.manager.dismiss(card) }
                } label: {
                    Image(systemName: "xmark")
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundStyle(Palette.muted)
                        .frame(width: 20, height: 20)
                }
                .buttonStyle(.plain)
                .help("Put away (or swipe it aside)")
            }

            Text(card.title)
                .font(TextStyle.body)
                .foregroundStyle(Palette.text)
                .textSelection(.enabled)
            if let detail = card.detail, !detail.isEmpty {
                CodeBox(text: detail)
            }
            if let reason = card.reason, !reason.isEmpty {
                Label("Waits for you: \(reason)", systemImage: "hand.raised")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.needsYouText)
            }
            if let context = card.context, !context.isEmpty {
                Text(context)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
                    .lineLimit(3)
            }
            if card.suggested != nil {
                Label("Agentman's pick is marked. One tap sends it.", systemImage: "sparkle")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.working)
            }

            if let question = card.question {
                QuestionChoices(question: question, suggested: card.suggested) { answer in
                    await send(answer)
                }
            } else {
                HStack(spacing: 8) {
                    TextField("Reply to \(card.agentName)", text: $reply, axis: .vertical)
                        .textFieldStyle(.roundedBorder)
                        .font(TextStyle.body)
                        .lineLimit(1...5)
                        .onSubmit { replyInWords() }
                    Button("Send", action: replyInWords)
                        .buttonStyle(.borderedProminent)
                        .disabled(reply.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || busy)
                }
            }
            if let problem {
                Text(problem)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.errorText)
            }
        }
        .padding(16)
        .background(Palette.needsYouWash, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 16, style: .continuous).strokeBorder(Palette.needsYouEdge))
        .disabled(busy)
        .swipeToDismiss {
            problem = await store.manager.dismiss(card)
            return problem == nil
        }
    }

    private func send(_ answer: QuestionReply) async -> Bool {
        problem = await store.manager.reply(to: card, with: answer)
        return problem == nil
    }

    private func replyInWords() {
        let text = reply.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        act {
            let problem = await store.manager.reply(to: card, with: QuestionReply(text: text))
            if problem == nil { reply = "" }
            return problem
        }
    }

    private func act(_ work: @escaping () async -> String?) {
        busy = true
        Task {
            problem = await work()
            busy = false
        }
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
            Text("\(card.agentName) · \(card.outcome ?? "Answered")")
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

private struct Suggestions: View {
    var send: (String) -> Void
    private let prompts = ["What is everyone working on?", "Does anything need me?", "What changed in the last hour?"]

    var body: some View {
        FlowLayout(spacing: 8) {
            ForEach(prompts, id: \.self) { prompt in
                Button {
                    send(prompt)
                } label: {
                    Text(prompt)
                        .font(TextStyle.body)
                        .foregroundStyle(Palette.text)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 8)
                        .background(Palette.surface, in: Capsule())
                        .overlay(Capsule().strokeBorder(Palette.line))
                }
                .buttonStyle(.plain)
            }
        }
    }
}

/// The composer, whose button stops Agentman while it is replying.
private struct ManagerComposer: View {
    @Binding var text: String
    var thinking: Bool
    var sending: Bool
    var onSend: () -> Void
    var onStop: () -> Void

    var body: some View {
        HStack(alignment: .bottom, spacing: 10) {
            TextField("Ask Agentman about your agents", text: $text, axis: .vertical)
                .textFieldStyle(.plain)
                .font(TextStyle.body)
                .lineLimit(1...8)
                .onSubmit { if !thinking { onSend() } }
                .padding(.vertical, 6)
            Button {
                thinking ? onStop() : onSend()
            } label: {
                Image(systemName: thinking ? "stop.fill" : "arrow.up")
                    .font(.system(size: thinking ? 11 : 13, weight: .bold))
                    .foregroundStyle(Palette.onInverse)
                    .frame(width: 28, height: 28)
                    .background(Palette.inverse, in: Circle())
            }
            .buttonStyle(.plain)
            .disabled(!thinking && (sending || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty))
            .help(thinking ? "Stop" : "Send")
        }
        .padding(.leading, 16)
        .padding(.trailing, 8)
        .padding(.vertical, 8)
        .background(Palette.surface, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 20, style: .continuous).strokeBorder(Palette.line, lineWidth: 1))
        .shadow(color: .black.opacity(0.04), radius: 8, y: 2)
    }
}

/// Where the API key goes, in place of the composer until there is one.
private struct KeySetupCard: View {
    @Environment(AgentsStore.self) private var store
    @State private var key = ""
    @State private var saving = false
    @State private var problem: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 8) {
                Image(systemName: "key")
                    .foregroundStyle(Palette.working)
                Text("Agentman runs on your Anthropic API key")
                    .font(TextStyle.bodyMedium)
                    .foregroundStyle(Palette.text)
            }
            Text("It stays in your Mac's Keychain and is only sent to Anthropic. Create one in the Claude Console.")
                .font(TextStyle.caption)
                .foregroundStyle(Palette.muted)
            HStack(spacing: 8) {
                SecureField("sk-ant-…", text: $key)
                    .textFieldStyle(.roundedBorder)
                    .font(TextStyle.code)
                    .onSubmit(save)
                Button("Save", action: save)
                    .buttonStyle(.borderedProminent)
                    .disabled(key.trimmingCharacters(in: .whitespaces).isEmpty || saving)
            }
            HStack {
                Link("Get a key", destination: URL(string: "https://console.anthropic.com/settings/keys")!)
                    .font(TextStyle.caption)
                if let problem {
                    Text(problem)
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.errorText)
                }
            }
        }
        .padding(16)
        .background(Palette.surface, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 16, style: .continuous).strokeBorder(Palette.line))
    }

    private func save() {
        let value = key.trimmingCharacters(in: .whitespaces)
        guard !value.isEmpty else { return }
        saving = true
        Task {
            problem = await store.manager.setKey(value)
            if problem == nil { key = "" }
            saving = false
        }
    }
}
