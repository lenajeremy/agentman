import SwiftUI

/// A note Agentman suggests keeping: what it would save and why, with Save,
/// Edit and Dismiss. Nothing is kept until the user saves it.
struct NoteSuggestionCard: View {
    @Environment(AgentsStore.self) private var store
    let card: ManagerCard
    /// "1 of 3" when more are waiting.
    var position: String?
    @State private var editing = false
    @State private var title = ""
    @State private var text = ""
    @State private var tier = "shared"
    @State private var always = false
    @State private var busy = false
    @State private var problem: String?

    private var note: Note? { card.note }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(spacing: 8) {
                Image(systemName: "bookmark")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(Palette.working)
                Text("Agentman suggests a note")
                    .font(TextStyle.captionMedium)
                    .foregroundStyle(Palette.textSecondary)
                Spacer(minLength: 8)
                if let position {
                    Text(position)
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.faint)
                }
                Button {
                    act { await store.manager.dismiss(card) }
                } label: {
                    Image(systemName: "xmark")
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundStyle(Palette.muted)
                        .frame(width: 22, height: 22)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help("Dismiss: don't keep it")
                .disabled(busy)
            }

            if editing {
                editor
            } else {
                reading
            }

            if let problem {
                Text(problem)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.errorText)
            }

            HStack(spacing: 10) {
                Spacer()
                if editing {
                    Button("Cancel") { editing = false }
                        .disabled(busy)
                    Button("Save note") { save(edited: true) }
                        .buttonStyle(.borderedProminent)
                        .keyboardShortcut(.defaultAction)
                        .disabled(busy || title.trimmingCharacters(in: .whitespaces).isEmpty
                            || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                } else {
                    Button("Dismiss") { act { await store.manager.dismiss(card) } }
                        .disabled(busy)
                    Button("Edit") { beginEditing() }
                        .disabled(busy)
                    Button("Save note") { save(edited: false) }
                        .buttonStyle(.borderedProminent)
                        .disabled(busy)
                }
            }
            .controlSize(.regular)
        }
        .padding(18)
        .background(Palette.surface, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 16, style: .continuous).strokeBorder(Palette.line))
        .onAppear {
            // For screenshots: the suggestion opened for editing.
            if ProcessInfo.processInfo.environment["AGENTMAN_EDIT_NOTE"] == "1", !editing { beginEditing() }
        }
    }

    private var reading: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(note?.title ?? card.title)
                .font(TextStyle.bodyMedium)
                .foregroundStyle(Palette.text)
                .fixedSize(horizontal: false, vertical: true)
            Text(Self.inline(note?.body ?? card.detail ?? ""))
                .font(TextStyle.body)
                .foregroundStyle(Palette.textSecondary)
                .lineSpacing(3)
                .lineLimit(8)
                .fixedSize(horizontal: false, vertical: true)
                .textSelection(.enabled)
            if let why = card.reason, !why.isEmpty {
                Text(why)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack(spacing: 6) {
                Image(systemName: note?.tier == "private" ? "lock" : "person.2")
                    .font(.system(size: 10, weight: .medium))
                Text(note?.tier == "private" ? "Only Agentman reads it" : "Shared with your agents")
                if note?.always == true {
                    Text("· In every project's instructions")
                }
            }
            .font(TextStyle.caption)
            .foregroundStyle(Palette.faint)
            .padding(.top, 2)
        }
    }

    private var editor: some View {
        VStack(alignment: .leading, spacing: 10) {
            TextField("Title", text: $title)
                .textFieldStyle(.roundedBorder)
                .font(TextStyle.bodyMedium)
            TextEditor(text: $text)
                .font(TextStyle.body)
                .scrollContentBackground(.hidden)
                .padding(6)
                .frame(minHeight: 90, maxHeight: 160)
                .background(Palette.paper, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(Palette.line))
            Picker("", selection: $tier) {
                Text("Shared with your agents").tag("shared")
                Text("Only Agentman").tag("private")
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            Toggle("Put it in every project's instructions", isOn: $always)
                .toggleStyle(.checkbox)
                .font(TextStyle.caption)
                .disabled(tier == "private")
        }
    }

    /// A note's Markdown as it reads: bold, italics and code, not their
    /// marks.
    static func inline(_ markdown: String) -> AttributedString {
        (try? AttributedString(markdown: markdown, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)))
            ?? AttributedString(markdown)
    }

    private func beginEditing() {
        title = note?.title ?? card.title
        text = note?.body ?? card.detail ?? ""
        tier = note?.tier ?? "shared"
        always = note?.always ?? false
        problem = nil
        editing = true
    }

    private func save(edited: Bool) {
        let changed: Note? = edited ? Note(
            name: note?.name ?? "", tier: tier,
            title: title.trimmingCharacters(in: .whitespacesAndNewlines),
            always: tier == "private" ? false : always,
            body: text.trimmingCharacters(in: .whitespacesAndNewlines), updatedAt: 0
        ) : nil
        act { await store.manager.saveNote(card, as: changed) }
    }

    private func act(_ work: @escaping () async -> String?) {
        busy = true
        problem = nil
        Task {
            problem = await work()
            busy = false
        }
    }
}

/// A suggested note once the user decided: kept, or not.
struct NoteReceipt: View {
    let card: ManagerCard

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: card.state == "answered" ? "bookmark.fill" : "bookmark.slash")
                .font(.system(size: 10, weight: .semibold))
                .foregroundStyle(card.state == "answered" ? Palette.ok : Palette.faint)
            Text("Note “\(card.note?.title ?? card.title)” · \(card.outcome ?? (card.state == "answered" ? "Saved" : "Not saved"))")
                .lineLimit(1)
        }
        .font(TextStyle.caption)
        .foregroundStyle(Palette.muted)
        .padding(.leading, 34)
    }
}

/// The suggestion waiting longest, at the top right of whatever is on screen
/// (where the Mac shows its own notifications, clear of the message box), so
/// a note Agentman suggests is seen without opening its conversation. In
/// that conversation the card is shown where it was suggested instead.
struct NoteSuggestionPopup: View {
    @Environment(AgentsStore.self) private var store

    var body: some View {
        let waiting = store.manager.noteSuggestions
        if let card = waiting.first, store.selection != .manager {
            NoteSuggestionCard(card: card, position: waiting.count > 1 ? "1 of \(waiting.count)" : nil)
                .id(card.id)
                .frame(width: 400)
                .shadow(color: .black.opacity(0.18), radius: 24, y: 10)
                .padding(.trailing, 22)
                .padding(.top, 64)
                .transition(.move(edge: .trailing).combined(with: .opacity))
        }
    }
}
