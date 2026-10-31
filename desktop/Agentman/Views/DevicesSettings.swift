import CoreImage.CIFilterBuiltins
import SwiftUI

/// The phones and tablets that reach this Mac, and pairing another, laid out
/// as T3 Code's Connections page: a one-time code made on the page, shown as
/// a QR to scan beside the digits to type, and each device below with how it
/// is connected.
struct DevicesSettings: View {
    @Environment(AgentsStore.self) private var store
    @State private var pairing: Bool
    /// What the pairing panel shows in pictures of the page.
    private let shown: PairingPanel?

    init(pairing: PairingPanel? = nil) {
        _pairing = State(initialValue: pairing != nil)
        shown = pairing
    }

    var body: some View {
        SettingsPageView(title: "Devices") {
            SettingsSection {
                VStack(spacing: 0) {
                    HStack(alignment: .center, spacing: 16) {
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Pair a device")
                                .font(TextStyle.body)
                                .foregroundStyle(Palette.text)
                            Text("Make a one-time code another phone or tablet can scan to reach your agents from anywhere.")
                                .font(TextStyle.caption)
                                .foregroundStyle(Palette.muted)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                        Spacer(minLength: 8)
                        if pairing {
                            SmallButton(title: "Cancel") { pairing = false }
                        } else {
                            SmallButton(title: "Create code", symbol: "qrcode") { pairing = true }
                        }
                    }
                    .padding(.horizontal, 16)
                    .padding(.vertical, 13)
                    if pairing {
                        Rectangle().fill(Palette.line).frame(height: 1)
                        (shown ?? PairingPanel(onDone: { pairing = false }))
                            .padding(16)
                    }
                }
            }
            SettingsSection(title: "Your devices") {
                if let devices = store.devices {
                    if devices.isEmpty {
                        Text("No devices yet. Pair one above.")
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.muted)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(.horizontal, 16)
                            .padding(.vertical, 14)
                    } else {
                        ForEach(Array(devices.enumerated()), id: \.element.id) { index, device in
                            DeviceRow(device: device, divider: index < devices.count - 1)
                        }
                    }
                } else {
                    HStack(spacing: 10) {
                        Loader(size: 11)
                        Text("Looking…").font(TextStyle.caption).foregroundStyle(Palette.muted)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 14)
                }
            }
        }
        .task { if shown == nil { await store.loadDevices() } }
        .onAppear { takeRequest(store.pairingRequested) }
        .onChange(of: store.pairingRequested) { _, requested in takeRequest(requested) }
    }

    /// The palette's Connect a Device opens the page pairing.
    private func takeRequest(_ requested: Bool) {
        guard requested else { return }
        store.pairingRequested = false
        pairing = true
    }
}

/// How a device is named and drawn: its own name where it gave one.
enum DeviceLabel {
    static func name(_ device: Device) -> String {
        if let name = device.name, !name.isEmpty { return name }
        if let model = device.model, !model.isEmpty { return model }
        return device.platform == "android" ? "Android phone" : "Phone"
    }

    static func symbol(_ device: Device) -> String {
        if device.model?.localizedCaseInsensitiveContains("ipad") == true { return "ipad" }
        if device.platform == "ios" || device.model?.localizedCaseInsensitiveContains("iphone") == true { return "iphone" }
        return "smartphone"
    }

    /// "Connected", or when it last was, and when it was added.
    static func detail(_ device: Device, now: Date = .now) -> String {
        let added = "Added " + date(device.firstSeen).formatted(.dateTime.day().month(.abbreviated))
        if device.connected == true { return "Connected · " + added }
        let seen = Relative.short(date(device.lastSeen), now: now)
        return (seen == "now" ? "Last connected just now" : "Last connected \(seen) ago") + " · " + added
    }

    private static func date(_ millis: Int64) -> Date { Date(timeIntervalSince1970: Double(millis) / 1000) }
}

private struct DeviceRow: View {
    let device: Device
    var divider: Bool

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 12) {
                Image(systemName: DeviceLabel.symbol(device))
                    .font(.system(size: 15))
                    .foregroundStyle(Palette.muted)
                    .frame(width: 22)
                VStack(alignment: .leading, spacing: 3) {
                    HStack(spacing: 6) {
                        Text(DeviceLabel.name(device))
                            .font(TextStyle.bodyMedium)
                            .foregroundStyle(Palette.text)
                        if let model = device.model, device.name != nil, model != device.name {
                            Text(model)
                                .font(.system(size: 10.5))
                                .foregroundStyle(Palette.muted)
                                .padding(.horizontal, 5)
                                .padding(.vertical, 1.5)
                                .background(Palette.fill.opacity(0.6), in: RoundedRectangle(cornerRadius: 5, style: .continuous))
                                .overlay(RoundedRectangle(cornerRadius: 5, style: .continuous).strokeBorder(Palette.line))
                        }
                    }
                    HStack(spacing: 6) {
                        Circle()
                            .fill(device.connected == true ? Palette.ok : Palette.faint.opacity(0.6))
                            .frame(width: 6, height: 6)
                        Text(DeviceLabel.detail(device))
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.muted)
                    }
                }
                Spacer(minLength: 8)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 11)
            if divider {
                Rectangle().fill(Palette.line).frame(height: 1).padding(.leading, 50)
            }
        }
    }
}

/// Pairing, on the page: the code as a QR beside the digits to type, a new
/// one as each runs out, until a device comes in.
struct PairingPanel: View {
    @Environment(AgentsStore.self) private var store
    @State private var code: PairCode?
    @State private var problem: String?
    @State private var connected: Device?
    /// The devices connected when pairing began: one connecting after is the
    /// one just paired.
    @State private var before: Set<String>?
    @State private var copied: String?
    var onDone: () -> Void = {}
    /// False for pictures, which hold what they show.
    private let asks: Bool

    init(showing code: PairCode? = nil, connected: Device? = nil, problem: String? = nil, onDone: @escaping () -> Void = {}) {
        _code = State(initialValue: code)
        _connected = State(initialValue: connected)
        _problem = State(initialValue: problem)
        self.onDone = onDone
        asks = code == nil && connected == nil && problem == nil
    }

    var body: some View {
        Group {
            if let connected {
                HStack(spacing: 10) {
                    Image(systemName: "checkmark.circle.fill")
                        .font(.system(size: 18))
                        .foregroundStyle(Palette.ok)
                    VStack(alignment: .leading, spacing: 2) {
                        Text("\(DeviceLabel.name(connected)) is connected")
                            .font(TextStyle.bodyMedium)
                            .foregroundStyle(Palette.text)
                        Text("Agentman tells it when an agent finishes or needs you.")
                            .font(TextStyle.caption)
                            .foregroundStyle(Palette.muted)
                    }
                    Spacer(minLength: 8)
                    SmallButton(title: "Done", primary: true, action: onDone)
                }
            } else if let code {
                codeView(code)
            } else if let problem {
                HStack(spacing: 10) {
                    Image(systemName: "exclamationmark.triangle")
                        .font(.system(size: 13))
                        .foregroundStyle(Palette.needsYouText)
                    Text(problem)
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.textSecondary)
                        .fixedSize(horizontal: false, vertical: true)
                    Spacer(minLength: 8)
                    SmallButton(title: "Try again", symbol: "arrow.clockwise") { Task { await fetch() } }
                }
            } else {
                HStack(spacing: 10) {
                    Loader(size: 11)
                    Text("Making a code…").font(TextStyle.caption).foregroundStyle(Palette.muted)
                    Spacer()
                }
                .frame(height: 28)
            }
        }
        .task {
            guard asks else { return }
            before = Set((store.devices ?? []).filter { $0.connected == true }.map(\.id))
            await fetch()
        }
        .task(id: code?.code) {
            // A new code just before this one runs out, while pairing waits.
            guard asks, let expires = code?.expires else { return }
            let wait = expires.timeIntervalSinceNow - 1
            if wait > 0 { try? await Task.sleep(for: .seconds(wait)) }
            if !Task.isCancelled, connected == nil { await fetch() }
        }
        .onChange(of: store.devices) { _, devices in
            guard asks, connected == nil, let before else { return }
            if let device = devices?.first(where: { $0.connected == true && !before.contains($0.id) }) {
                connected = device
            }
        }
    }

    private func codeView(_ code: PairCode) -> some View {
        HStack(alignment: .top, spacing: 18) {
            QRCode(text: code.url)
                .frame(width: 132, height: 132)
                .padding(10)
                .background(Color.white, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(Palette.line))
                .accessibilityLabel("QR code. Scan it with the Agentman app to pair.")
            VStack(alignment: .leading, spacing: 12) {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Scan with the Agentman app")
                        .font(TextStyle.bodyMedium)
                        .foregroundStyle(Palette.text)
                    Text("On the phone, tap Scan QR code, or Enter the code instead and type it.")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.muted)
                        .fixedSize(horizontal: false, vertical: true)
                }
                // The buttons beside the code, or under it when narrow.
                ViewThatFits(in: .horizontal) {
                    HStack(spacing: 8) {
                        codePill(code)
                        copyButtons(code)
                    }
                    VStack(alignment: .leading, spacing: 8) {
                        codePill(code)
                        HStack(spacing: 8) { copyButtons(code) }
                    }
                }
                TimelineView(.periodic(from: .now, by: 1)) { context in
                    // Pictures hold it 42 seconds from running out.
                    let now = asks ? context.date : code.expires.addingTimeInterval(-42)
                    let left = Int(max(0, code.expires.timeIntervalSince(now)).rounded(.up))
                    Text("Works for 0:\(String(format: "%02d", left)) · a new one follows")
                        .font(TextStyle.caption)
                        .foregroundStyle(Palette.faint)
                        .monospacedDigit()
                }
            }
            Spacer(minLength: 0)
        }
        .onChange(of: code.code) { _, _ in copied = nil }
    }

    private func codePill(_ code: PairCode) -> some View {
        Text(Self.grouped(code.code))
            .font(Fonts.mono(14, .medium))
            .foregroundStyle(Palette.text)
            .textSelection(.enabled)
            .fixedSize()
            .padding(.horizontal, 10)
            .frame(height: 28)
            .background(Palette.fill.opacity(0.45), in: RoundedRectangle(cornerRadius: 7, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 7, style: .continuous).strokeBorder(Palette.line))
    }

    @ViewBuilder private func copyButtons(_ code: PairCode) -> some View {
        SmallButton(title: copied == "code" ? "Copied" : "Copy code", symbol: copied == "code" ? "checkmark" : "doc.on.doc") {
            copy(code.code, as: "code")
        }
        SmallButton(title: copied == "link" ? "Copied" : "Copy link", symbol: copied == "link" ? "checkmark" : "link") {
            copy(code.url, as: "link")
        }
    }

    private func copy(_ text: String, as what: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        copied = what
    }

    private func fetch() async {
        do {
            code = try await store.pairCode()
            problem = nil
        } catch is CancellationError {
        } catch {
            code = nil
            problem = error.localizedDescription
        }
    }

    /// "0123456789" as "01234 56789", easier to read across a room.
    static func grouped(_ code: String) -> String {
        guard code.count == 10 else { return code }
        return String(code.prefix(5)) + " " + String(code.suffix(5))
    }
}

/// A small outlined button with an optional symbol, as T3 Code's settings
/// use; the primary one filled.
struct SmallButton: View {
    let title: String
    var symbol: String?
    var primary = false
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 5) {
                if let symbol {
                    Image(systemName: symbol).font(.system(size: 10.5, weight: .medium))
                }
                Text(title).font(Fonts.sans(12, .medium))
            }
            .foregroundStyle(primary ? Palette.onInverse : Palette.text)
            .padding(.horizontal, 10)
            .frame(height: 26)
            .background(
                RoundedRectangle(cornerRadius: 7, style: .continuous)
                    .fill(primary ? Palette.inverse.opacity(hovering ? 0.85 : 1) : hovering ? Palette.fill : Palette.surface)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 7, style: .continuous)
                    .strokeBorder(primary ? Color.clear : Palette.line)
            )
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .fixedSize()
    }
}

/// A QR code, drawn sharp at any size.
struct QRCode: View {
    let text: String

    var body: some View {
        if let image = Self.image(text) {
            Image(nsImage: image)
                .resizable()
                .interpolation(.none)
                .scaledToFit()
        } else {
            Color.clear
        }
    }

    static func image(_ text: String) -> NSImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(text.utf8)
        filter.correctionLevel = "M"
        // Each module drawn as a 12-pixel square, so the code stays sharp
        // however it is scaled down.
        guard let output = filter.outputImage?.samplingNearest().transformed(by: CGAffineTransform(scaleX: 12, y: 12)),
              let cgImage = CIContext().createCGImage(output, from: output.extent) else { return nil }
        return NSImage(cgImage: cgImage, size: NSSize(width: output.extent.width / 12, height: output.extent.height / 12))
    }
}
