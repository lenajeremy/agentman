import SwiftUI

/// The user's notes for their agents: style guides, conventions, how they
/// like things done. Shared notes any agent can read through Agentman's
/// tools; private ones only Agentman reads.
struct KnowledgeSettings: View {
    @Environment(AgentsStore.self) private var store
    @State private var selection: Note.ID?
    @State private var draft = NoteDraft()
    @State private var problem: String?
    @State private var saving = false

    private var manager: ManagerStore { store.manager }

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Knowledge")
                        .font(TextStyle.display)
                        .foregroundStyle(Palette.text)
                    Text("Notes for your agents: how you like things done, facts about your projects. Shared notes any agent can read through Agentman; private ones only Agentman reads.")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer(minLength: 16)
                Button {
                    selection = nil
                    draft = NoteDraft()
                    problem = nil
                } label: {
                    Label("New note", systemImage: "plus")
                }
            }

            HStack(alignment: .top, spacing: 12) {
                ScrollView {
                    VStack(spacing: 2) {
                        ForEach(manager.notes) { note in
                            NoteRow(note: note, selected: selection == note.id) { selection = note.id }
                        }
                    }
                    .padding(6)
                }
                .frame(width: 190)
                .background(Palette.surface, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(Palette.line))
                .overlay {
                    if manager.notes.isEmpty {
                        VStack(spacing: 6) {
                            Image(systemName: "book")
                                .font(.system(size: 18))
                                .foregroundStyle(Palette.faint)
                            Text("No notes yet")
                                .font(TextStyle.captionMedium)
                                .foregroundStyle(Palette.textSecondary)
                            Text("Notes you write, or save from Agentman's suggestions, show here.")
                                .font(TextStyle.caption)
                                .foregroundStyle(Palette.muted)
                                .multilineTextAlignment(.center)
                        }
                        .padding(18)
                    }
                }

                editor
                    .background(Palette.surface, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(Palette.line))
            }
        }
        .padding(.horizontal, 28)
        .padding(.vertical, 26)
        .task {
            await manager.loadNotes()
            // For screenshots and tests: a note open at launch, by name.
            if let name = ProcessInfo.processInfo.environment["AGENTMAN_NOTE"] {
                selection = manager.notes.first(where: { $0.name == name })?.id
            }
        }
        .onChange(of: selection) { _, id in
            guard let id, let note = manager.notes.first(where: { $0.id == id }) else { return }
            Task {
                if let full = await manager.note(note.name, tier: note.tier) {
                    draft = NoteDraft(full)
                    problem = nil
                }
            }
        }
    }

    /// The note: its title, who reads it, then its words on the card itself,
    /// the way a notes app has them.
    private var editor: some View {
        VStack(alignment: .leading, spacing: 0) {
            TextField("Title", text: $draft.title, axis: .vertical)
                .textFieldStyle(.plain)
                .font(Fonts.sans(18, .medium))
                .lineLimit(1...3)
                .padding(.bottom, 10)
            HStack(spacing: 10) {
                Menu {
                    Picker("Who reads it", selection: $draft.tier) {
                        Label("Shared with agents", systemImage: "person.2").tag("shared")
                        Label("Private to Agentman", systemImage: "lock").tag("private")
                    }
                    .pickerStyle(.inline)
                } label: {
                    HStack(spacing: 5) {
                        Image(systemName: draft.tier == "shared" ? "person.2" : "lock")
                            .font(.system(size: 10, weight: .medium))
                        Text(draft.tier == "shared" ? "Shared" : "Private")
                        Image(systemName: "chevron.down")
                            .font(.system(size: 7, weight: .bold))
                            .foregroundStyle(Palette.faint)
                    }
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.textSecondary)
                    .padding(.horizontal, 9)
                    .frame(height: 24)
                    .background(Palette.fill, in: Capsule())
                }
                .menuStyle(.button)
                .buttonStyle(.plain)
                .menuIndicator(.hidden)
                .fixedSize()
                .help(draft.tier == "shared"
                    ? "Any agent can read it through Agentman" : "Only Agentman reads it; agents get what it tells them")
                if draft.tier == "shared" {
                    Toggle("In project instructions", isOn: $draft.always)
                        .toggleStyle(.checkbox)
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.textSecondary)
                        .help("Agentman writes it into CLAUDE.md or AGENTS.md when you ask it to update an agent's instructions.")
                }
                Spacer(minLength: 0)
            }
            .padding(.bottom, 12)
            Divider()
            TextEditor(text: $draft.body)
                .font(TextStyle.body)
                .scrollContentBackground(.hidden)
                .padding(.horizontal, -5)
                .padding(.top, 10)
                .overlay(alignment: .topLeading) {
                    if draft.body.isEmpty {
                        Text("Write the note: how you like things done, or what agents should know.")
                            .font(TextStyle.body)
                            .foregroundStyle(Palette.faint)
                            .padding(.top, 10)
                            .allowsHitTesting(false)
                    }
                }
            HStack(spacing: 10) {
                if let problem {
                    Text(problem)
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.errorText)
                        .lineLimit(2)
                }
                Spacer(minLength: 0)
                if let original = draft.original {
                    Button("Delete", role: .destructive) {
                        Task {
                            problem = await manager.delete(original)
                            if problem == nil {
                                selection = nil
                                draft = NoteDraft()
                            }
                        }
                    }
                }
                Button(draft.original == nil ? "Add note" : "Save") { save() }
                    .keyboardShortcut("s", modifiers: .command)
                    .buttonStyle(.borderedProminent)
                    .disabled(saving || draft.title.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            .padding(.top, 10)
        }
        .padding(18)
    }

    private func save() {
        saving = true
        Task {
            switch await manager.save(draft.note) {
            case .success(let saved):
                draft = NoteDraft(saved)
                selection = saved.id
                problem = nil
            case .failure(let failure):
                problem = failure.message
            }
            saving = false
        }
    }
}

/// A note in the list: its title, and who can read it.
private struct NoteRow: View {
    let note: Note
    let selected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(alignment: .leading, spacing: 2) {
                Text(note.title)
                    .font(Fonts.sans(13, .medium))
                    .foregroundStyle(Palette.text)
                    .lineLimit(2)
                    .multilineTextAlignment(.leading)
                HStack(spacing: 4) {
                    Image(systemName: note.tier == "shared" ? "person.2" : "lock")
                    Text(note.tier == "shared" ? "Shared" : "Private")
                    if note.always == true {
                        Text("· In instructions")
                    }
                }
                .font(TextStyle.caption)
                .foregroundStyle(Palette.muted)
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .fill(Color.primary.opacity(selected ? 0.08 : 0))
            )
            .overlay(
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .strokeBorder(Color.primary.opacity(selected ? 0.08 : 0))
            )
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

/// A note being edited.
private struct NoteDraft {
    var title = ""
    var tier = "shared"
    var always = false
    var body = ""
    /// The note as saved, when this edits one.
    var original: Note?

    init() {}

    init(_ note: Note) {
        title = note.title
        tier = note.tier
        always = note.always ?? false
        body = note.body ?? ""
        original = note
    }

    var note: Note {
        Note(name: original?.name ?? "", tier: tier, title: title, always: tier == "shared" && always,
             body: body, updatedAt: 0)
    }
}
