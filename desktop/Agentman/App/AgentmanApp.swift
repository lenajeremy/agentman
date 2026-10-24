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
                .task { store.start() }
                .onOpenURL { store.open($0) }
        }
        .defaultSize(width: 1180, height: 760)
        .windowToolbarStyle(.unified(showsTitle: false))
    }
}
