# macOS live system stats for Vitals (Swift, AppKit, macOS 14+)

## Summary
- CPU, memory, swap, pressure, GPU utilization, battery, net and disk counters are all readable **without root, without entitlements, without TCC prompts** via Mach/`sysctl`/IOKit/IOPowerSources. Per-process CPU/memory is the exception: `proc_pidinfo`/`proc_pid_rusage` return `EPERM` for other users' processes (root/`_windowserver`/daemons) unless the caller is root (XNU `proc_security_policy`, `PRIV_GLOBAL_PROC_INFO`).
- Every counter below is cumulative; sample on a timer (1 s), diff against the previous sample, and keep the previous sample per source.
- The reference implementation for all of this is exelban/stats (Swift). Formulas, key strings and buffer handling below are copied from its source and cross-checked against XNU/IOKit headers.
- GPU "PerformanceStatistics" keys differ by driver family: `Device Utilization %`/`Renderer Utilization %`/`Tiler Utilization %`/`In use system memory`/`Alloc system memory` on Apple Silicon (AGX); `GPU Activity(%)`/`vramUsedBytes`/`vramFreeBytes`/`Temperature(C)` on Intel/AMD.
- Temperature on Apple Silicon needs the private `IOHIDEventSystemClient*` symbols (what Stats/MenuMeters use). Feasible, but private; recommend skipping or shipping behind a flag.

## 1. CPU — `host_processor_info(PROCESSOR_CPU_LOAD_INFO)`

| Item | Exact value / signature | Source |
|---|---|---|
| Routine | `host_processor_info(host: host_t, flavor: processor_flavor_t, out count: natural_t, out info: processor_info_array_t)` — info is an **OOL array** the kernel allocates in your task | xnu `osfmk/mach/mach_host.defs` |
| Flavor | `PROCESSOR_CPU_LOAD_INFO = 2`; `struct processor_cpu_load_info { unsigned int cpu_ticks[CPU_STATE_MAX]; }` | xnu `osfmk/mach/processor_info.h` |
| Indices | `CPU_STATE_USER 0`, `CPU_STATE_SYSTEM 1`, `CPU_STATE_IDLE 2`, `CPU_STATE_NICE 3`, `CPU_STATE_MAX 4`; core *i* state *s* is `info[CPU_STATE_MAX*i + s]` | xnu `osfmk/mach/machine.h`; Stats `Modules/CPU/readers.swift` |
| Free | `vm_deallocate(mach_task_self_, vm_address_t(bitPattern: info), vm_size_t(Int(infoCount) * MemoryLayout<integer_t>.stride))` — every call, or you leak | xnu `osfmk/mach/vm_map.defs`; Stats `LoadReader` |
| Total | `host_statistics(host, HOST_CPU_LOAD_INFO /*3*/, ptr, &count)` → `host_cpu_load_info.cpu_ticks` (tuple `.0 user .1 system .2 idle .3 nice`), `HOST_CPU_LOAD_INFO_COUNT = sizeof/sizeof(integer_t)` | xnu `osfmk/mach/host_info.h` |
| P/E split | `sysctl hw.nperflevels`, `hw.perflevel0.logicalcpu`, `hw.perflevel0.physicalcpu`, `hw.perflevel0.name`, `hw.perflevel1.*` (level 0 = highest-performance cluster) | xnu `bsd/kern/kern_mib.c` lines ~1002-1020 |

Usage per interval: `busy = Δuser + Δsystem + Δnice`, `pct = busy / (busy + Δidle)`. Ticks are `natural_t` (UInt32) and wrap; diff with `UInt32(bitPattern:) &- UInt32(bitPattern:)` (Stats does exactly this). Cache `mach_host_self()` once (Stats stores `machHostPort`), don't call it per sample.

## 2. Memory — `host_statistics64(HOST_VM_INFO64)`, swap, pressure

`HOST_VM_INFO64 = 4`; count = `HOST_VM_INFO64_COUNT = sizeof(vm_statistics64_data_t)/sizeof(integer_t)` (xnu `osfmk/mach/host_info.h`). Fields (xnu `osfmk/mach/vm_statistics.h`), all page counts × `vm_page_size` (16384 on Apple Silicon, 4096 on Intel):

| field | type | meaning |
|---|---|---|
| `free_count` `active_count` `inactive_count` `wire_count` | natural_t | page queues |
| `speculative_count` `purgeable_count` `throttled_count` | natural_t | |
| `compressor_page_count` | natural_t | pages held by the compressor = "Compressed" |
| `external_page_count` | natural_t | file-backed (non-swap) pages |
| `internal_page_count` | natural_t | anonymous pages |
| `swapins` `swapouts` `compressions` `decompressions` `pageins` `pageouts` | uint64 | lifetime counters (rate = Δ) |
| `swapped_count` | uint64 | compressor pages currently in swap |

Reproducing Activity Monitor (Apple's guide defines the rows; the UI shows Memory Used = App Memory + Wired + Compressed):

| Activity Monitor row | formula | source |
|---|---|---|
| Wired Memory | `wire_count` | Stats, htop |
| Compressed | `compressor_page_count` | Stats, htop |
| App Memory | `internal_page_count − purgeable_count` (htop `darwin/Platform.c`, PR 1976 "aligns with Activity Monitor semantics") — Stats instead uses `used − wired − compressed` with `used = active+inactive+speculative+wired+compressed − purgeable − external` | htop, Stats `Modules/RAM/readers.swift` |
| Memory Used | `app + wired + compressed` | derived |
| Cached Files | `external_page_count + purgeable_count` | Stats |
| Physical Memory | `sysctl hw.memsize` / `ProcessInfo.processInfo.physicalMemory` | |
| Swap Used | `sysctlbyname("vm.swapusage")` → `struct xsw_usage { xsu_total, xsu_avail, xsu_used: u_int64; xsu_pagesize: u_int32; xsu_encrypted: boolean_t }` (mib form `[CTL_VM, VM_SWAPUSAGE=5]`, used by Apple's `top`) | xnu `bsd/sys/sysctl.h`; top `libtop.c` |

Memory pressure (three routes):
- `sysctlbyname("kern.memorystatus_vm_pressure_level")` → Int32 **dispatch-encoded**: `1` normal, `2` warn (kernel Warning *or* Urgent), `4` critical (`convert_internal_pressure_level_to_dispatch_level`, xnu `bsd/kern/kern_memorystatus_notify.c`; values `NOTE_MEMORYSTATUS_PRESSURE_{NORMAL,WARN,CRITICAL}` = 0x1/0x2/0x4 in `bsd/sys/event_private.h`). The priv check is `#if !XNU_TARGET_OS_OSX`, so it is readable by any user on macOS. Stats reads this key.
- `sysctlbyname("kern.memorystatus_level")` → Int32 percent = `available_pages*100/total_pages` (xnu `osfmk/vm/vm_pageout.c` `vm_pressure_response`). Useful as a 0–100 "headroom" gauge, not the same thing as the pressure state.
- `DispatchSource.makeMemoryPressureSource(eventMask: [.normal, .warning, .critical], queue:)`; handler reads `source.data` (`DispatchSource.MemoryPressureEvent`, OptionSet over `DISPATCH_MEMORYPRESSURE_NORMAL 0x01 / WARN 0x02 / CRITICAL 0x04`). Fires only on **transitions**; seed the initial state from the sysctl.

## 3. GPU — IOKit `IOAccelerator` + Metal (no root)

- Match: `IOServiceGetMatchingServices(kIOMainPortDefault, IOServiceMatching("IOAccelerator"), &it)`. `kIOAcceleratorClassName = "IOAccelerator"` (IOGraphics `IOKit/graphics/IOAccelClientConnect.h`). Apple Silicon drivers are subclasses (`IOClass` = `AGXAcceleratorG13X`/`G14X`/`G15X`…), so the same match works; Stats uses `fetchIOService(kIOAcceleratorClassName)` (`Kit/helpers.swift`), fastfetch additionally adds `"IOMatchCategory" = "IOAccelerator"` to the matching dict.
- Read: `IORegistryEntryCreateCFProperty(entry, "PerformanceStatistics" as CFString, kCFAllocatorDefault, 0)` (cheaper than `IORegistryEntryCreateCFProperties`, which copies the whole table). Values are CFNumbers.

Keys seen in `PerformanceStatistics`:

| Driver | keys | source |
|---|---|---|
| Apple Silicon (AGX, M1–M4) | `Device Utilization %`, `Renderer Utilization %`, `Tiler Utilization %`, `In use system memory`, `In use system memory (driver)`, `Alloc system memory`, `Allocated PB Size`, `TiledSceneBytes`, `SplitSceneCount`, `recoveryCount`, `lastRecoveryTime` | ioreg dump of `AGXAcceleratorG15X` in syswatch `src/collect/gpu.rs`, pinvou-agent `macos_gpu.rs`; Stats `Modules/GPU/reader.swift` reads the three `%` keys |
| Intel iGPU / AMD / NVIDIA | `Device Utilization %` (newer) or `GPU Activity(%)` (older AMD), `GPU Core Utilization` (fastfetch divides by 1e7), `Temperature(C)`, `Fan Speed(%)`, `Core Clock(MHz)`, `Memory Clock(MHz)`, `vramUsedBytes`, `vramFreeBytes`, `inUseVidMemoryBytes` | Stats `reader.swift`; fastfetch `src/detection/gpu/gpu_apple.c`; XRG `XRGGPUMiner.m`; WhateverGreen `Tools/GPUInfo/gpuinfo.c` |

Sibling properties on the accelerator entry: `IOClass`, `CFBundleIdentifier`, `MetalPluginName`, Apple Silicon `model` (String, e.g. "Apple M3 Pro") and `gpu-core-count` (Int; macmon reads it from `IOServiceMatching("AGXAccelerator")`), Intel dual-GPU `AGCInfo` → `poweredOffByAGC`, Intel/AMD `VRAM,totalMB`, `IOPCIMatch`/`IOPCIPrimaryMatch` ("0x<devid><vendorid>").
Name/vendor on Intel Macs: walk `IORegistryEntryGetParentEntry(entry, kIOServicePlane, &parent)` to the `IOPCIDevice` and read `model` (CFData, NUL-terminated ASCII), `vendor-id` (4-byte LE Data: `86 80 00 00` Intel 0x8086, `02 10 00 00` AMD 0x1002), `device-id` (Stats matches PCI devices with `IOName == "display"` to accelerators via the `IOPCIMatch` string).
Metal (public): `MTLCopyAllDevices() -> [MTLDevice]` (10.11), `MTLCreateSystemDefaultDevice()`, `device.name: String`, `recommendedMaxWorkingSetSize: UInt64` (10.12, "memory the GPU can allocate without affecting performance" — on unified memory ≈ 2/3–3/4 of RAM), `currentAllocatedSize: Int` (bytes this device has allocated), `hasUnifiedMemory: Bool`, `registryID: UInt64`.
How others do it: Stats = IOAccelerator (above) + private IOReport for ANE power/DCP frame rate (`Modules/GPU/reader.swift`, decls in `Modules/Sensors/bridge.h`); macmon = private IOReport only; iStats (Chris911) has **no GPU support** (SMC temps/fans/battery only); iStat Menus is closed source.

## 4. Process list — libproc

| Call | Signature / flavor | Notes |
|---|---|---|
| `proc_listpids(UInt32(PROC_ALL_PIDS /*1*/), 0, buf, bytes) -> Int32` | returns **bytes** written; with `buf == nil` returns `(nprocs+20)*4` (xnu `proc_info.c`) | `count = ret / 4`; buffer is zero-filled padding, pid 0 (`kernel_task`) is real |
| `proc_pidinfo(pid, PROC_PIDTASKINFO /*4*/, 0, &proc_taskinfo, size) -> Int32` | returns bytes (== size) or 0 with `errno` | `pti_virtual_size`, `pti_resident_size` (bytes), `pti_total_user`, `pti_total_system` (**mach absolute-time units** — "total time"), `pti_threadnum`, `pti_numrunning`, `pti_priority` |
| `PROC_PIDTASKALLINFO /*2*/` → `proc_taskallinfo { pbsd: proc_bsdinfo; ptinfo: proc_taskinfo }` | `pbi_pid pbi_ppid pbi_uid pbi_status pbi_start_tvsec pbi_comm[16] pbi_name[32]` ("empty if no name is registered") | same-user check |
| `PROC_PIDT_SHORTBSDINFO /*13*/` → `proc_bsdshortinfo` | `pbsi_pid pbsi_ppid pbsi_pgid pbsi_status pbsi_comm[16] pbsi_flags pbsi_uid pbsi_gid pbsi_ruid …` | **no** same-user check |
| `proc_pidpath(pid, buf, UInt32(PROC_PIDPATHINFO_MAXSIZE /*4*MAXPATHLEN*/))` | returns length | no same-user check |
| `proc_pid_rusage(pid, RUSAGE_INFO_V4 /*4*/, &rusage_info_v4)` | `ri_phys_footprint` = Activity Monitor "Memory" column (top `mem` = "Physical memory footprint of the process") | same-user check |
| `sysctl([CTL_KERN, KERN_PROC /*14*/, KERN_PROC_ALL /*0*/, 0])` → `[kinfo_proc]` | `kp_proc.p_pid`, `p_comm[17]`, `p_stat`, `p_starttime`; `kp_eproc.e_ppid`, `e_ucred.cr_uid`, `e_pcred.p_ruid` | works for all users |

CPU%: convert ticks with `mach_timebase_info` (htop: `(t/denom)*numer + ((t%denom)*numer)/denom`; Apple Silicon numer/denom = 125/3, Intel 1/1), then `Δ(user+system)_ns / Δwall_ns * 100` → 100 = one full core (Activity Monitor convention; divide by core count for a 0–100 scale).

Names/icons: `NSRunningApplication(processIdentifier: pid)` → `nil` for anything that is not an app (daemons, helpers); `.localizedName`, `.icon: NSImage?`, `.bundleIdentifier`, `.activationPolicy` (`.regular/.accessory/.prohibited`), `.launchDate`; `NSWorkspace.shared.runningApplications` enumerates GUI apps. Fallback: `proc_pidpath` → `NSWorkspace.shared.icon(forFile:)`, name = last path component (`p_comm`/`pbsi_comm` are truncated to `MAXCOMLEN` = 16 chars).

**Cross-user limitation.** xnu `bsd/kern/proc_info.c`: only `PROC_PIDT_SHORTBSDINFO`, `PROC_PIDUNIQIDENTIFIERINFO`, `PROC_PIDPATHINFO`, `PROC_PIDCOALITIONINFO`, `PROC_PIDPLATFORMINFO` are `NO_CHECK_SAME_USER`; everything else (incl. `PROC_PIDTASKINFO`, `proc_pid_rusage`) goes through `proc_security_policy`, which returns `EPERM` when uids differ unless `priv_check_cred(cred, PRIV_GLOBAL_PROC_INFO)` passes (root). Apple's `ps`/`top` get everything because they call `task_read_for_pid` from Apple-signed binaries (adv_cmds `ps/tasks.c`; `%cpu` = Σ thread `cpu_usage / TH_USAGE_SCALE(1000)` from `THREAD_BASIC_INFO`, a kernel-decayed recent average). Activity Monitor uses the private `sysmond` XPC service. Options for Vitals: (a) show CPU/mem only for same-uid processes and list the rest via `KERN_PROC_ALL`; (b) shell out like Stats: `/bin/ps -Aceo pid,pcpu,rss,comm -r` and `/usr/bin/top -l 2 -s 1 -stats pid,cpu,mem,command` (top's man page: "the first sample displayed will have an invalid %CPU … calculated using the delta between samples"); (c) a root helper (SMJobBless/launchd) — overkill for an ad-hoc-signed utility.

## 5. Battery / charger — IOPowerSources (`import IOKit.ps`)

| API (IOKitUser `ps.subproj/IOPowerSources.h`) | Swift | Ownership |
|---|---|---|
| `CFTypeRef IOPSCopyPowerSourcesInfo(void)` | `IOPSCopyPowerSourcesInfo().takeRetainedValue()` | caller releases |
| `CFArrayRef IOPSCopyPowerSourcesList(CFTypeRef blob)` | `.takeRetainedValue() as [CFTypeRef]` | caller releases |
| `CFDictionaryRef IOPSGetPowerSourceDescription(blob, ps)` | `.takeUnretainedValue() as? [String: Any]` | **owned by blob** — do not release |
| `CFStringRef IOPSGetProvidingPowerSourceType(blob)` | `.takeUnretainedValue()` → `"AC Power"`, `"Battery Power"`, `"UPS Power"` | |
| `CFTimeInterval IOPSGetTimeRemainingEstimate(void)` | `kIOPSTimeRemainingUnknown = -1.0`, `kIOPSTimeRemainingUnlimited = -2.0` | |
| `IOPSNotificationCreateRunLoopSource(callback, context)` | `CFRunLoopAddSource(CFRunLoopGetMain(), src.takeRetainedValue(), .defaultMode)` | or `notify_register_dispatch("com.apple.system.powersources.source", …)` |

Description keys (`IOPSKeys.h`): `kIOPSTypeKey "Type"` (`"InternalBattery"`, `"UPS"`), `kIOPSPowerSourceStateKey "Power Source State"` (`"AC Power"`, `"Battery Power"`, `"Off Line"`), `kIOPSCurrentCapacityKey "Current Capacity"`, `kIOPSMaxCapacityKey "Max Capacity"` (percent for internal batteries, so Current is already %), `kIOPSIsChargingKey "Is Charging"`, `kIOPSIsChargedKey "Is Charged"`, `kIOPSIsPresentKey "Is Present"`, `kIOPSTimeToEmptyKey "Time to Empty"`, `kIOPSTimeToFullChargeKey "Time to Full Charge"` (minutes, `-1` = still estimating), `kIOPSNameKey "Name"`, `kIOPSBatteryHealthKey "BatteryHealth"`, `kIOPSTemperatureKey "Temperature"`, `kIOPSVoltageKey "Voltage"` (mV), `kIOPSCurrentKey "Current"` (mA), `kIOPSHardwareSerialNumberKey`.
Deeper data: `IOServiceGetMatchingService(kIOMainPortDefault, IOServiceMatching("AppleSmartBattery"))` + `IORegistryEntryCreateCFProperty` for `CycleCount`, `DesignCapacity`, `MaxCapacity`/`AppleRawMaxCapacity`, `NominalChargeCapacity`, `Voltage`, `Amperage`, `Temperature`, `IsCharging`, `AdapterDetails`, `BatteryData`, `ChargerData` (keys Stats reads in `Modules/Battery/readers.swift`). Desktops: power-source list is empty → treat as AC.

## 6. Network / disk throughput (brief)

- `getifaddrs`: for `ifa_addr.pointee.sa_family == AF_LINK`, `ifa_data` points to `struct if_data` whose `ifi_ibytes`/`ifi_obytes` are **`u_int32_t`** (wrap at 4 GiB; Libinfo `getifaddrs.3`, xnu `bsd/net/if_var.h`). Stats only uses it for `ifi_baudrate`.
- Preferred: `sysctl([CTL_NET, PF_ROUTE, 0, 0, NET_RT_IFLIST2 /*6*/, 0])`, walk messages by `ifm_msglen`, keep `ifm_type == RTM_IFINFO2 (0x12)`, read `if_msghdr2.ifm_data: if_data64` → `ifi_ibytes`/`ifi_obytes` (`u_int64_t`), `ifm_index` → `if_indextoname`. Primary interface: `SCDynamicStoreCopyValue(nil, "State:/Network/Global/IPv4")["PrimaryInterface"]` (Stats `Modules/Net/readers.swift`).
- Disk: `IOServiceMatching("IOBlockStorageDriver")` entries carry `"Statistics"` (`kIOBlockStorageDriverStatisticsKey`) with `"Bytes (Read)"`, `"Bytes (Write)"`, `"Operations (Read)"`, `"Operations (Write)"`, `"Total Time (Read)"`, `"Total Time (Write)"`, `"Latency Time (Read)"`, `"Latency Time (Write)"`, `"Errors (Read|Write)"`, `"Retries (Read|Write)"` (IOStorageFamily `IOBlockStorageDriver.h`; times in ns). Physical-device granularity (one entry per `disk0`, APFS volumes share it). BSD name: child `IOMedia` via `IORegistryEntryGetChildEntry(drv, kIOServicePlane, &child)` → `"BSD Name"` (`kIOBSDNameKey`). Stats goes the other way: `IOBSDNameMatching(kIOMainPortDefault, 0, bsdName)` then `IORegistryEntryGetParentEntry` up to the driver; volume→BSD name via `statfs().f_mntfromname`.

## 7. Temperature (Apple Silicon) — feasibility

Route used by Stats (`Modules/Sensors/bridge.h` + `reader.m`, derived from MenuMeters `applesilicon_hardware_reader.m`; same as freedomtan/sensors):
`IOHIDEventSystemClientCreate(kCFAllocatorDefault)` → `IOHIDEventSystemClientSetMatching(client, {"PrimaryUsagePage": 0xff00, "PrimaryUsage": 5})` → `IOHIDEventSystemClientCopyServices(client)` → per service `IOHIDServiceClientCopyProperty(svc, "Product")` (sensor name) and `IOHIDServiceClientCopyEvent(svc, kIOHIDEventTypeTemperature /*15*/, 0, 0)` → `IOHIDEventGetFloatValue(event, IOHIDEventFieldBase(15) /* 15<<16 */)` = °C. Voltage/current: page `0xff08`, usage `3`/`2`, event type `kIOHIDEventTypePower = 25`. Symbols are exported by IOKit.framework but have **no public header**; Stats declares them in an ObjC bridging header. Intel: SMC user client (`AppleSMC`, keys `TC0P`, GPU `TCGC`/`TGDD` — Stats `SMC/smc.swift`).
Risk: private API (fine for ad-hoc-signed, not App Store), sensor names and availability vary per SoC, no documented mapping to "CPU die"; Stats disables HID sensors by default on Apple Silicon because polling them cost ~10% CPU before optimization. Recommendation: skip for v1, or poll ≤ every 5 s behind a setting.

## Permissions & detection
- No TCC category covers any of the above; nothing to request. Works outside the App Sandbox (Vitals is ad-hoc signed, unsandboxed). `kIOMainPortDefault` needs macOS 12+ (old name `kIOMasterPortDefault` deprecated).
- Detect the per-process limitation at runtime: `proc_pidinfo(...) == 0 && errno == EPERM` → mark row "n/a" or use the `ps`/`top` fallback. `geteuid() == 0` means full access.
- `kern.memorystatus_vm_pressure_level` is `CTLFLAG_MASKED` on release kernels (hidden from `sysctl -a`) but readable by name.

## Pitfalls
1. `host_processor_info` leaks a fresh OOL buffer per call unless `vm_deallocate`d; `mach_host_self()` returns a new send right each call — cache it.
2. Tick counters are UInt32 and wrap; first sample has no baseline → skip.
3. `vm_page_size` is 16 KiB on Apple Silicon; never hard-code 4096. Numbers will differ from Activity Monitor by tens of MB (it uses sysmond's view); pick one formula and keep it stable.
4. Pressure sysctl is 1/2/4, not 0/1/2/3; the Dispatch source only reports transitions.
5. `Device Utilization %` can momentarily exceed 100 (Stats clamps). Intel dual-GPU Macs expose two accelerators; the inactive one has `AGCInfo.poweredOffByAGC == 1`. Cache `io_object_t`s instead of re-matching every tick; `IOObjectRelease` iterator and entries.
6. `pti_total_user/system` are mach ticks, not ns; on Apple Silicon multiply by 125/3. `pti_resident_size` (RSS) ≠ Activity Monitor "Memory" (footprint).
7. `proc_listpids` size: call once with `nil`, allocate, call again, use the returned byte count (not a zero scan — pid 0 is valid).
8. `comm` names are 16 chars; prefer `NSRunningApplication.localizedName` or the executable basename.
9. `getifaddrs` counters are 32-bit; use `NET_RT_IFLIST2`. Parse with `loadUnaligned` (messages are 4-byte aligned, `if_data64` has 8-byte fields).
10. Battery: never `CFRelease` the description dictionary; `Time to Empty == -1` while estimating; UPS entries have `Type == "UPS"`.
11. Do all polling on a background `DispatchQueue`; marshal to `WKWebView.evaluateJavaScript` on main.

## Minimal Swift sketches

```swift
import Darwin, Foundation
// --- 1. CPU ---------------------------------------------------------------
final class CPUSampler {
    private let host = mach_host_self()          // cache; each call returns a new send right
    private var prev: [UInt32] = []
    struct Sample { var perCore: [Double]; var total: Double }
    func sample() -> Sample? {
        var ncpu: natural_t = 0; var info: processor_info_array_t?; var count: mach_msg_type_number_t = 0
        guard host_processor_info(host, PROCESSOR_CPU_LOAD_INFO, &ncpu, &info, &count) == KERN_SUCCESS, let info else { return nil }
        defer { vm_deallocate(mach_task_self_, vm_address_t(bitPattern: info), vm_size_t(Int(count) * MemoryLayout<integer_t>.stride)) }
        let cur = (0..<Int(count)).map { UInt32(bitPattern: info[$0]) }
        defer { prev = cur }
        guard prev.count == cur.count else { return nil }   // first sample: no baseline
        var cores: [Double] = []; var busyAll: UInt64 = 0, allAll: UInt64 = 0
        for i in 0..<Int(ncpu) {
            let b = Int(CPU_STATE_MAX) * i
            func d(_ s: Int32) -> UInt32 { cur[b + Int(s)] &- prev[b + Int(s)] }
            let busy = UInt64(d(CPU_STATE_USER)) + UInt64(d(CPU_STATE_SYSTEM)) + UInt64(d(CPU_STATE_NICE))
            let all = busy + UInt64(d(CPU_STATE_IDLE))
            cores.append(all == 0 ? 0 : Double(busy) / Double(all)); busyAll += busy; allAll += all
        }
        return Sample(perCore: cores, total: allAll == 0 ? 0 : Double(busyAll) / Double(allAll))
    }
}
// --- 2. Memory ------------------------------------------------------------
struct MemSnapshot { var total, used, app, wired, compressed, cached, swapUsed, swapTotal: UInt64; var pressure: Int32; var level: Int32 }
func memorySnapshot(host: host_t) -> MemSnapshot? {
    var vm = vm_statistics64()
    var count = mach_msg_type_number_t(MemoryLayout<vm_statistics64_data_t>.size / MemoryLayout<integer_t>.size)
    let kr = withUnsafeMutablePointer(to: &vm) { $0.withMemoryRebound(to: integer_t.self, capacity: Int(count)) { host_statistics64(host, HOST_VM_INFO64, $0, &count) } }
    guard kr == KERN_SUCCESS else { return nil }
    let page = UInt64(vm_page_size)
    let wired = UInt64(vm.wire_count) * page, comp = UInt64(vm.compressor_page_count) * page
    let app = UInt64(max(0, Int64(vm.internal_page_count) - Int64(vm.purgeable_count))) * page   // htop/Activity Monitor
    let cached = (UInt64(vm.external_page_count) + UInt64(vm.purgeable_count)) * page              // Stats
    var xsw = xsw_usage(); var len = MemoryLayout<xsw_usage>.size
    sysctlbyname("vm.swapusage", &xsw, &len, nil, 0)
    var pressure: Int32 = 1, level: Int32 = 0; var ilen = MemoryLayout<Int32>.size
    sysctlbyname("kern.memorystatus_vm_pressure_level", &pressure, &ilen, nil, 0)   // 1 normal, 2 warn, 4 critical
    sysctlbyname("kern.memorystatus_level", &level, &ilen, nil, 0)                  // % available pages
    return MemSnapshot(total: ProcessInfo.processInfo.physicalMemory, used: app + wired + comp, app: app, wired: wired,
                       compressed: comp, cached: cached, swapUsed: xsw.xsu_used, swapTotal: xsw.xsu_total, pressure: pressure, level: level)
}
let pressureSource = DispatchSource.makeMemoryPressureSource(eventMask: [.normal, .warning, .critical], queue: .global())
pressureSource.setEventHandler { let ev = pressureSource.data; _ = ev.contains(.critical) }   // transitions only
pressureSource.activate()
```

```swift
import IOKit, Metal
// --- 3. GPU ---------------------------------------------------------------
struct GPUStat { var ioClass: String; var model: String?; var cores: Int?; var util, renderer, tiler: Int?; var memUsed, memAlloc: UInt64?; var temp: Int? }
func prop<T>(_ e: io_registry_entry_t, _ k: String) -> T? {
    IORegistryEntryCreateCFProperty(e, k as CFString, kCFAllocatorDefault, 0)?.takeRetainedValue() as? T
}
func gpuStats() -> [GPUStat] {
    var it: io_iterator_t = 0
    guard IOServiceGetMatchingServices(kIOMainPortDefault, IOServiceMatching("IOAccelerator"), &it) == KERN_SUCCESS else { return [] }
    defer { IOObjectRelease(it) }
    var out: [GPUStat] = []
    while case let e = IOIteratorNext(it), e != 0 {
        defer { IOObjectRelease(e) }
        guard let s: [String: Any] = prop(e, "PerformanceStatistics") else { continue }
        var model: String? = prop(e, "model")                       // Apple Silicon: String on the accelerator
        if model == nil {                                            // Intel/AMD: CFData on parent IOPCIDevice
            var parent: io_registry_entry_t = 0
            if IORegistryEntryGetParentEntry(e, kIOServicePlane, &parent) == KERN_SUCCESS {
                defer { IOObjectRelease(parent) }
                if let d: Data = prop(parent, "model") { model = String(decoding: d.prefix { $0 != 0 }, as: UTF8.self) }
            }
        }
        out.append(GPUStat(ioClass: prop(e, "IOClass") ?? "", model: model, cores: prop(e, "gpu-core-count"),
            util: (s["Device Utilization %"] ?? s["GPU Activity(%)"]) as? Int, renderer: s["Renderer Utilization %"] as? Int,
            tiler: s["Tiler Utilization %"] as? Int, memUsed: (s["In use system memory"] ?? s["vramUsedBytes"]) as? UInt64,
            memAlloc: (s["Alloc system memory"] ?? s["vramFreeBytes"]) as? UInt64, temp: s["Temperature(C)"] as? Int))
    }
    return out
}
let metal = MTLCopyAllDevices().map { ($0.name, $0.recommendedMaxWorkingSetSize, $0.currentAllocatedSize, $0.hasUnifiedMemory, $0.registryID) }
```

```swift
import AppKit
// --- 4. Processes ---------------------------------------------------------
var tb = mach_timebase_info_data_t(); mach_timebase_info(&tb)
func ns(_ t: UInt64) -> UInt64 { (t / UInt64(tb.denom)) * UInt64(tb.numer) + ((t % UInt64(tb.denom)) * UInt64(tb.numer)) / UInt64(tb.denom) }
func allPIDs() -> [pid_t] {
    let bytes = proc_listpids(UInt32(PROC_ALL_PIDS), 0, nil, 0); guard bytes > 0 else { return [] }
    var buf = [pid_t](repeating: 0, count: Int(bytes) / MemoryLayout<pid_t>.size)
    let got = proc_listpids(UInt32(PROC_ALL_PIDS), 0, &buf, bytes)
    return Array(buf.prefix(Int(got) / MemoryLayout<pid_t>.size))
}
struct ProcSample { var cpuNS: UInt64; var rss: UInt64; var footprint: UInt64?; var threads: Int32 }
func sampleProcess(_ pid: pid_t) -> ProcSample? {              // returns nil (errno == EPERM) for other users' processes
    var ti = proc_taskinfo(); let sz = Int32(MemoryLayout<proc_taskinfo>.size)
    guard proc_pidinfo(pid, PROC_PIDTASKINFO, 0, &ti, sz) == sz else { return nil }
    var ri = rusage_info_v4()
    let ok = withUnsafeMutablePointer(to: &ri) { $0.withMemoryRebound(to: rusage_info_t?.self, capacity: 1) { proc_pid_rusage(pid, RUSAGE_INFO_V4, $0) } } == 0
    return ProcSample(cpuNS: ns(ti.pti_total_user) + ns(ti.pti_total_system), rss: ti.pti_resident_size,
                      footprint: ok ? ri.ri_phys_footprint : nil, threads: ti.pti_threadnum)
}
// cpuPercent = Double(cur.cpuNS - prev.cpuNS) / Double(wallDeltaNS) * 100   (100 == one core)
func identity(_ pid: pid_t) -> (name: String, icon: NSImage?, uid: uid_t, ppid: pid_t) {
    var sb = proc_bsdshortinfo()
    _ = proc_pidinfo(pid, PROC_PIDT_SHORTBSDINFO, 0, &sb, Int32(MemoryLayout<proc_bsdshortinfo>.size))   // no same-user check
    if let app = NSRunningApplication(processIdentifier: pid) { return (app.localizedName ?? "", app.icon, sb.pbsi_uid, pid_t(sb.pbsi_ppid)) }
    var path = [CChar](repeating: 0, count: Int(PROC_PIDPATHINFO_MAXSIZE))
    if proc_pidpath(pid, &path, UInt32(path.count)) > 0 {
        let p = String(cString: path); return ((p as NSString).lastPathComponent, NSWorkspace.shared.icon(forFile: p), sb.pbsi_uid, pid_t(sb.pbsi_ppid))
    }
    return (withUnsafePointer(to: &sb.pbsi_comm) { String(cString: UnsafeRawPointer($0).assumingMemoryBound(to: CChar.self)) }, nil, sb.pbsi_uid, pid_t(sb.pbsi_ppid))
}
// Fallback for other users' processes (what Stats does): /bin/ps -Aceo pid,pcpu,rss,comm -r
```

```swift
import IOKit.ps
// --- 5. Battery -----------------------------------------------------------
struct Battery { var percent: Int; var charging: Bool; var onAC: Bool; var minutesToEmpty: Int; var minutesToFull: Int }
func battery() -> Battery? {
    let blob = IOPSCopyPowerSourcesInfo().takeRetainedValue()
    let onAC = (IOPSGetProvidingPowerSourceType(blob).takeUnretainedValue() as String) == kIOPSACPowerValue
    let list = IOPSCopyPowerSourcesList(blob).takeRetainedValue() as [CFTypeRef]
    for ps in list {
        guard let d = IOPSGetPowerSourceDescription(blob, ps).takeUnretainedValue() as? [String: Any],
              d[kIOPSTypeKey] as? String == kIOPSInternalBatteryType else { continue }
        return Battery(percent: d[kIOPSCurrentCapacityKey] as? Int ?? 0, charging: d[kIOPSIsChargingKey] as? Bool ?? false, onAC: onAC,
                       minutesToEmpty: d[kIOPSTimeToEmptyKey] as? Int ?? -1, minutesToFull: d[kIOPSTimeToFullChargeKey] as? Int ?? -1)
    }
    return nil                                                   // desktop: no internal battery
}
let src = IOPSNotificationCreateRunLoopSource({ _ in /* re-read battery() */ }, nil).takeRetainedValue()
CFRunLoopAddSource(CFRunLoopGetMain(), src, .defaultMode)
```

```swift
// --- 6. Network (64-bit counters) / Disk ----------------------------------
func netBytes() -> [String: (rx: UInt64, tx: UInt64)] {
    var mib: [Int32] = [CTL_NET, PF_ROUTE, 0, 0, NET_RT_IFLIST2, 0]; var len = 0
    guard sysctl(&mib, 6, nil, &len, nil, 0) == 0 else { return [:] }
    var buf = [UInt8](repeating: 0, count: len); guard sysctl(&mib, 6, &buf, &len, nil, 0) == 0 else { return [:] }
    var out: [String: (UInt64, UInt64)] = [:]
    buf.withUnsafeBytes { raw in
        var off = 0
        while off + MemoryLayout<if_msghdr>.size <= len {
            let h = raw.loadUnaligned(fromByteOffset: off, as: if_msghdr.self)
            if Int32(h.ifm_type) == RTM_IFINFO2 {
                let h2 = raw.loadUnaligned(fromByteOffset: off, as: if_msghdr2.self)
                var name = [CChar](repeating: 0, count: Int(IF_NAMESIZE))
                if if_indextoname(UInt32(h2.ifm_index), &name) != nil { out[String(cString: name)] = (h2.ifm_data.ifi_ibytes, h2.ifm_data.ifi_obytes) }
            }
            off += Int(h.ifm_msglen)
        }
    }
    return out
}
func diskBytes() -> [String: (read: UInt64, write: UInt64)] {
    var it: io_iterator_t = 0
    guard IOServiceGetMatchingServices(kIOMainPortDefault, IOServiceMatching("IOBlockStorageDriver"), &it) == KERN_SUCCESS else { return [:] }
    defer { IOObjectRelease(it) }
    var out: [String: (UInt64, UInt64)] = [:]
    while case let drv = IOIteratorNext(it), drv != 0 {
        defer { IOObjectRelease(drv) }
        guard let s: [String: Any] = prop(drv, "Statistics") else { continue }
        var child: io_registry_entry_t = 0; var bsd = "disk?"
        if IORegistryEntryGetChildEntry(drv, kIOServicePlane, &child) == KERN_SUCCESS { bsd = prop(child, "BSD Name") ?? bsd; IOObjectRelease(child) }
        out[bsd] = (s["Bytes (Read)"] as? UInt64 ?? 0, s["Bytes (Write)"] as? UInt64 ?? 0)
    }
    return out
}
```

```c
// --- 7. Temperature shim (private API) — Shim.h, pass with: swiftc -import-objc-header Shim.h -framework IOKit
#include <CoreFoundation/CoreFoundation.h>
typedef struct __IOHIDEventSystemClient *IOHIDEventSystemClientRef;
typedef struct __IOHIDServiceClient *IOHIDServiceClientRef;
typedef struct __IOHIDEvent *IOHIDEventRef;
#define kIOHIDEventTypeTemperature 15
#define IOHIDEventFieldBase(type) ((type) << 16)
IOHIDEventSystemClientRef IOHIDEventSystemClientCreate(CFAllocatorRef);
int IOHIDEventSystemClientSetMatching(IOHIDEventSystemClientRef, CFDictionaryRef);
CFArrayRef IOHIDEventSystemClientCopyServices(IOHIDEventSystemClientRef);
CFTypeRef IOHIDServiceClientCopyProperty(IOHIDServiceClientRef, CFStringRef);
IOHIDEventRef IOHIDServiceClientCopyEvent(IOHIDServiceClientRef, int64_t type, int32_t, int64_t);
double IOHIDEventGetFloatValue(IOHIDEventRef, int32_t field);
```
```swift
// Swift side: client = IOHIDEventSystemClientCreate(kCFAllocatorDefault); IOHIDEventSystemClientSetMatching(client, ["PrimaryUsagePage": 0xff00, "PrimaryUsage": 5] as CFDictionary)
// for svc in IOHIDEventSystemClientCopyServices(client) as! [IOHIDServiceClientRef] { name = IOHIDServiceClientCopyProperty(svc, "Product" as CFString) as? String;
//   if let ev = IOHIDServiceClientCopyEvent(svc, 15, 0, 0) { celsius = IOHIDEventGetFloatValue(ev, 15 << 16); CFRelease(ev) } }   (Stats Modules/Sensors/reader.m)
```

## Unverified
- `MTLDevice.registryID` == `IORegistryEntryGetRegistryEntryID` of the matching `IOAccelerator` entry (plausible, used by fastfetch for its id, not checked against Apple docs here).
- Which entitlement lets Apple's `ps`/`top` call `task_read_for_pid` for other users (behaviour is empirical: Stats relies on it; the kernel-side rule verified is `PRIV_GLOBAL_PROC_INFO` for libproc).
- `IOPSGetTimeRemainingEstimate` unit: the header abstract says "estimated minutes remaining" but the return type is `CFTimeInterval`; prefer `kIOPSTimeToEmptyKey` (documented as minutes).
- AppleSmartBattery units (`Temperature` scale, `Amperage` sign) — key names verified from Stats, units not.
- Apple Silicon per-core ordering in `host_processor_info` (E-cores first on M1/M2 is commonly reported, not verified here); `hw.perflevel0.name` string values ("Performance"/"Efficiency").
- `GPU Core Utilization` scaling (fastfetch comments "Nvidia?" and divides by 1e7); `Fan Speed(RPM)`/`Total Power(W)` keys appear only in third-party code.
- Exact ±MB agreement of the memory formulas with Activity Monitor on macOS 14/15 (Activity Monitor reads sysmond; both formulas above are approximations used by htop/Stats).

## Sources
- Stats (exelban/stats, master): `Modules/CPU/readers.swift`, `Modules/RAM/readers.swift`, `Modules/GPU/reader.swift`, `Modules/Disk/readers.swift`, `Modules/Net/readers.swift`, `Modules/Battery/readers.swift`, `Modules/Sensors/readers.swift`, `Modules/Sensors/bridge.h`, `Modules/Sensors/reader.m`, `Kit/helpers.swift` — https://github.com/exelban/stats
- XNU (apple-oss-distributions/xnu, main): `osfmk/mach/mach_host.defs`, `osfmk/mach/machine.h`, `osfmk/mach/processor_info.h`, `osfmk/mach/host_info.h`, `osfmk/mach/vm_statistics.h`, `osfmk/mach/vm_map.defs`, `osfmk/vm/vm_pageout.c`, `bsd/sys/sysctl.h`, `bsd/sys/proc.h`, `bsd/sys/proc_info.h`, `bsd/kern/proc_info.c`, `bsd/kern/kern_memorystatus.c`, `bsd/kern/kern_memorystatus_notify.c`, `bsd/sys/event_private.h`, `bsd/sys/resource.h`, `bsd/net/if_var.h`, `bsd/net/if.h`, `bsd/net/route.h`, `bsd/sys/socket.h`, `bsd/kern/kern_mib.c`, `iokit/IOKit/IOBSD.h`, `libsyscall/wrappers/libproc/libproc.h`, `libproc.c` — https://github.com/apple-oss-distributions/xnu
- IOKitUser: `IOKitLib.h`, `ps.subproj/IOPowerSources.h`, `ps.subproj/IOPSKeys.h` — https://github.com/apple-oss-distributions/IOKitUser
- IOStorageFamily `IOBlockStorageDriver.h` — https://github.com/apple-oss-distributions/IOStorageFamily; IOGraphics `IOKit/graphics/IOAccelClientConnect.h` — https://github.com/apple-oss-distributions/IOGraphics
- libdispatch `dispatch/source.h` — https://github.com/apple/swift-corelibs-libdispatch
- Apple docs: DispatchSource.MemoryPressureEvent, DispatchSourceMemoryPressure, MTLDevice.recommendedMaxWorkingSetSize, MTLCopyAllDevices(), NSRunningApplication — https://developer.apple.com/documentation/
- Apple `top`: `libtop.c`, `top.1` — https://github.com/apple-oss-distributions/top; `ps`: `ps/print.c`, `ps/tasks.c` — https://github.com/apple-oss-distributions/adv_cmds; Libinfo `gen.subproj/getifaddrs.3`
- htop `darwin/DarwinProcess.c`, `darwin/Platform.c`, PR #1976 — https://github.com/htop-dev/htop
- fastfetch `src/detection/gpu/gpu_apple.c` — https://github.com/fastfetch-cli/fastfetch; XRG `Data Miners/XRGGPUMiner.m`; WhateverGreen `Tools/GPUInfo/gpuinfo.c`
- ioreg dumps with full AGX key set: https://github.com/matthart1983/syswatch (`src/collect/gpu.rs`), https://github.com/Pinvou/pinvou-agent (`macos_gpu.rs`)
- freedomtan/sensors `sensors/sensors.m`; vladkens/macmon `readme.md`, `src_lib/sources.rs`; Chris911/iStats README
- Apple Developer Forums thread 655349 "Obtaining CPU usage by process" (EPERM for other users; Activity Monitor uses sysmond)
- Apple Support, Activity Monitor User Guide "View memory usage" (actmntr1004)
