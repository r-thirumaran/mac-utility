import Foundation

/// A pairing this machine started (`edgehop.pair.start`): the link doing the pairing
/// handshake and what the UI has told us so far.
@MainActor
final class PairingSession {

    /// The peer's id when known (empty for a typed address until `hello_ack` names it).
    var peerID: String
    var peerName = ""
    let address: String
    var link: PeerLink?
    var codeSubmitted = false

    init(peerID: String, address: String) {
        self.peerID = peerID
        self.address = address
    }
}
