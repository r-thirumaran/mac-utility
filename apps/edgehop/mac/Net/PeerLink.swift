import Foundation
import Network

/// One TCP connection to a peer: the plaintext handshake, the switch to `SecureChannel`, the
/// `auth` / `auth_ok` exchange and then encrypted frames in both directions — the Swift
/// counterpart of `securechan.Initiate` / `Accept` plus the per-connection reader and writer
/// of `core/transport.go`.
///
/// All socket work, the `Handshake` and the `SecureChannel` (neither is thread-safe) live on
/// `queue`; every report to the `delegate` hops to the main actor. `send`, `resume(code:)` and
/// `close` may be called from the main actor at any time.
final class PeerLink {

    enum Role {
        case acceptor
        /// `pair` is `true` for a pairing started in the UI (hello.pair = true).
        case initiator(pair: Bool)
    }

    private enum Phase {
        case connecting
        case handshaking
        case awaitingAuth
        case established
        case closed
    }

    static let handshakeTimeout: TimeInterval = 15
    /// §4: the code is valid for 120 s; allow the user some slack to type it.
    static let pairingTimeout: TimeInterval = 150
    static let dialTimeout: TimeInterval = 10
    /// `PeerTimeout + 5 s`: the node declares a peer gone after 15 s of silence anyway.
    static let idleTimeout: TimeInterval = 20
    /// Frames queued but not yet handed to the kernel before new ones are dropped.
    static let maxQueuedFrames = 4096

    let role: Role
    /// `host:port` this link was dialled at ("" for an inbound connection).
    let address: String
    /// Trusted dial: the peer that must answer.
    var expectedPeerID: String?
    /// Whether a failed trusted dial is worth a log line (the first attempt after a success).
    var verbose = true
    weak var delegate: PeerLinkDelegate?
    /// Acceptor: decides synchronously (on the link queue) whether a pairing request may
    /// proceed — the rate limit of §4.
    var pairingGate: ((PeerInfo) -> Bool)?

    /// The authenticated peer once `auth` / `auth_ok` went through.
    private(set) var peer: PeerInfo?
    /// Acceptor: the peer a code was generated for (the handshake is a pairing).
    private(set) var pairingPeer: PeerInfo?
    private(set) var isEstablished = false
    /// Remote host of the socket, for guessing where an inbound peer listens.
    private(set) var remoteHost = ""

    private let queue = DispatchQueue(label: "edgehop.peerlink")
    private let connection: NWConnection
    private var handshake: Handshake?
    private var buffer = Data()
    private var channel: SecureChannel?
    private var phase: Phase = .connecting
    private var inFlight = 0
    private var dropped = 0
    private var lastReceive = Date()
    private var deadlineExtended = false
    private var refusedPairing = false
    private var waitingForCode = false

    var isInitiator: Bool {
        if case .initiator = role { return true }
        return false
    }

    init(connection: NWConnection, role: Role, address: String, options base: HandshakeOptions) {
        self.connection = connection
        self.role = role
        self.address = address
        var options = base
        switch role {
        case .acceptor:
            options.pair = false
            options.showCode = { [weak self] code, remotePeer in
                self?.showCode(code, for: remotePeer)
            }
        case .initiator(let pair):
            options.pair = pair
        }
        handshake = Handshake(options: options, initiator: isInitiator)
    }

    // MARK: - Lifecycle

    func start() {
        connection.stateUpdateHandler = { [weak self] state in
            guard let self else { return }
            switch state {
            case .ready:
                self.handleReady()
            case .waiting(let error):
                self.fail(self.phase == .connecting ? "unreachable" : nil, detail: "waiting: \(error)")
            case .failed(let error):
                self.fail(self.phase == .connecting ? "unreachable" : nil, detail: "failed: \(error)")
            case .cancelled:
                self.fail(nil, detail: nil)
            default:
                break
            }
        }
        connection.start(queue: queue)
        if isInitiator {
            arm(after: PeerLink.dialTimeout) { [weak self] in
                guard let self, self.phase == .connecting else { return }
                self.fail("unreachable", detail: "connect timeout")
            }
        }
    }

    /// Queues one encrypted frame (`u8 type || body`). Dropped when the link is not
    /// established or the kernel is not keeping up.
    func send(_ frame: Data) {
        queue.async { [weak self] in
            guard let self, self.phase == .established, let secure = self.channel else { return }
            guard self.inFlight < PeerLink.maxQueuedFrames else {
                self.dropped += 1
                if self.dropped == 1 || self.dropped % 1000 == 0 {
                    Log.error("PeerLink: send queue full (\(self.dropped) dropped)", category: "net")
                }
                return
            }
            do {
                try self.sendEncrypted(frame, with: secure)
            } catch {
                self.fail(nil, detail: "seal: \(error)")
            }
        }
    }

    /// Initiator pairing: continue with the code the user typed.
    func resume(code: String) {
        queue.async { [weak self] in
            guard let self, self.phase == .handshaking, self.waitingForCode, let handshake = self.handshake else { return }
            self.waitingForCode = false
            do {
                try self.handle(step: handshake.resume(code: code))
            } catch {
                self.fail("failed", detail: "resume: \(error)")
            }
        }
    }

    /// Closes the connection; `reason` reaches the delegate.
    func close(reason: String? = nil) {
        queue.async { [weak self] in
            self?.fail(reason, detail: nil)
        }
    }

    // MARK: - Connection (on `queue`)

    private func handleReady() {
        guard phase == .connecting else { return }
        phase = .handshaking
        remoteHost = AddressParser.host(of: connection.currentPath?.remoteEndpoint)
        lastReceive = Date()
        if isInitiator {
            do {
                if let hello = try handshake?.start() {
                    sendPlaintext(hello)
                }
            } catch {
                fail("failed", detail: "handshake start: \(error)")
                return
            }
        }
        arm(after: PeerLink.handshakeTimeout) { [weak self] in
            self?.handshakeDeadline()
        }
        scheduleIdleCheck()
        receiveLoop()
    }

    private func arm(after seconds: TimeInterval, _ body: @escaping () -> Void) {
        queue.asyncAfter(deadline: .now() + seconds, execute: body)
    }

    private func handshakeDeadline() {
        guard phase != .established && phase != .closed else { return }
        let pairing = handshake?.isPairing ?? false
        if (pairing || waitingForCode) && !deadlineExtended {
            deadlineExtended = true
            arm(after: PeerLink.pairingTimeout - PeerLink.handshakeTimeout) { [weak self] in
                self?.handshakeDeadline()
            }
            return
        }
        fail(waitingForCode ? "code_expired" : "timeout", detail: "handshake deadline")
    }

    private func scheduleIdleCheck() {
        arm(after: 5) { [weak self] in
            guard let self, self.phase != .closed else { return }
            if self.phase == .established && Date().timeIntervalSince(self.lastReceive) > PeerLink.idleTimeout {
                self.fail(nil, detail: "idle for \(Int(PeerLink.idleTimeout)) s")
                return
            }
            self.scheduleIdleCheck()
        }
    }

    private func receiveLoop() {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 65536) { [weak self] data, _, isComplete, error in
            guard let self, self.phase != .closed else { return }
            if let data, !data.isEmpty {
                self.lastReceive = Date()
                self.buffer.append(data)
                self.drain()
            }
            if self.phase == .closed { return }
            if let error {
                self.fail(nil, detail: "receive: \(error)")
                return
            }
            if isComplete {
                self.fail(nil, detail: "peer closed")
                return
            }
            self.receiveLoop()
        }
    }

    private func drain() {
        do {
            while phase != .closed, let payload = try FrameCodec.decode(from: &buffer) {
                if let secure = channel {
                    let plaintext: Data
                    do {
                        plaintext = try secure.open(payload)
                    } catch {
                        let reason: String
                        if phase == .awaitingAuth {
                            reason = (handshake?.isPairing ?? false) ? "wrong_code" : "auth_failed"
                        } else {
                            reason = "bad_frame"
                        }
                        fail(reason, detail: "decrypt: \(error)")
                        return
                    }
                    try handleEncrypted(plaintext)
                } else {
                    guard let handshake else {
                        fail("failed", detail: "no handshake")
                        return
                    }
                    try handle(step: handshake.receive(payload))
                }
            }
        } catch {
            fail("protocol", detail: "\(error)")
        }
    }

    // MARK: - Handshake

    private func handle(step: HandshakeStep) throws {
        switch step {
        case .send(let data):
            sendPlaintext(data)
        case .established(let keys, let remotePeer, let reply):
            if refusedPairing {
                fail("rate_limited", detail: "too many failed pairings from \(remotePeer.id.prefix(8))")
                return
            }
            if let reply {
                sendPlaintext(reply)
            }
            let secure = SecureChannel(keys: keys, isInitiator: isInitiator)
            channel = secure
            peer = remotePeer
            phase = .awaitingAuth
            if isInitiator {
                try sendEncrypted(Handshake.authFrame, with: secure)
            }
        case .needCode(let remotePeer):
            waitingForCode = true
            notify { $0.link(self, needsCodeFor: remotePeer) }
        case .failed(let reason, let reply):
            if let reply {
                sendPlaintext(reply) { [weak self] in
                    self?.fail(reason, detail: "handshake failed")
                }
            } else {
                fail(reason, detail: "handshake failed")
            }
        }
    }

    private func handleEncrypted(_ plaintext: Data) throws {
        switch phase {
        case .awaitingAuth:
            guard let secure = channel else { return }
            if isInitiator {
                guard plaintext == Handshake.authOKFrame else {
                    fail("protocol", detail: "expected auth_ok")
                    return
                }
            } else {
                guard plaintext == Handshake.authFrame else {
                    fail("protocol", detail: "expected auth")
                    return
                }
                try sendEncrypted(Handshake.authOKFrame, with: secure)
            }
            handshake?.confirmAuthenticated()
            phase = .established
            isEstablished = true
            if let remotePeer = peer {
                notify { $0.linkDidEstablish(self, peer: remotePeer) }
            }
        case .established:
            notify { $0.link(self, didReceive: plaintext) }
        default:
            break
        }
    }

    /// Acceptor: the handshake generated a code for a pairing request (runs inside
    /// `Handshake.receive`, before `hello_ack` goes out).
    private func showCode(_ code: String, for remotePeer: PeerInfo) {
        pairingPeer = remotePeer
        if let gate = pairingGate, !gate(remotePeer) {
            refusedPairing = true
            return
        }
        notify { $0.link(self, showCode: code, for: remotePeer) }
    }

    // MARK: - Sending

    private func sendPlaintext(_ payload: Data, then completion: (() -> Void)? = nil) {
        connection.send(content: FrameCodec.encode(payload), completion: .contentProcessed { [weak self] error in
            if let error {
                self?.fail(nil, detail: "send: \(error)")
                return
            }
            completion?()
        })
    }

    private func sendEncrypted(_ payload: Data, with secure: SecureChannel) throws {
        let sealed = try secure.seal(payload)
        inFlight += 1
        connection.send(content: FrameCodec.encode(sealed), completion: .contentProcessed { [weak self] error in
            guard let self else { return }
            self.inFlight -= 1
            if let error {
                self.fail(nil, detail: "send: \(error)")
            }
        })
    }

    // MARK: - Closing

    private func fail(_ reason: String?, detail: String?) {
        guard phase != .closed else { return }
        phase = .closed
        if let detail {
            Log.debug("PeerLink: closing (\(reason ?? "closed")): \(detail)", category: "net")
        }
        connection.cancel()
        notify { $0.link(self, didCloseWith: reason) }
    }

    private func notify(_ body: @escaping @MainActor (PeerLinkDelegate) -> Void) {
        DispatchQueue.main.async { [weak self] in
            guard let self, let delegate = self.delegate else { return }
            MainActor.assumeIsolated {
                body(delegate)
            }
        }
    }
}
