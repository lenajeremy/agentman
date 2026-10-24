import SwiftUI

/// The decision an agent is blocked on, answerable in place: a permission
/// prompt, a multiple-choice question, a typed answer, or one of Claude
/// Code's own dialogs.
struct QuestionCard: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    let question: Question
    @State private var note = ""
    @State private var noting: QuestionOption?
    @State private var checked: Set<String> = []
    @State private var busy = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 6) {
                Circle().fill(Palette.needsYou).frame(width: 7, height: 7)
                Text(question.title ?? "Needs your answer")
                    .font(TextStyle.bodyMedium)
                    .foregroundStyle(Palette.text)
            }
            if let detail = question.detail, !detail.isEmpty {
                Text(detail)
                    .font(TextStyle.code)
                    .foregroundStyle(Palette.text)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(10)
                    .background(Palette.fill, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            }
            if !question.prompt.isEmpty {
                Text(Markdown.inline(question.prompt))
                    .font(TextStyle.body)
                    .foregroundStyle(Palette.textSecondary)
            }

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
                        .onSubmit { answer(optionKey: nil, text: note) }
                    Button("Send") { answer(optionKey: nil, text: note) }
                        .disabled(note.trimmingCharacters(in: .whitespaces).isEmpty || busy)
                }
            }
        }
        .padding(16)
        .background(Palette.needsYouWash, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 16, style: .continuous)
                .strokeBorder(Palette.needsYouEdge, lineWidth: 1)
        )
        .disabled(busy)
    }

    private var singleChoice: some View {
        FlowLayout(spacing: 8) {
            ForEach(question.options) { option in
                Button {
                    if option.withText == true {
                        noting = option
                    } else {
                        answer(optionKey: option.key, text: nil)
                    }
                } label: {
                    Text(option.label)
                        .font(TextStyle.bodyMedium)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 7)
                }
                .buttonStyle(ChoiceButtonStyle(primary: option.id == primaryKey))
                .help(option.description ?? option.label)
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
                submitMultiple()
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
                    .onSubmit { answer(optionKey: option.key, text: note) }
                Button("Cancel") { noting = nil }
                Button("Send") { answer(optionKey: option.key, text: note) }
                    .disabled(note.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
    }

    /// The first affirmative option is the one most people reach for.
    private var primaryKey: String? {
        let affirmative = question.options.first {
            let label = $0.label.lowercased()
            return label.hasPrefix("yes") || label.hasPrefix("allow") || label.hasPrefix("approve") ||
                label.hasPrefix("continue") || label.hasPrefix("proceed")
        }
        return (affirmative ?? question.options.first)?.key
    }

    private func answer(optionKey: String?, text: String?) {
        busy = true
        Task {
            if await store.answer(session.id, question: question, optionKey: optionKey, text: text) {
                note = ""
                noting = nil
            }
            busy = false
        }
    }

    private func submitMultiple() {
        busy = true
        Task {
            _ = await store.answerMultiple(session.id, question: question, keys: Array(checked))
            busy = false
        }
    }
}

private struct ChoiceButtonStyle: ButtonStyle {
    var primary: Bool

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .foregroundStyle(primary ? Palette.onInverse : Palette.text)
            .background(
                Capsule().fill(primary ? Palette.inverse : Palette.surface)
            )
            .overlay(Capsule().strokeBorder(primary ? Color.clear : Palette.line, lineWidth: 1))
            .opacity(configuration.isPressed ? 0.75 : 1)
    }
}

/// Lays children out in rows, wrapping when a row is full, as chips do.
struct FlowLayout: Layout {
    var spacing: CGFloat = 8

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? .infinity
        var x: CGFloat = 0, y: CGFloat = 0, rowHeight: CGFloat = 0, widest: CGFloat = 0
        for subview in subviews {
            let size = subview.sizeThatFits(.unspecified)
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
            let size = subview.sizeThatFits(.unspecified)
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
