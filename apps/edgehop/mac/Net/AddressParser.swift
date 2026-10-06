import Foundation
import Network

/// `host:port` strings the way Go's `net.SplitHostPort` / `net.JoinHostPort` treat them
/// (IPv6 literals in brackets), and the `NWEndpoint` to dial for one.
enum AddressParser {

    /// Splits `host:port` / `[v6]:port`; `nil` when there is no port.
    static func split(_ address: String) -> (host: String, port: Int)? {
        let trimmed = address.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        if trimmed.hasPrefix("[") {
            guard let close = trimmed.firstIndex(of: "]") else { return nil }
            let host = String(trimmed[trimmed.index(after: trimmed.startIndex)..<close])
            let rest = trimmed[trimmed.index(after: close)...]
            guard rest.hasPrefix(":"), let port = Int(rest.dropFirst()), port >= 0, port <= 65535 else { return nil }
            return (host, port)
        }
        let colons = trimmed.filter { $0 == ":" }.count
        guard colons == 1, let colon = trimmed.lastIndex(of: ":") else { return nil }
        let host = String(trimmed[trimmed.startIndex..<colon])
        guard let port = Int(trimmed[trimmed.index(after: colon)...]), port >= 0, port <= 65535 else { return nil }
        return (host, port)
    }

    /// `host:port`, with brackets around an IPv6 literal.
    static func join(_ host: String, _ port: Int) -> String {
        if host.contains(":") && !host.hasPrefix("[") {
            return "[" + host + "]:" + String(port)
        }
        return host + ":" + String(port)
    }

    /// The endpoint for `host:port` (a hostname, IPv4 or IPv6 literal). `nil` without a port.
    static func endpoint(_ address: String) -> NWEndpoint? {
        guard let parts = split(address), !parts.host.isEmpty,
              let port = NWEndpoint.Port(rawValue: UInt16(parts.port)) else { return nil }
        return NWEndpoint.hostPort(host: NWEndpoint.Host(parts.host), port: port)
    }

    /// The host string of a connection's remote endpoint ("" when unknown).
    static func host(of endpoint: NWEndpoint?) -> String {
        guard let endpoint, let hostPort = BonjourService.HostPort(endpoint: endpoint) else { return "" }
        return hostPort.host
    }
}
