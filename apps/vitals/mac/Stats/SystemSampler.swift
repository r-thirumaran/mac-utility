import Foundation

/// Runs every stats sampler on one background queue and assembles `vitals.sample`.
///
/// A single `DispatchSourceTimer` drives everything. While a page is subscribed and the
/// window is visible it fires at the requested interval (default 1 s, clamped to 250 ms … 10 s,
/// the fastest subscriber wins — the same rules as `sampler.go`); otherwise it drops to a 2 s
/// background cadence that keeps the menu bar title fresh. Every tick produces one
/// `[String: Any]` shaped exactly like `normalizeSample` in the Go backend: `perCore` is
/// never missing, the `gpu` array and the `battery` object are always present and `pressure`
/// always carries a label. `onSample` runs on the main actor after each tick with the sample
/// and whether it should reach the page.
final class SystemSampler {

    typealias Sample = [String: Any]

    static let defaultIntervalMs = 1000
    static let minIntervalMs = 250
    static let maxIntervalMs = 10_000
    static let backgroundIntervalMs = 2000

    /// Called on the main actor after every tick. The flag is `true` when at least one page
    /// is subscribed and the window is visible, i.e. the sample should be emitted.
    var onSample: (@MainActor (Sample, Bool) -> Void)?

    let cpu = CPUSampler()
    let memory = MemorySampler()
    let battery = BatterySampler()

    private let gpu = GPUSampler()
    private let network = NetworkSampler()
    private let disk = DiskSampler()

    private let queue = DispatchQueue(label: "vitals.sampler", qos: .utility)
    private var timer: DispatchSourceTimer?
    private var subscribers = 0
    private var requestedMs = SystemSampler.defaultIntervalMs
    private var pageVisible = false
    private var currentMs = 0

    // MARK: - Lifecycle

    /// Starts the background cadence.
    func start() {
        queue.async { self.reschedule() }
    }

    func stop() {
        queue.async {
            self.timer?.cancel()
            self.timer = nil
            self.currentMs = 0
        }
    }

    // MARK: - Subscriptions

    /// Adds a subscriber asking for `intervalMs` (≤ 0 means the default) and returns the
    /// interval the loop now runs at plus the subscriber count.
    func subscribe(intervalMs: Int) -> (intervalMs: Int, subscribers: Int) {
        let wanted = SystemSampler.clamp(intervalMs)
        return queue.sync {
            subscribers += 1
            requestedMs = subscribers == 1 ? wanted : min(requestedMs, wanted)
            reschedule()
            return (requestedMs, subscribers)
        }
    }

    /// Removes one subscriber; the loop falls back to the background cadence with the last.
    func unsubscribe() -> Int {
        queue.sync {
            subscribers = max(0, subscribers - 1)
            reschedule()
            return subscribers
        }
    }

    /// The window was shown or closed. A hidden page keeps its subscription but stops
    /// receiving samples, and the loop slows down to the background cadence.
    func setPageVisible(_ visible: Bool) {
        queue.async {
            self.pageVisible = visible
            self.reschedule()
        }
    }

    static func clamp(_ intervalMs: Int) -> Int {
        let wanted = intervalMs <= 0 ? defaultIntervalMs : intervalMs
        return min(maxIntervalMs, max(minIntervalMs, wanted))
    }

    // MARK: - Timer (on `queue`)

    private func reschedule() {
        let live = subscribers > 0 && pageVisible
        let interval = live ? requestedMs : SystemSampler.backgroundIntervalMs
        if timer != nil, interval == currentMs { return }
        currentMs = interval

        if let existing = timer {
            existing.schedule(deadline: .now(), repeating: .milliseconds(interval), leeway: .milliseconds(interval / 10))
            return
        }
        let source = DispatchSource.makeTimerSource(queue: queue)
        source.setEventHandler { [weak self] in self?.tick() }
        source.schedule(deadline: .now(), repeating: .milliseconds(interval), leeway: .milliseconds(interval / 10))
        timer = source
        source.resume()
    }

    private func tick() {
        let sample = assemble()
        let live = subscribers > 0 && pageVisible
        guard let onSample else { return }
        DispatchQueue.main.async {
            MainActor.assumeIsolated {
                onSample(sample, live)
            }
        }
    }

    // MARK: - Assembly

    private func assemble() -> Sample {
        let cpuReading = cpu.sample()
        let memoryReading = memory.sample()
        let gpuReadings = gpu.sample()
        let netReading = network.sample()
        let diskReading = disk.sample()
        let batteryReading = battery.sample()

        var cpuObject: [String: Any] = [
            "total": cpuReading?.total ?? 0,
            "perCore": cpuReading?.perCore ?? [Double](repeating: 0, count: cpu.coreCount),
        ]
        if let loadAvg = cpuReading?.loadAvg, !loadAvg.isEmpty {
            cpuObject["loadAvg"] = loadAvg
        }

        let total = memoryReading?.total ?? memory.total
        let used = memoryReading?.used ?? 0
        let memObject: [String: Any] = [
            "total": total,
            "used": used,
            "app": memoryReading?.app ?? 0,
            "wired": memoryReading?.wired ?? 0,
            "compressed": memoryReading?.compressed ?? 0,
            "cached": memoryReading?.cached ?? 0,
            "free": total > used ? total - used : 0,
            "swapUsed": memoryReading?.swapUsed ?? 0,
            "swapTotal": memoryReading?.swapTotal ?? 0,
            "pressure": SystemSampler.pressureLabel(memoryReading?.pressure),
        ]

        let gpuObjects: [[String: Any]] = gpuReadings.map { reading in
            var object: [String: Any] = [
                "name": reading.name,
                "utilization": reading.utilization,
                "memUsed": reading.memUsed,
                "memTotal": reading.memTotal,
            ]
            if let renderer = reading.renderer { object["renderer"] = renderer }
            if let tiler = reading.tiler { object["tiler"] = tiler }
            return object
        }

        var batteryObject: [String: Any] = [
            "present": batteryReading.present,
            "percent": batteryReading.percent,
            "charging": batteryReading.charging,
            "onAC": batteryReading.onAC,
        ]
        if let minutes = batteryReading.timeToEmptyMin {
            batteryObject["timeToEmptyMin"] = minutes
        }

        let netObject: [String: Any] = ["rxBps": netReading.rxBps, "txBps": netReading.txBps]
        let diskObject: [String: Any] = ["readBps": diskReading.readBps, "writeBps": diskReading.writeBps]

        return [
            "t": Int64(Date().timeIntervalSince1970 * 1000),
            "cpu": cpuObject,
            "mem": memObject,
            "gpu": gpuObjects,
            "net": netObject,
            "disk": diskObject,
            "battery": batteryObject,
        ]
    }

    /// The UI knows "normal", "warning" and "critical".
    private static func pressureLabel(_ raw: String?) -> String {
        switch raw {
        case "warning", "warn": return "warning"
        case "critical": return "critical"
        default: return "normal"
        }
    }
}
