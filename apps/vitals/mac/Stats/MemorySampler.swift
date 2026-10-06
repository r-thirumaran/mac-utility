import Darwin
import Foundation

/// Physical memory, swap and pressure.
///
/// Page counts come from `host_statistics64(HOST_VM_INFO64)` and are multiplied by the
/// kernel page size (16 KiB on Apple silicon, 4 KiB on Intel — never hard-code it). The
/// rows follow Activity Monitor: app memory = anonymous − purgeable, used = app + wired +
/// compressed, cached files = file-backed + purgeable. Swap is `vm.swapusage`; pressure is the
/// kernel's dispatch-encoded `kern.memorystatus_vm_pressure_level` (1 normal, 2 warning,
/// 4 critical), with the free-page percentage `kern.memorystatus_level` as a fallback.
final class MemorySampler {

    struct Reading {
        var total: UInt64
        var used: UInt64
        var app: UInt64
        var wired: UInt64
        var compressed: UInt64
        var cached: UInt64
        var swapUsed: UInt64
        var swapTotal: UInt64
        /// "normal" | "warning" | "critical" (the labels the UI and the Go backend use).
        var pressure: String
        /// Percentage of pages the kernel considers available, 0 … 100 (−1 if unreadable).
        var availablePercent: Int
    }

    private let host: host_t = mach_host_self()
    private let pageSize: UInt64
    let total: UInt64

    init() {
        pageSize = UInt64(vm_page_size)
        total = ProcessInfo.processInfo.physicalMemory
    }

    // MARK: - Sampling

    func sample() -> Reading? {
        var stats = vm_statistics64()
        var count = mach_msg_type_number_t(MemoryLayout<vm_statistics64_data_t>.size / MemoryLayout<integer_t>.size)
        let result = withUnsafeMutablePointer(to: &stats) { pointer in
            pointer.withMemoryRebound(to: integer_t.self, capacity: Int(count)) { rebound in
                host_statistics64(host, HOST_VM_INFO64, rebound, &count)
            }
        }
        guard result == KERN_SUCCESS else { return nil }

        let wired = UInt64(stats.wire_count) * pageSize
        let compressed = UInt64(stats.compressor_page_count) * pageSize
        let anonymous = Int64(stats.internal_page_count) - Int64(stats.purgeable_count)
        let app = UInt64(max(0, anonymous)) * pageSize
        let cached = (UInt64(stats.external_page_count) + UInt64(stats.purgeable_count)) * pageSize
        let used = min(total, app + wired + compressed)

        let swap = MemorySampler.swapUsage()
        let level = MemorySampler.readInt32("kern.memorystatus_level") ?? -1

        return Reading(total: total,
                       used: used,
                       app: app,
                       wired: wired,
                       compressed: compressed,
                       cached: cached,
                       swapUsed: swap.used,
                       swapTotal: swap.total,
                       pressure: MemorySampler.pressure(availablePercent: level),
                       availablePercent: Int(level))
    }

    // MARK: - Helpers

    private static func swapUsage() -> (used: UInt64, total: UInt64) {
        var usage = xsw_usage()
        var size = MemoryLayout<xsw_usage>.size
        guard sysctlbyname("vm.swapusage", &usage, &size, nil, 0) == 0 else { return (0, 0) }
        return (usage.xsu_used, usage.xsu_total)
    }

    /// Dispatch-encoded level first; the free-page percentage when that key is unreadable.
    private static func pressure(availablePercent: Int32) -> String {
        if let level = readInt32("kern.memorystatus_vm_pressure_level") {
            switch level {
            case 4: return "critical"
            case 2: return "warning"
            case 1: return "normal"
            default: break
            }
        }
        guard availablePercent >= 0 else { return "normal" }
        if availablePercent < 5 { return "critical" }
        if availablePercent < 15 { return "warning" }
        return "normal"
    }

    private static func readInt32(_ name: String) -> Int32? {
        var value: Int32 = 0
        var size = MemoryLayout<Int32>.size
        guard sysctlbyname(name, &value, &size, nil, 0) == 0 else { return nil }
        return value
    }
}
