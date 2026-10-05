import Foundation

/// The two directional ChaCha20-Poly1305 keys a handshake derives
/// (`key_init→acc = okm[0..32]`, `key_acc→init = okm[32..64]`).
struct SecureChannelKeys: Equatable {
    var initiatorToAcceptor: Data
    var acceptorToInitiator: Data

    init(initiatorToAcceptor: Data, acceptorToInitiator: Data) {
        self.initiatorToAcceptor = initiatorToAcceptor
        self.acceptorToInitiator = acceptorToInitiator
    }
}
