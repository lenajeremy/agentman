import SwiftUI

/// Find in one conversation, at the top right of its thread as T3 Code has
/// it: the words, which match is shown of how many, and the way to the one
/// before and the next. The daemon finds them in the whole conversation,
/// not just what has been scrolled to.
struct Finding: Equatable {
    var query: String
    /// The query the matches are for, which trails the one typed.
    var searched: String?
    /// Every message that matched, oldest first.
    var matches: [SearchHit] = []
    /// The match shown.
    var index: Int?

    var current: String? { index.flatMap { matches.indices.contains($0) ? matches[$0].messageId : nil } }
    var marks: FindMarks { FindMarks(words: SearchWords.of(searched ?? ""), current: current) }
}

/// A message to scroll to; new each time, so the same one can be gone back to.
struct ScrollTarget: Equatable {
    var id: String
    var serial = UUID()
}

struct FindBar: View {
    @Binding var finding: Finding?
    let previous: () -> Void
    let next: () -> Void
    @FocusState private var focused: Bool

    var body: some View {
        if let finding {
            HStack(spacing: 6) {
                Image(systemName: "magnifyingglass")
                    .font(.system(size: 11))
                    .foregroundStyle(Palette.muted)
                TextField("Find in conversation", text: Binding(
                    get: { finding.query },
                    set: { self.finding?.query = $0 }
                ))
                .textFieldStyle(.plain)
                .font(TextStyle.body)
                .frame(width: 190)
                .focused($focused)
                .onSubmit(next)
                .onKeyPress(.return, phases: .down) { press in
                    guard press.modifiers.contains(.shift) else { return .ignored }
                    previous()
                    return .handled
                }
                .onExitCommand { self.finding = nil }
                Text(count(finding))
                    .font(Fonts.sans(12).monospacedDigit())
                    .foregroundStyle(missing(finding) ? Palette.errorText : Palette.muted)
                    .frame(minWidth: 36, alignment: .trailing)
                Group {
                    Button(action: previous) { Image(systemName: "chevron.up") }
                        .help("Previous match (⇧↩)")
                    Button(action: next) { Image(systemName: "chevron.down") }
                        .help("Next match (↩)")
                }
                .disabled(finding.matches.isEmpty)
                Button { self.finding = nil } label: { Image(systemName: "xmark") }
                    .help("Close (esc)")
            }
            .font(.system(size: 11, weight: .semibold))
            .foregroundStyle(Palette.muted)
            .buttonStyle(.plain)
            .fixedSize()
            .padding(.leading, 11)
            .padding(.trailing, 12)
            .padding(.vertical, 7)
            .background(Palette.surface, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Palette.line))
            .shadow(color: .black.opacity(0.08), radius: 8, y: 2)
            .onAppear { focused = true }
        }
    }

    /// "2/5", or "0/0" when nothing matched; a dash while the first answer
    /// is on its way.
    private func count(_ finding: Finding) -> String {
        guard finding.searched != nil else { return "–" }
        guard !finding.matches.isEmpty else { return "0/0" }
        return "\(finding.index.map { String($0 + 1) } ?? "–")/\(finding.matches.count)"
    }

    private func missing(_ finding: Finding) -> Bool {
        finding.searched != nil && finding.matches.isEmpty && AgentsStore.searchable(finding.query)
    }
}
