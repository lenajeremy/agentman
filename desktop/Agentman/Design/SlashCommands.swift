import AppKit
import SwiftUI

/// `/` and a few letters at the start of a message box: the agent's commands
/// that match, as its own terminal would offer them, to pick from with the
/// arrow keys, Return or Tab, or the pointer. A pick goes into the box, with
/// a space after it when the command takes something.
@MainActor @Observable
final class SlashCommands {
    /// Everything the agent offers, once asked for.
    private(set) var commands: [SlashCommand] = []
    /// What is typed after the `/`, while the list is open.
    private(set) var query: String?
    private(set) var results: [SlashCommand] = []
    private(set) var problem: String?
    var selected = 0
    @ObservationIgnored weak var textView: NSTextView?
    @ObservationIgnored private let load: () async -> Result<[SlashCommand], TerminalProblem>
    @ObservationIgnored private var askedAt: Date?
    /// What opens the list: "/", and "$" for an agent whose skills take it.
    @ObservationIgnored private let triggers: Set<Character>
    /// What opened it.
    private(set) var prefix: Character = "/"

    static let shown = 9

    init(triggers: Set<Character> = ["/"], load: @escaping () async -> Result<[SlashCommand], TerminalProblem>) {
        self.triggers = triggers
        self.load = load
    }

    var isOpen: Bool { query != nil && (!results.isEmpty || problem != nil) }

    /// Follows the box: open while all it holds is `/` (or a `$` it takes)
    /// and a name being typed.
    func update(text: String) {
        guard let first = text.first, triggers.contains(first), !text.contains(where: \.isWhitespace) else { return dismiss() }
        prefix = first
        query = String(text.dropFirst())
        refresh()
        // Asked for when first needed, and again after a minute, as skills
        // come and go.
        if askedAt.map({ Date().timeIntervalSince($0) > 60 }) ?? true {
            askedAt = Date()
            Task {
                switch await load() {
                case .success(let list):
                    commands = list
                    problem = nil
                case .failure(let failure):
                    askedAt = nil
                    problem = failure.message.contains("unsupported request")
                        ? "Update agentman on your Mac to use commands here."
                        : failure.message
                }
                refresh()
            }
        }
    }

    func dismiss() {
        query = nil
        results = []
        selected = 0
    }

    func move(_ by: Int) {
        guard !results.isEmpty else { return }
        selected = (selected + by + results.count) % results.count
    }

    /// Puts the highlighted command, or the one clicked, in the box.
    func choose(_ index: Int? = nil) {
        let index = index ?? selected
        guard results.indices.contains(index), let textView else { return dismiss() }
        let command = results[index]
        // A skill is followed by what it is for.
        let picked = command.label + (command.argumentHint?.isEmpty == false || command.prefix == "$" ? " " : "")
        let all = NSRange(location: 0, length: (textView.string as NSString).length)
        textView.insertText(picked, replacementRange: all)
        dismiss()
    }

    private func refresh() {
        guard let query else { return }
        results = Array(Self.match(commands, query, prefix: prefix).prefix(60))
        selected = min(selected, max(results.count - 1, 0))
    }

    /// Best first: the name itself, then names that start with what is typed,
    /// then a word within a name that does ("deploy" finds vercel:deploy),
    /// then any that contain it. Built-in commands lead within each.
    /// "$" lists only what takes it: Codex's skills.
    static func match(_ commands: [SlashCommand], _ query: String, prefix: Character = "/") -> [SlashCommand] {
        func squash(_ text: String) -> String {
            text.lowercased().filter { !":_- ".contains($0) }
        }
        let typed = query.lowercased()
        let squashed = squash(query)
        func rank(_ command: SlashCommand) -> Int? {
            let name = command.name.lowercased()
            if typed.isEmpty { return 3 }
            if name == typed { return 0 }
            if name.hasPrefix(typed) || squash(name).hasPrefix(squashed) { return 1 }
            if name.split(whereSeparator: { ":_-".contains($0) }).contains(where: { $0.hasPrefix(typed) }) { return 2 }
            if !squashed.isEmpty, squash(name).contains(squashed) { return 3 }
            return nil
        }
        return commands
            .filter { prefix == "/" || $0.prefix == String(prefix) }
            .compactMap { command in rank(command).map { (command, $0) } }
            .sorted { a, b in
                if a.1 != b.1 { return a.1 < b.1 }
                let builtinA = a.0.source == "builtin", builtinB = b.0.source == "builtin"
                if builtinA != builtinB { return builtinA }
                return a.0.name < b.0.name
            }
            .map(\.0)
    }

    /// What sending a message does, when it is a command: a panel command
    /// sent bare is read off the agent's terminal, and a bare /model would
    /// open a picker there.
    enum Send: Equatable {
        case panel(String)
        case model
        case message
    }

    static func send(_ text: String, commands: [SlashCommand]) -> Send {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed.hasPrefix("/"), trimmed.count > 1, !trimmed.contains(where: \.isWhitespace) else { return .message }
        let name = String(trimmed.dropFirst())
        if commands.first(where: { $0.name == name })?.panel == true { return .panel(name) }
        if name == "model" { return .model }
        return .message
    }

    /// Commands are typed into the agent's prompt, so only a Claude or Codex
    /// the Mac started in a terminal it can type into takes them.
    static func takes(_ session: Session?) -> Bool {
        (session?.kind == "claude" || session?.kind == "codex") && session?.inject == "tmux"
    }

    /// What opens a session's list: Codex's skills are typed with "$".
    static func triggers(for session: Session) -> Set<Character> {
        session.kind == "codex" ? ["/", "$"] : ["/"]
    }
}

/// The commands that match what follows `/`, over the message box: the
/// highlighted one is what Return or Tab picks.
struct SlashList: View {
    let slash: SlashCommands

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            if let problem = slash.problem, slash.results.isEmpty {
                Text(problem)
                    .font(Fonts.sans(12.5))
                    .foregroundStyle(Palette.muted)
                    .padding(8)
            } else {
                ScrollViewReader { proxy in
                    ScrollView {
                        VStack(alignment: .leading, spacing: 1) {
                            ForEach(Array(slash.results.enumerated()), id: \.element.label) { index, command in
                                SlashRow(command: command, selected: index == slash.selected)
                                    .id(command.label)
                                    .contentShape(Rectangle())
                                    .onTapGesture { slash.choose(index) }
                                    .onHover { if $0 { slash.selected = index } }
                            }
                        }
                    }
                    .frame(maxHeight: CGFloat(SlashCommands.shown) * 39)
                    .onChange(of: slash.selected) { _, index in
                        if slash.results.indices.contains(index) { proxy.scrollTo(slash.results[index].label) }
                    }
                }
            }
        }
        .padding(5)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Palette.surface, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(Palette.line))
        .shadow(color: .black.opacity(0.12), radius: 14, y: 4)
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Commands")
    }
}

private struct SlashRow: View {
    let command: SlashCommand
    let selected: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            HStack(spacing: 6) {
                Text(command.label)
                    .font(Fonts.mono(12.5, selected ? .medium : .regular))
                    .foregroundStyle(Palette.text)
                    .lineLimit(1)
                    .layoutPriority(1)
                if let hint = command.argumentHint, !hint.isEmpty {
                    Text(hint)
                        .font(Fonts.mono(11.5))
                        .foregroundStyle(Palette.faint)
                        .lineLimit(1)
                }
                Spacer(minLength: 0)
                if selected {
                    Image(systemName: "return")
                        .font(.system(size: 9.5, weight: .semibold))
                        .foregroundStyle(Palette.faint)
                }
            }
            if let description = command.description, !description.isEmpty {
                Text(description)
                    .font(Fonts.sans(11.5))
                    .foregroundStyle(Palette.muted)
                    .lineLimit(1)
            }
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 4)
        .frame(minHeight: 38, alignment: .leading)
        .background(selected ? Palette.fill : .clear, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

/// What a panel command showed, in a sheet: the agent's terminal panel as
/// text, with its own lines kept.
struct CommandPanelSheet: View {
    let panel: CommandPanel
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text(panel.command)
                    .font(Fonts.mono(13, .medium))
                Spacer()
                Button("Done") { dismiss() }
                    .keyboardShortcut(.defaultAction)
            }
            .padding(.horizontal, 18)
            .padding(.vertical, 12)
            Divider()
            ScrollView([.vertical, .horizontal]) {
                Text(panel.text)
                    .font(TextStyle.code)
                    .foregroundStyle(Palette.text)
                    .textSelection(.enabled)
                    .fixedSize()
                    .padding(18)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .frame(minWidth: 560, idealWidth: 680, minHeight: 360, idealHeight: 560)
        .background(Palette.surface)
    }
}

/// A shown panel, for `.sheet(item:)`.
struct PanelItem: Identifiable {
    let panel: CommandPanel
    var id: String { panel.command + "\n" + panel.text }
}

extension SlashCommand {
    /// As the menu and the box write it: "/compact", "$imagegen".
    var label: String { (prefix ?? "/") + name }
}
