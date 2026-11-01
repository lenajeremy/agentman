import Foundation

// Running am itself, as opposed to talking to am serve over its socket: the
// app runs the am it carries to set everything up (am setup), and the copy
// it manages to upgrade it (am upgrade). Each goes through AmRunner, so tests
// can run none of it.

/// What `am version --json` says of an am. The app reads it from the am it
/// carries and from the copy it manages, to tell whether that copy is behind.
struct AmVersion: Decodable, Equatable, Sendable {
    var version: String
    /// internal/protocol.APILevel, as this am would report it serving; nil
    /// from an am older than the field.
    var apiLevel: Int?
    /// How it was installed, as DaemonInfo.install says it.
    var install: String?
    /// Whether it is the copy this app manages, ~/.agentman/bin/am.
    var managed: Bool?
    /// The am, links followed.
    var path: String?
    /// The version of the app it came with.
    var appVersion: String?
}

/// One line of `am setup -json`: a step as it ends, or, last, the summary
/// (step "summary"). See cmd/am/setup.go.
struct SetupLine: Decodable, Equatable, Sendable {
    var step: String
    /// changed, unchanged, skipped or failed; the summary has none.
    var status: String?
    /// What happened, in am's words, for a person.
    var detail: String?
    var path: String?
    var version: String?
    var apiLevel: Int?
    /// What the hooks and tools steps did for each agent.
    var agents: [AgentChange]?

    // The summary's own.
    var ok: Bool?
    /// The am the daemon runs, and whether it is the copy the app manages.
    var binary: String?
    var managed: Bool?
    var install: String?
    var socket: String?
    var log: String?
    /// The steps that failed.
    var failed: [String]?

    var isSummary: Bool { step == "summary" }
    var didFail: Bool { status == "failed" }

    struct AgentChange: Decodable, Equatable, Sendable {
        var agent: String
        var status: String
        var path: String?
        var error: String?
    }
}

/// How a program the app ran ended.
struct ProgramExit: Equatable, Sendable {
    var status: Int32
    /// The last of what it wrote to standard error: why it failed, as am
    /// says it ("am: upgrade: …").
    var error: String

    var succeeded: Bool { status == 0 }
}

/// Running am. The app's runs the real program; tests give one that runs
/// nothing and answers with canned lines, so no test can set up, upgrade or
/// restart anything on the Mac running them.
protocol AmRunner: Sendable {
    /// `<am> version --json`.
    func version(of am: String) async throws -> AmVersion
    /// `<am> <arguments…>`, handing over each line it prints as it prints it.
    func run(_ am: String, _ arguments: [String], line: @escaping @Sendable (String) -> Void) async -> ProgramExit
}

/// The real am.
struct ProcessAmRunner: AmRunner {
    struct Unreadable: LocalizedError {
        let output: String
        var errorDescription: String? { "am didn't say its version: \(output)" }
    }

    func version(of am: String) async throws -> AmVersion {
        let output = Subprocess.Collected()
        let exit = await Subprocess.run(am, ["version", "--json"], timeout: 10) { output.add($0) }
        let text = output.text
        guard exit.succeeded, let version = try? JSONDecoder().decode(AmVersion.self, from: Data(text.utf8)) else {
            throw Unreadable(output: exit.error.isEmpty ? text : exit.error)
        }
        return version
    }

    func run(_ am: String, _ arguments: [String], line: @escaping @Sendable (String) -> Void) async -> ProgramExit {
        // The tests run inside the app: whatever a test does, the real am
        // only ever says its version there.
        guard !AgentmanApp.underTests else {
            return ProgramExit(status: -1, error: "am isn't run under tests")
        }
        return await Subprocess.run(am, arguments, timeout: nil, line: line)
    }
}

/// A program run as a child of the app, with nothing on its standard input,
/// so it knows no one is there to answer.
enum Subprocess {
    static func run(_ program: String, _ arguments: [String], timeout: TimeInterval?,
                    line: @escaping @Sendable (String) -> Void) async -> ProgramExit {
        await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                let process = Process()
                process.executableURL = URL(fileURLWithPath: program)
                process.arguments = arguments
                process.standardInput = FileHandle.nullDevice
                let out = Pipe(), err = Pipe()
                process.standardOutput = out
                process.standardError = err
                do {
                    try process.run()
                } catch {
                    continuation.resume(returning: ProgramExit(status: -1, error: error.localizedDescription))
                    return
                }
                if let timeout {
                    DispatchQueue.global().asyncAfter(deadline: .now() + timeout) {
                        if process.isRunning { process.terminate() }
                    }
                }
                // Standard error read beside standard output, so neither pipe
                // filling up can stall the program.
                let errors = Collected()
                let reading = DispatchGroup()
                reading.enter()
                DispatchQueue.global(qos: .utility).async {
                    errors.add(String(decoding: err.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self))
                    reading.leave()
                }
                var framer = LineFramer()
                while true {
                    let data = out.fileHandleForReading.availableData
                    if data.isEmpty { break }
                    for each in framer.append(data) {
                        line(String(decoding: each, as: UTF8.self))
                    }
                }
                process.waitUntilExit()
                reading.wait()
                continuation.resume(returning: ProgramExit(status: process.terminationStatus, error: lastLines(errors.text)))
            }
        }
    }

    /// The end of what a program wrote to standard error: am's error is its
    /// last line.
    static func lastLines(_ text: String) -> String {
        text.split(whereSeparator: \.isNewline).suffix(3).joined(separator: "\n")
    }

    /// Text gathered from another thread.
    final class Collected: @unchecked Sendable {
        private let lock = NSLock()
        private var parts: [String] = []

        func add(_ part: String) {
            lock.lock()
            parts.append(part)
            lock.unlock()
        }

        var text: String {
            lock.lock()
            defer { lock.unlock() }
            return parts.joined(separator: "\n")
        }
    }
}
