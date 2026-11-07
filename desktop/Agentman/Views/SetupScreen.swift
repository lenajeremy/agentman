import SwiftUI

/// The welcome and setup screen, over the main window (DaemonSetup). On first
/// launch it says in a few lines what setting up does, with one button to
/// go; then it shows am setup's steps as each ends, and what to do if one
/// fails.
struct SetupScreen: View {
    @Environment(AgentsStore.self) private var store
    @Environment(\.openSettings) private var openSettings
    @AppStorage("settingsTab") private var settingsTab = "general"

    static let width: CGFloat = 540

    var body: some View {
        let setup = store.setup
        ZStack {
            Color.black.opacity(0.22)
                .ignoresSafeArea()
            VStack(alignment: .leading, spacing: 0) {
                header(setup)
                Text(subtitle(setup))
                    .font(TextStyle.body)
                    .foregroundStyle(Palette.muted)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.horizontal, 24)
                    .padding(.top, 10)
                    .padding(.bottom, 16)
                VStack(alignment: .leading, spacing: 12) {
                    if setup.phase == .ready {
                        WelcomePoints()
                    } else {
                        SetupSteps(setup: setup)
                    }
                    if case .failed(let failure) = setup.phase {
                        FailureNote(failure: failure)
                    }
                    if setup.tmuxMissing, !setup.running {
                        TmuxNote()
                    }
                }
                .padding(.horizontal, 24)
                footer(setup)
                    .padding(.horizontal, 24)
                    .padding(.vertical, 18)
            }
            .frame(width: Self.width)
            .background(Palette.surface)
            .clipShape(RoundedRectangle(cornerRadius: 18, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 18, style: .continuous).strokeBorder(Palette.edge, lineWidth: 1.5))
            .shadow(color: .black.opacity(0.2), radius: 40, y: 16)
        }
        .transition(.opacity)
        .task { await setup.lookAtShell() }
    }

    /// The website's orange band, with its terminal sticker and the title in
    /// Rubik: before setting up, "Set up" over "Agentman" outlined.
    private func header(_ setup: DaemonSetup) -> some View {
        FloodBand {
            HStack(alignment: .top, spacing: 0) {
                VStack(alignment: .leading, spacing: 8) {
                    Text("Agentman for Mac").eyebrow(Palette.onFlood.opacity(0.9))
                    if setup.phase == .ready {
                        VStack(alignment: .leading, spacing: -4) {
                            Text("Set up").font(Fonts.display(36)).foregroundStyle(Palette.onFlood)
                            OutlinedWord(text: "Agentman", font: Fonts.display(36))
                        }
                    } else {
                        Text(title(setup))
                            .font(Fonts.display(30))
                            .foregroundStyle(Palette.onFlood)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                .padding(.top, 22)
                .padding(.bottom, 18)
                Spacer(minLength: 8)
                Sticker(name: .terminal, size: 150)
                    .padding(.top, -6)
                    .padding(.bottom, -22)
            }
            .padding(.leading, 24)
            .padding(.trailing, 12)
        }
        .zIndex(1)
    }

    private func title(_ setup: DaemonSetup) -> String {
        switch setup.phase {
        case .ready: "Set up Agentman"
        case .running: "Setting up Agentman…"
        case .finished: "Agentman is running"
        case .failed: "Setup didn't finish"
        }
    }

    private func subtitle(_ setup: DaemonSetup) -> String {
        switch setup.phase {
        case .ready:
            "Agentman watches your agents through a small helper that runs in the background on this Mac. Setting it up takes a few seconds and needs nothing from you."
        case .running:
            "A few seconds. Your agents keep running."
        case .finished(let summary):
            "am serve \(summary.version ?? "") runs in the background from login, and your agents report to it."
        case .failed:
            "What it did before that is kept. Try again, or see what went wrong below."
        }
    }

    @ViewBuilder private func footer(_ setup: DaemonSetup) -> some View {
        HStack(spacing: 8) {
            switch setup.phase {
            case .ready:
                Spacer()
                SmallButton(title: "Not now") { setup.notNow() }
                    .keyboardShortcut(.cancelAction)
                SmallButton(title: "Set Up Agentman", primary: true) { Task { await setup.run() } }
                    .keyboardShortcut(.defaultAction)
                    .disabled(!setup.allowed)
            case .running:
                Spacer()
                SmallButton(title: "Setting Up…", primary: true) {}
                    .disabled(true)
                    .opacity(0.6)
            case .finished:
                Text("Pair a phone to hear from your agents anywhere.")
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
                Spacer()
                SmallButton(title: "Pair a Phone…", symbol: "iphone") {
                    setup.showsScreen = false
                    settingsTab = SettingsPage.devices.rawValue
                    store.pairingRequested = true
                    openSettings()
                }
                SmallButton(title: "Done", primary: true) { setup.showsScreen = false }
                    .keyboardShortcut(.defaultAction)
            case .failed:
                Spacer()
                SmallButton(title: "Close") { setup.showsScreen = false }
                    .keyboardShortcut(.cancelAction)
                SmallButton(title: "Try Again", symbol: "arrow.clockwise", primary: true) { Task { await setup.run() } }
                    .keyboardShortcut(.defaultAction)
            }
        }
    }
}

/// What setting up does, before it does it.
private struct WelcomePoints: View {
    var body: some View {
        VStack(spacing: 0) {
            point("power", "Starts in the background at login",
                  "It keeps watching your agents while this window is closed, and tells your phone when one needs you.")
            divider
            point("point.3.connected.trianglepath.dotted", "Connects your agents",
                  "Claude Code, Codex, OpenCode and the rest report to it, and get Agentman's tools.")
            divider
            point("arrow.triangle.swap", "Replaces one you started yourself",
                  "If you already run the helper (am serve) from Homebrew or a terminal, this one takes its place. The am command keeps working.")
        }
        // The site's cream card, on its edge and hard shadow.
        .edged(14, fill: Palette.cream, depth: 3)
    }

    private var divider: some View {
        Rectangle().fill(Palette.edge.opacity(0.15)).frame(height: 1).padding(.leading, 56)
    }

    private func point(_ symbol: String, _ title: String, _ detail: String) -> some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: symbol)
                .font(.system(size: 13, weight: .semibold))
                .foregroundStyle(Palette.prompt)
                .frame(width: 30, height: 30)
                .edged(9, fill: Palette.surface)
            VStack(alignment: .leading, spacing: 3) {
                Text(title)
                    .font(TextStyle.bodyMedium)
                    .foregroundStyle(Palette.text)
                Text(detail)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.muted)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
    }
}

/// am setup's steps, each with what it said once it has ended, the one
/// under way turning.
struct SetupSteps: View {
    let setup: DaemonSetup

    var body: some View {
        let said = Dictionary(setup.steps.map { ($0.step, $0) }, uniquingKeysWith: { _, last in last })
        // Steps am setup said that this app doesn't know of go last, so a
        // newer am's are still shown.
        let names = SetupStepName.order + setup.steps.map(\.step).filter { !SetupStepName.order.contains($0) }
        let current = setup.running ? names.first { said[$0] == nil } : nil
        VStack(spacing: 0) {
            ForEach(Array(names.enumerated()), id: \.element) { index, name in
                SetupStepRow(name: name, line: said[name], current: name == current, divider: index < names.count - 1)
            }
        }
        .background(Palette.paper.opacity(0.6), in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(Palette.edge, lineWidth: 1.5))
    }
}

/// How a step of am setup is named on the screen.
enum SetupStepName {
    /// The steps in the order am setup takes them on a Mac.
    static let order = ["config", "binary", "hooks", "tools", "service", "daemon"]

    static func title(_ step: String) -> String {
        switch step {
        case "config": "Agentman's settings"
        case "binary": "The daemon's copy of am"
        case "hooks": "Your agents' hooks"
        case "tools": "Agentman's tools for agents"
        case "service": "Starting at login"
        case "daemon": "am serve answering"
        default: step.capitalized
        }
    }
}

private struct SetupStepRow: View {
    let name: String
    let line: SetupLine?
    let current: Bool
    let divider: Bool

    var body: some View {
        VStack(spacing: 0) {
            HStack(alignment: .top, spacing: 12) {
                mark
                    .frame(width: 18, height: 18)
                VStack(alignment: .leading, spacing: 3) {
                    HStack(spacing: 6) {
                        Text(SetupStepName.title(name))
                            .font(TextStyle.bodyMedium)
                            .foregroundStyle(line == nil && !current ? Palette.faint : Palette.text)
                        Spacer(minLength: 6)
                        // The agents a step set up, by their marks.
                        if let agents = line?.agents, !agents.isEmpty {
                            HStack(spacing: 4) {
                                ForEach(agents, id: \.agent) { agent in
                                    AgentMark(kind: agent.agent, size: 13)
                                        .opacity(agent.status == "failed" ? 0.35 : 1)
                                        .help(AgentName.of(agent.agent) + (agent.error.map { ": \($0)" } ?? ""))
                                }
                            }
                        }
                    }
                    if let detail = line?.detail, !detail.isEmpty {
                        Text(detail)
                            .font(TextStyle.caption)
                            .foregroundStyle(line?.didFail == true ? Palette.errorText : Palette.muted)
                            .lineLimit(line?.didFail == true ? 4 : 2)
                            .fixedSize(horizontal: false, vertical: true)
                            .textSelection(.enabled)
                    }
                    // Why each agent that failed did: it was only in a
                    // tooltip on its faded mark.
                    ForEach(line?.agents?.filter { $0.status == "failed" && $0.error?.isEmpty == false } ?? [], id: \.agent) { agent in
                        HStack(alignment: .firstTextBaseline, spacing: 6) {
                            AgentMark(kind: agent.agent, size: 11)
                            Text("\(AgentName.of(agent.agent)): \(DaemonSetup.plain(agent.error ?? ""))")
                                .font(TextStyle.caption)
                                .foregroundStyle(Palette.textSecondary)
                                .fixedSize(horizontal: false, vertical: true)
                                .textSelection(.enabled)
                        }
                        .padding(.top, 2)
                    }
                }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 10)
            if divider {
                Rectangle().fill(Palette.line).frame(height: 1).padding(.leading, 44)
            }
        }
    }

    @ViewBuilder private var mark: some View {
        if current {
            Loader(size: 11)
        } else if let line {
            switch line.status {
            case "failed":
                Image(systemName: "xmark.circle.fill").foregroundStyle(Palette.error)
            case "skipped":
                Image(systemName: "minus.circle").foregroundStyle(Palette.faint)
            default:
                Image(systemName: "checkmark.circle.fill").foregroundStyle(Palette.ok)
            }
        } else {
            Image(systemName: "circle").foregroundStyle(Palette.faint.opacity(0.6))
        }
    }
}

/// What went wrong, and where the daemon says more.
private struct FailureNote: View {
    let failure: DaemonSetup.Failure

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "exclamationmark.triangle.fill")
                .font(.system(size: 13))
                .foregroundStyle(Palette.errorText)
                .padding(.top, 1)
            VStack(alignment: .leading, spacing: 6) {
                // A step that failed says why on its row, above.
                Text(failure.step.map { "\(SetupStepName.title($0)) didn't work." } ?? failure.message)
                    .font(TextStyle.caption)
                    .foregroundStyle(Palette.text)
                    .fixedSize(horizontal: false, vertical: true)
                    .textSelection(.enabled)
                // An agent's hooks or tools: the reason is on its row, and
                // the daemon's log has nothing more to say about it.
                if let step = failure.step, step == "hooks" || step == "tools" {
                    Text("Everything else is set up, and Agentman works without it. Fix what it says above and try again, or close this.")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                        .fixedSize(horizontal: false, vertical: true)
                } else {
                    HStack(spacing: 6) {
                        Text("The daemon's log is \((failure.log as NSString).abbreviatingWithTildeInPath).")
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.muted)
                            .textSelection(.enabled)
                        Button("Open Log") { NSWorkspace.shared.open(URL(fileURLWithPath: failure.log)) }
                            .buttonStyle(.link)
                            .font(TextStyle.caption)
                    }
                }
            }
            Spacer(minLength: 0)
        }
        .padding(12)
        .background(Palette.errorWash, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
    }
}
