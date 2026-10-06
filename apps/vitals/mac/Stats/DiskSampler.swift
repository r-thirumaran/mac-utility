import Foundation
import IOKit

/// Read / write bytes per second summed over every block storage driver.
///
/// Each `IOBlockStorageDriver` (one per physical disk; APFS volumes share their container's
/// driver) publishes a `Statistics` dictionary with cumulative `Bytes (Read)` /
/// `Bytes (Write)` counters. Drivers are re-matched on every tick — cheap for the handful of
/// disks a Mac has, and it picks up external drives as they are plugged in. Deltas are kept
/// per registry entry id so a disk that comes or goes never spikes the rate. Zeros when IOKit
/// reports nothing (virtual machines without the storage family, for instance).
///
/// Not thread-safe: call it from one queue (the `SystemSampler` queue).
final class DiskSampler {

    struct Reading {
        var readBps: Double
        var writeBps: Double
    }

    private struct Counters {
        var read: UInt64
        var written: UInt64
    }

    private var previous: [UInt64: Counters] = [:]
    private var previousTime: TimeInterval = 0

    // MARK: - Sampling

    func sample() -> Reading {
        let now = ProcessInfo.processInfo.systemUptime
        let current = DiskSampler.readCounters()
        defer {
            previous = current
            previousTime = now
        }
        let elapsed = now - previousTime
        guard previousTime > 0, elapsed > 0 else { return Reading(readBps: 0, writeBps: 0) }

        var read: UInt64 = 0
        var written: UInt64 = 0
        for (id, counters) in current {
            guard let before = previous[id] else { continue }
            if counters.read >= before.read { read += counters.read - before.read }
            if counters.written >= before.written { written += counters.written - before.written }
        }
        return Reading(readBps: Double(read) / elapsed, writeBps: Double(written) / elapsed)
    }

    // MARK: - IOKit

    private static func readCounters() -> [UInt64: Counters] {
        var iterator: io_iterator_t = 0
        let matching = IOServiceMatching("IOBlockStorageDriver")
        guard IOServiceGetMatchingServices(kIOMainPortDefault, matching, &iterator) == KERN_SUCCESS else { return [:] }
        defer { IOObjectRelease(iterator) }

        var result: [UInt64: Counters] = [:]
        while true {
            let entry = IOIteratorNext(iterator)
            guard entry != 0 else { break }
            defer { IOObjectRelease(entry) }

            var id: UInt64 = 0
            guard IORegistryEntryGetRegistryEntryID(entry, &id) == KERN_SUCCESS,
                  let property = IORegistryEntryCreateCFProperty(entry, "Statistics" as CFString, kCFAllocatorDefault, 0),
                  let statistics = property.takeRetainedValue() as? [String: Any] else {
                continue
            }
            result[id] = Counters(read: unsigned(statistics["Bytes (Read)"]),
                                  written: unsigned(statistics["Bytes (Write)"]))
        }
        return result
    }

    private static func unsigned(_ value: Any?) -> UInt64 {
        guard let number = value as? NSNumber, number.doubleValue >= 0 else { return 0 }
        return number.uint64Value
    }
}
