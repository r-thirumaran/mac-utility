#if canImport(Network)
import Foundation
import Network

/// One service found by `BonjourBrowser`.
///
/// To talk to it, hand `endpoint` straight to `NWConnection(to:using:)` — Network.framework
/// resolves the name. `resolve` exists for the cases that need a literal address, such as
/// Reach's `/api/peers` listing.
struct BonjourService: Equatable {

    /// A resolved address.
    struct HostPort: Equatable {
        var host: String
        var port: UInt16

        init?(endpoint: NWEndpoint) {
            guard case .hostPort(let host, let port) = endpoint else { return nil }
            self.host = HostPort.string(for: host)
            self.port = port.rawValue
        }

        static func string(for host: NWEndpoint.Host) -> String {
            switch host {
            case .ipv4(let address):
                return String(describing: address)
            case .ipv6(let address):
                // Drop the "%en0" scope so the string is usable from another machine.
                let text = String(describing: address)
                if let percent = text.firstIndex(of: "%") {
                    return String(text[text.startIndex..<percent])
                }
                return text
            case .name(let name, _):
                return name
            @unknown default:
                return String(describing: host)
            }
        }
    }

    /// Instance name (the peer's display name).
    let name: String
    /// Service type, e.g. `_edgehop._tcp`.
    let type: String
    let domain: String
    let endpoint: NWEndpoint
    /// Parsed TXT record.
    let txt: [String: String]

    // MARK: - Resolution

    /// Connects briefly to learn the address behind the Bonjour name. `completion` runs on
    /// the main queue with `nil` on failure or after `timeout` seconds.
    static func resolve(_ endpoint: NWEndpoint,
                        timeout: TimeInterval = 5,
                        completion: @escaping (HostPort?) -> Void) {
        final class State {
            var finished = false
        }
        let state = State()
        let queue = DispatchQueue(label: "shared.bonjour.resolve")
        let connection = NWConnection(to: endpoint, using: .tcp)

        func finish(_ result: HostPort?) {
            guard !state.finished else { return }
            state.finished = true
            connection.cancel()
            DispatchQueue.main.async {
                completion(result)
            }
        }

        connection.stateUpdateHandler = { newState in
            switch newState {
            case .ready:
                let remote = connection.currentPath?.remoteEndpoint
                finish(remote.flatMap { HostPort(endpoint: $0) })
            case .failed, .cancelled, .waiting:
                finish(nil)
            default:
                break
            }
        }
        connection.start(queue: queue)
        queue.asyncAfter(deadline: .now() + timeout) {
            finish(nil)
        }
    }
}
#endif
