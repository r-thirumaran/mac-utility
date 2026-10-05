import Foundation

/// What a handshake tells us about the other machine, and what a trust store remembers.
struct PeerInfo: Equatable, Codable {
    /// `machineId` — derived from `staticPublicKey`, verified by the handshake.
    var id: String
    /// Display name.
    var name: String
    /// `macos`, `windows` or `linux`.
    var plat: String
    /// Raw 32-byte X25519 public key.
    var staticPublicKey: Data

    init(id: String, name: String, plat: String, staticPublicKey: Data) {
        self.id = id
        self.name = name
        self.plat = plat
        self.staticPublicKey = staticPublicKey
    }
}
