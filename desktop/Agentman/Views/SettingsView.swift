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
            // The pages, with the same quiet highlight as the main window's
            // sidebar rather than the accent-coloured one a List draws.
            VStack(spacing: 2) {
                ForEach(SettingsPage.allCases) { item in
                    SettingsTab(page: item, selected: page == item.rawValue) { page = item.rawValue }
                }
                Spacer(minLength: 0)
            }
            .padding(.horizontal, 10)
            .padding(.top, 8)
            .frame(width: 200)
            .background(SidebarMaterial().ignoresSafeArea())

            Divider()

            VStack(spacing: 0) {
                switch SettingsPage(rawValue: page) ?? .general {
                case .general:
                    SettingsPageView(title: "General") { GeneralPage(draft: $draft) }
                case .permissions:
                    SettingsPageView(title: "Permissions") { PermissionsPage(draft: $draft) }
                case .personality:
                    SettingsPageView(title: "Personality") { PersonalityPage(draft: $draft) }
                case .notifications:
                    SettingsPageView(title: "Notifications") { NotificationsPage() }
                case .knowledge:
                    KnowledgeSettings()
                case .keys:
                    KeysSettings()
                case .languages:
                    LanguagesSettings()
                }
                if let draft, draft.editable != store.manager.settings?.editable,
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

/// One page in the settings' sidebar.
private struct SettingsTab: View {
    let page: SettingsPage
    let selected: Bool
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 10) {
                Image(systemName: page.icon)
                    .font(.system(size: 13, weight: .medium))
                    .foregroundStyle(selected ? Palette.text : Palette.muted)
                    .frame(width: 18)
                Text(page.title)
                    .font(TextStyle.body)
                    .foregroundStyle(Palette.text)
                Spacer(minLength: 0)
            }
            .padding(.horizontal, 10)
            .frame(height: 32)
            .background(
                RoundedRectangle(cornerRadius: 9, style: .continuous)
                    .fill(Color.primary.opacity(selected ? 0.10 : hovering ? 0.05 : 0))
            )
            .overlay(
                RoundedRectangle(cornerRadius: 9, style: .continuous)
                    .strokeBorder(Color.primary.opacity(selected ? 0.10 : 0))
            )
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

enum SettingsPage: String, CaseIterable, Identifiable {
    case general, notifications, permissions, personality, knowledge, keys, languages

    var id: String { rawValue }

    var title: String {
        switch self {
        case .general: "General"
        case .notifications: "Notifications"
        case .permissions: "Permissions"
        case .personality: "Personality"
        case .knowledge: "Knowledge"
        case .keys: "Keys"
        case .languages: "Languages"
        }
    }

    var icon: String {
        switch self {
        case .general: "gearshape"
        case .notifications: "bell.badge"
        case .permissions: "checkmark.shield"
        case .personality: "text.bubble"
        case .knowledge: "book"
        case .keys: "key"
        case .languages: "curlybraces"
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
        ("gemini-4-argon", "Gemini 4 Argon"),
        ("gemini-3.8-flash", "Gemini 3.8 Flash"),
        ("gemini-3.1-pro-preview", "Gemini 3.1 Pro (preview)"),
        ("gemini-3.5-flash-lite", "Gemini 3.5 Flash-Lite"),
    ]
    static let efforts = ["low", "medium", "high", "xhigh", "max"]
    static let defaultStyle = "Calm and concise. Lead with what matters, in as few words as it takes. " +
        "No preamble, no filler, no exclamation marks. Name agents by name. " +
        "When nothing needs the user, say so in one line."
}

/// The keys Agentman runs on, and the models it uses.
private struct GeneralPage: View {
    @Environment(AgentsStore.self) private var store
    @Binding var draft: ManagerSettings?

    var body: some View {
        let settings = store.manager.settings
        KeySection(provider: "anthropic", saved: settings?.anthropicKeySet ?? settings?.keySet == true)
        KeySection(provider: "gemini", saved: settings?.geminiKeySet == true)

        AppearanceSection()
        EditorSection()

        if let binding = Binding($draft) {
            SettingsSection(title: "Models", footer: missingKeyNote(binding.wrappedValue)) {
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
        about
    }

    @ViewBuilder private var about: some View {
        AboutSection()
    }

    /// A model on an engine without its key cannot run; say so under the
    /// pickers rather than when the first reply fails.
    private func missingKeyNote(_ draft: ManagerSettings) -> String? {
        let settings = store.manager.settings
        let keys = ["anthropic": settings?.anthropicKeySet ?? settings?.keySet == true,
                    "gemini": settings?.geminiKeySet == true]
        let missing = Set([draft.chatModel, draft.briefModel].map(ManagerSettings.provider(of:))).filter { keys[$0] != true }
        guard let engine = missing.sorted().first else { return nil }
        return "Add your \(engine == "gemini" ? "Gemini" : "Anthropic") API key above to use these models."
    }

    private func modelPicker(_ selection: Binding<String>) -> some View {
        Picker("", selection: selection) {
            Section("Claude") {
                ForEach(Choices.models.filter { !$0.id.hasPrefix("gemini-") }, id: \.id) { Text($0.name).tag($0.id) }
            }
            Section("Gemini") {
                ForEach(Choices.models.filter { $0.id.hasPrefix("gemini-") }, id: \.id) { Text($0.name).tag($0.id) }
            }
        }
        .labelsHidden()
        .fixedSize()
    }
}

/// One engine's API key: whether it is saved, and a field to save one.
private struct KeySection: View {
    @Environment(AgentsStore.self) private var store
    let provider: String
    let saved: Bool
    @State private var key = ""
    @State private var message: String?

    private var gemini: Bool { provider == "gemini" }

    var body: some View {
        SettingsSection(title: gemini ? "Gemini API key" : "Anthropic API key",
                        footer: message ?? "Kept in your Mac's Keychain and only ever sent to \(gemini ? "Google" : "Anthropic").") {
            SettingsRow(title: saved ? "Saved in your Keychain" : "Not set yet",
                        detail: saved ? "Paste a new key below to replace it."
                            : gemini ? "For running Agentman on Gemini models." : "For running Agentman on Claude models.") {
                if saved {
                    Button("Remove", role: .destructive) { save("") }
                }
            }
            SettingsRow(title: "Key", divider: false) {
                HStack(spacing: 8) {
                    SecureField("", text: $key, prompt: Text(gemini ? "Gemini API key" : "sk-ant-…"))
                        .textFieldStyle(.roundedBorder)
                        .font(TextStyle.code)
                        .frame(width: 240)
                        .onSubmit { save(key) }
                    Button("Save") { save(key) }
                        .disabled(key.trimmingCharacters(in: .whitespaces).isEmpty)
                }
            }
        }
    }

    private func save(_ value: String) {
        Task {
            if let problem = await store.manager.setKey(value.trimmingCharacters(in: .whitespaces), provider: provider) {
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
    @State private var adding = false

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
            SettingsSection(title: "Agentman may approve", footer: "Inside the agent's own folder, and never anything that touches keys or credentials.") {
                kindRow("Reading and searching files", \.reads)
                kindRow("Changing files", \.edits)
                kindRow("Tests, builds and read-only commands", \.commands)
                kindRow("“Continue?” prompts", \.continue, divider: false)
            }
            .disabled(!binding.wrappedValue.autoApprove)
            .opacity(binding.wrappedValue.autoApprove ? 1 : 0.5)

            SettingsSection(title: "Your rules",
                            footer: "An “Always ask” rule wins over everything. No rule lets an agent touch keys and credentials or run anything as root. Use * for anything and ? for one character.") {
                let rules = binding.wrappedValue.rules ?? []
                if rules.isEmpty {
                    HStack {
                        Text("No rules yet. Allow something Agentman would ask you about, or make it always ask.")
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.muted)
                        Spacer()
                    }
                    .padding(.horizontal, 16)
                    .padding(.vertical, 12)
                    Divider()
                }
                ForEach(Array(rules.enumerated()), id: \.offset) { index, rule in
                    PermissionRuleRow(rule: rule) {
                        draft?.rules?.remove(at: index)
                    }
                }
                HStack {
                    Button {
                        adding = true
                    } label: {
                        Label("Add Rule…", systemImage: "plus")
                    }
                    .buttonStyle(.link)
                    Spacer()
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 10)
            }
            .sheet(isPresented: $adding) {
                NewRuleSheet { rule in
                    draft?.rules = (draft?.rules ?? []) + [rule]
                }
            }
        }

        TryPermission()

        SettingsSection(title: "Always waits for you", footer: "Unless one of your rules allows it. Anything Agentman doesn't recognise waits for you too, as a card.") {
            ForEach(Array(Self.asked.enumerated()), id: \.offset) { index, item in
                RuleRow(icon: "hand.raised", tint: Palette.needsYou, text: item, divider: index < Self.asked.count - 1)
            }
        }
    }

    private func kindRow(_ title: String, _ key: WritableKeyPath<ApproveKinds, Bool>, divider: Bool = true) -> some View {
        SettingsRow(title: title, divider: divider) {
            Toggle("", isOn: Binding(
                get: { (draft?.approve ?? .all)[keyPath: key] },
                set: { on in
                    var kinds = draft?.approve ?? .all
                    kinds[keyPath: key] = on
                    draft?.approve = kinds
                }
            ))
            .toggleStyle(.switch)
            .labelsHidden()
        }
    }

    static let asked = [
        "Deleting files",
        "Installing software",
        "Commits, pushes and anything else that changes git history",
        "Deploys and cloud changes",
        "Anything on the network",
        "Keys, tokens and credentials (no rule can allow these)",
        "Anything run as root (no rule can allow these)",
    ]
}

/// A rule as a line: what it does, to what, and where.
private struct PermissionRuleRow: View {
    let rule: PermissionRule
    let remove: () -> Void
    @State private var hovering = false

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                Text(rule.action == "allow" ? "Allow" : "Always ask")
                    .font(TextStyle.captionMedium)
                    .foregroundStyle(rule.action == "allow" ? Palette.ok : Palette.needsYouText)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 2)
                    .background(rule.action == "allow" ? Palette.ok.opacity(0.12) : Palette.needsYouWash, in: Capsule())
                Image(systemName: RuleKind.icon(rule.kind))
                    .font(.system(size: 11))
                    .foregroundStyle(Palette.muted)
                    .help(RuleKind.title(rule.kind))
                Text(rule.pattern)
                    .font(Fonts.mono(12))
                    .foregroundStyle(Palette.text)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .textSelection(.enabled)
                Text(rule.folder.map { "in " + ($0 as NSString).lastPathComponent } ?? "everywhere")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.faint)
                    .help(rule.folder ?? "Every agent")
                Spacer()
                Button(action: remove) {
                    Image(systemName: "xmark")
                        .font(.system(size: 9, weight: .bold))
                        .foregroundStyle(Palette.muted)
                        .frame(width: 20, height: 20)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .opacity(hovering ? 1 : 0)
                .help("Remove the rule")
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 9)
            .contentShape(Rectangle())
            .onHover { hovering = $0 }
            Divider()
        }
    }
}

enum RuleKind {
    static let all = ["command", "edit", "read", "network", "any"]

    static func title(_ kind: String) -> String {
        switch kind {
        case "command": "Commands"
        case "edit": "Changing files"
        case "read": "Reading files"
        case "network": "Network"
        default: "Anything"
        }
    }

    static func icon(_ kind: String) -> String {
        switch kind {
        case "command": "terminal"
        case "edit": "pencil"
        case "read": "doc.text.magnifyingglass"
        case "network": "globe"
        default: "asterisk"
        }
    }

    static func placeholder(_ kind: String) -> String {
        switch kind {
        case "command": "npm run *"
        case "edit", "read": "docs/*"
        case "network": "https://api.example.com/*"
        default: "*.env"
        }
    }
}

/// Adding a rule: allow or always ask, for what, matching what, and where.
private struct NewRuleSheet: View {
    let add: (PermissionRule) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var action = "allow"
    @State private var kind = "command"
    @State private var pattern = ""
    @State private var folder: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text("New Rule")
                .font(TextStyle.title)
                .foregroundStyle(Palette.text)
            Picker("", selection: $action) {
                Text("Allow").tag("allow")
                Text("Always ask").tag("ask")
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            VStack(alignment: .leading, spacing: 6) {
                Text("For")
                    .font(TextStyle.label)
                    .foregroundStyle(Palette.faint)
                Picker("", selection: $kind) {
                    ForEach(RuleKind.all, id: \.self) { Label(RuleKind.title($0), systemImage: RuleKind.icon($0)).tag($0) }
                }
                .labelsHidden()
                .fixedSize()
            }
            VStack(alignment: .leading, spacing: 6) {
                Text(kind == "command" ? "Command" : kind == "network" ? "Address" : kind == "any" ? "Matching" : "Path")
                    .font(TextStyle.label)
                    .foregroundStyle(Palette.faint)
                TextField(RuleKind.placeholder(kind), text: $pattern)
                    .textFieldStyle(.roundedBorder)
                    .font(Fonts.mono(12.5))
                Text(hint)
                    .font(TextStyle.caption)
                    .foregroundStyle(problem == nil ? Palette.muted : Palette.errorText)
            }
            VStack(alignment: .leading, spacing: 6) {
                Text("Where")
                    .font(TextStyle.label)
                    .foregroundStyle(Palette.faint)
                HStack {
                    Text(folder.map { ($0 as NSString).abbreviatingWithTildeInPath } ?? "Every agent, in any folder")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.textSecondary)
                        .lineLimit(1)
                        .truncationMode(.head)
                    Spacer()
                    if folder != nil {
                        Button("Everywhere") { folder = nil }
                    }
                    Button("Choose a Folder…", action: chooseFolder)
                }
            }
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Add Rule") {
                    add(PermissionRule(action: action, kind: kind, pattern: pattern.trimmingCharacters(in: .whitespaces), folder: folder))
                    dismiss()
                }
                .keyboardShortcut(.defaultAction)
                .disabled(problem != nil)
            }
        }
        .padding(24)
        .frame(width: 460)
    }

    private var problem: String? {
        let trimmed = pattern.trimmingCharacters(in: .whitespaces)
        if trimmed.isEmpty { return "" }
        if action == "allow" && trimmed.trimmingCharacters(in: CharacterSet(charactersIn: "*? ")).isEmpty {
            return "That would allow everything. Name something more specific."
        }
        return nil
    }

    private var hint: String {
        if let problem, !problem.isEmpty { return problem }
        switch kind {
        case "command": return "Checked against each part of a command: `npm run *` covers npm run build."
        case "edit", "read": return "A path inside the agent's folder, or a full one: `docs/*`, `*.md`."
        case "network": return "The address the agent wants to open."
        default: return "Any request whose command, path or address matches."
        }
    }

    private func chooseFolder() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.directoryURL = URL(fileURLWithPath: folder ?? NSHomeDirectory())
        panel.prompt = "Choose"
        if panel.runModal() == .OK, let url = panel.url { folder = url.path }
    }
}

/// "Try it": what Agentman would do with a command or path, under the
/// settings as saved.
private struct TryPermission: View {
    @Environment(AgentsStore.self) private var store
    @State private var kind = "command"
    @State private var target = ""
    @State private var folder = NSHomeDirectory()
    @State private var check: PermissionCheck?

    var body: some View {
        SettingsSection(title: "Try it", footer: "Uses your saved settings, so save changes first to try them.") {
            VStack(alignment: .leading, spacing: 10) {
                HStack(spacing: 8) {
                    Picker("", selection: $kind) {
                        ForEach(["command", "edit", "read", "network"], id: \.self) { Text(RuleKind.title($0)).tag($0) }
                    }
                    .labelsHidden()
                    .fixedSize()
                    TextField(kind == "command" ? "git push origin main" : kind == "network" ? "https://example.com" : "src/app.ts", text: $target)
                        .textFieldStyle(.roundedBorder)
                        .font(Fonts.mono(12))
                        .onSubmit(run)
                    Button("Check", action: run)
                        .disabled(target.trimmingCharacters(in: .whitespaces).isEmpty)
                }
                HStack(spacing: 6) {
                    Text("For an agent in")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                    Menu((folder as NSString).abbreviatingWithTildeInPath) {
                        ForEach(store.folders.prefix(12)) { recent in
                            Button((recent.path as NSString).abbreviatingWithTildeInPath) { folder = recent.path }
                        }
                    }
                    .menuStyle(.button)
                    .buttonStyle(.plain)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.textSecondary)
                    .fixedSize()
                }
                if let check {
                    Label {
                        Text(check.auto ? "Agentman would approve it: \(check.summary)." : "It would wait for you: \(check.reason ?? check.summary).")
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.text)
                    } icon: {
                        Image(systemName: check.auto ? "checkmark.circle.fill" : "hand.raised.fill")
                            .foregroundStyle(check.auto ? Palette.ok : Palette.needsYou)
                    }
                }
            }
            .padding(16)
        }
        .task { await store.loadFolders() }
    }

    private func run() {
        let target = target
        Task { check = await store.manager.checkPermission(kind: kind, target: target, folder: folder) }
    }
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

/// Light, dark, or as the Mac is set.
private struct AppearanceSection: View {
    @AppStorage("appearance") private var appearance = "system"

    var body: some View {
        SettingsSection(title: "Appearance") {
            SettingsRow(title: "Look", divider: false) {
                Picker("", selection: $appearance) {
                    Text("Match the Mac").tag("system")
                    Text("Light").tag("light")
                    Text("Dark").tag("dark")
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .fixedSize()
            }
        }
        .onChange(of: appearance) { _, now in Appearance.apply(now) }
    }
}

enum Appearance {
    @MainActor static func apply(_ choice: String) {
        switch choice {
        case "light": NSApp.appearance = NSAppearance(named: .aqua)
        case "dark": NSApp.appearance = NSAppearance(named: .darkAqua)
        default: NSApp.appearance = nil
        }
    }
}

/// Voice prompts: Celery writes out what the user says. Agentman signs in to
/// their Celery account as a device of its own, with a code Celery emails;
/// the sign-in stays in the Mac's Keychain and never reaches the app.
struct VoiceSection: View {
    @Environment(AgentsStore.self) private var store
    @State private var account: VoiceAccount?
    @State private var email = ""
    @State private var code = ""
    @State private var requestId: String?
    @State private var busy = false
    @State private var problem: String?

    var body: some View {
        SettingsSection(title: "Voice prompts",
                        footer: "Speak a message with the mic beside any message box, here or on your phone. Celery writes it out: the ums and false starts go, and nothing is reworded.") {
            if account?.signedIn == true {
                SettingsRow(title: "Celery", detail: signedInDetail, divider: false) {
                    Button("Sign Out") { act(.voiceSignOut, nil) }
                        .disabled(busy)
                }
            } else if let requestId {
                SettingsRow(title: "Code", detail: "Celery emailed a code to \(email).", divider: false) {
                    HStack(spacing: 8) {
                        TextField("Code", text: $code)
                            .textFieldStyle(.roundedBorder)
                            .frame(width: 100)
                            .onSubmit { verify(requestId) }
                        Button("Sign In") { verify(requestId) }
                            .disabled(code.trimmingCharacters(in: .whitespaces).isEmpty || busy)
                        Button("Back") {
                            self.requestId = nil
                            code = ""
                        }
                        .buttonStyle(.link)
                    }
                }
            } else {
                SettingsRow(title: "Celery", detail: "Sign in to your Celery account to speak your messages.", divider: false) {
                    HStack(spacing: 8) {
                        // Verbatim: as a localized key, the example address
                        // became a mailto link.
                        TextField("", text: $email, prompt: Text(verbatim: "you@example.com"))
                            .textFieldStyle(.roundedBorder)
                            .frame(width: 210)
                            .onSubmit(start)
                        Button("Send Code", action: start)
                            .disabled(!email.contains("@") || busy)
                    }
                }
            }
            if let problem {
                Text(problem)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.errorText)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 18)
                    .padding(.bottom, 12)
            }
        }
        .task {
            if case .success(let found) = await store.voiceAccount() { account = found }
        }
    }

    private var signedInDetail: String {
        let who = account?.email.map { "Signed in as \($0)" } ?? "Signed in"
        guard let plan = account?.plan, !plan.isEmpty else { return who }
        return who + " · " + plan.capitalized + " plan"
    }

    private func start() {
        guard email.contains("@") else { return }
        act(.voiceSignInStart, VoiceSignIn(email: email.trimmingCharacters(in: .whitespaces)))
    }

    private func verify(_ requestId: String) {
        act(.voiceSignInVerify, VoiceSignIn(requestId: requestId, code: code.trimmingCharacters(in: .whitespaces)))
    }

    private func act(_ type: RequestType, _ step: VoiceSignIn?) {
        busy = true
        problem = nil
        Task {
            switch await store.voiceAccount(type, step) {
            case .success(let found):
                if let id = found.requestId, type == .voiceSignInStart {
                    requestId = id
                } else {
                    account = found
                    requestId = nil
                    code = ""
                }
            case .failure(let failure):
                problem = failure.message
            }
            busy = false
        }
    }
}

/// How the code editor takes keys.
private struct EditorSection: View {
    @AppStorage("editorVim") private var vim = false

    var body: some View {
        SettingsSection(title: "Editor") {
            SettingsRow(title: "Vim mode",
                        detail: "Vim's keys in the code editor and its file tree. Press ? in the tree to see its keys.",
                        divider: false) {
                Toggle("", isOn: $vim).toggleStyle(.switch).labelsHidden()
            }
        }
    }
}

/// Which app and which am serve, and whether a newer one is out.
private struct AboutSection: View {
    @Environment(AgentsStore.self) private var store
    @State private var copied = false

    var body: some View {
        let app = Bundle.main.infoDictionary
        let version = (app?["CFBundleShortVersionString"] as? String) ?? "?"
        let build = (app?["CFBundleVersion"] as? String) ?? "?"
        SettingsSection(title: "About") {
            SettingsRow(title: "Agentman for Mac") {
                Text("\(version) (\(build))").font(TextStyle.caption).foregroundStyle(Palette.muted)
            }
            SettingsRow(title: "am serve", detail: updateDetail, divider: false) {
                HStack(spacing: 8) {
                    Text(store.daemonInfo?.version ?? "Not connected")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                    if let upgrade = store.daemonInfo?.upgrade, (store.daemonInfo?.behind ?? 0) > 0 {
                        Button(copied ? "Copied" : "Copy upgrade command") {
                            NSPasteboard.general.clearContents()
                            NSPasteboard.general.setString(upgrade, forType: .string)
                            copied = true
                        }
                    }
                }
            }
        }
    }

    private var updateDetail: String? {
        guard let info = store.daemonInfo else { return nil }
        if let latest = info.latest, (info.behind ?? 0) > 0 {
            return "\(latest) is out. Run \(info.upgrade ?? "the upgrade") in a terminal."
        }
        return info.latest == nil ? nil : "Up to date."
    }
}

/// When the Mac speaks up: which kinds, folders kept quiet, quiet hours,
/// and sound. The phone has its own, in its settings.
private struct NotificationsPage: View {
    @Environment(AgentsStore.self) private var store
    @AppStorage(NotificationPolicy.Key.finished) private var finished = true
    @AppStorage(NotificationPolicy.Key.needsYou) private var needsYou = true
    @AppStorage(NotificationPolicy.Key.agentman) private var agentman = true
    @AppStorage(NotificationPolicy.Key.notes) private var notes = true
    @AppStorage(NotificationPolicy.Key.sound) private var sound = true
    @AppStorage(NotificationPolicy.Key.quiet) private var quiet = false
    @AppStorage(NotificationPolicy.Key.quietStart) private var quietStart = NotificationPolicy.defaultQuietStart
    @AppStorage(NotificationPolicy.Key.quietEnd) private var quietEnd = NotificationPolicy.defaultQuietEnd
    @AppStorage(NotificationPolicy.Key.quietLetsNeedsYou) private var quietLetsNeedsYou = true
    @AppStorage(NotificationPolicy.Key.everyAgent) private var everyAgent = false
    @State private var muted: [String] = NotificationPolicy.mutedFolders()
    @AppStorage("awaySummary") private var awaySummary = true
    @AppStorage("awayMinutes") private var awayMinutes = 15.0

    var body: some View {
        SettingsSection(title: "Agentman") {
            toggle("Agentman's messages", "Its replies and briefs while you're elsewhere.", $agentman)
            toggle("Note suggestions", "Quietly, when it suggests something worth remembering.", $notes, divider: false)
        }
        SettingsSection(title: "Agents", footer: "Never for the thread you're looking at. While Agentman is on, it tells you what matters from your agents, so they only notify you themselves when you ask: here for all of them, or one at a time from an agent's menu in the sidebar.") {
            toggle("An agent finishes", "When it ends a turn, with what it said last.", $finished)
            toggle("An agent needs you", "When it waits on an approval or a question.", $needsYou)
            toggle("Every agent, while Agentman is on", "Hear from each agent itself too, not only through Agentman.", $everyAgent, divider: false)
        }
        SettingsSection(title: "Quiet hours", footer: "Nothing else comes through in quiet hours; it's all in Agentman when you look.") {
            toggle("Use quiet hours", nil, $quiet)
            SettingsRow(title: "From", divider: true) {
                HStack(spacing: 8) {
                    timePicker($quietStart)
                    Text("to").font(TextStyle.caption).foregroundStyle(Palette.muted)
                    timePicker($quietEnd)
                }
            }
            .disabled(!quiet)
            .opacity(quiet ? 1 : 0.5)
            toggle("Still tell me when an agent needs me", nil, $quietLetsNeedsYou, divider: false)
                .disabled(!quiet)
                .opacity(quiet ? 1 : 0.5)
        }
        SettingsSection(title: "Muted folders", footer: "Agents working in these folders, or in folders inside them, don't notify. You can also mute a folder from its name in the sidebar.") {
            if muted.isEmpty {
                HStack {
                    Text("None")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                    Spacer()
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 11)
                Divider()
            }
            ForEach(muted, id: \.self) { folder in
                VStack(spacing: 0) {
                    HStack(spacing: 10) {
                        Image(systemName: "bell.slash")
                            .font(.system(size: 11))
                            .foregroundStyle(Palette.muted)
                        Text((folder as NSString).abbreviatingWithTildeInPath)
                            .font(TextStyle.body)
                            .foregroundStyle(Palette.text)
                            .lineLimit(1)
                            .truncationMode(.head)
                        Spacer()
                        Button("Unmute") { setMuted(folder, false) }
                            .buttonStyle(.link)
                            .font(TextStyle.caption)
                    }
                    .padding(.horizontal, 16)
                    .padding(.vertical, 10)
                    Divider()
                }
            }
            HStack {
                Menu("Mute a Folder…") {
                    ForEach(store.folders.prefix(15).filter { !muted.contains($0.path) }) { folder in
                        Button((folder.path as NSString).abbreviatingWithTildeInPath) { setMuted(folder.path, true) }
                    }
                    Divider()
                    Button("Choose…") {
                        let panel = NSOpenPanel()
                        panel.canChooseDirectories = true
                        panel.canChooseFiles = false
                        panel.directoryURL = URL(fileURLWithPath: NSHomeDirectory())
                        panel.prompt = "Mute"
                        if panel.runModal() == .OK, let url = panel.url { setMuted(url.path, true) }
                    }
                }
                .menuStyle(.button)
                .fixedSize()
                Spacer()
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
        }
        .task { await store.loadFolders() }
        SettingsSection(title: "While you were away", footer: "Back at the Mac after a while (the app in the background, the screen locked or the Mac asleep), a summary of what each agent did and what needs you.") {
            toggle("Show a summary when I'm back", nil, $awaySummary)
            SettingsRow(title: "After being away for", divider: false) {
                Picker("", selection: $awayMinutes) {
                    Text("10 minutes").tag(10.0)
                    Text("15 minutes").tag(15.0)
                    Text("30 minutes").tag(30.0)
                    Text("1 hour").tag(60.0)
                    Text("2 hours").tag(120.0)
                }
                .labelsHidden()
                .fixedSize()
            }
            .disabled(!awaySummary)
            .opacity(awaySummary ? 1 : 0.5)
        }
        SettingsSection(title: "Sound") {
            toggle("Play a sound", "With each notification but note suggestions.", $sound, divider: false)
        }
    }

    private func toggle(_ title: String, _ detail: String?, _ value: Binding<Bool>, divider: Bool = true) -> some View {
        SettingsRow(title: title, detail: detail, divider: divider) {
            Toggle("", isOn: value).toggleStyle(.switch).labelsHidden()
        }
    }

    /// Minutes after midnight, as a time of day.
    private func timePicker(_ minutes: Binding<Int>) -> some View {
        DatePicker("", selection: Binding(
            get: { Calendar.current.startOfDay(for: Date()).addingTimeInterval(TimeInterval(minutes.wrappedValue * 60)) },
            set: { date in
                let parts = Calendar.current.dateComponents([.hour, .minute], from: date)
                minutes.wrappedValue = (parts.hour ?? 0) * 60 + (parts.minute ?? 0)
            }
        ), displayedComponents: .hourAndMinute)
        .labelsHidden()
        .datePickerStyle(.field)
        .fixedSize()
    }

    private func setMuted(_ folder: String, _ on: Bool) {
        NotificationPolicy.setMuted(folder, on)
        muted = NotificationPolicy.mutedFolders()
    }
}
