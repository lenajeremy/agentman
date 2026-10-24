import SwiftUI

/// The decision an agent is blocked on, answerable in place: a permission
/// prompt, a multiple-choice question, a typed answer, or one of Claude
/// Code's own dialogs.
struct QuestionCard: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    let question: Question

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 6) {
                Circle().fill(Palette.needsYou).frame(width: 7, height: 7)
                Text(question.title ?? "Needs your answer")
                    .font(TextStyle.bodyMedium)
                    .foregroundStyle(Palette.text)
            }
            if let detail = question.detail, !detail.isEmpty {
                CodeBox(text: detail)
            }
            if !question.prompt.isEmpty {
                Text(inlineMarkdown(question.prompt))
                    .font(TextStyle.body)
                    .foregroundStyle(Palette.textSecondary)
            }
            QuestionChoices(question: question) { reply in
                await store.answer(session.id, question: question, reply: reply)
            }
        }
        .padding(16)
        .background(Palette.needsYouWash, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 16, style: .continuous)
                .strokeBorder(Palette.needsYouEdge, lineWidth: 1)
        )
    }
}

/// Text shown as it was written, such as a command or a diff.
struct CodeBox: View {
    let text: String

    var body: some View {
        Text(text)
            .font(TextStyle.code)
            .foregroundStyle(Palette.text)
            .textSelection(.enabled)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(10)
            .background(Palette.fill, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
    }
}

/// An answer to an agent's question, however it was given: one option, a
/// few ticked, or words.
struct QuestionReply: Sendable {
    var optionKey: String?
    var optionKeys: [String]?
    var text: String?
}

/// A question's options as buttons, with a note for an option that takes
/// one, ticks for several, and a box for a typed answer.
struct QuestionChoices: View {
    let question: Question
    /// The option Agentman would pick, marked for the user to confirm.
    var suggested: String?
    let answer: (QuestionReply) async -> Bool
    @State private var note = ""
    @State private var noting: QuestionOption?
    @State private var checked: Set<String> = []
    @State private var busy = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            if question.multiple == true {
                multipleChoice
            } else if let option = noting {
                noteField(for: option)
            } else {
                singleChoice
            }

            if question.custom == true, question.multiple != true, noting == nil {
                HStack {
                    TextField("Or type an answer", text: $note)
                        .textFieldStyle(.roundedBorder)
                        .font(TextStyle.body)
                        .onSubmit { send(QuestionReply(text: note)) }
                    Button("Send") { send(QuestionReply(text: note)) }
                        .disabled(note.trimmingCharacters(in: .whitespaces).isEmpty || busy)
                }
            }
        }
        .disabled(busy)
    }

    private var singleChoice: some View {
        FlowLayout(spacing: 8) {
            ForEach(question.options) { option in
                Button {
                    if option.withText == true {
                        noting = option
                    } else {
                        send(QuestionReply(optionKey: option.key))
                    }
                } label: {
                    HStack(spacing: 5) {
                        if option.key == suggested {
                            Image(systemName: "sparkle")
                                .font(.system(size: 10, weight: .bold))
                        }
                        Text(option.label)
                            .font(TextStyle.bodyMedium)
                            .multilineTextAlignment(.leading)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    .padding(.horizontal, 14)
                    .padding(.vertical, 7)
                }
                .buttonStyle(ChoiceButtonStyle(primary: option.id == primaryKey))
                .help(option.key == suggested ? "Agentman's pick" : (option.description ?? option.label))
            }
        }
    }

    private var multipleChoice: some View {
        VStack(alignment: .leading, spacing: 6) {
            ForEach(question.options) { option in
                Toggle(isOn: Binding(
                    get: { checked.contains(option.key) },
                    set: { on in
                        if on { checked.insert(option.key) } else { checked.remove(option.key) }
                    }
                )) {
                    Text(option.label).font(TextStyle.body)
                }
                .toggleStyle(.checkbox)
            }
            Button("Submit") {
                send(QuestionReply(optionKeys: Array(checked)))
            }
            .buttonStyle(ChoiceButtonStyle(primary: true))
            .disabled(checked.isEmpty)
        }
        .onAppear {
            checked = Set(question.options.filter { $0.checked == true }.map(\.key))
        }
    }

    private func noteField(for option: QuestionOption) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(option.label)
                .font(TextStyle.caption)
                .foregroundStyle(Palette.muted)
            HStack {
                TextField("Tell the agent what to do instead", text: $note)
                    .textFieldStyle(.roundedBorder)
                    .font(TextStyle.body)
                    .onSubmit { send(QuestionReply(optionKey: option.key, text: note)) }
                Button("Cancel") { noting = nil }
                Button("Send") { send(QuestionReply(optionKey: option.key, text: note)) }
                    .disabled(note.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
    }

    /// Agentman's pick when it made one; otherwise the first affirmative
    /// option, the one most people reach for.
    private var primaryKey: String? {
        if let suggested, question.options.contains(where: { $0.key == suggested }) { return suggested }
        let affirmative = question.options.first {
            let label = $0.label.lowercased()
            return label.hasPrefix("yes") || label.hasPrefix("allow") || label.hasPrefix("approve") ||
                label.hasPrefix("continue") || label.hasPrefix("proceed")
        }
        return (affirmative ?? question.options.first)?.key
    }

    private func send(_ reply: QuestionReply) {
        var reply = reply
        if let text = reply.text?.trimmingCharacters(in: .whitespacesAndNewlines) {
            reply.text = text.isEmpty ? nil : text
        }
        guard reply.optionKey != nil || reply.optionKeys?.isEmpty == false || reply.text != nil else { return }
        busy = true
        Task {
            if await answer(reply) {
                note = ""
                noting = nil
            }
            busy = false
        }
    }
}

struct ChoiceButtonStyle: ButtonStyle {
    var primary: Bool

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .foregroundStyle(primary ? Palette.onInverse : Palette.text)
            // A capsule on one line; a long label that wraps keeps the same
            // corners rather than turning into a lozenge.
            .background(
                RoundedRectangle(cornerRadius: 15, style: .continuous).fill(primary ? Palette.inverse : Palette.surface)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 15, style: .continuous)
                    .strokeBorder(primary ? Color.clear : Palette.line, lineWidth: 1)
            )
            .opacity(configuration.isPressed ? 0.75 : 1)
    }
}

/// Lays children out in rows, wrapping when a row is full, as chips do.
struct FlowLayout: Layout {
    var spacing: CGFloat = 8

    /// A child's size: as wide as it likes, or, wider than a row, a row wide,
    /// with its text wrapped. Only a real row wraps it: measured for its
    /// smallest size (a zero width), every label would wrap a letter a line,
    /// and a card outside a scroll view would push the window that tall.
    private func size(of subview: LayoutSubview, within width: CGFloat) -> CGSize {
        let ideal = subview.sizeThatFits(.unspecified)
        guard width.isFinite, width >= 160, ideal.width > width else { return ideal }
        return subview.sizeThatFits(ProposedViewSize(width: width, height: nil))
    }

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? .infinity
        var x: CGFloat = 0, y: CGFloat = 0, rowHeight: CGFloat = 0, widest: CGFloat = 0
        for subview in subviews {
            let size = size(of: subview, within: width)
            if x > 0, x + size.width > width {
                y += rowHeight + spacing
                x = 0
                rowHeight = 0
            }
            x += size.width + spacing
            rowHeight = max(rowHeight, size.height)
            widest = max(widest, x - spacing)
        }
        return CGSize(width: min(widest, width), height: y + rowHeight)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX, y = bounds.minY, rowHeight: CGFloat = 0
        for subview in subviews {
            let size = size(of: subview, within: bounds.width)
            if x > bounds.minX, x + size.width > bounds.maxX {
                y += rowHeight + spacing
                x = bounds.minX
                rowHeight = 0
            }
            subview.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(size))
            x += size.width + spacing
            rowHeight = max(rowHeight, size.height)
        }
    }
}
