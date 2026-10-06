import Foundation

/// The payload of the `edgehop.pairing` event and the `pairing` field of `edgehop.state`
/// (`core.PairingState`): progress of a pairing this machine started.
struct PairingState: Codable {
    /// `connecting` | `waiting_code` | `paired` | `failed` | `cancelled`.
    var stage: String
    var peerId: String?
    var name: String?
    var address: String?
    var error: String?

    init(stage: String, peerId: String = "", name: String = "", address: String = "", error: String = "") {
        self.stage = stage
        self.peerId = peerId.isEmpty ? nil : peerId
        self.name = name.isEmpty ? nil : name
        self.address = address.isEmpty ? nil : address
        self.error = error.isEmpty ? nil : error
    }
}
