import Darwin
import Foundation

/// The connection to `am serve` on this Mac.
///
/// The daemon listens on a Unix socket in its config directory, which only
/// this user can open, and speaks the same protocol as the phone: one JSON
/// envelope per line in each direction. Replies carry the ID of the request
/// they answer; everything else is something the daemon tells every app.
///
/// All state lives on one serial queue. The socket is read when the system
/// says bytes are waiting, so reads never block, and the connection comes
/// back by itself when the daemon restarts.
final class DaemonClient: @unchecked Sendable {
    enum Status: Sendable, Equatable {
        case connecting
        case connected
        case unavailable(String)
    }

    enum ClientError: LocalizedError {
        case notConnected, timedOut, closed, pathTooLong

        var errorDescription: String? {
            switch self {
            case .notConnected: "Not connected to am serve."
            case .timedOut: "am serve did not answer in time."
            case .closed: "The connection to am serve closed."
            case .pathTooLong: "The socket path is too long."
            }
        }
    }

    let socketPath: String
    let events: AsyncStream<DaemonEvent>
    let statuses: AsyncStream<Status>

    private let eventSink: AsyncStream<DaemonEvent>.Continuation
    private let statusSink: AsyncStream<Status>.Continuation
    private let queue = DispatchQueue(label: "dev.agentman.mac.daemon")
    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()
    private var fd: Int32 = -1
    private var source: DispatchSourceRead?
    private var framer = LineFramer()
    private var pending: [String: CheckedContinuation<DaemonEvent, Error>] = [:]
    private var retryDelay: TimeInterval = 0.5
    private var running = false
    private var counter = 0

    /// `~/.agentman/am.sock`, unless `AGENTMAN_SOCKET` or the `socketPath`
    /// default names another, such as a test daemon's.
    static var defaultSocketPath: String {
        if let path = ProcessInfo.processInfo.environment["AGENTMAN_SOCKET"], !path.isEmpty {
            return path
        }
        if let path = UserDefaults.standard.string(forKey: "socketPath"), !path.isEmpty {
            return path
        }
        return (NSHomeDirectory() as NSString).appendingPathComponent(".agentman/am.sock")
    }

    init(socketPath: String = DaemonClient.defaultSocketPath) {
        self.socketPath = socketPath
        (events, eventSink) = AsyncStream.makeStream(of: DaemonEvent.self)
        (statuses, statusSink) = AsyncStream.makeStream(of: Status.self)
    }

    func start() {
        queue.async { [self] in
            running = true
            connect()
        }
    }

    func stop() {
        queue.async { [self] in
            running = false
            disconnect(reason: nil)
        }
    }

    /// Sends a request whose answer does not matter, such as subscribe.
    func send(_ request: DaemonRequest) {
        queue.async { [self] in
            try? write(request, id: nextID())
        }
    }

    /// For tests: answers requests in place of am serve, where it returns
    /// an event.
    var answer: (@Sendable (DaemonRequest) -> DaemonEvent?)?

    /// Sends a request and waits for the daemon's answer to it.
    func request(_ request: DaemonRequest, timeout: TimeInterval = 30) async throws -> DaemonEvent {
        if let answer, let event = answer(request) { return event }
        return try await withCheckedThrowingContinuation { continuation in
            queue.async { [self] in
                let id = nextID()
                do {
                    try write(request, id: id)
                    // Set after the write, on the same queue the reply is
                    // routed on, so the reply cannot arrive first.
                    pending[id] = continuation
                    queue.asyncAfter(deadline: .now() + timeout) { [self] in
                        pending.removeValue(forKey: id)?.resume(throwing: ClientError.timedOut)
                    }
                } catch {
                    continuation.resume(throwing: error)
                }
            }
        }
    }

    // MARK: - Connection

    private func connect() {
        guard running, fd < 0 else { return }
        statusSink.yield(.connecting)
        do {
            let socket = try Self.open(path: socketPath)
            fd = socket
            retryDelay = 0.5
            let source = DispatchSource.makeReadSource(fileDescriptor: socket, queue: queue)
            source.setEventHandler { [weak self] in self?.readAvailable() }
            source.setCancelHandler { Darwin.close(socket) }
            self.source = source
            source.resume()
            statusSink.yield(.connected)
        } catch {
            statusSink.yield(.unavailable(Self.describe(error)))
            scheduleReconnect()
        }
    }

    private func readAvailable() {
        guard fd >= 0 else { return }
        var buffer = [UInt8](repeating: 0, count: 64 * 1024)
        let count = Darwin.read(fd, &buffer, buffer.count)
        if count < 0, errno == EAGAIN || errno == EINTR {
            return
        }
        guard count > 0 else {
            disconnect(reason: "am serve stopped")
            return
        }
        for line in framer.append(Data(buffer[0..<count])) {
            route(line)
        }
    }

    private func route(_ line: Data) {
        guard let header = try? decoder.decode(EnvelopeHeader.self, from: line) else { return }
        let event = (try? decoder.decode(Envelope<DaemonEvent>.self, from: line))?.payload
        if let replyTo = header.replyTo, !replyTo.isEmpty, let waiter = pending.removeValue(forKey: replyTo) {
            if let event {
                waiter.resume(returning: event)
            } else {
                waiter.resume(throwing: ClientError.closed)
            }
            return
        }
        if let event {
            eventSink.yield(event)
        }
    }

    private func write(_ request: DaemonRequest, id: String) throws {
        guard fd >= 0 else { throw ClientError.notConnected }
        let envelope = Envelope(v: protocolVersion, id: id, replyTo: nil, to: "daemon", payload: request)
        var data = try encoder.encode(envelope)
        data.append(0x0A)
        try data.withUnsafeBytes { raw in
            guard let base = raw.baseAddress else { return }
            var offset = 0
            while offset < raw.count {
                let written = Darwin.write(fd, base + offset, raw.count - offset)
                if written < 0 {
                    if errno == EINTR { continue }
                    throw ClientError.closed
                }
                offset += written
            }
        }
    }

    private func disconnect(reason: String?) {
        source?.cancel()
        source = nil
        fd = -1
        framer.reset()
        let waiters = pending
        pending.removeAll()
        for (_, waiter) in waiters {
            waiter.resume(throwing: ClientError.closed)
        }
        if let reason {
            statusSink.yield(.unavailable(reason))
            scheduleReconnect()
        }
    }

    private func scheduleReconnect() {
        guard running else { return }
        let delay = retryDelay
        retryDelay = min(retryDelay * 2, 5)
        queue.asyncAfter(deadline: .now() + delay) { [weak self] in self?.connect() }
    }

    private func nextID() -> String {
        counter += 1
        return "mac-\(counter)"
    }

    // MARK: - Sockets

    private static func open(path: String) throws -> Int32 {
        let socket = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard socket >= 0 else { throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO) }
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        let bytes = Array(path.utf8)
        guard bytes.count < MemoryLayout.size(ofValue: address.sun_path) else {
            Darwin.close(socket)
            throw ClientError.pathTooLong
        }
        withUnsafeMutableBytes(of: &address.sun_path) { raw in
            raw.copyBytes(from: bytes)
            raw[bytes.count] = 0
        }
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        let result = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.connect(socket, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard result == 0 else {
            let code = errno
            Darwin.close(socket)
            throw POSIXError(POSIXErrorCode(rawValue: code) ?? .ECONNREFUSED)
        }
        // A write to a socket the daemon has closed must fail, not end the app.
        var on: Int32 = 1
        setsockopt(socket, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))
        return socket
    }

    private static func describe(_ error: Error) -> String {
        if let posix = error as? POSIXError, posix.code == .ENOENT || posix.code == .ECONNREFUSED {
            return "am serve isn't running"
        }
        return error.localizedDescription
    }
}
