import AppKit
import XCTest
@testable import Agentman

@MainActor
final class PasteTests: XCTestCase {
    private func pasteboard(_ fill: (NSPasteboard) -> Void) -> NSPasteboard {
        let board = NSPasteboard(name: NSPasteboard.Name("agentman-test-\(UUID().uuidString)"))
        board.clearContents()
        fill(board)
        return board
    }

    private func png() -> Data {
        let image = NSImage(size: NSSize(width: 40, height: 30), flipped: false) { rect in
            NSColor.systemBlue.setFill()
            rect.fill()
            return true
        }
        let rep = NSBitmapImageRep(data: image.tiffRepresentation!)!
        return rep.representation(using: .png, properties: [:])!
    }

    func testTheKeyThatPastes() {
        XCTAssertTrue(PasteMonitor.isPasteKey(characters: "v", keyCode: 9))
        XCTAssertTrue(PasteMonitor.isPasteKey(characters: "V", keyCode: 9))
        XCTAssertTrue(PasteMonitor.isPasteKey(characters: "м", keyCode: 9))
        XCTAssertTrue(PasteMonitor.isPasteKey(characters: "v", keyCode: 47))
        XCTAssertFalse(PasteMonitor.isPasteKey(characters: "k", keyCode: 9))
        XCTAssertFalse(PasteMonitor.isPasteKey(characters: "c", keyCode: 8))
    }

    // The bug: the monitor kept the box's focus from when it was installed,
    // before the box took focus, so it never took a paste.
    func testAPasteGoesToTheBoxThatIsFocusedNow() {
        let monitor = PasteMonitor()
        var got: [Attachment] = []
        monitor.onImages = { got = $0 }
        let board = pasteboard { $0.setData(png(), forType: .png) }
        XCTAssertFalse(monitor.take(from: board))
        monitor.active = true
        XCTAssertTrue(monitor.take(from: board))
        XCTAssertEqual(got.count, 1)
    }

    func testTextIsLeftToTheTextField() {
        let monitor = PasteMonitor()
        monitor.active = true
        let board = pasteboard { $0.setString("hello", forType: .string) }
        XCTAssertFalse(monitor.take(from: board))
    }
}
