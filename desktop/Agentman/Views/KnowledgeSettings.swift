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
        HSplitView {
            VStack(spacing: 0) {
                List(selection: $selection) {
                    ForEach(manager.notes) { note in
                        VStack(alignment: .leading, spacing: 2) {
                            Text(note.title)
                                .font(TextStyle.bodyMedium)
                                .lineLimit(1)
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
                        .padding(.vertical, 2)
                        .tag(note.id)
                    }
                }
                .overlay {
                    if manager.notes.isEmpty {
                        Text("No notes yet")
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.muted)
                    }
                }
                Divider()
                HStack {
                    Button {
                        selection = nil
                        draft = NoteDraft()
                    } label: {
                        Image(systemName: "plus")
                    }
                    .buttonStyle(.borderless)
                    .help("New note")
                    Spacer()
                }
                .padding(6)
            }
            .frame(minWidth: 180, idealWidth: 200, maxWidth: 260)

            editor
                .frame(minWidth: 320)
        }
        .task { await manager.loadNotes() }
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

    private var editor: some View {
        VStack(alignment: .leading, spacing: 10) {
            TextField("Title", text: $draft.title)
                .textFieldStyle(.plain)
                .font(TextStyle.title)
            HStack(spacing: 12) {
                Picker("", selection: $draft.tier) {
                    Text("Shared with agents").tag("shared")
                    Text("Private to Agentman").tag("private")
                }
                .labelsHidden()
                .fixedSize()
                if draft.tier == "shared" {
                    Toggle("Put in project instructions", isOn: $draft.always)
                        .help("Agentman writes it into CLAUDE.md or AGENTS.md when you ask it to update an agent's instructions.")
                }
                Spacer()
            }
            .font(TextStyle.caption)
            TextEditor(text: $draft.body)
                .font(TextStyle.code)
                .scrollContentBackground(.hidden)
                .padding(8)
                .background(Palette.fill, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            HStack {
                if let problem {
                    Text(problem)
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.errorText)
                }
                Spacer()
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
        }
        .padding(16)
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
