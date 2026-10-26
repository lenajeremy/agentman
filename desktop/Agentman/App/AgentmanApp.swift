import SwiftUI

@main
struct AgentmanApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @State private var store = AgentsStore()
    @Environment(\.openWindow) private var openWindow

    /// Unit tests run inside the app, as its host, and get none of it: no
    /// window, no daemon, no editor on the user's folders, and nothing
    /// restored from the user's own copy of the app.
    static let underTests = ProcessInfo.processInfo.environment["XCTestConfigurationFilePath"] != nil

    init() {
        if Self.underTests {
            UserDefaults.standard.register(defaults: ["ApplePersistenceIgnoreState": true])
        }
        Fonts.register()
    }

    var body: some Scene {
        // One window, not a group: links (agentman://agent/…) open in it
        // rather than in a new window each time.
        Window("Agentman", id: "main") {
            if !Self.underTests {
                ContentView()
                    .environment(store)
                    .task {
                        Appearance.apply(UserDefaults.standard.string(forKey: "appearance") ?? "system")
                        store.start()
                    }
            }
        }
        .defaultLaunchBehavior(Self.underTests ? .suppressed : .automatic)
        .defaultSize(width: 1180, height: 760)
        .windowToolbarStyle(.unified(showsTitle: true))
        .commands {
            CommandGroup(replacing: .newItem) {
                Button("New Agent") { store.selection = .newAgent }
                    .keyboardShortcut("n", modifiers: .command)
            }
            CommandMenu("Go") {
                Button("Command Palette…") {
                    openWindow(id: "main")
                    store.showPalette = true
                }
                .keyboardShortcut("k", modifiers: .command)
                Button("Agentman") { openWindow(id: "main"); store.selection = .manager }
                    .keyboardShortcut("0", modifiers: [.command, .option])
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
        .defaultSize(width: 1180, height: 760)
        .windowToolbarStyle(.unified(showsTitle: true))
        .commands { EditorCommands() }

        Settings {
            SettingsView()
                .environment(store)
        }
    }
}

/// Asks before quitting with unsaved edits in the editor.
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        MainActor.assumeIsolated {
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
