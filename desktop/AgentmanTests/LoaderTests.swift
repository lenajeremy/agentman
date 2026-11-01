import AppKit
import ImageIO
import Metal
import QuartzCore
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

    /// The moving ring at a moment, as Core Animation draws its layers: by
    /// its own renderer, as the window server would, at `time` on the
    /// clock the drawn ring reads.
    private func layers(at time: TimeInterval, size: CGFloat = 11, colors: [Color] = Palette.loader,
                        dark: Bool, scale: CGFloat) throws -> CGImage {
        let ring = LoaderLayer()
        ring.scale = scale
        var environment = EnvironmentValues()
        environment.colorScheme = dark ? .dark : .light
        ring.show(size: size, colors: colors, in: environment, clock: (media: 1000, reference: 0))
        let host = CALayer()
        host.isGeometryFlipped = true
        host.frame = CGRect(x: 0, y: 0, width: size * scale, height: size * scale)
        host.addSublayer(ring)
        ring.anchorPoint = .zero
        ring.position = .zero
        ring.setAffineTransform(CGAffineTransform(scaleX: scale, y: scale))

        let pixels = Int(size * scale)
        let device = try XCTUnwrap(MTLCreateSystemDefaultDevice())
        let queue = try XCTUnwrap(device.makeCommandQueue())
        let description = MTLTextureDescriptor.texture2DDescriptor(pixelFormat: .bgra8Unorm, width: pixels, height: pixels, mipmapped: false)
        description.usage = [.renderTarget, .shaderRead]
        description.storageMode = .shared
        let texture = try XCTUnwrap(device.makeTexture(descriptor: description))
        let renderer = CARenderer(mtlTexture: texture, options: [kCARendererColorSpace: CGColorSpace(name: CGColorSpace.sRGB)!,
                                                                kCARendererMetalCommandQueue: queue])
        renderer.layer = host
        renderer.bounds = host.frame
        // The renderer draws the layers as committed once they are its own.
        CATransaction.flush()
        renderer.beginFrame(atTime: 1000 + time, timeStamp: nil)
        renderer.addUpdate(renderer.bounds)
        renderer.render()
        renderer.endFrame()
        let done = try XCTUnwrap(queue.makeCommandBuffer())
        done.commit()
        done.waitUntilCompleted()
        var bytes = [UInt8](repeating: 0, count: pixels * pixels * 4)
        texture.getBytes(&bytes, bytesPerRow: pixels * 4, from: MTLRegionMake2D(0, 0, pixels, pixels), mipmapLevel: 0)
        let context = try XCTUnwrap(CGContext(data: &bytes, width: pixels, height: pixels, bitsPerComponent: 8, bytesPerRow: pixels * 4,
                                              space: CGColorSpace(name: CGColorSpace.sRGB)!,
                                              bitmapInfo: CGImageAlphaInfo.premultipliedFirst.rawValue | CGBitmapInfo.byteOrder32Little.rawValue))
        return try XCTUnwrap(context.makeImage())
    }

    /// An image's pixels in sRGB, premultiplied, row by row from the top.
    private func pixels(_ image: CGImage) throws -> [UInt8] {
        var bytes = [UInt8](repeating: 0, count: image.width * image.height * 4)
        let context = try XCTUnwrap(CGContext(data: &bytes, width: image.width, height: image.height, bitsPerComponent: 8,
                                              bytesPerRow: image.width * 4, space: CGColorSpace(name: CGColorSpace.sRGB)!,
                                              bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue))
        context.draw(image, in: CGRect(x: 0, y: 0, width: image.width, height: image.height))
        return bytes
    }

    // The ring Core Animation moves is the ring SwiftUI draws: at moments
    // through a whole round of its colours, its turn, its length and its
    // colours, light and dark, differing only where their edges are
    // smoothed.
    func testTheLayersMoveAsTheDrawingDoes() throws {
        for dark in [false, true] {
            for step in 0..<24 {
                // Between frames the window server is given, where it guesses most.
                let time = Double(step) * Loader.hold * 2 / 24 + 0.5 / LoaderLayer.coloursPerSecond
                let drawn = try pixels(image(Loader(at: time), look: dark ? .darkAqua : .aqua, scale: 8))
                let moved = try pixels(layers(at: time, dark: dark, scale: 8))
                XCTAssertEqual(drawn.count, moved.count)
                var off = 0, far = 0
                for index in stride(from: 0, to: drawn.count, by: 4) {
                    let difference = (0..<4).map { abs(Int(drawn[index + $0]) - Int(moved[index + $0])) }.max() ?? 0
                    if difference > 24 { off += 1 }
                    if difference > 96 { far += 1 }
                }
                let total = drawn.count / 4
                XCTAssertLessThan(Double(off) / Double(total), 0.04, "at \(time)s, \(dark ? "dark" : "light"): \(off) pixels off")
                XCTAssertLessThan(Double(far) / Double(total), 0.01, "at \(time)s, \(dark ? "dark" : "light"): \(far) pixels far off")
            }
        }
    }

    // In a window, the moving ring takes the window's appearance, and turns
    // to the other when the window does, as the drawn one did at its next
    // frame.
    func testTheMovingRingFollowsTheAppearance() throws {
        if NSWorkspace.shared.accessibilityDisplayShouldReduceMotion { throw XCTSkip("still with reduced motion") }
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 40, height: 40), styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.appearance = NSAppearance(named: .aqua)
        window.contentView = NSHostingView(rootView: Loader().padding(10))
        window.orderFrontRegardless()
        defer { window.orderOut(nil) }
        RunLoop.main.run(until: Date().addingTimeInterval(0.3))
        func ring(in view: NSView) -> LoaderView? {
            (view as? LoaderView) ?? view.subviews.lazy.compactMap(ring(in:)).first
        }
        let shown = try XCTUnwrap(ring(in: try XCTUnwrap(window.contentView)), "the moving ring, not a drawing")
        var light = EnvironmentValues()
        light.colorScheme = .light
        XCTAssertEqual(shown.ring.colors, Palette.loader.map { $0.resolve(in: light) })
        window.appearance = NSAppearance(named: .darkAqua)
        RunLoop.main.run(until: Date().addingTimeInterval(0.3))
        var dark = EnvironmentValues()
        dark.colorScheme = .dark
        XCTAssertEqual(shown.ring.colors, Palette.loader.map { $0.resolve(in: dark) })
        XCTAssertNotNil(shown.ring.animation(forKey: "turn") ?? shown.ring.sublayers?.first?.animation(forKey: "turn"), "it turns")
    }

    // The colours flow round the layers' ring as round the drawn one.
    func testTheLayersColoursFlowRoundTheRing() throws {
        func redAndBlue(at time: TimeInterval) throws -> (red: Int, blue: Int) {
            let bitmap = NSBitmapImageRep(cgImage: try layers(at: time, colors: [.red, .blue], dark: true, scale: 8))
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
            // The same moments drawn, over the layers that move on screen.
            let moments = (0..<12).map { Double($0) * Loader.hold / 4 }
            let dark = appearance == .darkAqua
            try sideBySide(try moments.map { try image(Loader(at: $0), look: appearance, scale: 8) },
                           over: try moments.map { try layers(at: $0, dark: dark, scale: 8) }, dark: dark,
                           to: URL(fileURLWithPath: out).appendingPathComponent("loader-layers-\(look).png"))
        }
    }

    /// Two rows of pictures on the page's colour, one over the other.
    private func sideBySide(_ top: [CGImage], over bottom: [CGImage], dark: Bool, to url: URL) throws {
        let cell = top.map(\.width).max() ?? 0, gap = 8
        let width = top.count * (cell + gap) + gap, height = 2 * (cell + gap) + gap
        let context = try XCTUnwrap(CGContext(data: nil, width: width, height: height, bitsPerComponent: 8, bytesPerRow: 0,
                                              space: CGColorSpace(name: CGColorSpace.sRGB)!,
                                              bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue))
        context.setFillColor(dark ? CGColor(gray: 0, alpha: 1) : CGColor(srgbRed: 0.98, green: 0.98, blue: 0.98, alpha: 1))
        context.fill(CGRect(x: 0, y: 0, width: width, height: height))
        for (row, images) in [top, bottom].enumerated() {
            for (column, picture) in images.enumerated() {
                context.draw(picture, in: CGRect(x: gap + column * (cell + gap), y: height - (row + 1) * (cell + gap),
                                                 width: picture.width, height: picture.height))
            }
        }
        let png = NSBitmapImageRep(cgImage: try XCTUnwrap(context.makeImage()))
        try XCTUnwrap(png.representation(using: .png, properties: [:])).write(to: url)
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
