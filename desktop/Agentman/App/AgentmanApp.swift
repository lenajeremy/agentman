import SwiftUI

@main
struct AgentmanApp: App {
    @State private var store = AgentsStore()

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
        }

        Settings {
            SettingsView()
                .environment(store)
        }
    }
}
