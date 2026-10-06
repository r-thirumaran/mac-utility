import Foundation

/// The payload of the `edgehop.pairCode` event (`core.PairCode`): the code this machine
/// generated for an incoming pairing request, shown for `ttlSec` seconds.
struct PairCode: Codable {
    var code: String
    var peer: Machine
    var ttlSec: Int
}
