import SwiftUI

/// The main window: agents on the left, and on the right either Agentman or
/// the agent you picked.
struct ContentView: View {
    @Environment(AgentsStore.self) private var store
    @Environment(\.openSettings) private var openSettings
    @Environment(\.openWindow) private var openWindow
    @State private var headerLeading: CGFloat = 82
    @AppStorage("settingsTab") private var settingsTab = "general"
    /// The sidebar's width, as dragged, and whether it is hidden (⌃⌘S).
    @AppStorage(Sidebar.widthKey) private var sidebarWidth = Sidebar.defaultWidth
    @AppStorage(Sidebar.hiddenKey) private var sidebarHidden = false

    var body: some View {
        GeometryReader { geometry in
            HStack(spacing: 0) {
                if !sidebarHidden {
                    Sidebar()
                        .frame(width: Sidebar.clamp(sidebarWidth, window: geometry.size.width))
                        .overlay(alignment: .trailing) {
                            PaneResizeHandle(width: $sidebarWidth, clamp: { Sidebar.clamp($0, window: geometry.size.width) }, reset: Sidebar.defaultWidth)
                        }
                        .transition(.move(edge: .leading))
                }
                detail
                    // With the sidebar hidden, the headers make room for the
                    // window's buttons and the one that brings it back.
                    .environment(\.detailHeaderLeading, sidebarHidden ? headerLeading + 34 : 0)
                    .overlay(alignment: .topLeading) {
                        if sidebarHidden {
                            ChromeButton(symbol: "sidebar.left", help: "Show the sidebar (⌃⌘S)") { sidebarHidden = false }
                                .padding(.leading, headerLeading)
                                .padding(.top, 2)
                        }
                    }
            }
            .animation(.easeOut(duration: 0.18), value: sidebarHidden)
        }
        // Agentman's news, small, over whatever is open.
        .overlayPreferenceValue(MessageBoxFrame.self) { box in
            GeometryReader { proxy in
                AgentmanToasts(messageBox: box.map { proxy[$0] })
            }
        }
        .environment(\.windowHeaderLeading, headerLeading)
        .background(Palette.paper)
        .background(WindowReader { window in
            WindowChrome.configure(window)
            headerLeading = WindowChrome.leading(window)
        })
        .ignoresSafeArea(.container, edges: .top)
        // A note Agentman suggests, over whatever is on screen.
        .overlay(alignment: .topTrailing) {
            NoteSuggestionPopup()
        }
        .animation(.spring(duration: 0.3), value: store.manager.noteSuggestions.first?.id)
        .overlay {
            if store.showPalette {
                CommandPalette()
            }
        }
        // What happened while the user was away, on their return.
        .overlay {
            if let digest = store.away.digest {
                AwaySummary(digest: digest)
            }
        }
        .animation(.easeOut(duration: 0.2), value: store.away.digest?.id)
        .overlay {
            if let box = store.lightbox, box.place == nil {
                LightboxView(box: box)
            }
        }
        .animation(.easeOut(duration: 0.15), value: store.lightbox?.id)
        .onOpenURL(perform: open)
        .task {
            // AGENTMAN_OPEN names a link to open at launch, so a copy started
            // for a screenshot or a test opens where it is needed.
            let environment = ProcessInfo.processInfo.environment
            if let link = environment["AGENTMAN_OPEN"], let url = URL(string: link) {
                open(url)
            }
            if let folder = environment["AGENTMAN_FOLDER"] {
                store.folderFilter = folder
            }
            if let session = environment["AGENTMAN_RENAMING"] {
                store.renaming = session
            }
            if let path = environment["AGENTMAN_LIGHTBOX"], let image = NSImage(contentsOfFile: path) {
                store.lightbox = Lightbox(image: image, title: (path as NSString).lastPathComponent)
            }
        }
    }

    /// agentman://settings opens Settings, at a tab with
    /// agentman://settings/keys; the rest are the store's to open.

    /// Agentman, or the agent picked, beside the sidebar.
    private var detail: some View {
        Group {
            switch store.selection {
            case .agent(let id):
                if let session = store.session(id) {
                    AgentThreadView(session: session)
                        .id(session.id)
                } else {
                    GoneView()
                }
            case .newAgent:
                NewAgentView()
            case .usage:
                UsageView()
            case .manager, nil:
                ManagerChatView()
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Palette.paper)
    }

    private func open(_ url: URL) {
        if url.host() == "terminal" {
            let items = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
            if let folder = items.first(where: { $0.name == "folder" })?.value {
                EditorOpener.openTerminal(folder: folder, with: openWindow)
            }
        } else if url.host() == "edit" {
            let items = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
            // agentman://edit?folder=…&file=…&agent=here, as `am open` sends.
            if let folder = items.first(where: { $0.name == "folder" })?.value {
                EditorOpener.open(folder: folder, file: items.first(where: { $0.name == "file" })?.value,
                                  showingAgent: items.first(where: { $0.name == "agent" })?.value == "here", with: openWindow)
            }
        } else if url.host() == "settings" {
            let tab = url.path().trimmingCharacters(in: CharacterSet(charactersIn: "/"))
            if SettingsPage(rawValue: tab) != nil { settingsTab = tab }
            openSettings()
        } else {
            store.open(url)
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
