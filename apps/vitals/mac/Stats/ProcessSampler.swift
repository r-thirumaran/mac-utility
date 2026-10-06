import Darwin
import Foundation

/// The process table behind `vitals.processes`: `proc_listpids` for the pid set,
/// `proc_pidinfo(PROC_PIDTASKINFO)` for CPU time, `proc_pid_rusage` for the physical
/// footprint (Activity Monitor's "Memory" column; `pti_resident_size` when that is refused).
///
/// CPU % is the share of the whole machine used since the previous call (Task Manager
/// semantics, 100 = every core busy): mach-time ticks are converted to nanoseconds with
/// `mach_timebase_info`, diffed against the previous call and divided by the wall-clock delta
/// and the core count. The first call therefore reports 0 % for every row.
///
/// `proc_pidinfo` and `proc_pid_rusage` return `EPERM` for other users' processes (root
/// daemons, `_windowserver`, …) unless the caller is root; those rows are skipped silently
/// and the listing is flagged `partial`. Names come from the `apps` map (built on the main
/// actor from `NSRunningApplication`, which this class must not touch) or from the
/// executable path; the 16-character `pbsi_comm` is the last resort.
///
/// Not thread-safe: call it from one queue (the bridge's process queue).
final class ProcessSampler {

    struct Row {
        var pid: Int
        var name: String
        /// 0 … 100 of the whole machine.
        var cpu: Double
        var memBytes: UInt64
        /// 32 px PNG, base64 (GUI apps only, on request).
        var icon: String?
        var user: String?
    }

    /// What the caller already knows about a GUI app (from `NSRunningApplication`).
    struct AppIdentity {
        var name: String
        var icon: String?
    }

    struct Listing {
        var rows: [Row]
        /// `true` when at least one process was skipped because its info is not readable.
        var partial: Bool
        var skipped: Int
    }

    private struct CPUTime {
        var nanoseconds: UInt64
    }

    /// `PROC_PIDPATHINFO_MAXSIZE` (4 × MAXPATHLEN).
    private static let pathBufferSize = 4096

    private let coreCount: Int
    private let timebase: mach_timebase_info_data_t
    private var previous: [pid_t: CPUTime] = [:]
    private var previousWall: UInt64 = 0
    private var userNames: [uid_t: String] = [:]
    private var loggedPartial = false

    init(coreCount: Int) {
        self.coreCount = max(1, coreCount)
        var info = mach_timebase_info_data_t()
        _ = mach_timebase_info(&info)
        if info.numer == 0 || info.denom == 0 {
            info = mach_timebase_info_data_t(numer: 1, denom: 1)
        }
        timebase = info
    }

    // MARK: - Listing

    func list(apps: [pid_t: AppIdentity]) -> Listing {
        let pids = ProcessSampler.allPIDs()
        let now = mach_absolute_time()
        let wallNanoseconds = previousWall == 0 ? 0 : nanoseconds(now &- previousWall)

        var current: [pid_t: CPUTime] = [:]
        current.reserveCapacity(pids.count)
        var rows: [Row] = []
        rows.reserveCapacity(pids.count)
        var skipped = 0

        for pid in pids {
            var task = proc_taskinfo()
            let taskSize = Int32(MemoryLayout<proc_taskinfo>.size)
            guard proc_pidinfo(pid, PROC_PIDTASKINFO, 0, &task, taskSize) == taskSize else {
                // EPERM for another user's process, ESRCH for one that exited meanwhile.
                skipped += 1
                continue
            }
            let cpuNanoseconds = nanoseconds(task.pti_total_user) &+ nanoseconds(task.pti_total_system)
            current[pid] = CPUTime(nanoseconds: cpuNanoseconds)

            var cpu = 0.0
            if let before = previous[pid], wallNanoseconds > 0, cpuNanoseconds >= before.nanoseconds {
                let busy = Double(cpuNanoseconds - before.nanoseconds)
                cpu = busy / Double(wallNanoseconds) * 100 / Double(coreCount)
                cpu = min(100, max(0, (cpu * 10).rounded() / 10))
            }

            var short = proc_bsdshortinfo()
            let shortSize = Int32(MemoryLayout<proc_bsdshortinfo>.size)
            let haveShort = proc_pidinfo(pid, PROC_PIDT_SHORTBSDINFO, 0, &short, shortSize) == shortSize

            let identity = apps[pid]
            var name = identity?.name ?? ProcessSampler.executableName(pid) ?? ""
            if name.isEmpty, haveShort {
                name = ProcessSampler.command(short)
            }
            rows.append(Row(pid: Int(pid),
                            name: name.isEmpty ? "pid \(pid)" : name,
                            cpu: cpu,
                            memBytes: ProcessSampler.footprint(pid) ?? task.pti_resident_size,
                            icon: identity?.icon,
                            user: haveShort ? userName(short.pbsi_uid) : nil))
        }

        previous = current
        previousWall = now
        if skipped > 0, !loggedPartial {
            loggedPartial = true
            Log.info("process list is partial: \(skipped) processes belong to other users", category: "processes")
        }
        return Listing(rows: rows, partial: skipped > 0, skipped: skipped)
    }

    // MARK: - Ordering

    /// Mirrors the Go backend: "cpu" (default) and "mem" / "memory" / "memBytes" descending,
    /// "name" ascending (case-insensitive), "pid" ascending; ties break on pid so the order
    /// is stable between refreshes.
    static func sort(_ rows: [Row], by sortBy: String) -> [Row] {
        let key = sortBy.trimmingCharacters(in: .whitespaces).lowercased()
        return rows.sorted { a, b in
            switch key {
            case "mem", "memory", "membytes":
                if a.memBytes != b.memBytes { return a.memBytes > b.memBytes }
            case "name":
                let left = a.name.lowercased()
                let right = b.name.lowercased()
                if left != right { return left < right }
            case "pid":
                return a.pid < b.pid
            default:
                if a.cpu != b.cpu { return a.cpu > b.cpu }
            }
            return a.pid < b.pid
        }
    }

    /// Truncates to `limit` rows when `limit` is positive.
    static func limit(_ rows: [Row], to limit: Int) -> [Row] {
        guard limit > 0, rows.count > limit else { return rows }
        return Array(rows.prefix(limit))
    }

    // MARK: - libproc

    /// Every pid, from a buffer sized by a first `nil` call (the kernel returns bytes, and
    /// pid 0 is real, so the returned length is used rather than a zero scan).
    private static func allPIDs() -> [pid_t] {
        let bytes = proc_listpids(UInt32(PROC_ALL_PIDS), 0, nil, 0)
        guard bytes > 0 else { return [] }
        // Leave room for processes that start between the two calls.
        let capacity = Int(bytes) / MemoryLayout<pid_t>.stride + 64
        var buffer = [pid_t](repeating: 0, count: capacity)
        let written = buffer.withUnsafeMutableBytes { raw -> Int32 in
            proc_listpids(UInt32(PROC_ALL_PIDS), 0, raw.baseAddress, Int32(raw.count))
        }
        guard written > 0 else { return [] }
        let count = min(capacity, Int(written) / MemoryLayout<pid_t>.stride)
        return buffer.prefix(count).filter { $0 > 0 }
    }

    /// `ri_phys_footprint`, or `nil` when the call is refused (EPERM) or reports nothing.
    private static func footprint(_ pid: pid_t) -> UInt64? {
        var usage = rusage_info_v4()
        let status = withUnsafeMutablePointer(to: &usage) { pointer -> Int32 in
            pointer.withMemoryRebound(to: rusage_info_t?.self, capacity: 1) { rebound in
                proc_pid_rusage(pid, RUSAGE_INFO_V4, rebound)
            }
        }
        guard status == 0, usage.ri_phys_footprint > 0 else { return nil }
        return usage.ri_phys_footprint
    }

    /// Last path component of the executable (`proc_pidpath` has no same-user check).
    private static func executableName(_ pid: pid_t) -> String? {
        var buffer = [CChar](repeating: 0, count: pathBufferSize)
        let length = proc_pidpath(pid, &buffer, UInt32(buffer.count))
        guard length > 0 else { return nil }
        let name = (String(cString: buffer) as NSString).lastPathComponent
        return name.isEmpty ? nil : name
    }

    /// `pbsi_comm` (NUL-terminated, at most 16 characters).
    private static func command(_ info: proc_bsdshortinfo) -> String {
        var command = info.pbsi_comm
        let capacity = MemoryLayout.size(ofValue: command)   // computed before taking the pointer (exclusivity)
        return withUnsafePointer(to: &command) { pointer in
            pointer.withMemoryRebound(to: CChar.self, capacity: capacity) { text in
                String(cString: text)
            }
        }
    }

    private func userName(_ uid: uid_t) -> String? {
        if let cached = userNames[uid] { return cached }
        guard let entry = getpwuid(uid) else { return nil }
        let name = String(cString: entry.pointee.pw_name)
        userNames[uid] = name
        return name
    }

    /// Mach absolute time → nanoseconds (125/3 on Apple silicon, 1/1 on Intel).
    private func nanoseconds(_ ticks: UInt64) -> UInt64 {
        let numer = UInt64(timebase.numer)
        let denom = UInt64(timebase.denom)
        if numer == denom { return ticks }
        return (ticks / denom) * numer + ((ticks % denom) * numer) / denom
    }
}
