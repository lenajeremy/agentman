import AppKit
import SwiftUI

/// The language servers behind the editor's errors, completions and Go to
/// Definition: where each comes from, what am downloaded, and the user's say
/// over them. Kept by am serve, which runs them.
struct LanguagesSettings: View {
    @Environment(AgentsStore.self) private var store
    @State private var state: LanguagesState?
    @State private var problem: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Languages")
                        .font(TextStyle.display)
                        .foregroundStyle(Palette.text)
                    Text("Errors, completions and Go to Definition in the editor come from language servers that am serve runs on this Mac. Each starts when a file of its language opens, and stops when no window has used it for ten minutes.")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                        .fixedSize(horizontal: false, vertical: true)
                }

                if let state {
                    SettingsSection {
                        SettingsRow(title: "Download servers when needed",
                                    detail: "The first file of a language gets its server, once. Off, only servers already on this Mac run.",
                                    divider: false) {
                            Toggle("", isOn: Binding(get: { !state.noDownloads },
                                                     set: { change(LanguageChange(noDownloads: !$0)) }))
                                .labelsHidden()
                                .toggleStyle(.switch)
                        }
                    }
                    SettingsSection(title: "Servers") {
                        ForEach(Array(state.servers.enumerated()), id: \.element.id) { index, server in
                            SettingsRow(title: server.language, detail: Self.detail(server),
                                        divider: index < state.servers.count - 1) {
                                controls(server)
                            }
                            .help(server.path ?? "")
                        }
                    }
                    if let node = state.nodeSize, node > 0 {
                        SettingsSection {
                            SettingsRow(title: "Node.js", detail: "Downloaded to run the TypeScript and Python servers · \(Self.size(node))",
                                        divider: false) {
                                Button("Remove") { remove("node") }
                            }
                        }
                    }
                } else if problem == nil {
                    ProgressView().controlSize(.small).frame(maxWidth: .infinity, minHeight: 120)
                }

                if let problem {
                    Text(problem)
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.needsYouText)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .padding(.horizontal, 28)
            .padding(.vertical, 26)
        }
        .task { await load() }
    }

    @ViewBuilder
    private func controls(_ server: LanguageServer) -> some View {
        HStack(spacing: 10) {
            if server.source == "downloaded" {
                Button("Remove") { remove(server.id) }
            }
            Menu {
                Button("Use My Own Program…") { choose(server) }
                if server.source == "yours" {
                    Button("Use the Usual One") { change(LanguageChange(server: server.id, path: "")) }
                }
                Divider()
                if server.source == "off" {
                    Button("Turn On") { change(LanguageChange(server: server.id, disabled: false)) }
                } else {
                    Button("Turn Off") { change(LanguageChange(server: server.id, disabled: true)) }
                }
            } label: {
                Image(systemName: "ellipsis.circle")
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .fixedSize()
        }
    }

    /// Where a server comes from, in a few words.
    static func detail(_ server: LanguageServer) -> String {
        var parts = [server.name]
        switch server.source {
        case "yours":
            parts.append("your own: " + ((server.path ?? "") as NSString).abbreviatingWithTildeInPath)
        case "mac":
            parts.append("on this Mac")
        case "downloaded":
            parts.append("downloaded" + (server.size.map { " · " + size($0) } ?? ""))
        case "download":
            parts.append("downloads when first needed" + (server.downloadSize.map { ", " + $0 } ?? ""))
        case "off":
            parts.append("off")
        default:
            parts.append(server.missing ?? "not on this Mac")
        }
        if let running = server.running, !running.isEmpty {
            let names = running.map { ($0 as NSString).lastPathComponent }
            parts.append("running for " + names.joined(separator: ", "))
        }
        return parts.joined(separator: " · ")
    }

    static func size(_ bytes: Int64) -> String {
        ByteCountFormatter.string(fromByteCount: bytes, countStyle: .file)
    }

    // MARK: - Asking am serve

    private func load() async {
        apply(try? await store.client.request(DaemonRequest(.lspStatus)))
    }

    private func change(_ change: LanguageChange) {
        var request = DaemonRequest(.lspSet)
        request.language = change
        Task { apply(try? await store.client.request(request)) }
    }

    private func remove(_ id: String) {
        var request = DaemonRequest(.lspRemove)
        request.language = LanguageChange(server: id)
        Task { apply(try? await store.client.request(request)) }
    }

    private func apply(_ reply: DaemonEvent?) {
        if let languages = reply?.languages {
            state = languages
            problem = nil
        } else if let error = reply?.error {
            problem = error.contains("unknown") ? "Language servers need a newer am serve: brew upgrade agentman" : error
        } else {
            problem = "Couldn't reach am serve."
        }
    }

    /// The user's own program for a server, from a file chooser.
    private func choose(_ server: LanguageServer) {
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        panel.canChooseDirectories = false
        panel.directoryURL = URL(fileURLWithPath: server.path.map { ($0 as NSString).deletingLastPathComponent } ?? "/opt/homebrew/bin")
        panel.message = "Choose the program that runs \(server.name)"
        panel.prompt = "Use"
        guard panel.runModal() == .OK, let url = panel.url else { return }
        change(LanguageChange(server: server.id, path: url.path))
    }
}
