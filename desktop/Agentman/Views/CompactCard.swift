import SwiftUI

/// An agent waiting on the user, small: who, what it asks in a line or
/// two, and the answer a tap away (its options, or a line to Agentman).
/// The same in a toast in the window's corner and in Agentman's chat.
struct CompactCard: View {
    @Environment(AgentsStore.self) private var store
    let card: ManagerCard
    /// What the cross does: put the card away in the chat, or only hide a
    /// toast, leaving the card where it is.
    var closeHelp = "Put away"
    let close: () async -> String?
    @State private var reply = ""
    @State private var problem: String?
    @State private var busy = false

    var body: some View {
        let session = store.session(card.sessionId)
        let name = session?.name ?? card.agentName
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                AgentMark(kind: session?.kind ?? "", size: 14)
                Text(name)
                    .font(TextStyle.captionMedium)
                    .foregroundStyle(Palette.text)
                    .lineLimit(1)
                Circle().fill(Palette.needsYou).frame(width: 5, height: 5)
                Text("needs you")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.needsYouText)
                Spacer(minLength: 6)
                if session != nil {
                    iconButton("arrow.up.right", help: "Open \(name)") { store.selection = .agent(card.sessionId) }
                }
                iconButton("xmark", help: closeHelp) {
                    act { await close() }
                }
            }
            Text(Self.inline(card.title))
                .font(Fonts.sans(13))
                .foregroundStyle(Palette.text)
                .lineLimit(2)
                .fixedSize(horizontal: false, vertical: true)
                .help(card.title)
            if let line = shownDetail {
                Text(line)
                    .font(Fonts.mono(11))
                    .foregroundStyle(Palette.textSecondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Palette.fill.opacity(0.7), in: RoundedRectangle(cornerRadius: 6, style: .continuous))
                    .help(card.detail ?? line)
            }
            if let question = card.question {
                QuestionChoices(question: question, suggested: card.suggested, typedPrompt: "Or reply to Agentman") { answer in
                    problem = await store.manager.reply(to: card, with: answer)
                    return problem == nil
                }
                .environment(\.compactChoices, true)
            } else {
                HStack(spacing: 6) {
                    TextField("Reply to Agentman about \(name)", text: $reply)
                        .textFieldStyle(.plain)
                        .font(TextStyle.caption)
                        .onSubmit(replyInWords)
                    Button(action: replyInWords) {
                        Image(systemName: "arrow.up")
                            .font(.system(size: 10, weight: .bold))
                            .foregroundStyle(Palette.onInverse)
                            .frame(width: 20, height: 20)
                            .background(Palette.inverse.opacity(typed ? 1 : 0.25), in: Circle())
                    }
                    .buttonStyle(.plain)
                    .disabled(!typed || busy)
                    .help("Agentman acts on it and passes on what \(name) needs")
                }
                .padding(.leading, 10)
                .padding(.trailing, 4)
                .padding(.vertical, 4)
                .background(Palette.fill, in: Capsule())
            }
            if let problem {
                Text(problem)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.errorText)
                    .lineLimit(2)
            }
        }
        .padding(12)
        .disabled(busy)
    }

    private var typed: Bool { !reply.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }

    /// The command or file in a line, unless the title already says it:
    /// "wants to run `git push`" needs no box saying git push again.
    private var shownDetail: String? {
        guard let detail = card.detail?.trimmingCharacters(in: .whitespacesAndNewlines), !detail.isEmpty else { return nil }
        let line = detail.split(separator: "\n").last.map { $0.trimmingCharacters(in: .whitespaces) } ?? detail
        return line.isEmpty || card.title.contains(line) ? nil : line
    }

    /// A title's inline Markdown, code in backticks among it, as text.
    static func inline(_ text: String) -> AttributedString {
        (try? AttributedString(markdown: text, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)))
            ?? AttributedString(text)
    }

    private func iconButton(_ symbol: String, help: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: 9, weight: .semibold))
                .foregroundStyle(Palette.muted)
                .frame(width: 18, height: 18)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(help)
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
