import Darwin
import Foundation

/// Bytes in / out per second summed over the physical interfaces.
///
/// Counters come from the routing socket's `NET_RT_IFLIST2` dump: one `if_msghdr2` per
/// interface with 64-bit `if_data64` counters (`getifaddrs` only exposes the 32-bit ones,
/// which wrap after 4 GiB). Loopback and point-to-point tunnels are skipped — a `utun` VPN
/// would otherwise count the same bytes twice, once on the tunnel and once on the link
/// underneath. Deltas are kept per interface index so an interface that appears or
/// disappears between ticks never produces a spike; the first `sample()` only seeds the
/// baseline and reports zero.
///
/// Not thread-safe: call it from one queue (the `SystemSampler` queue).
final class NetworkSampler {

    struct Reading {
        var rxBps: Double
        var txBps: Double
    }

    private struct Counters {
        var received: UInt64
        var sent: UInt64
    }

    private var previous: [UInt16: Counters] = [:]
    private var previousTime: TimeInterval = 0

    // MARK: - Sampling

    func sample() -> Reading {
        let now = ProcessInfo.processInfo.systemUptime
        let current = NetworkSampler.readCounters()
        defer {
            previous = current
            previousTime = now
        }
        let elapsed = now - previousTime
        guard previousTime > 0, elapsed > 0 else { return Reading(rxBps: 0, txBps: 0) }

        var received: UInt64 = 0
        var sent: UInt64 = 0
        for (index, counters) in current {
            guard let before = previous[index] else { continue }
            if counters.received >= before.received { received += counters.received - before.received }
            if counters.sent >= before.sent { sent += counters.sent - before.sent }
        }
        return Reading(rxBps: Double(received) / elapsed, txBps: Double(sent) / elapsed)
    }

    // MARK: - sysctl

    /// Walks the `NET_RT_IFLIST2` message list by `ifm_msglen`, keeping `RTM_IFINFO2`
    /// records. Messages are 4-byte aligned while `if_data64` has 8-byte fields, hence
    /// `loadUnaligned`.
    private static func readCounters() -> [UInt16: Counters] {
        var mib: [Int32] = [CTL_NET, PF_ROUTE, 0, 0, NET_RT_IFLIST2, 0]
        var length = 0
        guard sysctl(&mib, UInt32(mib.count), nil, &length, nil, 0) == 0, length > 0 else { return [:] }
        var buffer = [UInt8](repeating: 0, count: length)
        guard sysctl(&mib, UInt32(mib.count), &buffer, &length, nil, 0) == 0 else { return [:] }

        var result: [UInt16: Counters] = [:]
        let headerSize = MemoryLayout<if_msghdr>.size
        let messageSize = MemoryLayout<if_msghdr2>.size
        buffer.withUnsafeBytes { raw in
            var offset = 0
            while offset + headerSize <= length {
                let header = raw.loadUnaligned(fromByteOffset: offset, as: if_msghdr.self)
                let messageLength = Int(header.ifm_msglen)
                guard messageLength > 0 else { break }
                if Int32(header.ifm_type) == RTM_IFINFO2, offset + messageSize <= length {
                    let message = raw.loadUnaligned(fromByteOffset: offset, as: if_msghdr2.self)
                    let flags = Int32(message.ifm_flags)
                    let virtual = flags & IFF_LOOPBACK != 0 || flags & IFF_POINTOPOINT != 0
                    if !virtual {
                        result[message.ifm_index] = Counters(received: message.ifm_data.ifi_ibytes,
                                                             sent: message.ifm_data.ifi_obytes)
                    }
                }
                offset += messageLength
            }
        }
        return result
    }
}
