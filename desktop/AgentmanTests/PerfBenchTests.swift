import AppKit
import Darwin
import SwiftUI
import Synchronization
import XCTest
@testable import Agentman

/// What the main window costs the Mac while it sits there: CPU time, the
/// instructions behind it, allocations and memory, over a stretch of
/// seconds each. The main window, offscreen in a window nobody sees, over a
/// store fed as the daemon feeds it: a dozen agents in six repositories, a
/// long conversation open, and while agents work, each one's session every
/// second and the open one's steps as it takes them. Nothing reaches a
/// daemon.
///
/// The stretches: idle, nothing working; busy, three agents working, one of
/// them open; busy with the window put away, as when the app is hidden; and
/// busy with the window still up but nowhere to be seen, as when others
/// cover it. The tests' app is never brought to the front, so all of them
/// are as with the app in the background. Runs with
/// TEST_RUNNER_AGENTMAN_PERF=<seconds a stretch>;
/// with TEST_RUNNER_AGENTMAN_PERF_ONLY=<stretch>, only that one, and its
/// process ID written to TEST_RUNNER_AGENTMAN_PERF_PID=<file> for a
/// profiler to find it by.
@MainActor
final class PerfBenchTests: XCTestCase {
    private var folders: [URL] = []

    override func tearDown() {
        for folder in folders { try? FileManager.default.removeItem(at: folder) }
        super.tearDown()
    }

    func testWhatTheWindowCosts() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let seconds = environment["AGENTMAN_PERF"].flatMap(Double.init) else { throw XCTSkip("for measuring") }
        let only = environment["AGENTMAN_PERF_ONLY"].flatMap { $0.isEmpty ? nil : $0 }
        if let file = environment["AGENTMAN_PERF_PID"] {
            try Data(String(getpid()).utf8).write(to: URL(fileURLWithPath: file))
        }
        Allocations.install()

        let bench = try Bench(folders: makeFolders())
        let window = host(ContentView().environment(bench.store))
        defer { window.orderOut(nil) }
        // Laid out, its rows made, and the first branches read.
        RunLoop.main.run(until: Date().addingTimeInterval(3))

        var report: [String] = []
        let onScreen = window.frame.origin
        func stretch(_ name: String, busy: Bool, shown: Bool = true, covered: Bool = false) {
            guard only == nil || only == name else { return }
            bench.working(busy)
            // Off every screen, a window is as hidden as one covered.
            window.setFrameOrigin(covered ? NSPoint(x: -20_000, y: -20_000) : onScreen)
            if shown { window.orderFrontRegardless() } else { window.orderOut(nil) }
            // Settled into it before it counts.
            RunLoop.main.run(until: Date().addingTimeInterval(2))
            let feed = busy ? bench.feed() : nil
            let before = Reading.now()
            RunLoop.main.run(until: Date().addingTimeInterval(seconds))
            let after = Reading.now()
            feed?.invalidate()
            report.append(after.since(before, name: name, seconds: seconds) + " · visible \(window.occlusionState.contains(.visible))")
        }
        stretch("idle", busy: false)
        stretch("busy", busy: true)
        stretch("hidden", busy: true, shown: false)
        stretch("occluded", busy: true, covered: true)
        bench.working(false)
        print("PERFBENCH the app in front: \(NSApp.isActive)")
        for line in report { print("PERFBENCH " + line) }
    }

    /// The bench's window, light and dark, with nothing working and then
    /// with its agents at work: a sidebar of a dozen agents with their
    /// branches, and a long conversation with tables, code and steps. For
    /// pictures before and after a change to what it costs, which must not
    /// change how it looks. Runs with TEST_RUNNER_AGENTMAN_SHOTS=<folder>.
    func testRenderTheBench() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let folders = try makeFolders()
        for dark in [false, true] {
            let bench = try Bench(folders: folders)
            let size = CGSize(width: 1560, height: 980)
            let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.titled, .closable, .resizable, .miniaturizable],
                                  backing: .buffered, defer: false)
            window.isReleasedWhenClosed = false
            window.alphaValue = 0
            window.ignoresMouseEvents = true
            window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
            window.contentView = NSHostingView(rootView: ContentView().environment(bench.store).frame(width: size.width, height: size.height))
            window.setContentSize(size)
            window.orderFrontRegardless()
            func shoot(_ name: String) throws {
                let content = try XCTUnwrap(window.contentView)
                let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
                content.cacheDisplay(in: content.bounds, to: bitmap)
                try XCTUnwrap(bitmap.representation(using: .png, properties: [:]))
                    .write(to: URL(fileURLWithPath: out).appendingPathComponent("bench-\(name)-\(dark ? "dark" : "light").png"))
            }
            // Its branches read and its rows laid out.
            RunLoop.main.run(until: Date().addingTimeInterval(3))
            try shoot("idle")
            // Three steps in: a step running in the conversation.
            bench.working(true)
            for _ in 0..<3 {
                bench.step()
                RunLoop.main.run(until: Date().addingTimeInterval(0.3))
            }
            RunLoop.main.run(until: Date().addingTimeInterval(0.6))
            try shoot("busy")
            // Put away rather than closed: closing the last titled window
            // ends the app the tests run in.
            window.orderOut(nil)
        }
    }

    /// The window as the user has it, but transparent and out of the way of
    /// the pointer.
    private func host(_ view: some View) -> NSWindow {
        let size = CGSize(width: 1560, height: 980)
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.contentView = NSHostingView(rootView: view.frame(width: size.width, height: size.height))
        window.setContentSize(size)
        window.orderFrontRegardless()
        return window
    }

    /// Six repositories on a branch each, as agents' folders are.
    private func makeFolders() throws -> [String] {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        folders.append(root)
        return try ["site", "api", "mobile", "infra", "docs", "billing"].enumerated().map { index, name in
            let folder = root.appendingPathComponent(name)
            try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
            try Data("# \(name)\n".utf8).write(to: folder.appendingPathComponent("README.md"))
            for args in [["init", "-q", "-b", "feature/\(name)-\(index)"], ["add", "-A"], ["commit", "-qm", "Start"]] {
                let process = Process()
                process.executableURL = URL(fileURLWithPath: "/usr/bin/git")
                process.arguments = ["-c", "user.name=Test", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"] + args
                process.currentDirectoryURL = folder
                process.standardOutput = FileHandle.nullDevice
                process.standardError = FileHandle.nullDevice
                try process.run()
                process.waitUntilExit()
            }
            return folder.resolvingSymlinksInPath().path
        }
    }
}

/// The agents, the open conversation, and what the daemon would send.
@MainActor
private final class Bench {
    let store: AgentsStore
    private var sessions: [Session]
    /// The ones that work while the stretch is busy; the first is open.
    private let busy = ["s0", "s1", "s2"]
    private var tick = 0
    private var serial = 0

    init(folders: [String]) throws {
        let client = DaemonClient(socketPath: "/nonexistent/agentman-perf.sock")
        // Answers nothing, at once: no daemon to wait for.
        client.answer = { _ in DaemonEvent(type: "error") }
        store = AgentsStore(client: client)
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        let kinds = ["claude", "codex", "opencode", "claude", "cursor", "kiro"]
        let names = ["Fix Safari sign-in", "Rate limit the uploads", "Write the changelog", "Move the build to Bazel",
                     "Tidy the settings page", "Answer the support queue", "Port the charts to Swift Charts",
                     "Find the memory leak", "Ship the onboarding emails", "Split the monorepo",
                     "Translate the app", "Check the analytics events"]
        sessions = (0..<12).map { index in
            Session(id: "s\(index)", kind: kinds[index % kinds.count], name: names[index], cwd: folders[index % folders.count],
                    state: index == 4 ? .waitingInput : .idle, inject: "tmux", startedAt: now - 7_200_000,
                    lastActivityAt: now - Int64(index) * 600_000, model: "claude-opus-5-5",
                    servers: index == 0 ? [Server(port: 3000, command: "node", title: "Site")] : nil, contextPercent: 20 + index,
                    question: index == 4 ? Question(id: "q1", prompt: "Run the migration on staging?", title: "Run the migration?",
                                                    options: [QuestionOption(key: "1", label: "Yes"), QuestionOption(key: "2", label: "No")]) : nil)
        }
        var event = DaemonEvent(type: "sessions")
        event.sessions = sessions
        store.apply(event)
        var thread = DaemonEvent(type: "messages")
        thread.sessionId = "s0"
        thread.messages = Self.conversation(now: now)
        store.apply(thread)
        store.selection = .agent("s0")
    }

    /// Working or not: the busy agents' state, as the daemon would send it.
    func working(_ on: Bool) {
        for id in busy {
            update(id) { $0.state = on ? .busy : .idle }
        }
        if !on { finishSteps() }
    }

    /// The daemon's news at its usual pace while the agents work: each one's
    /// session every second, the open one's steps every second or so, and a
    /// reply now and then.
    func feed() -> Timer {
        let timer = Timer(timeInterval: 0.5, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated { self?.step() }
        }
        RunLoop.main.add(timer, forMode: .default)
        return timer
    }

    /// Half a second of the daemon's news.
    func step() {
        tick += 1
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        if tick % 2 == 0 {
            for id in busy {
                update(id) { session in
                    session.lastActivityAt = now
                    session.contextPercent = ((session.contextPercent ?? 0) + 1) % 100
                }
            }
        }
        // The open agent: a step starts, finishes half a second later, and
        // every few steps it says something.
        if tick % 2 == 1 {
            serial += 1
            messages([Message(id: "live-\(serial)", sessionId: "s0", role: .tool, ts: now,
                              tool: ToolCall(name: "Bash", summary: "go test ./internal/... -run TestSessions", status: "running",
                                             title: "Ran the session tests"))])
        } else {
            finishSteps(now: now)
            if tick % 8 == 0 {
                serial += 1
                messages([Message(id: "live-\(serial)", sessionId: "s0", role: .assistant, ts: now + 1,
                                  text: "The tests pass now. I moved the **retry** into `Sweep`, so a pane that\n\n- vanishes mid-read\n- or comes back\n\nis counted once.")])
            }
        }
    }

    private func finishSteps(now: Int64 = Int64(Date().timeIntervalSince1970 * 1000)) {
        let running = (store.messages["s0"] ?? []).filter { $0.tool?.status == "running" }
        guard !running.isEmpty else { return }
        messages(running.map { call in
            var done = call
            done.tool?.status = "ok"
            done.text = "ok  \tgithub.com/agentman/internal/daemon\t1.92s"
            return done
        })
    }

    private func messages(_ messages: [Message]) {
        var event = DaemonEvent(type: "messages")
        event.sessionId = "s0"
        event.messages = messages
        store.apply(event)
    }

    private func update(_ id: String, _ change: (inout Session) -> Void) {
        guard let index = sessions.firstIndex(where: { $0.id == id }) else { return }
        change(&sessions[index])
        var event = DaemonEvent(type: "session_update")
        event.session = sessions[index]
        store.apply(event)
    }

    /// A long conversation: two hundred turns of asking, steps and answers.
    static func conversation(now: Int64) -> [Message] {
        var messages: [Message] = []
        var ts = now - 6 * 3_600_000
        func add(_ role: Role, _ text: String? = nil, tool: ToolCall? = nil) {
            ts += 7_000
            messages.append(Message(id: "m\(messages.count)", sessionId: "s0", role: role, ts: ts, text: text, tool: tool))
        }
        for turn in 0..<200 {
            switch turn % 4 {
            case 0:
                add(.user, "Can you look at why the sidebar flickers when an agent finishes? Turn \(turn).")
            case 1:
                add(.tool, "func sweep() {\n    for pane in panes { read(pane) }\n}", tool: ToolCall(name: "Read", summary: "internal/daemon/sweep.go", status: "ok", title: "Read sweep.go"))
                add(.tool, "@@ -1,3 +1,4 @@\n func sweep() {\n+    defer mu.Unlock()\n     for pane in panes {\n", tool: ToolCall(name: "Edit", summary: "internal/daemon/sweep.go", status: "ok", title: "Held the lock across the sweep"))
                add(.tool, "ok  \tgithub.com/agentman/internal/daemon\t2.31s", tool: ToolCall(name: "Bash", summary: "go test ./internal/daemon", status: "ok", title: "Ran the daemon tests"))
            case 2:
                add(.assistant, """
                Found it. Each sweep **replaced** the whole list, so every row redrew:

                1. `Sweep` reads every pane
                2. it sorts them by activity
                3. and sends the lot

                | Step | Before | After |
                | --- | --- | --- |
                | Sweep | 14 ms | 3 ms |
                | Send | every pane | what changed |

                ```go
                if !reflect.DeepEqual(old, new) {
                    send(new)
                }
                ```
                """)
            default:
                add(.tool, "src/Sidebar.swift\nsrc/AgentRow.swift", tool: ToolCall(name: "Grep", summary: "rg -l Sidebar", status: "ok", title: "Searched for the sidebar"))
                add(.assistant, "That's the change in place; the tests pass and nothing else moved.")
            }
        }
        return messages
    }
}

/// The counters a stretch is measured by, at one moment.
private struct Reading {
    var cpu: Double
    var mainThread: Double
    var children: Double
    var instructions: UInt64
    var allocations: Int
    var footprint: UInt64
    var heap: Int

    static func now() -> Reading {
        var own = rusage(), children = rusage()
        getrusage(RUSAGE_SELF, &own)
        getrusage(RUSAGE_CHILDREN, &children)
        var info = rusage_info_v4()
        _ = withUnsafeMutablePointer(to: &info) { pointer in
            pointer.withMemoryRebound(to: rusage_info_t?.self, capacity: 1) { proc_pid_rusage(getpid(), RUSAGE_INFO_V4, $0) }
        }
        return Reading(cpu: seconds(own.ru_utime) + seconds(own.ru_stime), mainThread: mainThreadTime(),
                       children: seconds(children.ru_utime) + seconds(children.ru_stime), instructions: info.ri_instructions,
                       allocations: Allocations.count, footprint: info.ri_phys_footprint, heap: Int(mstats().bytes_used))
    }

    func since(_ start: Reading, name: String, seconds: Double) -> String {
        String(format: "%@: CPU %.1f ms/s (main thread %.1f, git %.1f) · %.1f M instructions/s · %.0f allocations/s · footprint %.1f MB · heap %.1f MB",
               name.padding(toLength: 8, withPad: " ", startingAt: 0) as NSString,
               (cpu - start.cpu) * 1000 / seconds, (mainThread - start.mainThread) * 1000 / seconds,
               (children - start.children) * 1000 / seconds,
               Double(instructions - start.instructions) / seconds / 1e6, Double(allocations - start.allocations) / seconds,
               Double(footprint) / 1_048_576, Double(heap) / 1_048_576)
    }

    private static func seconds(_ time: timeval) -> Double { Double(time.tv_sec) + Double(time.tv_usec) / 1e6 }

    /// The main thread's own CPU time: SwiftUI's share, apart from the
    /// threads that draw and read.
    private static func mainThreadTime() -> Double {
        let thread = pthread_mach_thread_np(pthread_self())
        var info = thread_basic_info()
        var count = mach_msg_type_number_t(MemoryLayout<thread_basic_info>.size / MemoryLayout<integer_t>.size)
        let result = withUnsafeMutablePointer(to: &info) { pointer in
            pointer.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
                thread_info(thread, thread_flavor_t(THREAD_BASIC_INFO), $0, &count)
            }
        }
        guard result == KERN_SUCCESS else { return 0 }
        return Double(info.user_time.seconds) + Double(info.user_time.microseconds) / 1e6
            + Double(info.system_time.seconds) + Double(info.system_time.microseconds) / 1e6
    }
}

/// Every allocation the process makes, counted through malloc's logging
/// hook, which Instruments uses too.
private enum Allocations {
    static let counter = Atomic<Int>(0)

    static var count: Int { counter.load(ordering: .relaxed) }

    static func install() {
        _ = count
        guard let hook = dlsym(UnsafeMutableRawPointer(bitPattern: -2), "malloc_logger") else { return }
        hook.assumingMemoryBound(to: MallocLogger?.self).pointee = { type, _, _, _, _, _ in
            // MALLOC_LOG_TYPE_ALLOCATE
            if type & 2 != 0 { Allocations.counter.add(1, ordering: .relaxed) }
        }
    }

    typealias MallocLogger = @convention(c) (UInt32, UInt, UInt, UInt, UInt, UInt32) -> Void
}
