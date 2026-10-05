#if canImport(Network)
import Foundation
import Network

/// Live connections of one `HTTPServer`, so `stop()` can close them. Lock-protected because
/// it is touched from the I/O queue and the main actor.
final class HTTPConnectionRegistry: @unchecked Sendable {

    private let lock = NSLock()
    private var connections: [ObjectIdentifier: HTTPConnection] = [:]

    func add(_ connection: HTTPConnection) {
        lock.lock()
        connections[ObjectIdentifier(connection)] = connection
        lock.unlock()
    }

    func remove(_ connection: HTTPConnection) {
        lock.lock()
        connections[ObjectIdentifier(connection)] = nil
        lock.unlock()
    }

    var count: Int {
        lock.lock()
        defer { lock.unlock() }
        return connections.count
    }

    /// Closes every connection on its I/O queue.
    func closeAll(on queue: DispatchQueue) {
        lock.lock()
        let all = Array(connections.values)
        lock.unlock()
        queue.async {
            for connection in all {
                connection.closeNow()
            }
        }
    }
}

/// One accepted TCP connection and its tiny state machine:
///
/// ```
/// .http ──(request parsed)──▶ server.dispatch on main ──▶ respond()  ──▶ send + close
///                                                     └─▶ upgrade()  ──▶ .webSocket
/// .webSocket ──(frames)──▶ text/binary → socket callbacks on main; ping → pong;
///                          close → echo close, then .closed
/// .closed    ── terminal; NWConnection cancelled, registry entry gone, onClose fired once
/// ```
///
/// Every member is touched only on `queue` (the server's I/O queue); the public entry points
/// `respond`, `upgrade`, `enqueueFrame` and `enqueueClose` hop there first, so they may be
/// called from anywhere. Ordering between a `101` and the first frame the app sends is
/// guaranteed because both travel through the same serial queue.
final class HTTPConnection {

    enum Mode: Equatable {
        case http
        case webSocket
        case closed
    }

    /// `host:port` of the peer, for logs.
    let remoteDescription: String

    private let connection: NWConnection
    private let queue: DispatchQueue
    private let registry: HTTPConnectionRegistry
    private weak var server: HTTPServer?

    private var buffer = Data()
    private var mode: Mode = .http
    private var requestInFlight = false
    private var socket: WebSocketConnection?
    private var fragmentOpcode: WebSocketFrame.Opcode?
    private var fragments = Data()
    private var closeFrameSent = false

    init(connection: NWConnection,
         queue: DispatchQueue,
         registry: HTTPConnectionRegistry,
         server: HTTPServer) {
        self.connection = connection
        self.queue = queue
        self.registry = registry
        self.server = server
        self.remoteDescription = HTTPConnection.describe(connection.endpoint)
    }

    // MARK: - Lifecycle (I/O queue)

    func start() {
        connection.stateUpdateHandler = { [weak self] state in
            guard let self else { return }
            switch state {
            case .ready:
                self.receiveNext()
            case .failed, .cancelled:
                self.closeNow()
            default:
                break
            }
        }
        connection.start(queue: queue)
    }

    /// Tears the connection down. Idempotent. I/O queue only.
    func closeNow() {
        guard mode != .closed else { return }
        mode = .closed
        buffer = Data()
        connection.cancel()
        registry.remove(self)
        if let socket {
            self.socket = nil
            DispatchQueue.main.async {
                socket.onClose?()
            }
        }
    }

    private func receiveNext() {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) { [weak self] content, _, isComplete, error in
            guard let self, self.mode != .closed else { return }
            if let content, !content.isEmpty {
                self.buffer.append(content)
                self.process()
            }
            if error != nil || isComplete {
                self.closeNow()
                return
            }
            if self.mode != .closed {
                self.receiveNext()
            }
        }
    }

    // MARK: - Entry points (any queue)

    /// Sends an HTTP response and closes.
    func respond(_ response: HTTPResponse) {
        queue.async { [self] in
            guard mode == .http else { return }
            var final = response
            final.headers["Connection"] = "close"
            send(final.serialize(), final: true) { [weak self] in
                self?.closeNow()
            }
        }
    }

    /// Sends the `101` and switches to frame mode.
    func upgrade(to socket: WebSocketConnection, response: HTTPResponse) {
        queue.async { [self] in
            guard mode == .http else { return }
            self.socket = socket
            mode = .webSocket
            requestInFlight = false
            send(response.serialize())
            // The client may already have sent frames behind its upgrade request.
            processFrames()
        }
    }

    func enqueueFrame(_ frame: WebSocketFrame) {
        queue.async { [self] in
            guard mode == .webSocket else { return }
            send(WebSocketFrame.encode(frame))
        }
    }

    /// Sends a close frame, then cancels after the peer's close or a short grace period.
    func enqueueClose(code: UInt16, reason: String) {
        queue.async { [self] in
            sendClose(code: code, reason: reason)
        }
    }

    // MARK: - HTTP mode (I/O queue)

    private func process() {
        switch mode {
        case .http:
            processRequest()
        case .webSocket:
            processFrames()
        case .closed:
            break
        }
    }

    private func processRequest() {
        guard !requestInFlight else { return }
        do {
            guard let request = try HTTPRequestParser.parse(from: &buffer) else { return }
            requestInFlight = true
            DispatchQueue.main.async { [weak self] in
                guard let self else { return }
                MainActor.assumeIsolated {
                    guard let server = self.server else {
                        self.respond(.internalError("server gone"))
                        return
                    }
                    server.dispatch(request, from: self)
                }
            }
        } catch {
            respond(.badRequest())
        }
    }

    // MARK: - WebSocket mode (I/O queue)

    private func processFrames() {
        while mode == .webSocket {
            let frame: WebSocketFrame?
            do {
                frame = try WebSocketFrame.decode(from: &buffer, requireMask: true)
            } catch {
                let code: UInt16 = (error as? WebSocketFrameError) == .tooLarge ? 1009 : 1002
                sendClose(code: code, reason: "protocol error")
                return
            }
            guard let frame else { return }
            handle(frame)
        }
    }

    private func handle(_ frame: WebSocketFrame) {
        switch frame.opcode {
        case .ping:
            send(WebSocketFrame.encode(WebSocketFrame(opcode: .pong, payload: frame.payload)))
        case .pong:
            break
        case .close:
            if closeFrameSent {
                closeNow()
            } else {
                closeFrameSent = true
                let echo = WebSocketFrame.close(code: frame.closeCode ?? 1000)
                send(WebSocketFrame.encode(echo)) { [weak self] in
                    self?.closeNow()
                }
            }
        case .text, .binary:
            if frame.fin {
                deliver(opcode: frame.opcode, payload: frame.payload)
            } else {
                fragmentOpcode = frame.opcode
                fragments = frame.payload
            }
        case .continuation:
            guard let opcode = fragmentOpcode else {
                sendClose(code: 1002, reason: "unexpected continuation")
                return
            }
            fragments.append(frame.payload)
            if fragments.count > WebSocketFrame.maxPayloadLength {
                sendClose(code: 1009, reason: "message too large")
                return
            }
            if frame.fin {
                let payload = fragments
                fragments = Data()
                fragmentOpcode = nil
                deliver(opcode: opcode, payload: payload)
            }
        }
    }

    private func deliver(opcode: WebSocketFrame.Opcode, payload: Data) {
        guard let socket else { return }
        if opcode == .text {
            guard let text = String(data: payload, encoding: .utf8) else {
                sendClose(code: 1007, reason: "invalid UTF-8")
                return
            }
            DispatchQueue.main.async {
                socket.onText?(text)
            }
        } else {
            DispatchQueue.main.async {
                socket.onBinary?(payload)
            }
        }
    }

    private func sendClose(code: UInt16, reason: String) {
        guard mode == .webSocket, !closeFrameSent else { return }
        closeFrameSent = true
        send(WebSocketFrame.encode(WebSocketFrame.close(code: code, reason: reason)))
        // RFC 6455 §7.1.1: give the peer a moment to answer with its own close frame.
        queue.asyncAfter(deadline: .now() + 1.0) { [weak self] in
            self?.closeNow()
        }
    }

    // MARK: - Sending (I/O queue)

    private func send(_ data: Data, final: Bool = false, completion: (() -> Void)? = nil) {
        let context: NWConnection.ContentContext = final ? .finalMessage : .defaultMessage
        connection.send(content: data, contentContext: context, isComplete: true,
                        completion: .contentProcessed { [weak self] error in
            if error != nil {
                self?.closeNow()
            }
            completion?()
        })
    }

    // MARK: - Helpers

    private static func describe(_ endpoint: NWEndpoint) -> String {
        switch endpoint {
        case .hostPort(let host, let port):
            return "\(host):\(port.rawValue)"
        default:
            return "\(endpoint)"
        }
    }
}
#endif
