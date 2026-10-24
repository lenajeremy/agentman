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
                case .notifications:
                    SettingsPageView(title: "Notifications") { NotificationsPage() }
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
    case general, notifications, permissions, personality, knowledge, keys

    var id: String { rawValue }

    var title: String {
        switch self {
        case .general: "General"
        case .notifications: "Notifications"
        case .permissions: "Permissions"
        case .personality: "Personality"
        case .knowledge: "Knowledge"
        case .keys: "Keys"
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

        AppearanceSection()

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
        about
    }

    @ViewBuilder private var about: some View {
        AboutSection()
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

/// When the Mac speaks up about agents.
private struct NotificationsPage: View {
    @AppStorage("notifyFinished") private var finished = true
    @AppStorage("notifyNeedsYou") private var needsYou = true

    var body: some View {
        SettingsSection(title: "Agents", footer: "Never for the thread you're looking at. With an Anthropic key set, Agentman's cards tell you when an agent needs you instead.") {
            SettingsRow(title: "An agent finishes", detail: "When it ends a turn, with what it said last.") {
                Toggle("", isOn: $finished).toggleStyle(.switch).labelsHidden()
            }
            SettingsRow(title: "An agent needs you", detail: "When it waits on an approval or a question.", divider: false) {
                Toggle("", isOn: $needsYou).toggleStyle(.switch).labelsHidden()
            }
        }
    }
}
