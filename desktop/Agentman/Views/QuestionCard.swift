import SwiftUI

/// The decision an agent is blocked on, answerable in place: a permission
/// prompt, a multiple-choice question, a typed answer, or one of Claude
/// Code's own dialogs.
///
/// A question the agent asked reads as Claude's own app shows one: the
/// question as the heading, the choices under it, a row to write your own,
/// and a ✕ to close it unanswered. A form of several questions names each
/// one along the top, ticked once answered, with ‹ › to move between them.
/// A permission prompt keeps its command first and its question under it.
struct QuestionCard: View {
    @Environment(AgentsStore.self) private var store
    let session: Session
    let question: Question
    /// The question a move was asked for, until the form shows it.
    @State private var moving: Int?
    @State private var closing = false

    /// The agent's own question, not a permission prompt: its words are the
    /// heading.
    private var asked: Bool {
        question.isReview || (!question.isApproval && (question.detail ?? "").isEmpty && !question.prompt.isEmpty)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: asked ? 12 : 14) {
            header
            if asked {
                Text(inlineMarkdown(question.prompt))
                    .font(Fonts.heading(17, classic: Fonts.sans(17, .semibold)))
                    .foregroundStyle(Palette.text)
                    .lineSpacing(3)
                    .fixedSize(horizontal: false, vertical: true)
                    .textSelection(.enabled)
            }
            if let detail = question.detail, !detail.isEmpty {
                if question.isReview, let answers = ReviewAnswers(detail: detail) {
                    answers
                } else {
                    CodeBox(text: detail)
                }
            }
            QuestionNotice(question: question)
            if !asked, !question.prompt.isEmpty, !question.promptRepeatsDetail {
                Text(inlineMarkdown(question.prompt))
                    .font(TextStyle.body)
                    .foregroundStyle(Palette.textSecondary)
            }
            QuestionChoices(question: question, typedPrompt: "Write your own answer") { reply in
                await store.answer(session.id, question: question, reply: reply)
            }
            // A new question starts with nothing typed or ticked.
            .id(question.id)
            // The form is moving to another question: these are going.
            .opacity(moving == nil && !closing ? 1 : 0.45)
            .disabled(moving != nil || closing)
        }
        .modifier(DecisionPanel())
        .onChange(of: question.id) {
            moving = nil
            closing = false
        }
    }

    private var header: some View {
        HStack(spacing: 7) {
            Circle().fill(Palette.needsYou).frame(width: 7, height: 7)
            if question.isForm, let steps = question.steps {
                // Every question's name, or, too many for the width, the one
                // on screen.
                ViewThatFits(in: .horizontal) {
                    QuestionSteps(steps: steps, current: moving ?? question.step ?? 0, move: move)
                    stepTitle(steps)
                }
            } else {
                Text(question.title ?? "Needs your answer")
                    .font(asked ? TextStyle.captionMedium : TextStyle.bodyMedium)
                    .foregroundStyle(asked ? Palette.muted : Palette.text)
                    .lineLimit(1)
            }
            Spacer(minLength: 8)
            if question.isForm, let steps = question.steps {
                StepPager(current: moving ?? question.step ?? 1, count: steps.count, move: move)
                    .disabled(moving != nil || closing)
            }
            if question.closable == true {
                HeaderButton(systemImage: "xmark", help: "Close the question unanswered: \(session.name) waits for your message") {
                    close()
                }
                .disabled(closing)
            }
        }
    }

    private func stepTitle(_ steps: [QuestionStep]) -> some View {
        let step = moving ?? question.step ?? 0
        let title = step >= 1 && step <= steps.count ? steps[step - 1].title : "Review"
        return Text(title)
            .font(TextStyle.captionMedium)
            .foregroundStyle(Palette.muted)
            .lineLimit(1)
    }

    private func move(to step: Int) {
        guard moving == nil, step != question.step else { return }
        moving = step
        let shown = question.id
        Task {
            let moved = await store.moveQuestion(session.id, question: question, to: step)
            // The next look at the agent shows where it went; if it never
            // does, the choices come back rather than staying greyed.
            try? await Task.sleep(for: .seconds(moved ? 4 : 0))
            if question.id == shown { moving = nil }
        }
    }

    private func close() {
        closing = true
        let shown = question.id
        Task {
            let closed = await store.closeQuestion(session.id, question: question)
            try? await Task.sleep(for: .seconds(closed ? 4 : 0))
            if question.id == shown { closing = false }
        }
    }
}

/// A form's questions by name along the top of its card: the one on screen
/// in full colour, a tick on each answered, and a click on one goes to it.
private struct QuestionSteps: View {
    let steps: [QuestionStep]
    let current: Int
    let move: (Int) -> Void

    var body: some View {
        HStack(spacing: 4) {
            ForEach(Array(steps.enumerated()), id: \.offset) { index, step in
                StepTab(title: step.title, answered: step.answered == true, current: current == index + 1) {
                    move(index + 1)
                }
            }
            StepTab(title: "Review", answered: false, current: current == steps.count + 1) {
                move(steps.count + 1)
            }
        }
        .fixedSize()
    }
}

private struct StepTab: View {
    let title: String
    let answered: Bool
    let current: Bool
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 4) {
                if answered {
                    Image(systemName: "checkmark")
                        .font(.system(size: 9, weight: .bold))
                        .foregroundStyle(Palette.ok)
                }
                Text(title)
                    .font(TextStyle.captionMedium)
                    .foregroundStyle(current ? Palette.text : hovering ? Palette.textSecondary : Palette.muted)
                    .lineLimit(1)
            }
            .padding(.horizontal, 7)
            .padding(.vertical, 3)
            .background(current ? Palette.fill : hovering ? Palette.fill.opacity(0.6) : .clear,
                        in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(answered ? "\(title): answered" : title)
        .accessibilityAddTraits(current ? .isSelected : [])
    }
}

/// ‹ 2 of 3 ›: back and on through a form, one question at a time. Past the
/// last question is the review of the answers.
private struct StepPager: View {
    let current: Int
    let count: Int
    let move: (Int) -> Void

    var body: some View {
        HStack(spacing: 2) {
            HeaderButton(systemImage: "chevron.left", help: "Previous question") { move(current - 1) }
                .disabled(current <= 1)
            Text(current > count ? "Review" : "\(current) of \(count)")
                .font(TextStyle.caption)
                .monospacedDigit()
                .foregroundStyle(Palette.muted)
                .fixedSize()
            HeaderButton(systemImage: "chevron.right", help: current == count ? "Review your answers" : "Next question") { move(current + 1) }
                .disabled(current > count)
        }
    }
}

/// A small icon button at the top of the card.
private struct HeaderButton: View {
    let systemImage: String
    let help: String
    let action: () -> Void
    @State private var hovering = false
    @Environment(\.isEnabled) private var enabled

    var body: some View {
        Button(action: action) {
            Image(systemName: systemImage)
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(enabled ? (hovering ? Palette.text : Palette.muted) : Palette.faint.opacity(0.6))
                .frame(width: 24, height: 24)
                .background(hovering && enabled ? Palette.fill : .clear, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(help)
        .accessibilityLabel(help)
    }
}

/// The answers a form's review lists ("● Which shape?" over "→ Square"), as
/// rows to read rather than terminal text, with its warning when some are
/// unanswered. Nil when the text is not in that shape.
private struct ReviewAnswers: View {
    let warning: String?
    let answers: [(question: String, answer: String)]

    init?(detail: String) {
        var warning: String?
        var answers: [(String, String)] = []
        var asked: String?
        for raw in detail.split(separator: "\n") {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("⚠") {
                warning = line.dropFirst().trimmingCharacters(in: .whitespaces)
            } else if line.hasPrefix("●") {
                asked = line.dropFirst().trimmingCharacters(in: .whitespaces)
            } else if line.hasPrefix("→"), let question = asked {
                answers.append((question, line.dropFirst().trimmingCharacters(in: .whitespaces)))
                asked = nil
            } else if !line.isEmpty {
                return nil
            }
        }
        guard warning != nil || !answers.isEmpty else { return nil }
        self.warning = warning
        self.answers = answers
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            ForEach(Array(answers.enumerated()), id: \.offset) { _, row in
                HStack(alignment: .firstTextBaseline, spacing: 10) {
                    Text(inlineMarkdown(row.question))
                        .font(TextStyle.body)
                        .foregroundStyle(Palette.muted)
                    Spacer(minLength: 12)
                    Text(row.answer)
                        .font(TextStyle.bodyMedium)
                        .foregroundStyle(Palette.text)
                        .multilineTextAlignment(.trailing)
                }
                .fixedSize(horizontal: false, vertical: true)
            }
            if let warning {
                Label(warning, systemImage: "exclamationmark.triangle")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.needsYouText)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .background(Palette.fill.opacity(0.6), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
    }
}

extension Question {
    /// Whether the question's sentence only says again what is shown above
    /// it ("Run git push origin main?" under that command): then it is left
    /// out, and the command is said once.
    var promptRepeatsDetail: Bool {
        guard let detail = detail?.trimmingCharacters(in: .whitespacesAndNewlines), !detail.isEmpty, !detail.contains("\n") else { return false }
        let prompt = self.prompt.trimmingCharacters(in: .whitespacesAndNewlines)
        guard prompt.localizedCaseInsensitiveContains(detail) else { return false }
        // What is left once the command is taken out is only a verb or two.
        let rest = prompt.replacingOccurrences(of: detail, with: "", options: .caseInsensitive)
            .trimmingCharacters(in: .punctuationCharacters.union(.whitespaces).union(CharacterSet(charactersIn: "`")))
        return rest.split(separator: " ").count <= 2
    }

    /// Whether it takes a typed answer as well as its options.
    var takesTypedAnswer: Bool { custom == true && multiple != true }

    /// Whether it asks for permission to do something: its options say yes
    /// or allow, rather than offering answers.
    var isApproval: Bool {
        options.contains {
            let label = $0.label.lowercased()
            return label.hasPrefix("yes") || label.hasPrefix("allow") || label.hasPrefix("approve")
        }
    }
}

/// The panel a decision sits in: the site's edge, lifted on the needs-you
/// orange, the same for an agent's question in its thread and a card in
/// Agentman's.
struct DecisionPanel: ViewModifier {
    func body(content: Content) -> some View {
        content
            .padding(20)
            .look {
                $0.background(
                    RoundedRectangle(cornerRadius: 16, style: .continuous).hardShadowFill(Palette.surface, shadow: Palette.needsYou, depth: 4)
                )
                .overlay(RoundedRectangle(cornerRadius: 16, style: .continuous).strokeBorder(Palette.edge, lineWidth: Palette.edgeWidth))
                .padding(.bottom, 4)
            } classic: {
                $0.background(Palette.surface, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: 16, style: .continuous).strokeBorder(Palette.line))
                    .shadow(color: .black.opacity(0.035), radius: 10, y: 3)
            }
    }
}

/// Text shown as it was written, such as a command or a diff.
struct CodeBox: View {
    let text: String
    /// Small, on a card of the Board.
    var compact = false

    var body: some View {
        Text(text)
            .font(compact ? Fonts.mono(11.5) : TextStyle.code)
            .foregroundStyle(Palette.text)
            .textSelection(.enabled)
            // Always whole: what is being approved must never be cut short.
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, compact ? 8 : 12)
            .padding(.vertical, compact ? 6 : 10)
            .background(Palette.fill, in: RoundedRectangle(cornerRadius: compact ? 6 : 10, style: .continuous))
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
extension EnvironmentValues {
    /// Choices drawn small, for a card in a toast or in Agentman's chat:
    /// smaller type and rows, an option's description left out.
    @Entry var compactChoices = false
    /// Compact choices with room to read them, on a card of the Board.
    @Entry var roomyChoices = false
}

struct QuestionChoices: View {
    let question: Question
    /// The option Agentman would pick, marked for the user to confirm.
    var suggested: String?
    /// The typed answer's box, which in Agentman's chat is for Agentman.
    var typedPrompt = "Or type an answer"
    /// Whether to show that box: not where the message box below takes it.
    var typed = true
    let answer: (QuestionReply) async -> Bool
    @State private var note = ""
    @State private var noting: QuestionOption?
    @State private var checked: Set<String> = []
    @State private var busy = false
    @Environment(\.compactChoices) private var compact
    @Environment(\.roomyChoices) private var roomy

    var body: some View {
        VStack(alignment: .leading, spacing: compact ? 6 : 12) {
            if question.multiple == true {
                multipleChoice
            } else if let option = noting {
                noteField(for: option)
            } else {
                singleChoice
                    // Words being written are the answer: the choices step back.
                    .opacity(writing ? 0.45 : 1)
            }

            if typed, question.custom == true, question.multiple != true, noting == nil {
                if !compact && !roomy {
                    WriteYourOwnRow(prompt: typedPrompt, text: $note, busy: busy) {
                        send(QuestionReply(text: note))
                    }
                } else {
                HStack {
                    TextField(typedPrompt, text: $note)
                        .textFieldStyle(.roundedBorder)
                        .font(compact ? TextStyle.caption : TextStyle.body)
                        .onSubmit { send(QuestionReply(text: note)) }
                    Button("Send") { send(QuestionReply(text: note)) }
                        .controlSize(compact ? .small : .regular)
                        .disabled(note.trimmingCharacters(in: .whitespaces).isEmpty || busy)
                }
                }
            }
        }
        .disabled(busy)
    }

    private var writing: Bool {
        typed && !compact && !roomy && question.custom == true && !note.trimmingCharacters(in: .whitespaces).isEmpty
    }

    private var singleChoice: some View {
        // On the Board in New the choices are buttons on hard shadows: room
        // for the shadows between them.
        VStack(spacing: Look.site && roomy ? 7 : compact ? 2 : 4) {
            ForEach(Array(question.options.enumerated()), id: \.element.id) { index, option in
                OptionRow(
                    number: index + 1, option: option,
                    pick: option.key == suggested, leading: option.id == primaryKey, strong: option.key == strongKey,
                    chosen: option.checked == true
                ) {
                    if option.withText == true {
                        noting = option
                    } else {
                        send(QuestionReply(optionKey: option.key))
                    }
                }
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

    /// The answer already given, back on a question of a form; Agentman's
    /// pick when it made one; otherwise the first affirmative option, the
    /// one most people reach for.
    private var primaryKey: String? {
        question.options.first { $0.checked == true }?.key ?? strongKey ?? question.options.first?.key
    }

    /// The answer worth the orange on the Board: Agentman's pick, or a yes.
    /// Never merely the first of several, which would push one at random.
    private var strongKey: String? {
        if let suggested, question.options.contains(where: { $0.key == suggested }) { return suggested }
        return question.options.first {
            let label = $0.label.lowercased()
            return label.hasPrefix("yes") || label.hasPrefix("allow") || label.hasPrefix("approve") ||
                label.hasPrefix("continue") || label.hasPrefix("proceed")
        }?.key
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
            // The site's buttons: ink, or edged white, on a hard shadow.
            .background(
                RoundedRectangle(cornerRadius: 15, style: .continuous).hardShadowFill(primary ? Palette.inverse : Palette.surface, shadow: primary ? Palette.inverseShadow : Palette.edge, depth: 2)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 15, style: .continuous)
                    .strokeBorder(primary ? Color.clear : Palette.edge, lineWidth: Palette.edgeWidth)
            )
            .opacity(configuration.isPressed ? 0.75 : 1)
    }
}

/// One choice: its number, its words in full, and Agentman's pick marked.
/// The likeliest answer is lightly filled; the rest fill as the pointer
/// passes.
private struct OptionRow: View {
    let number: Int
    let option: QuestionOption
    var pick = false
    var leading = false
    /// On the Board in New, in the orange: Agentman's pick, or a yes.
    var strong = false
    /// The answer given before, back on a question of a form.
    var chosen = false
    let action: () -> Void
    @State private var hovering = false
    @Environment(\.compactChoices) private var compact
    @Environment(\.roomyChoices) private var roomy

    /// On the Board in New: a button of the site's, not a row.
    private var siteButton: Bool { Look.site && roomy }

    var body: some View {
        Button(action: action) {
            HStack(alignment: .firstTextBaseline, spacing: compact ? 8 : 10) {
                Text("\(number)")
                    .font(TextStyle.label)
                    .monospacedDigit()
                    .foregroundStyle(siteButton && strong ? Palette.onFlood.opacity(0.8) : roomy ? Palette.faint : Palette.muted)
                    .frame(width: roomy ? 12 : compact ? 17 : 20, height: compact ? 17 : 20)
                    .background(roomy ? Color.clear : Palette.fill, in: RoundedRectangle(cornerRadius: compact ? 5 : 6, style: .continuous))
                    .alignmentGuide(.firstTextBaseline) { $0[.firstTextBaseline] }
                VStack(alignment: .leading, spacing: 2) {
                    Text(option.label)
                        .font(roomy ? Fonts.sans(13, siteButton && strong ? .semibold : .regular) : compact ? TextStyle.caption : TextStyle.body)
                        .foregroundStyle(siteButton && strong ? Palette.onFlood : Palette.text)
                        .lineSpacing(compact ? 2 : 4)
                        .multilineTextAlignment(.leading)
                        .fixedSize(horizontal: false, vertical: true)
                    if !compact, let description = option.description, !description.isEmpty {
                        Text(description)
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.muted)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                Spacer(minLength: 8)
                if pick, compact {
                    Image(systemName: "sparkle")
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundStyle(Palette.workingText)
                        .help("Agentman's pick")
                } else if pick {
                    Label("Agentman's pick", systemImage: "sparkle")
                        .font(TextStyle.label)
                        .foregroundStyle(Palette.workingText)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 3)
                        .background(Palette.workingWash, in: Capsule())
                }
                if chosen {
                    Image(systemName: "checkmark")
                        .font(.system(size: 11, weight: .bold))
                        .foregroundStyle(Palette.ok)
                        .help("Your answer")
                } else if hovering {
                    Image(systemName: "return")
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundStyle(Palette.faint)
                }
            }
            .padding(.horizontal, siteButton ? 10 : roomy ? 6 : compact ? 8 : 12)
            .padding(.vertical, siteButton ? 6 : roomy ? 4 : compact ? 5 : 10)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background {
                if siteButton {
                    // The site's buttons: edged, on a hard shadow; the likely
                    // answer in the orange.
                    RoundedRectangle(cornerRadius: 8, style: .continuous)
                        .hardShadowFill(strong ? Palette.flood : (hovering ? Palette.fill : Palette.surface), shadow: Palette.edge, depth: 2)
                        .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(Palette.edge, lineWidth: Palette.edgeWidth))
                } else {
                    RoundedRectangle(cornerRadius: roomy ? 6 : compact ? 8 : 10, style: .continuous)
                        .fill(hovering ? Palette.fillStrong : (leading || pick ? Palette.fill : Color.clear))
                }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(option.withText == true ? "Choose, and add a note for the agent" : option.label)
    }
}

/// The row under a question's choices for an answer of your own: a pencil
/// where a choice has its number, the words, and a send arrow once there
/// are some.
private struct WriteYourOwnRow: View {
    let prompt: String
    @Binding var text: String
    let busy: Bool
    let send: () -> Void
    @FocusState private var focused: Bool

    private var empty: Bool { text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }

    var body: some View {
        HStack(alignment: .center, spacing: 10) {
            Image(systemName: "pencil")
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(Palette.muted)
                .frame(width: 20, height: 20)
                .background(Palette.fill, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            TextField(prompt, text: $text, axis: .vertical)
                .textFieldStyle(.plain)
                .font(TextStyle.body)
                .lineLimit(1...6)
                .focused($focused)
                .onSubmit(send)
            Button(action: send) {
                Image(systemName: "arrow.up")
                    .font(.system(size: 11, weight: .bold))
                    .foregroundStyle(Palette.onInverse)
                    .frame(width: 24, height: 24)
                    .background(Palette.inverse, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
            }
            .buttonStyle(.plain)
            .opacity(empty ? 0 : 1)
            .disabled(empty || busy)
            .help("Send this answer")
        }
        .padding(.leading, 12)
        .padding(.trailing, 8)
        .padding(.vertical, 7)
        .background(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .strokeBorder(focused ? Palette.edge : Palette.line, lineWidth: Palette.edgeWidth)
        )
        .contentShape(Rectangle())
        .onTapGesture { focused = true }
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

/// That agreeing brings up a permission dialog from macOS itself.
struct QuestionNotice: View {
    let question: Question

    var body: some View {
        if let notice = question.notice, !notice.isEmpty {
            Label(notice, systemImage: "lock.shield")
                .font(TextStyle.caption)
                .foregroundStyle(Palette.textSecondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }
}
