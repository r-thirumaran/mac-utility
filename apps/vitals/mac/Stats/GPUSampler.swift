import Foundation
import IOKit
import Metal

/// GPU utilisation and memory from the `IOAccelerator` registry entries (no root, no
/// entitlement) plus names and working-set sizes from Metal.
///
/// Every accelerator publishes a `PerformanceStatistics` dictionary whose keys differ by
/// driver family: `Device Utilization %` / `Renderer Utilization %` / `Tiler Utilization %` /
/// `In use system memory` on Apple silicon, `GPU Activity(%)` / `vramUsedBytes` on older
/// Intel and AMD parts. Every key is optional and the reading degrades to zeros rather than
/// failing. Registry entries are matched once and cached; a failed property read (an eGPU
/// unplugged) triggers a re-discovery on the next tick.
///
/// Not thread-safe: call it from one queue (the `SystemSampler` queue).
final class GPUSampler {

    struct Reading {
        var name: String
        /// 0 … 100.
        var utilization: Double
        var memUsed: UInt64
        var memTotal: UInt64
        var renderer: Double?
        var tiler: Double?
    }

    private struct Accelerator {
        var entry: io_registry_entry_t
        var name: String
        var memTotal: UInt64
    }

    private struct MetalInfo {
        var name: String
        var registryID: UInt64
        var maxWorkingSet: UInt64
        var unified: Bool
    }

    private var accelerators: [Accelerator] = []
    private let metalDevices: [MetalInfo]
    private var needsDiscovery = true

    init() {
        metalDevices = MTLCopyAllDevices().map { device in
            MetalInfo(name: device.name,
                      registryID: device.registryID,
                      maxWorkingSet: device.recommendedMaxWorkingSetSize,
                      unified: device.hasUnifiedMemory)
        }
    }

    deinit {
        releaseEntries()
    }

    // MARK: - Sampling

    func sample() -> [Reading] {
        if needsDiscovery {
            discover()
        }
        var readings: [Reading] = []
        for accelerator in accelerators {
            guard let stats = GPUSampler.property(accelerator.entry, "PerformanceStatistics") as? [String: Any] else {
                // The entry has gone away (or never had statistics); look again next time.
                needsDiscovery = true
                continue
            }
            let utilization = GPUSampler.number(stats["Device Utilization %"])
                ?? GPUSampler.number(stats["GPU Activity(%)"])
                ?? GPUSampler.number(stats["Renderer Utilization %"])
                ?? 0
            var memUsed = GPUSampler.unsigned(stats["In use system memory"])
                ?? GPUSampler.unsigned(stats["vramUsedBytes"])
                ?? GPUSampler.unsigned(stats["inUseVidMemoryBytes"])
                ?? GPUSampler.unsigned(stats["Alloc system memory"])
                ?? 0
            if memUsed == 0, accelerator.memTotal > 0, let free = GPUSampler.unsigned(stats["vramFreeBytes"]),
               free <= accelerator.memTotal {
                memUsed = accelerator.memTotal - free
            }
            readings.append(Reading(name: accelerator.name,
                                    utilization: GPUSampler.clampPercent(utilization),
                                    memUsed: memUsed,
                                    memTotal: accelerator.memTotal,
                                    renderer: GPUSampler.number(stats["Renderer Utilization %"]).map(GPUSampler.clampPercent),
                                    tiler: GPUSampler.number(stats["Tiler Utilization %"]).map(GPUSampler.clampPercent)))
        }
        if readings.isEmpty, let fallback = metalDevices.first {
            // No accelerator statistics (virtual machine, unusual driver): still name the GPU.
            readings.append(Reading(name: fallback.name, utilization: 0, memUsed: 0,
                                    memTotal: fallback.maxWorkingSet, renderer: nil, tiler: nil))
        }
        return readings
    }

    // MARK: - Discovery

    private func discover() {
        releaseEntries()
        needsDiscovery = false

        var iterator: io_iterator_t = 0
        let matching = IOServiceMatching("IOAccelerator")
        guard IOServiceGetMatchingServices(kIOMainPortDefault, matching, &iterator) == KERN_SUCCESS else { return }
        defer { IOObjectRelease(iterator) }

        var index = 0
        while true {
            let entry = IOIteratorNext(iterator)
            guard entry != 0 else { break }
            // Entries without statistics (e.g. an inactive GPU) are not worth polling.
            guard GPUSampler.property(entry, "PerformanceStatistics") is [String: Any] else {
                IOObjectRelease(entry)
                continue
            }
            let metal = matchMetal(entry: entry, index: index)
            let name = GPUSampler.modelName(entry) ?? metal?.name ?? (GPUSampler.property(entry, "IOClass") as? String) ?? "GPU"
            let memTotal = metal?.maxWorkingSet ?? GPUSampler.vramTotal(entry) ?? 0
            accelerators.append(Accelerator(entry: entry, name: name, memTotal: memTotal))
            index += 1
        }
    }

    /// Matches an accelerator to its Metal device by registry entry id, falling back to the
    /// enumeration order (both lists are small and usually in the same order).
    private func matchMetal(entry: io_registry_entry_t, index: Int) -> MetalInfo? {
        var entryID: UInt64 = 0
        if IORegistryEntryGetRegistryEntryID(entry, &entryID) == KERN_SUCCESS,
           let match = metalDevices.first(where: { $0.registryID == entryID }) {
            return match
        }
        if index < metalDevices.count { return metalDevices[index] }
        return metalDevices.first
    }

    private func releaseEntries() {
        for accelerator in accelerators {
            IOObjectRelease(accelerator.entry)
        }
        accelerators.removeAll()
    }

    // MARK: - Registry helpers

    private static func property(_ entry: io_registry_entry_t, _ key: String) -> Any? {
        guard let value = IORegistryEntryCreateCFProperty(entry, key as CFString, kCFAllocatorDefault, 0) else {
            return nil
        }
        return value.takeRetainedValue()
    }

    /// `model` is a String on Apple silicon accelerators and NUL-terminated CFData on the
    /// parent `IOPCIDevice` of Intel/AMD parts.
    private static func modelName(_ entry: io_registry_entry_t) -> String? {
        if let text = property(entry, "model") as? String, !text.isEmpty { return text }
        var parent: io_registry_entry_t = 0
        guard IORegistryEntryGetParentEntry(entry, kIOServicePlane, &parent) == KERN_SUCCESS else { return nil }
        defer { IOObjectRelease(parent) }
        if let text = property(parent, "model") as? String, !text.isEmpty { return text }
        if let data = property(parent, "model") as? Data {
            let text = String(decoding: data.prefix { $0 != 0 }, as: UTF8.self)
            return text.isEmpty ? nil : text
        }
        return nil
    }

    /// `VRAM,totalMB` on the PCI parent of discrete GPUs.
    private static func vramTotal(_ entry: io_registry_entry_t) -> UInt64? {
        var parent: io_registry_entry_t = 0
        guard IORegistryEntryGetParentEntry(entry, kIOServicePlane, &parent) == KERN_SUCCESS else { return nil }
        defer { IOObjectRelease(parent) }
        guard let megabytes = unsigned(property(parent, "VRAM,totalMB")), megabytes > 0 else { return nil }
        return megabytes * 1_048_576
    }

    private static func number(_ value: Any?) -> Double? {
        (value as? NSNumber)?.doubleValue
    }

    private static func unsigned(_ value: Any?) -> UInt64? {
        guard let number = value as? NSNumber else { return nil }
        let double = number.doubleValue
        guard double >= 0 else { return nil }
        return number.uint64Value
    }

    private static func clampPercent(_ value: Double) -> Double {
        let clamped = min(100, max(0, value))
        return (clamped * 10).rounded() / 10
    }
}
