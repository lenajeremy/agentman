import XCTest
@testable import Agentman

@MainActor
final class SlashCommandsTests: XCTestCase {
    private let commands: [SlashCommand] = [
        SlashCommand(name: "compact", argumentHint: "[instructions]", source: "builtin"),
        SlashCommand(name: "usage", source: "builtin", panel: true),
        SlashCommand(name: "mcp", argumentHint: "[reconnect|enable|disable <server>]", source: "builtin", panel: true),
        SlashCommand(name: "model", argumentHint: "<model>", source: "builtin"),
        SlashCommand(name: "help", source: "builtin", panel: true),
        SlashCommand(name: "hello", argumentHint: "[name]", source: "project"),
        SlashCommand(name: "code-review", source: "bundled"),
        SlashCommand(name: "vercel:deploy", source: "plugin"),
        SlashCommand(name: "add-dir", source: "personal"),
    ]

    // As the agent's own menu ranks them, and as the phone does.
    func testMatchesLeadWithTheNameThenWordsWithin() {
        func names(_ query: String) -> [String] { SlashCommands.match(commands, query).map(\.name) }
        XCTAssertEqual(names("hel"), ["help", "hello"])
        XCTAssertEqual(names("deploy"), ["vercel:deploy"])
        XCTAssertEqual(names("adddir"), ["add-dir"])
        XCTAssertEqual(names("review"), ["code-review"])
        XCTAssertEqual(names("").first, "compact")
        XCTAssertEqual(names("zzz"), [])
    }

    func testBarePanelCommandsAreReadAndBareModelIsNotTyped() {
        XCTAssertEqual(SlashCommands.send("/usage", commands: commands), .panel("usage"))
        XCTAssertEqual(SlashCommands.send(" /mcp ", commands: commands), .panel("mcp"))
        XCTAssertEqual(SlashCommands.send("/mcp reconnect railway", commands: commands), .message)
        XCTAssertEqual(SlashCommands.send("/model", commands: commands), .model)
        XCTAssertEqual(SlashCommands.send("/model sonnet", commands: commands), .message)
        XCTAssertEqual(SlashCommands.send("/compact", commands: commands), .message)
        XCTAssertEqual(SlashCommands.send("hello", commands: commands), .message)
    }

    // Open while the box holds "/" and a name being typed; picking puts the
    // name in with room for its argument.
    func testTheMenuFollowsTheBoxAndPicksIntoIt() async throws {
        let slash = SlashCommands { [commands] in .success(commands) }
        let field = NSTextView()
        slash.textView = field
        slash.update(text: "/comp")
        for _ in 0..<50 where slash.results.isEmpty { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertTrue(slash.isOpen)
        XCTAssertEqual(slash.results.map(\.name), ["compact"])
        slash.choose()
        XCTAssertEqual(field.string, "/compact ")
        XCTAssertFalse(slash.isOpen)

        slash.update(text: "/compact now")
        XCTAssertFalse(slash.isOpen, "closed once the name is followed by a space")
        slash.update(text: "see /tmp")
        XCTAssertFalse(slash.isOpen)
    }

    func testOnlyAClaudeInATerminalTakesCommands() throws {
        let decode = { (json: String) in try JSONDecoder().decode(Session.self, from: Data(json.utf8)) }
        let base = #""id":"s","name":"n","cwd":"/","state":"idle","lastActivity":0"#
        XCTAssertTrue(SlashCommands.takes(try decode(#"{"kind":"claude","inject":"tmux","# + base + "}")))
        XCTAssertFalse(SlashCommands.takes(try decode(#"{"kind":"claude","inject":"hook","# + base + "}")))
        XCTAssertFalse(SlashCommands.takes(try decode(#"{"kind":"codex","inject":"tmux","# + base + "}")))
        XCTAssertFalse(SlashCommands.takes(nil))
    }
}

@MainActor
final class TakeoverTests: XCTestCase {
    private func session(_ id: String, previous: String? = nil) throws -> Session {
        var json = #"{"id":"\#(id)","kind":"claude","name":"n","cwd":"/","state":"idle","inject":"tmux""#
        if let previous { json += #","previous":"\#(previous)""# }
        return try JSONDecoder().decode(Session.self, from: Data((json + "}").utf8))
    }

    // /clear in the agent's terminal starts a new conversation that names
    // the old one: the window goes with it, as Claude's own app does.
    func testTheWindowFollowsAClearedConversation() throws {
        let store = AgentsStore()
        var first = DaemonEvent(type: "sessions")
        first.sessions = [try session("claude:a")]
        store.apply(first)
        store.selection = .agent("claude:a")

        var cleared = DaemonEvent(type: "session_update")
        cleared.session = try session("claude:b", previous: "claude:a")
        store.apply(cleared)
        XCTAssertEqual(store.selection, .agent("claude:b"))
        XCTAssertEqual(store.successor(of: "claude:a"), "claude:b")

        // Another agent's conversation changing moves nothing.
        store.selection = .agent("claude:b")
        var elsewhere = DaemonEvent(type: "session_update")
        elsewhere.session = try session("claude:d", previous: "claude:c")
        store.apply(elsewhere)
        XCTAssertEqual(store.selection, .agent("claude:b"))
    }
}

@MainActor
final class FolderMentionsTests: XCTestCase {
    // A thread outside an editor offers its agent's folder's files for `@`:
    // as git lists them, ignored ones left out, and what changed first.
    func testAThreadsFolderIsIndexedForMentions() async throws {
        let repo = FileManager.default.temporaryDirectory.appendingPathComponent("am-mentions-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: repo) }
        func write(_ path: String, _ body: String) throws {
            let url = repo.appendingPathComponent(path)
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            try body.write(to: url, atomically: true, encoding: .utf8)
        }
        func git(_ args: String...) async throws {
            let out = await Git.run(["-c", "user.email=t@t", "-c", "user.name=t"] + args, in: repo)
            XCTAssertNotNil(out, "git \(args)")
        }
        try write(".gitignore", "node_modules/\n")
        try write("app/login.tsx", "x")
        try write("node_modules/pad/index.js", "x")
        try await git("init", "-q")
        try await git("add", ".")
        try await git("commit", "-q", "-m", "first")
        try write("app/login.tsx", "changed")
        try write("notes/login-bug.md", "new")

        let index = FolderIndex(root: repo)
        await index.load()
        XCTAssertTrue(index.files.contains("app/login.tsx"))
        XCTAssertTrue(index.files.contains("notes/login-bug.md"))
        XCTAssertFalse(index.files.contains("node_modules/pad/index.js"))
        XCTAssertEqual(Set(index.changed), ["app/login.tsx", "notes/login-bug.md"])
        // Sent, a picked file goes as a path inside the agent's folder.
        let mentions = FileMentions.folder(repo.path)
        XCTAssertEqual(mentions.resolve("see @app/login.tsx please", folder: repo.path), "see @app/login.tsx please")
    }
}
