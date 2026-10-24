import SwiftUI

/// Agentman's settings: the key it runs on, the models it uses, and how it
/// talks. Saved in the daemon, so the phone sees the same Agentman.
struct SettingsView: View {
    @Environment(AgentsStore.self) private var store
    @State private var draft: ManagerSettings?
    @State private var key = ""
    @State private var message: String?
    @State private var problem: String?

    private static let models: [(id: String, name: String)] = [
        ("claude-opus-5-5", "Claude Opus 5.5"),
        ("claude-sonnet-5-5", "Claude Sonnet 5.5"),
        ("claude-haiku-5-5", "Claude Haiku 5.5"),
        ("claude-fable-5-1", "Claude Fable 5.1"),
    ]
    private static let efforts = ["low", "medium", "high", "xhigh", "max"]
    private static let defaultStyle = "Calm and concise. Lead with what matters, in as few words as it takes. " +
        "No preamble, no filler, no exclamation marks. Name agents by name. " +
        "When nothing needs the user, say so in one line."

    var body: some View {
        Form {
            Section("API key") {
                LabeledContent("Anthropic") {
                    Text(store.manager.settings?.keySet == true ? "Saved in your Keychain" : "Not set")
                        .foregroundStyle(store.manager.settings?.keySet == true ? Palette.ok : Palette.muted)
                }
                HStack {
                    SecureField("sk-ant-…", text: $key)
                        .font(TextStyle.code)
                    Button("Save") { saveKey(key) }
                        .disabled(key.trimmingCharacters(in: .whitespaces).isEmpty)
                    if store.manager.settings?.keySet == true {
                        Button("Remove", role: .destructive) { saveKey("") }
                    }
                }
                Link("Create a key in the Claude Console", destination: URL(string: "https://console.anthropic.com/settings/keys")!)
                    .font(TextStyle.caption)
            }

            if let binding = Binding($draft) {
                Section("Models") {
                    Picker("Chat", selection: binding.chatModel) {
                        ForEach(Self.models, id: \.id) { Text($0.name).tag($0.id) }
                    }
                    Picker("Briefs", selection: binding.briefModel) {
                        ForEach(Self.models, id: \.id) { Text($0.name).tag($0.id) }
                    }
                    Picker("Effort", selection: binding.effort) {
                        ForEach(Self.efforts, id: \.self) { Text($0.capitalized).tag($0) }
                    }
                    .pickerStyle(.segmented)
                    Toggle("Brief me on each agent after its turns", isOn: binding.briefs)
                }

                Section {
                    Toggle("Approve low-risk requests for me", isOn: binding.autoApprove)
                } header: {
                    Text("Permissions")
                } footer: {
                    Text("Reading and searching, tests and builds, \"continue?\", and changes to files in the agent's own folder. " +
                        "Deleting, installing, committing, pushing, deploying and anything on the network always wait for you, as a card.")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                }

                Section {
                    TextEditor(text: binding.style)
                        .font(TextStyle.body)
                        .frame(minHeight: 70)
                    Button("Use the default style") { draft?.style = Self.defaultStyle }
                        .buttonStyle(.link)
                } header: {
                    Text("How Agentman talks")
                }

                Section {
                    TextEditor(text: binding.instructions)
                        .font(TextStyle.body)
                        .frame(minHeight: 90)
                } header: {
                    Text("Anything else Agentman should know")
                } footer: {
                    Text("Both go into Agentman's prompt on every message.")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                }
            } else {
                Text(store.status == .connected ? "Loading…" : "Start am serve to change Agentman's settings.")
                    .foregroundStyle(Palette.muted)
            }

            HStack {
                if let problem {
                    Text(problem).foregroundStyle(Palette.errorText)
                } else if let message {
                    Text(message).foregroundStyle(Palette.muted)
                }
                Spacer()
                Button("Save") { save() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(draft == nil || draft == store.manager.settings)
            }
            .font(TextStyle.caption)
        }
        .formStyle(.grouped)
        .frame(width: 560, height: 640)
        .onAppear { draft = store.manager.settings }
        .onChange(of: store.manager.settings) { _, settings in
            if draft == nil { draft = settings }
        }
    }

    private func save() {
        guard let draft else { return }
        Task {
            problem = await store.manager.save(draft)
            message = problem == nil ? "Saved" : nil
        }
    }

    private func saveKey(_ value: String) {
        Task {
            problem = await store.manager.setKey(value.trimmingCharacters(in: .whitespaces))
            if problem == nil {
                key = ""
                message = value.isEmpty ? "Key removed" : "Key saved"
            }
        }
    }
}
