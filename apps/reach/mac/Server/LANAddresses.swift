import Foundation

/// The addresses phones can use: `http://<ip>:<port>/` for every non-loopback,
/// non-link-local IPv4 address of an interface that is up — what the settings page lists and
/// encodes in its QR code (the same selection as the Go agent's `lanURLs`).
enum LANAddresses {

    static func urls(port: Int) -> [String] {
        ipv4Addresses().map { "http://\($0):\(port)/" }
    }

    /// Usable IPv4 addresses in interface order.
    static func ipv4Addresses() -> [String] {
        var list: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&list) == 0, let first = list else { return [] }
        defer { freeifaddrs(list) }

        var addresses: [String] = []
        var cursor: UnsafeMutablePointer<ifaddrs>? = first
        while let entry = cursor {
            defer { cursor = entry.pointee.ifa_next }
            guard let address = entry.pointee.ifa_addr,
                  address.pointee.sa_family == sa_family_t(AF_INET) else { continue }
            let flags = Int32(bitPattern: entry.pointee.ifa_flags)
            guard flags & IFF_UP != 0, flags & IFF_LOOPBACK == 0 else { continue }

            var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
            let length = socklen_t(address.pointee.sa_len)
            guard getnameinfo(address, length, &host, socklen_t(host.count), nil, 0, NI_NUMERICHOST) == 0 else {
                continue
            }
            let text = String(cString: host)
            guard !text.isEmpty, !text.hasPrefix("127."), !text.hasPrefix("169.254.") else { continue }
            if !addresses.contains(text) {
                addresses.append(text)
            }
        }
        return addresses
    }
}
