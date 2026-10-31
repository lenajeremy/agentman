import AppKit
import CoreImage
import SwiftUI
import XCTest
@testable import Agentman

/// Settings › Devices: the daemon's list read and kept current, devices named
/// and dated, the QR code a phone can read, and pictures of the page and the
/// sheet, which run with TEST_RUNNER_AGENTMAN_SHOTS=<folder> set.
@MainActor
final class DevicesTests: XCTestCase {
    private static let now = Int64(Date().timeIntervalSince1970 * 1000)

    static let devices = """
    {"type":"devices","devices":[
     {"id":"a","name":"Jeremiah's iPhone","model":"iPhone 17 Pro","platform":"ios","os":"26.1","firstSeen":1760227200000,"lastSeen":\(now),"connected":true},
     {"id":"b","name":"iPad Air","model":"iPad Air","platform":"ios","firstSeen":1761091200000,"lastSeen":\(now - 3 * 3_600_000)},
     {"id":"c","firstSeen":1759622400000,"lastSeen":\(now - 2 * 86_400_000)}
    ]}
    """

    static let code = PairCode(code: "0481527396", url: "agentman://pair?token=pt_9f2c41d07be35a68c1e4b0d27f5a93c6",
                               expiresAt: now + 42_000)

    private func store() throws -> AgentsStore {
        let store = AgentsStore(client: DaemonClient(socketPath: "/nonexistent/agentman-devices.sock"))
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(Self.devices.utf8)))
        return store
    }

    func testTheDaemonsListIsKeptCurrent() throws {
        let store = try store()
        XCTAssertEqual(store.devices?.map(\.id), ["a", "b", "c"])
        XCTAssertEqual(store.devices?.first?.connected, true)
        store.apply(try JSONDecoder().decode(DaemonEvent.self, from: Data(#"{"type":"devices"}"#.utf8)))
        XCTAssertEqual(store.devices, [], "an empty list leaves the field out")
    }

    func testAskingForACode() throws {
        let event = try JSONDecoder().decode(DaemonEvent.self, from: Data("""
        {"type":"pair_code","pairCode":{"code":"0481527396","url":"agentman://pair?token=t","expiresAt":1761955200000}}
        """.utf8))
        XCTAssertEqual(event.pairCode?.code, "0481527396")
        XCTAssertEqual(event.pairCode?.expires, Date(timeIntervalSince1970: 1_761_955_200))
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(DaemonRequest(.pairCode))) as? [String: Any])
        XCTAssertEqual(json["type"] as? String, "pair_code")
        XCTAssertEqual(PairingPanel.grouped("0481527396"), "04815 27396")
        XCTAssertEqual(PairingPanel.grouped("123"), "123")
    }

    func testDevicesAreNamedAndDated() throws {
        let devices = try XCTUnwrap(try store().devices)
        XCTAssertEqual(devices.map(DeviceLabel.name), ["Jeremiah's iPhone", "iPad Air", "Phone"])
        XCTAssertEqual(devices.map(DeviceLabel.symbol), ["iphone", "ipad", "smartphone"])
        let now = Date(timeIntervalSince1970: Double(Self.now) / 1000)
        XCTAssertTrue(DeviceLabel.detail(devices[0], now: now).hasPrefix("Connected · Added "))
        XCTAssertTrue(DeviceLabel.detail(devices[1], now: now).hasPrefix("Last connected 3h ago · Added "))
        XCTAssertTrue(DeviceLabel.detail(devices[2], now: now).hasPrefix("Last connected 2d ago · Added "))
    }

    // What the phone's camera reads is what the daemon gave.
    func testTheQRCodeReadsBack() throws {
        let image = try XCTUnwrap(QRCode.image(Self.code.url))
        var rect = CGRect(origin: .zero, size: image.size)
        let cgImage = try XCTUnwrap(image.cgImage(forProposedRect: &rect, context: nil, hints: nil))
        // Scaled up with a quiet margin, as a camera sees it.
        let side = 600
        let context = try XCTUnwrap(CGContext(data: nil, width: side, height: side, bitsPerComponent: 8, bytesPerRow: 0,
                                              space: CGColorSpaceCreateDeviceGray(), bitmapInfo: CGImageAlphaInfo.none.rawValue))
        context.setFillColor(gray: 1, alpha: 1)
        context.fill(CGRect(x: 0, y: 0, width: side, height: side))
        context.interpolationQuality = .none
        context.draw(cgImage, in: CGRect(x: 50, y: 50, width: side - 100, height: side - 100))
        let scaled = try XCTUnwrap(context.makeImage())
        let detector = try XCTUnwrap(CIDetector(ofType: CIDetectorTypeQRCode, context: nil, options: [CIDetectorAccuracy: CIDetectorAccuracyHigh]))
        let found = detector.features(in: CIImage(cgImage: scaled)).compactMap { ($0 as? CIQRCodeFeature)?.messageString }
        XCTAssertEqual(found, [Self.code.url])
    }

    // MARK: Pictures

    func testRenderDevices() throws {
        guard let out = ProcessInfo.processInfo.environment["AGENTMAN_SHOTS"] else { throw XCTSkip("for screenshots") }
        let folder = URL(fileURLWithPath: out)
        let defaults = UserDefaults.standard
        defer { defaults.removeObject(forKey: "settingsTab") }
        let connected = try XCTUnwrap(try store().devices?.first)
        for dark in [false, true] {
            let look = dark ? "dark" : "light"
            defaults.set("devices", forKey: "settingsTab")
            try render(SettingsView(), size: CGSize(width: 800, height: 580), dark: dark, store: try store(),
                       to: folder.appendingPathComponent("devices-\(look).png"))
            let empty = try store()
            empty.devices = []
            try render(SettingsView(), size: CGSize(width: 800, height: 580), dark: dark, store: empty,
                       to: folder.appendingPathComponent("devices-empty-\(look).png"))
            defaults.set("general", forKey: "settingsTab")
            try render(SettingsView(), size: CGSize(width: 800, height: 580), dark: dark, store: try store(),
                       to: folder.appendingPathComponent("settings-general-\(look).png"))
            // The page as the window's right side shows it, pairing.
            for (name, panel) in [("code", PairingPanel(showing: Self.code)),
                                  ("connected", PairingPanel(connected: connected)),
                                  ("problem", PairingPanel(problem: "Agentman can't reach the relay right now. Check the internet connection and try again."))] {
                try render(DevicesSettings(pairing: panel), size: CGSize(width: 599, height: 580), dark: dark, store: try store(),
                           to: folder.appendingPathComponent("pairing-\(name)-\(look).png"))
            }
        }
    }

    private func render<V: View>(_ view: V, size: CGSize, dark: Bool, store: AgentsStore, to url: URL) throws {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentView = NSHostingView(rootView: view.environment(store).frame(width: size.width, height: size.height, alignment: .top)
            .background(Palette.paper))
        window.orderFrontRegardless()
        RunLoop.main.run(until: Date().addingTimeInterval(0.8))
        let content = try XCTUnwrap(window.contentView)
        let bitmap = try XCTUnwrap(content.bitmapImageRepForCachingDisplay(in: content.bounds))
        content.cacheDisplay(in: content.bounds, to: bitmap)
        try XCTUnwrap(bitmap.representation(using: .png, properties: [:])).write(to: url)
        window.close()
    }
}
