import Foundation

/// The payload of the `edgehop.pairResult` event (`core.PairResult`): how an incoming pairing
/// request ended.
struct PairResult: Codable {
    var peerId: String
    var name: String?
    var ok: Bool
    var error: String?

    init(peerId: String, name: String, ok: Bool, error: String = "") {
        self.peerId = peerId
        self.name = name.isEmpty ? nil : name
        self.ok = ok
        self.error = error.isEmpty ? nil : error
    }
}
