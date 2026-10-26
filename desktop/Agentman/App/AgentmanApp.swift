import SwiftUI

@main
struct AgentmanApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @State private var store = AgentsStore()
    @Environment(\.openWindow) private var openWindow

    init() {
        Fonts.register()
    }

    var body: some Scene {
        // One window, not a group: links (agentman://agent/…) open in it
        // rather than in a new window each time.
        Window("Agentman", id: "main") {
            ContentView()
                .environment(store)
                .task {
                    Appearance.apply(UserDefaults.standard.string(forKey: "appearance") ?? "system")
                    store.start()
                }
        }
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
            if let folder {
                EditorWindow(folder: folder, client: store.client)
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
