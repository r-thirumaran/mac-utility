import Foundation

/// Where paired peers' static keys live. The app implements this over its config file;
/// `MemoryTrustStore` is for tests and for the first run before anything is persisted.
///
/// Called from whichever queue drives the handshake, so implementations must be thread-safe.
protocol TrustStore: AnyObject {
    /// The static public key previously stored for `id`, or `nil` if the peer is unknown.
    func trustedKey(for id: String) -> Data?
    /// Remembers `peer` as paired. Called once the first encrypted frame has been verified.
    func trust(_ peer: PeerInfo)
}
