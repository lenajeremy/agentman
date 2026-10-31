import AppKit
import ImageIO
import SwiftUI
import UniformTypeIdentifiers
import XCTest
@testable import Agentman

@MainActor
final class LoaderTests: XCTestCase {
    private func image(_ view: some View, look: NSAppearance.Name, scale: CGFloat) throws -> CGImage {
        let renderer = ImageRenderer(content: view.environment(\.colorScheme, look == .darkAqua ? .dark : .light))
        renderer.scale = scale
        var image: CGImage?
        NSAppearance(named: look)?.performAsCurrentDrawingAppearance { image = renderer.cgImage }
        return try XCTUnwrap(image)
    }

    /// How many of the ring's pixels are more red than blue, and the other way.
    private func redAndBlue(at time: TimeInterval) throws -> (red: Int, blue: Int) {
        let bitmap = NSBitmapImageRep(cgImage: try image(Loader(colors: [.red, .blue], at: time), look: .darkAqua, scale: 8))
        var red = 0, blue = 0
        for x in 0..<bitmap.pixelsWide {
            for y in 0..<bitmap.pixelsHigh {
                guard let color = bitmap.colorAt(x: x, y: y)?.usingColorSpace(.sRGB), color.alphaComponent > 0.8 else { continue }
                if color.redComponent > color.blueComponent + 0.3 { red += 1 }
                if color.blueComponent > color.redComponent + 0.3 { blue += 1 }
            }
        }
        return (red, blue)
    }

    // The colours aren't pinned to the ring: it is all the first colour,
    // then the next comes in at its head, then it is all the next.
    func testTheColoursFlowRoundTheRing() throws {
        let first = try redAndBlue(at: 0)
        XCTAssertGreaterThan(first.red, 50)
        XCTAssertEqual(first.blue, 0, "all the first colour")
        let turning = try redAndBlue(at: Loader.hold * 0.62)
        XCTAssertGreaterThan(turning.red, 20, "the tail still the first")
        XCTAssertGreaterThan(turning.blue, 20, "the head already the next")
        let next = try redAndBlue(at: Loader.hold)
        XCTAssertGreaterThan(next.blue, 50)
        XCTAssertEqual(next.red, 0, "all the next colour")
    }

    // For a look at it moving: at its size beside a tool run's line and the
    // sidebar's Working, and close up, light and dark, as GIFs. Runs with
    // TEST_RUNNER_AGENTMAN_SHOTS=<folder>.
    func testRenderTheLoader() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        // Long enough for the colours to come round whole.
        let fps = 30.0, frames = Int(Loader.hold * Double(Palette.loader.count) * 2 * fps)
        func scene(_ time: TimeInterval) -> some View {
            VStack(alignment: .leading, spacing: 14) {
                HStack(spacing: 7) {
                    Loader(at: time)
                    Text("Put Working under Pinned, keep Archived at the foot, and re-render")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                }
                HStack(spacing: 4) {
                    Loader(size: 9, at: time)
                    Text("Working")
                }
                .font(TextStyle.label)
                .foregroundStyle(Palette.workingText)
            }
            .padding(16)
            .frame(width: 520, alignment: .leading)
            .background(Palette.paper)
        }
        func big(_ time: TimeInterval) -> some View {
            Loader(at: time).padding(2).background(Palette.paper)
        }
        for (look, appearance) in [("light", NSAppearance.Name.aqua), ("dark", .darkAqua)] {
            try gif((0..<frames).map { try image(scene(Double($0) / fps), look: appearance, scale: 2) },
                    to: URL(fileURLWithPath: out).appendingPathComponent("loader-\(look).gif"), delay: 1 / fps)
            try gif((0..<frames).map { try image(big(Double($0) / fps), look: appearance, scale: 12) },
                    to: URL(fileURLWithPath: out).appendingPathComponent("loader-big-\(look).gif"), delay: 1 / fps)
            // Moments a quarter of a colour apart, for a still look.
            let strip = HStack(spacing: 0) { ForEach(0..<12, id: \.self) { big(Double($0) * Loader.hold / 4) } }
            let png = NSBitmapImageRep(cgImage: try image(strip, look: appearance, scale: 6))
            try XCTUnwrap(png.representation(using: .png, properties: [:]))
                .write(to: URL(fileURLWithPath: out).appendingPathComponent("loader-strip-\(look).png"))
        }
    }

    private func gif(_ frames: [CGImage], to url: URL, delay: Double) throws {
        let destination = try XCTUnwrap(CGImageDestinationCreateWithURL(url as CFURL, UTType.gif.identifier as CFString, frames.count, nil))
        CGImageDestinationSetProperties(destination, [kCGImagePropertyGIFDictionary: [kCGImagePropertyGIFLoopCount: 0]] as CFDictionary)
        for frame in frames {
            CGImageDestinationAddImage(destination, frame, [kCGImagePropertyGIFDictionary: [kCGImagePropertyGIFDelayTime: delay]] as CFDictionary)
        }
        XCTAssertTrue(CGImageDestinationFinalize(destination))
    }
}
