import AppKit
import SwiftUI

/// The API keys Agentman hands to agents: their names, what they are for,
/// and when Agentman may give one without asking. Values go into the
/// Keychain and are never shown again.
struct KeysSettings: View {
    @Environment(AgentsStore.self) private var store
    @State private var editing: KeyDraft?

    private var manager: ManagerStore { store.manager }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Agents ask for these with Agentman's tools, or Agentman gives one when you ask. A key goes into the project's .env, and only when git ignores that file.")
                .font(TextStyle.caption)
                .foregroundStyle(Palette.muted)
                .fixedSize(horizontal: false, vertical: true)

            List {
                ForEach(manager.keys) { key in
                    HStack(spacing: 10) {
                        Image(systemName: "key")
                            .foregroundStyle(key.set ? Palette.working : Palette.faint)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(key.name)
                                .font(TextStyle.code)
                            Text([key.description, Self.ruleText(key)].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · "))
                                .font(TextStyle.caption)
                                .foregroundStyle(Palette.muted)
                                .lineLimit(1)
                        }
                        Spacer()
                        if !key.set {
                            Text("No value")
                                .font(TextStyle.caption)
                                .foregroundStyle(Palette.needsYouText)
                        }
                        Button("Edit") { editing = KeyDraft(key) }
                            .buttonStyle(.borderless)
                    }
                    .padding(.vertical, 3)
                }
            }
            .overlay {
                if manager.keys.isEmpty {
                    Text("No keys yet")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                }
            }

            HStack {
                Spacer()
                Button("Add key") { editing = KeyDraft() }
                    .buttonStyle(.borderedProminent)
            }
        }
        .padding(16)
        .task { await manager.loadKeys() }
        .sheet(item: $editing) { draft in
            KeyEditor(draft: draft)
        }
    }

    static func ruleText(_ key: Key) -> String {
        switch key.rule {
        case "ask": "Asks you each time"
        case "projects":
            "Only " + (key.projects ?? []).map { ($0 as NSString).lastPathComponent }.joined(separator: ", ")
        default: "Given to any agent"
        }
    }
}

/// A key being added or edited.
private struct KeyDraft: Identifiable {
    var id = UUID()
    var name = ""
    var value = ""
    var description = ""
    var rule = "auto"
    var projects: [String] = []
    var original: Key?

    init() {}

    init(_ key: Key) {
        name = key.name
        description = key.description ?? ""
        rule = key.rule
        projects = key.projects ?? []
        original = key
    }

    var key: Key {
        Key(name: name, description: description, rule: rule, projects: rule == "projects" ? projects : nil,
            set: original?.set ?? false, updatedAt: 0)
    }
}

private struct KeyEditor: View {
    @Environment(AgentsStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @State var draft: KeyDraft
    @State private var problem: String?
    @State private var busy = false

    var body: some View {
        Form {
            TextField("Name", text: $draft.name, prompt: Text("ELEVENLABS_API_KEY"))
                .font(TextStyle.code)
                .disabled(draft.original != nil)
            SecureField("Value", text: $draft.value,
                        prompt: Text(draft.original?.set == true ? "Leave empty to keep the current one" : "Paste the key"))
                .font(TextStyle.code)
            TextField("What it's for", text: $draft.description, prompt: Text("Voices for the launch videos"))
            Picker("Give it", selection: $draft.rule) {
                Text("To any agent that asks").tag("auto")
                Text("Only to agents in these folders").tag("projects")
                Text("Ask me every time").tag("ask")
            }
            if draft.rule == "projects" {
                ForEach(draft.projects, id: \.self) { folder in
                    HStack {
                        Text(folder).font(TextStyle.caption).lineLimit(1).truncationMode(.middle)
                        Spacer()
                        Button {
                            draft.projects.removeAll { $0 == folder }
                        } label: {
                            Image(systemName: "minus.circle")
                        }
                        .buttonStyle(.borderless)
                    }
                }
                Button("Add a folder…", action: addFolder)
            }
            if let problem {
                Text(problem)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.errorText)
            }
        }
        .formStyle(.grouped)
        .frame(width: 460)
        .toolbar {
            if let original = draft.original {
                ToolbarItem(placement: .destructiveAction) {
                    Button("Delete", role: .destructive) {
                        act { await store.manager.delete(original) }
                    }
                }
            }
            ToolbarItem(placement: .cancellationAction) {
                Button("Cancel") { dismiss() }
            }
            ToolbarItem(placement: .confirmationAction) {
                Button("Save") {
                    act { await store.manager.save(draft.key, value: draft.value.trimmingCharacters(in: .whitespacesAndNewlines)) }
                }
                .disabled(busy || draft.name.isEmpty || (draft.original == nil && draft.value.isEmpty))
            }
        }
    }

    private func addFolder() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = true
        if panel.runModal() == .OK {
            for url in panel.urls where !draft.projects.contains(url.path) {
                draft.projects.append(url.path)
            }
        }
    }

    private func act(_ work: @escaping () async -> String?) {
        busy = true
        Task {
            problem = await work()
            busy = false
            if problem == nil { dismiss() }
        }
    }
}
