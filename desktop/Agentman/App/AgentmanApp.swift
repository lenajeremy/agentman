import SwiftUI

@main
struct AgentmanApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @State private var store = AgentsStore()
    @Environment(\.openWindow) private var openWindow

    /// Unit tests run inside the app, as its host, and get none of it: no
    /// window, no daemon, no editor on the user's folders, and nothing
    /// restored from the user's own copy of the app.
    nonisolated static let underTests = ProcessInfo.processInfo.environment["XCTestConfigurationFilePath"] != nil

    init() {
        if Self.underTests {
            UserDefaults.standard.register(defaults: ["ApplePersistenceIgnoreState": true])
        }
        // A held key repeats, as in a code editor or a terminal, instead of
        // offering accents: holding j in the editor's Vim mode moves down.
        // Someone who set press-and-hold themselves keeps it.
        UserDefaults.standard.register(defaults: ["ApplePressAndHoldEnabled": false])
        Fonts.register()
    }

    var body: some Scene {
        // One window, not a group: links (agentman://agent/…) open in it
        // rather than in a new window each time.
        Window("Agentman", id: "main") {
            if !Self.underTests {
                ContentView()
                    .frame(minWidth: 1040, minHeight: 640)
                    .environment(store)
                    .task {
                        Appearance.apply(UserDefaults.standard.string(forKey: "appearance") ?? "system")
                        store.start()
                    }
            }
        }
        .defaultLaunchBehavior(Self.underTests ? .suppressed : .automatic)
        .defaultSize(width: 1560, height: 980)
        .windowStyle(.hiddenTitleBar)
        .commands {
            CommandGroup(after: .appInfo) {
                CheckForUpdatesCommand()
            }
            CommandGroup(replacing: .newItem) {
                Button("New Agent") { store.selection = .newAgent }
                    .keyboardShortcut("n", modifiers: .command)
            }
            CommandGroup(replacing: .sidebar) {
                Button("Toggle Sidebar") {
                    UserDefaults.standard.set(!UserDefaults.standard.bool(forKey: Sidebar.hiddenKey), forKey: Sidebar.hiddenKey)
                }
                .keyboardShortcut("s", modifiers: [.command, .control])
                // Light, Dark, Match the Mac, in turn, as T3 Code's ⌥⇧⌘A.
                Button("Cycle Appearance") {
                    let order = ["light", "dark", "system"]
                    let now = UserDefaults.standard.string(forKey: "appearance") ?? "system"
                    let next = order[((order.firstIndex(of: now) ?? 2) + 1) % order.count]
                    UserDefaults.standard.set(next, forKey: "appearance")
                    Appearance.apply(next)
                }
                .keyboardShortcut("a", modifiers: [.command, .option, .shift])
            }
            // The daemon outlives the app, by design: quitting the app
            // leaves it running, so a stuck one is restarted here.
            CommandGroup(after: .appSettings) {
                Button("Restart Daemon…") {
                    let setup = store.setup
                    DaemonRestart.confirm(setUp: setup.allowed ? {
                        openWindow(id: "main")
                        setup.runOnScreen()
                    } : nil)
                }
            }
            CommandMenu("Go") {
                Button("Command Palette…") {
                    // Over the window in front: an editor's, or the main one.
                    if let editor = EditorRegistry.shared.keyEditor {
                        store.paletteEditor = editor.root.path
                    } else {
                        store.paletteEditor = nil
                        openWindow(id: "main")
                    }
                    store.showPalette = true
                }
                .keyboardShortcut("k", modifiers: .command)
                Button("Agentman") { openWindow(id: "main"); store.selection = .manager }
                    .keyboardShortcut("0", modifiers: [.command, .option])
                Button("Usage") {
                    // In an editor, its own Usage, without leaving it.
                    if let editor = EditorRegistry.shared.keyEditor {
                        editor.showUsage = true
                    } else {
                        openWindow(id: "main")
                        store.selection = .usage
                    }
                }
                Button("What Happened While I Was Away") {
                    openWindow(id: "main")
                    store.showAway(since: store.away.last?.since ?? Date().addingTimeInterval(-3600))
                }
            }
        }

        // The editor: one window per folder, coming back as it was after a
        // relaunch.
        WindowGroup("Editor", id: "editor", for: String.self) { $folder in
            if let folder, !Self.underTests {
                EditorWindow(folder: folder, client: store.client)
                    .environment(store)
            }
        }
        .defaultSize(width: 1560, height: 980)
        .windowStyle(.hiddenTitleBar)
        // As small as its content allows and no smaller (EditorWindow's
        // minimum), so its columns never run past the window's edge.
        .windowResizability(.contentMinSize)
        .commands { EditorCommands() }

        Settings {
            SettingsView()
                .environment(store)
        }
        .windowResizability(.contentMinSize)
    }
}

/// Starts the updater, and asks before quitting with unsaved edits in the
/// editor.
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        MainActor.assumeIsolated { AppUpdater.shared.start() }
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        // Under tests a window closing can look like the last one going, and
        // would end the run; the test runner ends the app itself.
        if AgentmanApp.underTests { return .terminateCancel }
        return MainActor.assumeIsolated {
            let unsaved = EditorRegistry.shared.unsaved
            guard !unsaved.isEmpty else { return .terminateNow }
            let alert = NSAlert()
            alert.messageText = unsaved.count == 1
                ? "Save the changes to \(unsaved[0].name) before quitting?"
                : "Save the changes to \(unsaved.count) files before quitting?"
            alert.informativeText = unsaved.count == 1 ? "Your changes are lost if you don't save them." : unsaved.map(\.path).joined(separator: "\n")
            alert.addButton(withTitle: unsaved.count == 1 ? "Save" : "Save All")
            alert.addButton(withTitle: "Cancel")
            alert.addButton(withTitle: "Don't Save")
            switch alert.runModal() {
            case .alertFirstButtonReturn:
                return unsaved.allSatisfy { $0.save() } ? .terminateNow : .terminateCancel
            case .alertThirdButtonReturn:
                return .terminateNow
            default:
                return .terminateCancel
            }
        }
    }
}
