import Foundation

/// The reconnect loop for one paired peer this machine must connect to (the smaller id dials,
/// §2): try, back off 1 s … 30 s on failure, reset on success, try again a second after a
/// connection ends, and jump the queue when Bonjour announces the peer (`kick`). Mirrors
/// `dialLoop` in `core/transport.go`.
@MainActor
final class Dialer {

    static let minBackoff: TimeInterval = 1
    static let maxBackoff: TimeInterval = 30

    let peerID: String
    /// The attempt (or live connection) in flight.
    private(set) var link: PeerLink?

    private weak var transport: Transport?
    private var backoff = Dialer.minBackoff
    private var pending: DispatchWorkItem?
    private var stopped = false

    init(peerID: String, transport: Transport) {
        self.peerID = peerID
        self.transport = transport
    }

    // MARK: - Control

    /// Dials right now unless an attempt or a connection is already in flight.
    func kick() {
        guard !stopped, link == nil, let transport, !transport.isConnected(peerID) else { return }
        pending?.cancel()
        pending = nil
        run()
    }

    /// Ends the loop; the attempt in flight is closed.
    func stop() {
        stopped = true
        pending?.cancel()
        pending = nil
        link?.close(reason: nil)
        link = nil
    }

    /// The transport reports that the attempt (or the connection it produced) ended.
    func attemptEnded(established: Bool) {
        link = nil
        guard !stopped else { return }
        if established {
            backoff = Dialer.minBackoff
            schedule(after: Dialer.minBackoff)
        } else {
            backoff = min(backoff * 2, Dialer.maxBackoff)
            schedule(after: backoff)
        }
    }

    // MARK: - Loop

    private func schedule(after seconds: TimeInterval) {
        pending?.cancel()
        let item = DispatchWorkItem { [weak self] in
            MainActor.assumeIsolated { self?.run() }
        }
        pending = item
        DispatchQueue.main.asyncAfter(deadline: .now() + seconds, execute: item)
    }

    private func run() {
        pending = nil
        guard !stopped, let transport else { return }
        guard transport.isPaired(peerID) else {
            transport.dialerFinished(self)
            return
        }
        if transport.isConnected(peerID) || link != nil { return }
        // Only the first failure after a success is worth a log line.
        guard let attempt = transport.dial(peerID, dialer: self, verbose: backoff == Dialer.minBackoff) else {
            schedule(after: Dialer.maxBackoff) // no address known yet
            return
        }
        link = attempt
    }
}
