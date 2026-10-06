import Foundation

/// §4: five failed pairings per peer per ten minutes are refused. Thread-safe, because the
/// check runs synchronously on a connection queue while the handshake decides whether to show
/// a code.
final class PairingRateLimiter {

    private let lock = NSLock()
    private var failures: [String: [Date]] = [:]
    private let limit: Int
    private let window: TimeInterval

    init(limit: Int = 5, window: TimeInterval = 600) {
        self.limit = limit
        self.window = window
    }

    // MARK: - Checks

    func isLimited(_ peerID: String) -> Bool {
        lock.lock()
        defer { lock.unlock() }
        let now = Date()
        let recent = (failures[peerID] ?? []).filter { now.timeIntervalSince($0) < window }
        failures[peerID] = recent
        return recent.count >= limit
    }

    func recordFailure(_ peerID: String) {
        lock.lock()
        defer { lock.unlock() }
        failures[peerID, default: []].append(Date())
    }
}
