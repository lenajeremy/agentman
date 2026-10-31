import AppKit
import XCTest
@testable import Agentman

/// What keeps a long conversation quick to scroll: titles that redraw their
/// own run, rows made once per change, and pictures made small and kept.
@MainActor
final class ConversationSpeedTests: XCTestCase {
    private func store(_ messages: String) throws -> AgentsStore {
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-speed.sock"))
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(messages.utf8)))
        return store
    }

    private static let run = """
    {"type":"messages","sessionId":"s","messages":[
     {"id":"1","sessionId":"s","role":"user","ts":1,"text":"Fix the build"},
     {"id":"2","sessionId":"s","role":"tool","ts":2,"tool":{"name":"Read","summary":"Package.swift","status":"ok"}},
     {"id":"3","sessionId":"s","role":"tool","ts":3,"tool":{"name":"Bash","summary":"swift build","status":"ok"}},
     {"id":"4","sessionId":"s","role":"assistant","ts":4,"text":"Done."},
     {"id":"5","sessionId":"s","role":"tool","ts":5,"tool":{"name":"Read","summary":"README.md","status":"ok"}},
     {"id":"6","sessionId":"s","role":"tool","ts":6,"tool":{"name":"Bash","summary":"swift test","status":"ok"}}
    ]}
    """

    // A title arriving for one run is that run's alone: one dictionary every
    // run read redrew them all at each title.
    func testEachRunOfToolsHasItsOwnTitle() throws {
        let store = try store(Self.run)
        let messages = store.messages["s"] ?? []
        let first = Array(messages[1...2]), second = Array(messages[4...5])
        XCTAssertTrue(store.toolTitle(first) === store.toolTitle(first))
        XCTAssertFalse(store.toolTitle(first) === store.toolTitle(second))
        store.toolTitle(first).text = "Built the package"
        XCTAssertNil(store.toolTitle(second).text)
    }

    // Asking for a title that can't be had (no daemon here) isn't repeated
    // at every scroll past the run.
    func testAFailedTitleIsNotAskedForAgainAtOnce() async throws {
        let store = try store(Self.run)
        let calls = Array((store.messages["s"] ?? [])[1...2])
        await store.titleTools(calls, said: "")
        let started = Date()
        await store.titleTools(calls, said: "")
        XCTAssertLessThan(Date().timeIntervalSince(started), 0.05, "asked again straight away")
        XCTAssertNil(store.toolTitle(calls).text)
    }

    // The rows are made again only when the messages change, not at every
    // redraw while the conversation scrolls.
    func testRowsAreMadeOnlyWhenMessagesChange() throws {
        let store = try store(Self.run)
        let rows = store.threadItems("s")
        XCTAssertEqual(rows.map(\.id), ThreadItem.items(from: store.messages["s"] ?? []).map(\.id))
        XCTAssertEqual(store.threadItems("s").map(\.id), rows.map(\.id))
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data("""
        {"type":"messages","sessionId":"s","messages":[{"id":"7","sessionId":"s","role":"assistant","ts":7,"text":"Tests pass."}]}
        """.utf8)))
        XCTAssertEqual(store.threadItems("s").count, rows.count + 1)
    }

    // A screenshot shown as a thumbnail is decoded at the thumbnail's size,
    // off the main thread, and kept for the next time its row shows.
    func testThumbnailsAreSmallAndKept() async throws {
        let folder = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: folder) }
        let path = folder.appendingPathComponent("shot.png").path
        let rep = try XCTUnwrap(NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: 3000, pixelsHigh: 2000, bitsPerSample: 8,
                                                 samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                                 colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0))
        try XCTUnwrap(rep.representation(using: .png, properties: [:])).write(to: URL(fileURLWithPath: path))

        let made = await Thumbnail.file(path, height: 110)
        let small = try XCTUnwrap(made)
        XCTAssertEqual(small.size.height, 110, accuracy: 1)
        XCTAssertEqual(small.size.width, 165, accuracy: 1, "its shape kept")
        let pixels = try XCTUnwrap(small.representations.first)
        XCTAssertLessThanOrEqual(pixels.pixelsHigh, 221, "decoded at twice the points, not 2000 pixels")
        XCTAssertNotNil(Thumbnail.cached(Thumbnail.key(path, "110")), "kept for the next time")
    }
}
