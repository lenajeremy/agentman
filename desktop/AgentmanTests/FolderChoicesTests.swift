import AppKit
import SwiftUI
import XCTest
@testable import Agentman

@MainActor
final class FolderChoicesTests: XCTestCase {
    private let home = "/Users/me"

    private func folder(_ path: String, minutesAgo: Double, agents: Int = 1) -> Folder {
        Folder(path: path, agents: agents, running: nil, lastActivityAt: Int64((Date.now.timeIntervalSince1970 - minutesAgo * 60) * 1000))
    }

    private func defaults() -> UserDefaults {
        let name = "FolderChoicesTests-\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: name)!
        addTeardownBlock { UserDefaults().removePersistentDomain(forName: name) }
        return defaults
    }

    /// Where agents worked, newest first, without the places apps keep for
    /// themselves or folders since deleted.
    func testRecentFoldersWithoutAppPlacesOrDeletedOnes() {
        let folders = [
            folder("/Users/me/Documents/ferry", minutesAgo: 5),
            folder("/Users/me/Desktop/all/agentman", minutesAgo: 1),
            folder("/private/tmp/claude-501/lab/muse3", minutesAgo: 2),
            folder("/Users/me/Library/Application Support/Claude/scratch-workspaces/x/scratch-2026", minutesAgo: 3),
            folder("/private/var/folders/ab/T/run", minutesAgo: 3),
            folder("/Users/me/Documents/gone", minutesAgo: 4),
            folder("/Users/me/Ferry", minutesAgo: 6),
        ]
        let paths = FolderChoices.make(folders: folders, chosen: [:], selected: nil, home: home) { $0 != "/Users/me/Documents/gone" }
        XCTAssertEqual(paths, ["/Users/me/Desktop/all/agentman", "/Users/me/Documents/ferry", "/Users/me/Ferry"])
    }

    /// A folder the user chose is offered with the rest, by when it was
    /// chosen; only the newest few are; the one filtered to always is.
    func testChosenFoldersJoinInAndTheFilteredOneIsAlwaysOffered() {
        var folders = (0..<12).map { folder("/Users/me/p\($0)", minutesAgo: Double($0 + 10)) }
        folders.append(folder("/Users/me/Documents/screenshotter", minutesAgo: 30))
        let chosen = ["/Users/me/Documents/Agentman Walkthroughs": Date.now.addingTimeInterval(-60)]
        let paths = FolderChoices.make(folders: folders, chosen: chosen, selected: "/Users/me/Documents/screenshotter", home: home) { _ in true }
        XCTAssertEqual(paths.count, FolderChoices.shown + 1)
        XCTAssertEqual(paths[0], "/Users/me/Documents/screenshotter", "the folder filtered to, though old")
        XCTAssertEqual(paths[1], "/Users/me/Documents/Agentman Walkthroughs", "chosen a minute ago, before the agents' folders")
        XCTAssertEqual(paths[2], "/Users/me/p0")
        XCTAssertFalse(paths.contains("/Users/me/p11"))
    }

    func testAFoldersPlaceIsFromHome() {
        XCTAssertEqual(FolderChoices.place(of: "/Users/me/Documents/ferry", home: home), "~/Documents")
        XCTAssertEqual(FolderChoices.place(of: "/Users/me/Ferry", home: home), "~")
        XCTAssertEqual(FolderChoices.place(of: "/opt/work/site", home: home), "/opt/work")
    }

    func testOnlyTheNewestChosenFoldersAreKept() {
        let defaults = defaults()
        let start = Date.now.addingTimeInterval(-3600)
        for index in 0..<(FolderChoices.kept + 3) {
            FolderChoices.remember("/Users/me/f\(index)", at: start.addingTimeInterval(Double(index)), defaults)
        }
        let kept = FolderChoices.chosen(defaults)
        XCTAssertEqual(kept.count, FolderChoices.kept)
        XCTAssertNil(kept["/Users/me/f0"])
        XCTAssertNotNil(kept["/Users/me/f12"])
    }

    /// Choosing any folder shows it, and keeps it among the choices though
    /// no agent has worked there; New agent then starts in it.
    func testChoosingAFolderShowsItAndNewAgentStartsThere() {
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-folders.sock"))
        store.folderDefaults = defaults()
        store.folderExists = { _ in true }
        let walkthroughs = NSHomeDirectory() + "/Documents/Agentman Walkthroughs"
        store.chooseFolder(walkthroughs)
        XCTAssertEqual(store.folderFilter, walkthroughs)
        store.folderFilter = nil
        XCTAssertEqual(store.folderChoices, [walkthroughs], "still offered after the filter is cleared")

        store.newAgent(in: walkthroughs)
        XCTAssertEqual(store.newAgentPreset, NewAgentPreset(folder: walkthroughs))
        XCTAssertEqual(store.selection, .newAgent)
        store.newAgentPreset = nil
        store.newAgent(in: nil)
        XCTAssertNil(store.newAgentPreset, "no folder filtered to: the page's own choice stands")
    }

    func testAStateShowsHowManyAreInItWhenAny() {
        XCTAssertEqual(FilterPanel.counted("Idle", 15), "Idle 15")
        XCTAssertEqual(FilterPanel.counted("Needs you", 0), "Needs you")
    }

    // MARK: Pictures

    /// The filter panel, and the sidebar on a folder no agent has worked in.
    /// Runs with TEST_RUNNER_AGENTMAN_SHOTS=<folder>.
    func testRenderTheFilterPanel() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let folder = URL(fileURLWithPath: out)
        let home = NSHomeDirectory()
        let paths = ["/Desktop/all/agentman", "/Documents/screenshotter", "/Documents/wflow/claude", "/Desktop/all/intro-to-hacking",
                     "/Documents/finance-tracker", "/Documents/ferry", "/Documents/job-automation", "/Ferry"].map { home + $0 }
        let now = Int64(Date.now.timeIntervalSince1970 * 1000)
        func store(filter: String?) throws -> AgentsStore {
            let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-folder-shots.sock"))
            store.folderDefaults = defaults()
            store.folderExists = { _ in true }
            let sessions = [("a", paths[0], "busy"), ("b", paths[0], "idle"), ("c", paths[1], "waiting_input"), ("d", paths[1], "idle"),
                            ("e", paths[1], "idle"), ("f", paths[2], "idle"), ("g", paths[4], "idle"), ("h", paths[5], "idle")]
                .map { #"{"id":"\#($0.0)","kind":"claude","name":"Agent \#($0.0)","cwd":"\#($0.1)","state":"\#($0.2)","inject":"tmux","lastActivityAt":\#(now)}"# }
            store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(#"{"type":"sessions","sessions":[\#(sessions.joined(separator: ","))]}"#.utf8)))
            store.folders = paths.enumerated().map { Folder(path: $1, agents: 3, running: nil, lastActivityAt: now - Int64($0) * 60_000) }
            store.folderFilter = filter
            return store
        }
        let walkthroughs = home + "/Documents/Agentman Walkthroughs"
        // A folder that is there, for the New Agent page, which takes none
        // that isn't.
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let fresh = root.appendingPathComponent("Agentman Walkthroughs")
        try FileManager.default.createDirectory(at: fresh, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        for (look, appearance) in [("light", NSAppearance.Name.aqua), ("dark", .darkAqua)] {
            try render(FilterPanel().background(Palette.surface), store: try store(filter: nil), size: CGSize(width: 300, height: 500),
                       appearance: appearance, to: folder.appendingPathComponent("filter-panel-\(look).png"))
            let chosen = try store(filter: nil)
            chosen.chooseFolder(walkthroughs)
            try render(FilterPanel().background(Palette.surface), store: chosen, size: CGSize(width: 300, height: 500),
                       appearance: appearance, to: folder.appendingPathComponent("filter-panel-chosen-\(look).png"))
            let empty = try store(filter: fresh.resolvingSymlinksInPath().path)
            empty.newAgent(in: empty.folderFilter)
            try render(ContentView(), store: empty, size: CGSize(width: 1100, height: 640), appearance: appearance,
                       to: folder.appendingPathComponent("filter-empty-folder-\(look).png"))
        }
    }

    private static var kept: [NSWindow] = []

    private func render<V: View>(_ view: V, store: AgentsStore, size: CGSize, appearance: NSAppearance.Name, to url: URL) throws {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.titled, .closable, .resizable], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.appearance = NSAppearance(named: appearance)
        window.contentView = NSHostingView(rootView: view.environment(store).frame(width: size.width, height: size.height, alignment: .top))
        window.setContentSize(size)
        window.orderFrontRegardless()
        RunLoop.main.run(until: Date().addingTimeInterval(1.2))
        let content = try XCTUnwrap(window.contentView)
        let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
        content.cacheDisplay(in: content.bounds, to: bitmap)
        try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: url)
        // Put away, not closed: closing the last window ends the test app.
        window.orderOut(nil)
        Self.kept.append(window)
    }
}
