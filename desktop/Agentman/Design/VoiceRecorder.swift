import AVFoundation
import SwiftUI

/// Speaking a message instead of typing it. The microphone records 16 kHz
/// mono WAV, the daemon on this Mac has Celery write it out (ums and false
/// starts taken out, nothing paraphrased), and the words land in the box to
/// read over before sending.
@MainActor @Observable
final class VoiceRecorder {
    enum State: Equatable {
        case idle
        case recording(since: Date)
        case writing
    }

    private(set) var state: State = .idle
    /// How loud it is now, 0 to 1, for the button to show it listening.
    private(set) var level: Double = 0
    @ObservationIgnored private var recorder: AVAudioRecorder?
    @ObservationIgnored private var file: URL?
    @ObservationIgnored private var meter: Task<Void, Never>?

    /// The recording's format: what Celery takes.
    static let settings: [String: Any] = [
        AVFormatIDKey: kAudioFormatLinearPCM,
        AVSampleRateKey: 16000,
        AVNumberOfChannelsKey: 1,
        AVLinearPCMBitDepthKey: 16,
        AVLinearPCMIsFloatKey: false,
        AVLinearPCMIsBigEndianKey: false,
    ]

    /// Starts listening, asking for the microphone the first time. Returns
    /// why it couldn't, if it couldn't.
    func start() async -> String? {
        guard state == .idle else { return nil }
        switch AVCaptureDevice.authorizationStatus(for: .audio) {
        case .authorized:
            break
        case .notDetermined:
            guard await AVCaptureDevice.requestAccess(for: .audio) else { return Self.denied }
        default:
            return Self.denied
        }
        let url = FileManager.default.temporaryDirectory.appendingPathComponent("agentman-voice-\(UUID().uuidString).wav")
        do {
            let recorder = try AVAudioRecorder(url: url, settings: Self.settings)
            recorder.isMeteringEnabled = true
            // Ten minutes at most: Celery takes about twelve.
            guard recorder.record(forDuration: 600) else { return "The microphone didn't start. Try again." }
            self.recorder = recorder
            file = url
            state = .recording(since: Date())
            meter = Task { [weak self] in
                while !Task.isCancelled {
                    try? await Task.sleep(for: .milliseconds(80))
                    guard let self, let recorder = self.recorder else { return }
                    recorder.updateMeters()
                    // -50 dB is silence enough; 0 dB is as loud as it gets.
                    self.level = max(0, min(1, (Double(recorder.averagePower(forChannel: 0)) + 50) / 50))
                }
            }
            return nil
        } catch {
            return "The microphone didn't start: \(error.localizedDescription)"
        }
    }

    /// Stops listening and returns the recording.
    func finish() -> Data? {
        guard case .recording = state, let recorder, let file else { return nil }
        recorder.stop()
        stopMeter()
        self.recorder = nil
        defer { try? FileManager.default.removeItem(at: file) }
        guard let data = try? Data(contentsOf: file), data.count > 44 else {
            state = .idle
            return nil
        }
        state = .writing
        return data
    }

    /// Stops listening and forgets what was heard.
    func cancel() {
        recorder?.stop()
        recorder = nil
        stopMeter()
        if let file { try? FileManager.default.removeItem(at: file) }
        file = nil
        state = .idle
    }

    func done() {
        state = .idle
    }

    private func stopMeter() {
        meter?.cancel()
        meter = nil
        level = 0
    }

    private static let denied = "Agentman can't use the microphone. Allow it in System Settings › Privacy & Security › Microphone."
}

/// The mic beside a message box: tap to speak, tap again to have it written
/// out, Escape to throw it away.
struct VoiceButton: View {
    @Environment(AgentsStore.self) private var store
    var size: CGFloat = 28
    var enabled = true
    /// The words, to go into the box.
    let onText: (String) -> Void
    /// Why it didn't work.
    let onProblem: (String) -> Void
    @State private var recorder = VoiceRecorder()

    var body: some View {
        Button(action: tap) {
            Group {
                switch recorder.state {
                case .idle:
                    Image(systemName: "mic")
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundStyle(Palette.muted)
                        .frame(width: size, height: size)
                        .background(Palette.fill, in: Circle())
                case .recording(let since):
                    // Red while it listens, with how long, and a halo that
                    // grows with the voice so it is plainly hearing it.
                    TimelineView(.periodic(from: since, by: 1)) { context in
                        HStack(spacing: 6) {
                            RoundedRectangle(cornerRadius: 2, style: .continuous)
                                .fill(.white)
                                .frame(width: 8, height: 8)
                            Text(Self.elapsed(context.date.timeIntervalSince(since)))
                                .font(Fonts.mono(11, .semibold))
                                .monospacedDigit()
                                .foregroundStyle(.white)
                        }
                        .padding(.horizontal, 10)
                        .frame(height: size)
                        .background(Palette.error, in: Capsule())
                        .background {
                            Capsule()
                                .fill(Palette.error.opacity(0.25))
                                .scaleEffect(1 + recorder.level * 0.25)
                                .animation(.easeOut(duration: 0.12), value: recorder.level)
                        }
                    }
                case .writing:
                    ProgressView()
                        .controlSize(.small)
                        .frame(width: size, height: size)
                        .background(Palette.fill, in: Circle())
                }
            }
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .disabled(!enabled || recorder.state == .writing)
        .animation(.easeOut(duration: 0.15), value: recorder.state)
        .help(help)
        .accessibilityLabel(help)
        .onKeyPress(.escape) {
            guard case .recording = recorder.state else { return .ignored }
            recorder.cancel()
            return .handled
        }
        .onDisappear { recorder.cancel() }
    }

    private var help: String {
        switch recorder.state {
        case .idle: "Speak your message (Celery writes it out)"
        case .recording: "Stop and write it out (Escape throws it away)"
        case .writing: "Writing it out…"
        }
    }

    private func tap() {
        switch recorder.state {
        case .idle:
            Task {
                if let problem = await recorder.start() { onProblem(problem) }
            }
        case .recording:
            guard let wav = recorder.finish() else {
                onProblem("Nothing was recorded.")
                return
            }
            Task {
                switch await store.transcribe(wav) {
                case .success(let text): onText(text)
                case .failure(let problem): onProblem(problem.message)
                }
                recorder.done()
            }
        case .writing:
            break
        }
    }

    static func elapsed(_ seconds: TimeInterval) -> String {
        let whole = max(0, Int(seconds))
        return String(format: "%d:%02d", whole / 60, whole % 60)
    }
}

extension String {
    /// The words of a voice prompt added to what is already in a box.
    func adding(spoken text: String) -> String {
        let trimmed = trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? text : trimmed + " " + text
    }
}
