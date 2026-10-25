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

    private func field(_ got: @escaping ([Attachment]) -> Void) -> MessageTextView {
        let view = MessageTextView()
        view.onImages = got
        return view
    }

    func testAScreenshotBecomesAnAttachment() {
        var got: [Attachment] = []
        let board = pasteboard { $0.setData(png(), forType: .png) }
        XCTAssertTrue(field { got = $0 }.takeImages(from: board))
        XCTAssertEqual(got.count, 1)
        XCTAssertTrue(Attachments.holdsImages(board))
    }

    func testTextIsLeftToTheTextView() {
        let board = pasteboard { $0.setString("hello", forType: .string) }
        XCTAssertFalse(field { _ in }.takeImages(from: board))
        XCTAssertFalse(Attachments.holdsImages(board))
    }

    // A box that takes no images leaves them to the text view.
    func testABoxWithoutImagesLeavesThemAlone() {
        let view = MessageTextView()
        let board = pasteboard { $0.setData(png(), forType: .png) }
        XCTAssertFalse(view.takeImages(from: board))
    }

    // Finder puts a copied file's icon beside it; a file that is not an
    // image pastes as its name, never as its icon.
    func testACopiedFileThatIsNotAnImageIsLeftAlone() throws {
        let video = FileManager.default.temporaryDirectory.appendingPathComponent("clip-\(UUID().uuidString).mp4")
        try Data([0, 0, 0, 24]).write(to: video)
        defer { try? FileManager.default.removeItem(at: video) }
        let board = pasteboard {
            $0.writeObjects([video as NSURL])
            $0.setData(NSWorkspace.shared.icon(forFile: video.path).tiffRepresentation!, forType: .tiff)
        }
        XCTAssertFalse(field { _ in }.takeImages(from: board))
        XCTAssertFalse(Attachments.holdsImages(board))
    }

    func testACopiedImageFileIsTheFile() throws {
        let file = FileManager.default.temporaryDirectory.appendingPathComponent("shot-\(UUID().uuidString).png")
        try png().write(to: file)
        defer { try? FileManager.default.removeItem(at: file) }
        var got: [Attachment] = []
        let board = pasteboard { $0.writeObjects([file as NSURL]) }
        XCTAssertTrue(Attachments.holdsImages(board))
        XCTAssertTrue(field { got = $0 }.takeImages(from: board))
        XCTAssertEqual(got.first?.image.size.width, 40)
    }

    // A plain text view greys out Paste when the pasteboard holds only an
    // image, so ⌘V with a screenshot never reached the box.
    func testPasteStaysAvailableForAScreenshot() {
        let saved = NSPasteboard.general.pasteboardItems?.map { item in
            item.types.compactMap { type in item.data(forType: type).map { (type, $0) } }
        } ?? []
        defer {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.writeObjects(saved.map { pairs in
                let item = NSPasteboardItem()
                for (type, data) in pairs { item.setData(data, forType: type) }
                return item
            })
        }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setData(png(), forType: .png)
        let paste = NSMenuItem(title: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        XCTAssertTrue(field { _ in }.validateUserInterfaceItem(paste))
        XCTAssertFalse(MessageTextView().validateUserInterfaceItem(paste), "a box without images keeps the text view's rule")
    }
}

final class SentMessageTests: XCTestCase {
    func testTheImagesTypedAheadOfAMessageAreSplitOff() {
        let sent = SentMessage("/Users/me/.agentman/images/claude-s1/a1.png /Users/me/.agentman/images/claude-s1/b2.jpg why is this cut off?")
        XCTAssertEqual(sent.images, ["/Users/me/.agentman/images/claude-s1/a1.png", "/Users/me/.agentman/images/claude-s1/b2.jpg"])
        XCTAssertEqual(sent.text, "why is this cut off?")
    }

    func testAnImageAloneLeavesNoWords() {
        let sent = SentMessage("/Users/me/.agentman/images/codex-x/c3.webp")
        XCTAssertEqual(sent.images.count, 1)
        XCTAssertEqual(sent.text, "")
    }

    // Only what the daemon filed is read from disk: not any path a message
    // happens to start with.
    func testOtherPathsStayWords() {
        XCTAssertEqual(SentMessage("/etc/passwd.png look").images, [])
        XCTAssertEqual(SentMessage("/Users/me/.agentman/images/../../secret.png x").images, [])
        XCTAssertEqual(SentMessage("/Users/me/.agentman/images/s/notes.txt x").images, [])
        XCTAssertEqual(SentMessage("see /Users/me/.agentman/images/s/a.png").text, "see /Users/me/.agentman/images/s/a.png")
    }
}
