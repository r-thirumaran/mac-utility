import Darwin
import Foundation

/// Per-core and total CPU load from `host_processor_info(PROCESSOR_CPU_LOAD_INFO)`.
///
/// The kernel hands back cumulative tick counters per core; load is the delta between two
/// calls, so the first `sample()` after construction only seeds the baseline and returns
/// `nil`. Counters are `UInt32` and wrap, which the wrapping subtraction handles. Every call
/// must `vm_deallocate` the out-of-line buffer or the process leaks a page per tick.
///
/// Not thread-safe: call it from one queue (the `SystemSampler` queue).
final class CPUSampler {

    struct Reading {
        /// 0 … 100 for the whole machine.
        var total: Double
        /// 0 … 100 per logical core, in kernel order.
        var perCore: [Double]
        /// 1, 5 and 15 minute load averages.
        var loadAvg: [Double]
    }

    /// `machdep.cpu.brand_string`, e.g. "Apple M3 Pro".
    let brand: String
    /// Logical core count.
    let coreCount: Int

    /// `mach_host_self()` returns a new send right every call; cache it once.
    private let host: host_t = mach_host_self()
    private var previousTicks: [UInt32] = []

    init() {
        brand = CPUSampler.readBrand()
        coreCount = max(1, Int(CPUSampler.readInt32("hw.logicalcpu") ?? Int32(ProcessInfo.processInfo.activeProcessorCount)))
    }

    // MARK: - Sampling

    func sample() -> Reading? {
        var cpuCount: natural_t = 0
        var info: processor_info_array_t?
        var infoCount: mach_msg_type_number_t = 0
        let result = host_processor_info(host, PROCESSOR_CPU_LOAD_INFO, &cpuCount, &info, &infoCount)
        guard result == KERN_SUCCESS, let info else { return nil }
        defer {
            let bytes = vm_size_t(Int(infoCount) * MemoryLayout<integer_t>.stride)
            vm_deallocate(mach_task_self_, vm_address_t(bitPattern: info), bytes)
        }

        let current = (0..<Int(infoCount)).map { UInt32(bitPattern: info[$0]) }
        defer { previousTicks = current }
        guard previousTicks.count == current.count, !current.isEmpty else { return nil }

        let stateCount = Int(CPU_STATE_MAX)
        var perCore: [Double] = []
        perCore.reserveCapacity(Int(cpuCount))
        var busyAll: UInt64 = 0
        var allAll: UInt64 = 0

        for core in 0..<Int(cpuCount) {
            let base = stateCount * core
            guard base + stateCount <= current.count else { break }
            func delta(_ state: Int32) -> UInt64 {
                UInt64(current[base + Int(state)] &- previousTicks[base + Int(state)])
            }
            let busy = delta(CPU_STATE_USER) + delta(CPU_STATE_SYSTEM) + delta(CPU_STATE_NICE)
            let all = busy + delta(CPU_STATE_IDLE)
            perCore.append(all == 0 ? 0 : CPUSampler.percent(busy, of: all))
            busyAll += busy
            allAll += all
        }

        let total = allAll == 0 ? 0 : CPUSampler.percent(busyAll, of: allAll)
        return Reading(total: total, perCore: perCore, loadAvg: CPUSampler.loadAverages())
    }

    // MARK: - Helpers

    private static func percent(_ part: UInt64, of whole: UInt64) -> Double {
        let value = Double(part) / Double(whole) * 100
        return (value * 10).rounded() / 10
    }

    private static func loadAverages() -> [Double] {
        var loads = [Double](repeating: 0, count: 3)
        let got = getloadavg(&loads, 3)
        guard got == 3 else { return [] }
        return loads.map { ($0 * 100).rounded() / 100 }
    }

    private static func readBrand() -> String {
        var size = 0
        guard sysctlbyname("machdep.cpu.brand_string", nil, &size, nil, 0) == 0, size > 0 else {
            return "Unknown CPU"
        }
        var buffer = [CChar](repeating: 0, count: size + 1)
        guard sysctlbyname("machdep.cpu.brand_string", &buffer, &size, nil, 0) == 0 else {
            return "Unknown CPU"
        }
        let text = String(cString: buffer).trimmingCharacters(in: .whitespacesAndNewlines)
        return text.isEmpty ? "Unknown CPU" : text
    }

    private static func readInt32(_ name: String) -> Int32? {
        var value: Int32 = 0
        var size = MemoryLayout<Int32>.size
        guard sysctlbyname(name, &value, &size, nil, 0) == 0 else { return nil }
        return value
    }
}
