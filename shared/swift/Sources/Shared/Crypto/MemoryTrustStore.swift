import Foundation

/// In-memory `TrustStore`. Thread-safe; nothing is persisted.
final class MemoryTrustStore: TrustStore {

    private let lock = NSLock()
    private var peers: [String: PeerInfo] = [:]

    init(peers: [PeerInfo] = []) {
        for peer in peers {
            self.peers[peer.id] = peer
        }
    }

    // MARK: - TrustStore

    func trustedKey(for id: String) -> Data? {
        lock.lock()
        defer { lock.unlock() }
        return peers[id]?.staticPublicKey
    }

    func trust(_ peer: PeerInfo) {
        lock.lock()
        defer { lock.unlock() }
        peers[peer.id] = peer
    }

    // MARK: - Extras

    func forget(_ id: String) {
        lock.lock()
        defer { lock.unlock() }
        peers[id] = nil
    }

    /// Every remembered peer, in no particular order.
    var all: [PeerInfo] {
        lock.lock()
        defer { lock.unlock() }
        return Array(peers.values)
    }
}
