import SwiftUI

/// The main window: agents on the left, and on the right either Agentman or
/// the agent you picked.
struct ContentView: View {
    @Environment(AgentsStore.self) private var store
    @Environment(\.openSettings) private var openSettings
    @AppStorage("settingsTab") private var settingsTab = "general"

    var body: some View {
        NavigationSplitView {
            Sidebar()
                .navigationSplitViewColumnWidth(min: 240, ideal: 280, max: 380)
        } detail: {
            Group {
                switch store.selection {
                case .agent(let id):
                    if let session = store.session(id) {
                        AgentThreadView(session: session)
                            .id(session.id)
                    } else {
                        GoneView()
                    }
                case .manager, nil:
                    ManagerChatView()
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Palette.paper)
        }
        .onOpenURL { url in
            // agentman://settings opens Settings, at a tab with
            // agentman://settings/keys; the rest are the store's to open.
            if url.host() == "settings" {
                let tab = url.path().trimmingCharacters(in: CharacterSet(charactersIn: "/"))
                if ["general", "knowledge", "keys"].contains(tab) { settingsTab = tab }
                openSettings()
            } else {
                store.open(url)
            }
        }
    }
}

/// An agent that ended while it was open.
private struct GoneView: View {
    @Environment(AgentsStore.self) private var store

    var body: some View {
        VStack(spacing: 10) {
            Image(systemName: "moon.zzz")
                .font(.system(size: 28))
                .foregroundStyle(Palette.faint)
            Text("This agent has ended")
                .font(TextStyle.title)
                .foregroundStyle(Palette.text)
            Button("Back to Agentman") { store.selection = .manager }
                .buttonStyle(.link)
        }
    }
}
