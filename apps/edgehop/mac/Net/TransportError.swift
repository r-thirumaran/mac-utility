import Foundation

/// Errors the transport reports to the bridge (`edgehop.pair.*`).
enum TransportError: Error, CustomStringConvertible {
    case notStarted
    case noAddress(String)
    case badAddress(String)
    case pairingInProgress
    case noPairing
    case wrongPairing
    case codeAlreadySubmitted

    var description: String {
        switch self {
        case .notStarted: return "transport not started"
        case .noAddress(let id): return "no address known for \(id.prefix(8))"
        case .badAddress(let addr): return "cannot parse address \"\(addr)\""
        case .pairingInProgress: return "a pairing is already in progress"
        case .noPairing: return "no pairing in progress"
        case .wrongPairing: return "code is for a different pairing"
        case .codeAlreadySubmitted: return "code already submitted"
        }
    }
}
