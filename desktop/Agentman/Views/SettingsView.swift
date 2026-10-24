import SwiftUI

/// Agentman's settings: how it works, what it may approve, how it talks, the
/// notes it shares with agents, and the keys it hands out. Saved in the
/// daemon, so the phone sees the same Agentman.
struct SettingsView: View {
    @Environment(AgentsStore.self) private var store
    @AppStorage("settingsTab") private var page = "general"
    @State private var draft: ManagerSettings?

    var body: some View {
        HStack(spacing: 0) {
            List(selection: Binding(get: { page }, set: { page = $0 ?? "general" })) {
                ForEach(SettingsPage.allCases) { item in
                    Label(item.title, systemImage: item.icon)
                        .font(TextStyle.body)
                        .tag(item.rawValue)
                }
            }
            .listStyle(.sidebar)
            .frame(width: 200)

            Divider()

            VStack(spacing: 0) {
                switch SettingsPage(rawValue: page) ?? .general {
                case .general:
                    SettingsPageView(title: "General") { GeneralPage(draft: $draft) }
                case .permissions:
                    SettingsPageView(title: "Permissions") { PermissionsPage(draft: $draft) }
                case .personality:
                    SettingsPageView(title: "Personality") { PersonalityPage(draft: $draft) }
                case .knowledge:
                    KnowledgeSettings()
                case .keys:
                    KeysSettings()
                }
                if let draft, draft != store.manager.settings,
                   [SettingsPage.general, .permissions, .personality].contains(SettingsPage(rawValue: page) ?? .general) {
                    SaveBar(draft: $draft)
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Palette.paper)
        }
        .frame(width: 800, height: 580)
        .toolbarBackgroundVisibility(.hidden, for: .windowToolbar)
        .onAppear { draft = store.manager.settings }
        .onChange(of: store.manager.settings) { _, settings in
            if draft == nil { draft = settings }
        }
    }
}

enum SettingsPage: String, CaseIterable, Identifiable {
    case general, permissions, personality, knowledge, keys

    var id: String { rawValue }

    var title: String {
        switch self {
        case .general: "General"
        case .permissions: "Permissions"
        case .personality: "Personality"
        case .knowledge: "Knowledge"
        case .keys: "Keys"
        }
    }

    var icon: String {
        switch self {
        case .general: "gearshape"
        case .permissions: "checkmark.shield"
        case .personality: "text.bubble"
        case .knowledge: "book"
        case .keys: "key"
        }
    }
}

/// A settings page: its title, then its sections, scrolling.
struct SettingsPageView<Content: View>: View {
    let title: String
    @ViewBuilder var content: Content

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 28) {
                Text(title)
                    .font(TextStyle.display)
                    .foregroundStyle(Palette.text)
                content
            }
            .padding(.horizontal, 32)
            .padding(.vertical, 28)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

/// A group of rows on one panel, with a heading above.
struct SettingsSection<Content: View>: View {
    var title: String?
    var footer: String?
    @ViewBuilder var content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let title {
                Text(title)
                    .font(TextStyle.captionMedium)
                    .foregroundStyle(Palette.muted)
                    .padding(.leading, 4)
            }
            VStack(spacing: 0) {
                content
            }
            .background(Palette.surface, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(Palette.line))
            if let footer {
                Text(footer)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.horizontal, 4)
            }
        }
    }
}

/// One setting: what it is and what it does on the left, its control on the
/// right.
struct SettingsRow<Control: View>: View {
    let title: String
    var detail: String?
    var divider = true
    @ViewBuilder var control: Control

    var body: some View {
        VStack(spacing: 0) {
            HStack(alignment: .center, spacing: 16) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(title)
                        .font(TextStyle.body)
                        .foregroundStyle(Palette.text)
                    if let detail {
                        Text(detail)
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.muted)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                Spacer(minLength: 12)
                control
            }
            .padding(.horizontal, 18)
            .padding(.vertical, 14)
            if divider {
                Divider().padding(.leading, 18)
            }
        }
    }
}

private enum Choices {
    static let models: [(id: String, name: String)] = [
        ("claude-opus-5-5", "Claude Opus 5.5"),
        ("claude-sonnet-5-5", "Claude Sonnet 5.5"),
        ("claude-haiku-5-5", "Claude Haiku 5.5"),
        ("claude-fable-5-1", "Claude Fable 5.1"),
    ]
    static let efforts = ["low", "medium", "high", "xhigh", "max"]
    static let defaultStyle = "Calm and concise. Lead with what matters, in as few words as it takes. " +
        "No preamble, no filler, no exclamation marks. Name agents by name. " +
        "When nothing needs the user, say so in one line."
}

/// The key Agentman runs on, and the models it uses.
private struct GeneralPage: View {
    @Environment(AgentsStore.self) private var store
    @Binding var draft: ManagerSettings?
    @State private var key = ""
    @State private var message: String?

    var body: some View {
        let keySet = store.manager.settings?.keySet == true
        SettingsSection(title: "Anthropic API key", footer: message ?? "Kept in your Mac's Keychain and only ever sent to Anthropic.") {
            SettingsRow(title: keySet ? "Saved in your Keychain" : "Not set yet",
                        detail: keySet ? "Paste a new key below to replace it." : "Agentman needs one to read your agents.") {
                if keySet {
                    Button("Remove", role: .destructive) { saveKey("") }
                }
            }
            SettingsRow(title: "Key", divider: false) {
                HStack(spacing: 8) {
                    SecureField("", text: $key, prompt: Text("sk-ant-…"))
                        .textFieldStyle(.roundedBorder)
                        .font(TextStyle.code)
                        .frame(width: 240)
                        .onSubmit { saveKey(key) }
                    Button("Save") { saveKey(key) }
                        .disabled(key.trimmingCharacters(in: .whitespaces).isEmpty)
                }
            }
        }

        if let binding = Binding($draft) {
            SettingsSection(title: "Models") {
                SettingsRow(title: "Conversation", detail: "Answers you and acts on your agents.") {
                    modelPicker(binding.chatModel)
                }
                SettingsRow(title: "Briefs", detail: "Summarises each agent after its turns.") {
                    modelPicker(binding.briefModel)
                }
                SettingsRow(title: "Effort", detail: "How hard the conversation model thinks.", divider: false) {
                    Picker("", selection: binding.effort) {
                        ForEach(Choices.efforts, id: \.self) { Text($0 == "xhigh" ? "Extra high" : $0.capitalized).tag($0) }
                    }
                    .labelsHidden()
                    .fixedSize()
                }
            }
            SettingsSection(title: "Briefs") {
                SettingsRow(title: "Brief me on each agent",
                            detail: "A short summary after an agent's turns, so you don't have to read them all.", divider: false) {
                    Toggle("", isOn: binding.briefs)
                        .toggleStyle(.switch)
                        .labelsHidden()
                }
            }
        }
    }

    private func modelPicker(_ selection: Binding<String>) -> some View {
        Picker("", selection: selection) {
            ForEach(Choices.models, id: \.id) { Text($0.name).tag($0.id) }
        }
        .labelsHidden()
        .fixedSize()
    }

    private func saveKey(_ value: String) {
        Task {
            if let problem = await store.manager.setKey(value.trimmingCharacters(in: .whitespaces)) {
                message = problem
            } else {
                key = ""
                message = value.isEmpty ? "Key removed." : "Key saved."
            }
        }
    }
}

/// What Agentman may approve without asking.
private struct PermissionsPage: View {
    @Binding var draft: ManagerSettings?

    var body: some View {
        if let binding = Binding($draft) {
            SettingsSection {
                SettingsRow(title: "Approve low-risk requests for me",
                            detail: "Agentman answers these the moment an agent asks, and tells you in a line.", divider: false) {
                    Toggle("", isOn: binding.autoApprove)
                        .toggleStyle(.switch)
                        .labelsHidden()
                }
            }
        }
        SettingsSection(title: "Approved for you") {
            ForEach(Array(Self.approved.enumerated()), id: \.offset) { index, item in
                RuleRow(icon: "checkmark.circle", tint: Palette.ok, text: item, divider: index < Self.approved.count - 1)
            }
        }
        SettingsSection(title: "Always waits for you", footer: "Anything on neither list waits for you too, as a card in Agentman.") {
            ForEach(Array(Self.asked.enumerated()), id: \.offset) { index, item in
                RuleRow(icon: "hand.raised", tint: Palette.needsYou, text: item, divider: index < Self.asked.count - 1)
            }
        }
    }

    static let approved = [
        "Reading and searching files",
        "Running tests, builds and linters",
        "“Continue?” prompts",
        "Changing files inside the agent's own folder",
    ]
    static let asked = [
        "Deleting files",
        "Installing software",
        "Commits, pushes and anything else that changes git history",
        "Deploys and cloud changes",
        "Anything on the network",
        "Keys, tokens and credentials",
    ]
}

private struct RuleRow: View {
    let icon: String
    let tint: Color
    let text: String
    let divider: Bool

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                Image(systemName: icon)
                    .font(.system(size: 12, weight: .medium))
                    .foregroundStyle(tint)
                    .frame(width: 16)
                Text(text)
                    .font(TextStyle.body)
                    .foregroundStyle(Palette.text)
                Spacer()
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
            if divider {
                Divider().padding(.leading, 42)
            }
        }
    }
}

/// How Agentman talks, and anything else it should know.
private struct PersonalityPage: View {
    @Binding var draft: ManagerSettings?

    var body: some View {
        if let binding = Binding($draft) {
            VStack(alignment: .trailing, spacing: 6) {
                SettingsSection(title: "How Agentman talks") {
                    editor(binding.style, height: 96)
                }
                Button("Use the default style") { draft?.style = Choices.defaultStyle }
                    .buttonStyle(.link)
                    .font(TextStyle.caption)
            }
            SettingsSection(title: "Anything else Agentman should know",
                            footer: "Both go into Agentman's prompt on every message. Notes for your agents belong in Knowledge.") {
                editor(binding.instructions, height: 150)
            }
        }
    }

    private func editor(_ text: Binding<String>, height: CGFloat) -> some View {
        TextEditor(text: text)
            .font(TextStyle.body)
            .scrollContentBackground(.hidden)
            .padding(10)
            .frame(height: height)
    }
}

/// Appears when a page has changes, to save or undo them.
private struct SaveBar: View {
    @Environment(AgentsStore.self) private var store
    @Binding var draft: ManagerSettings?
    @State private var problem: String?
    @State private var saving = false

    var body: some View {
        HStack(spacing: 10) {
            Text(problem ?? "Unsaved changes")
                .font(TextStyle.caption)
                .foregroundStyle(problem == nil ? Palette.muted : Palette.errorText)
            Spacer()
            Button("Revert") { draft = store.manager.settings }
            Button("Save") {
                guard let draft else { return }
                saving = true
                Task {
                    problem = await store.manager.save(draft)
                    saving = false
                }
            }
            .keyboardShortcut("s", modifiers: .command)
            .buttonStyle(.borderedProminent)
            .disabled(saving)
        }
        .padding(.horizontal, 20)
        .padding(.vertical, 12)
        .background(.bar)
        .overlay(alignment: .top) { Divider() }
    }
}
