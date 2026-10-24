import AppKit
import XCTest
@testable import Agentman

@MainActor
final class AttachmentsTests: XCTestCase {
    private func image(width: Int, height: Int, noise: Bool = false) -> NSImage {
        let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: width, pixelsHigh: height, bitsPerSample: 8,
                                   samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
                                   bytesPerRow: 0, bitsPerPixel: 0)!
        if noise, let data = rep.bitmapData {
            // Noise doesn't compress, as a photo doesn't.
            for i in 0..<(rep.bytesPerRow * height) { data[i] = UInt8.random(in: 0...255) }
        }
        let image = NSImage(size: NSSize(width: width, height: height))
        image.addRepresentation(rep)
        return image
    }

    // A large image is shrunk to the size the model reads; a screenshot-like
    // one stays PNG.
    func testImagesAreShrunkToWhatTheModelReads() throws {
        let big = try XCTUnwrap(Attachments.make(image(width: 4000, height: 2000)))
        let rep = try XCTUnwrap(NSBitmapImageRep(data: big.data))
        XCTAssertEqual(rep.pixelsWide, 1568)
        XCTAssertEqual(rep.pixelsHigh, 784)
        XCTAssertEqual(big.mime, "image/png", "a flat image compresses well as PNG")

        let photo = try XCTUnwrap(Attachments.make(image(width: 1500, height: 1500, noise: true)))
        XCTAssertEqual(photo.mime, "image/jpeg", "what PNG can't keep small goes as JPEG")
    }

    // Four full-size screenshots go as they are; even four images that
    // can't be compressed stay within one frame to the daemon.
    func testAMessageStaysWithinItsBudget() throws {
        let screenshots = (0..<4).compactMap { _ in Attachments.make(image(width: 1568, height: 1000)) }
        let sent = Attachments.encoded(screenshots)
        XCTAssertEqual(sent.map(\.data), screenshots.map { $0.data.base64EncodedString() }, "screenshots weren't squeezed")

        let photos = (0..<4).compactMap { _ in Attachments.make(image(width: 1568, height: 1568, noise: true)) }
        XCTAssertEqual(photos.count, 4)
        let encoded = Attachments.encoded(photos)
        XCTAssertEqual(encoded.count, 4)
        XCTAssertLessThanOrEqual(encoded.reduce(0) { $0 + $1.data.count }, Attachments.budget)
    }

    // Pasting attaches images, and leaves text to paste as text.
    func testPastingFindsImagesButNotText() throws {
        let board = NSPasteboard(name: NSPasteboard.Name("agentman-test-\(UUID().uuidString)"))
        board.clearContents()
        board.writeObjects(["just words" as NSString])
        XCTAssertTrue(Attachments.from(pasteboard: board).isEmpty)

        board.clearContents()
        board.writeObjects([image(width: 40, height: 30)])
        XCTAssertEqual(Attachments.from(pasteboard: board).count, 1)

        let file = FileManager.default.temporaryDirectory.appendingPathComponent("paste-\(UUID().uuidString).png")
        let rep = try XCTUnwrap(image(width: 20, height: 20).representations.first as? NSBitmapImageRep)
        try XCTUnwrap(rep.representation(using: .png, properties: [:])).write(to: file)
        board.clearContents()
        board.writeObjects([file as NSURL])
        XCTAssertEqual(Attachments.from(pasteboard: board).count, 1, "an image file copied in Finder")
        board.releaseGlobally()
    }
}
