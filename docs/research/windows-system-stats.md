# Windows live system stats from pure Go (Vitals) — API fact sheet

## Summary
- Everything below is reachable from `GOOS=windows` pure Go: `golang.org/x/sys/windows` covers ntdll/toolhelp/process/registry; **PDH, GetSystemTimes, GlobalMemoryStatusEx, GetPerformanceInfo, GetProcessMemoryInfo, GetSystemPowerStatus, GetIfTable2 need `windows.NewLazySystemDLL`** (none are in x/sys, checked v0.33.0 and v0.48.0 `zsyscall_windows.go`).
- GPU like Task Manager: `\GPU Engine(*)\Utilization Percentage` per `pid_…_luid_…_phys_…_eng_…_engtype_…` instance; **sum instances per physical engine, clamp 100, adapter % = max over engines (busiest engine), process % = its busiest instance**. Never sum engine types.
- CPU: `GetSystemTimes` (kernel time *includes* idle); per core `NtQuerySystemInformation(8)` → 48-byte structs; process list in one call with `NtQuerySystemInformation(5)` (`windows.SYSTEM_PROCESS_INFORMATION`, no handles, includes `WorkingSetPrivateSize` = Task Manager "Memory").
- No admin rights, manifest entries or capabilities are needed for any of it; GPU counters need a WDDM 2.0 driver (Windows 10 1709+), else `PDH_CSTATUS_NO_OBJECT`.
- Repo pin `golang.org/x/sys v0.33.0` builds all sketches (verified `go vet`/`go build` for windows/amd64, arm64, 386). x/sys ≥ v0.42 adds `GetIfTable2Ex` but requires Go ≥ 1.25 (repo is `go 1.24`); v0.41.0 is the newest Go-1.24 release.

## 0. x/sys/windows coverage vs LazyDLL (verified against `$GOMODCACHE/golang.org/x/sys@v0.33.0/windows`)

| Need | In x/sys (`windows.`) | Must LazyDLL |
|---|---|---|
| NtQuerySystemInformation(class int32, unsafe.Pointer, len uint32, *uint32) error(NTStatus) | yes; consts `SystemProcessInformation`=5, `SystemProcessorPerformanceInformation`=8 (iota list), `STATUS_INFO_LENGTH_MISMATCH`=0xC0000004, type `SYSTEM_PROCESS_INFORMATION`, `NTUnicodeString` | `NtQuerySystemInformationEx` (ntdll) for >64 CPUs |
| Toolhelp | `CreateToolhelp32Snapshot`, `Process32First/Next(snapshot, *ProcessEntry32)`, `TH32CS_SNAPPROCESS`=2, `ProcessEntry32.ExeFile [MAX_PATH]uint16` | — |
| Per process | `OpenProcess(access, inherit, pid)`, `PROCESS_QUERY_LIMITED_INFORMATION`=0x1000, `GetProcessTimes(h,&c,&e,&k,&u *Filetime)`, `QueryFullProcessImageName(h, flags, *uint16, *uint32)`, `PROCESS_NAME_NATIVE`=1, `MAX_LONG_PATH`=32768, `Filetime.Nanoseconds()` | `GetProcessMemoryInfo` (psapi.dll / kernel32 `K32GetProcessMemoryInfo`) |
| CPU count | `GetActiveProcessorCount(group uint16) uint32`, `ALL_PROCESSOR_GROUPS`=0xFFFF | `GetSystemTimes`, `GetActiveProcessorGroupCount` (kernel32) |
| Registry | `golang.org/x/sys/windows/registry`: `OpenKey(LOCAL_MACHINE, path, QUERY_VALUE)`, `GetStringValue`, `GetIntegerValue` | — |
| Memory / power | — | `GlobalMemoryStatusEx`, `GetSystemPowerStatus` (kernel32), `GetPerformanceInfo` (psapi / `K32GetPerformanceInfo`) |
| Network | `MibIfRow2` type, `GetIfEntry2Ex`, `IF_TYPE_SOFTWARE_LOOPBACK`=24, `IfOperStatusUp`=1 | `GetIfTable2`, `FreeMibTable` (iphlpapi; `FreeMibTable` native from v0.38, `GetIfTable2Ex` from v0.42) |
| Disk | `GetDiskFreeSpaceEx`, `GetLogicalDriveStrings`, `GetDriveType` (`DRIVE_FIXED`=3), `CreateFile`, `DeviceIoControl` | PDH for rates |
| PDH | nothing | all of pdh.dll |
| Helpers | `NewLazySystemDLL` (System32 only, no DLL-preload risk), `LazyProc.Call(args ...uintptr) (r1, r2, lastErr)`, `LazyProc.Find()`, `UTF16PtrFromString`, `UTF16PtrToString`, `UTF16ToString`, `NTStatus`, `LUID{LowPart uint32; HighPart int32}` | — |

Calling rules: pointers go as `uintptr(unsafe.Pointer(&x))` written inside the `Call(...)` expression; strings as `*uint16` from `UTF16PtrFromString`; `lastErr` is always non-nil — only read it when `r1` says failure (BOOL APIs: `r1 == 0`). PDH functions return `PDH_STATUS` in `r1` (0 = success), ntdll/D3DKMT return `NTSTATUS` in `r1`; `GetLastError` is meaningless for them. `Call` panics when the export is missing: use `proc.Find() != nil` for optional DLLs (nvml.dll). `runtime.KeepAlive(buf)` after the last use of any buffer that an API filled with interior pointers. Go's struct layout matches MSVC on amd64/arm64 (LLP64) for the structs below; 386 differs where noted. `runtime.NumCPU()` uses `GetProcessAffinityMask` (≤64, affinity-limited) — use `GetActiveProcessorCount(ALL_PROCESSOR_GROUPS)` as the divisor.

## 1. PDH (pdh.dll)

| Function (W = UTF-16) | Signature | Notes |
|---|---|---|
| `PdhOpenQuery` / `PdhOpenQueryW` | `(LPCWSTR szDataSource /*NULL=live*/, DWORD_PTR dwUserData, PDH_HQUERY *phQuery)` | gopsutil and windows_exporter call the `PdhOpenQuery` export |
| `PdhAddEnglishCounterW` | `(PDH_HQUERY, LPCWSTR szFullCounterPath, DWORD_PTR dwUserData, PDH_HCOUNTER *phCounter)` | English names on any locale, wildcards `(*)` ok, Vista+ |
| `PdhCollectQueryData` | `(PDH_HQUERY)` | one sample for all counters; **rate counters need 2 samples**; first read after one sample → `PDH_INVALID_DATA` |
| `PdhGetFormattedCounterArrayW` | `(PDH_HCOUNTER, DWORD dwFormat, LPDWORD lpdwBufferSize, LPDWORD lpdwItemCount, PPDH_FMT_COUNTERVALUE_ITEM_W ItemBuffer)` | call with `ItemBuffer=NULL, *size=0` → `PDH_MORE_DATA` + required bytes; loop (instances can grow); `szName` points into `ItemBuffer` |
| `PdhGetFormattedCounterValue` | `(PDH_HCOUNTER, DWORD dwFormat, LPDWORD lpdwType /*may be NULL*/, PPDH_FMT_COUNTERVALUE)` | single-instance paths |
| `PdhRemoveCounter`, `PdhCloseQuery` | `(handle)` | |

Constants (mingw-w64 `pdh.h`/`pdhmsg.h`): `PDH_FMT_LONG`=0x100, `PDH_FMT_DOUBLE`=0x200, `PDH_FMT_LARGE`=0x400, `PDH_FMT_NOSCALE`=0x1000, `PDH_FMT_1000`=0x2000, `PDH_FMT_NOCAP100`=0x8000 (formatted values are capped at 100 otherwise). `PDH_CSTATUS_VALID_DATA`=0, `PDH_CSTATUS_NEW_DATA`=1, `PDH_CSTATUS_NO_INSTANCE`=0x800007D1, `PDH_MORE_DATA`=0x800007D2, `PDH_NO_DATA`=0x800007D5, `PDH_CALC_NEGATIVE_DENOMINATOR`=0x800007D6, `PDH_CSTATUS_NO_OBJECT`=0xC0000BB8, `PDH_CSTATUS_NO_COUNTER`=0xC0000BB9, `PDH_CSTATUS_INVALID_DATA`=0xC0000BBA, `PDH_INVALID_HANDLE`=0xC0000BBC, `PDH_INVALID_ARGUMENT`=0xC0000BBD, `PDH_INVALID_PATH`=0xC0000BC4, `PDH_INVALID_DATA`=0xC0000BC6.

Layouts: `PDH_FMT_COUNTERVALUE { DWORD CStatus; union { LONG longValue; double doubleValue; LONGLONG largeValue; LPCSTR; LPCWSTR } }` = 16 bytes, union at +8 on **all** arches (MSVC aligns double/LONGLONG to 8 even on x86). `PDH_FMT_COUNTERVALUE_ITEM_W { LPWSTR szName; PDH_FMT_COUNTERVALUE FmtValue; }` = 24 bytes everywhere (x86: szName 4 + pad 4 + 16). The Go struct in §9 is exact on amd64/arm64; on 386 add `_ uint32` after `Name`.

## 2. GPU

Counter sets (names verified in windows_exporter `internal/collector/gpu/types.go`; instance grammar in its `utils.go`):

| Path | Instance format | Value |
|---|---|---|
| `\GPU Engine(*)\Utilization Percentage` (also `Running Time`, 100 ns raw) | `pid_<pid>_luid_0x%08X_0x%08X_phys_<n>_eng_<n>_engtype_<Type>` e.g. `pid_10584_luid_0x00000000_0x00017FEA_phys_0_eng_0_engtype_3D` | 0–100 per (process, engine) |
| `\GPU Adapter Memory(*)\Dedicated Usage`, `Shared Usage`, `Total Committed` | `luid_0x%08X_0x%08X_phys_<n>` | bytes |
| `\GPU Process Memory(*)\Dedicated Usage`, `Shared Usage`, `Local Usage`, `Non Local Usage`, `Total Committed` | `pid_<pid>_luid_…_phys_<n>` | bytes; instance exists only while the process holds GPU allocations |
| `\GPU Local Adapter Memory(*)\Local Usage`, `\GPU Non Local Adapter Memory(*)\Non Local Usage` | `luid_…_phys_<n>_part_<n>` | bytes |

- LUID text = `fmt.Sprintf("0x%08X_0x%08X", HighPart, LowPart)` (upper-case hex, High first) — the same key windows_exporter and SpecialK build from `LUID`; it matches `D3DKMT_ADAPTERINFO.AdapterLuid` / `DXGI_ADAPTER_DESC1.AdapterLuid`, so adapter names come from `gdi32!D3DKMTEnumAdapters2` + `D3DKMTQueryAdapterInfo(KMTQAITYPE_ADAPTERREGISTRYINFO=8 → AdapterString[260]; KMTQAITYPE_GETSEGMENTSIZE=3 → Dedicated/Shared sizes)`; close each handle with `D3DKMTCloseAdapter`. Skip "Microsoft Basic Render Driver" (`PCI\VEN_1414&DEV_008C`).
- Engine types seen in the wild: `3D`, `Copy`, `VideoDecode`, `VideoEncode`, `VideoProcessing`, `Compute`, `Cuda` (driver-named); kernel enum `DXGK_ENGINE_TYPE`: OTHER 0, 3D 1, VIDEO_DECODE 2, VIDEO_ENCODE 3, VIDEO_PROCESSING 4, SCENE_ASSEMBLY 5, COPY 6, OVERLAY 7, CRYPTO 8. Compare case-insensitively.
- **Task Manager rule** (DirectX blog "GPUs in the Task Manager": per-process GPU % is "what % of the busiest engine is being utilized"; implemented identically by System Informer `EtGpuNodeUsage = max over nodes of runningTimeDelta/elapsed, clamped to 1`, tasksmack #1033, RustCat, cubecl): for each physical engine `(luid, phys, eng)` sum the per-pid instances → engine %, clamp to 100; adapter headline = max over its engines; per-process column = max over that pid's instances; "GPU Engine" column = the engtype of that max. Summing all instances (what naive code does) over-reports (decode 40 + 3D 20 + copy 10 → 70 vs Task Manager 40).
- Timing: Task Manager updates every 1 s. Keep ONE query open for the app lifetime, `Collect` once per tick, read arrays after the 2nd tick. Opening/closing per tick re-enumerates instances and always yields an invalid first sample. If a process stops using the GPU its instance disappears; a wildcard counter picks up new instances on later collects (typeperf/windows_exporter rely on this).
- Known skew: `engtype_3D` under-reports for full-screen-optimized borderless games (themia #32) — identical to Task Manager; NVML's `nvmlDeviceGetUtilizationRates` reports a different quantity (time with any kernel running) and will not match Task Manager.
- Alternatives (brief): **NVML** `nvml.dll` (NVIDIA driver only; `Find()` first): `nvmlInit_v2()`, `nvmlDeviceGetCount_v2(*uint32)`, `nvmlDeviceGetHandleByIndex_v2(uint32, *nvmlDevice_t /*opaque ptr*/)`, `nvmlDeviceGetUtilizationRates(dev, *struct{gpu, memory uint32})`, `nvmlDeviceGetMemoryInfo(dev, *struct{total, free, used uint64})`, `nvmlDeviceGetTemperature(dev, 0 /*NVML_TEMPERATURE_GPU*/, *uint32)`, `nvmlDeviceGetPowerUsage(dev, *uint32 /*mW*/)`, `nvmlDeviceGetName(dev, *byte, 64)`; `NVML_SUCCESS`=0. **DXGI** (COM vtables via `syscall.SyscallN`): `dxgi!CreateDXGIFactory1(REFIID{770aae78-f26f-4dba-a829-253c83d1b387}, **void)` → `IDXGIFactory1` slot 12 `EnumAdapters1(UINT, IDXGIAdapter1**)` (slot 7 `EnumAdapters`), `IDXGIAdapter1` slot 10 `GetDesc1(*DXGI_ADAPTER_DESC1{Description [128]uint16; VendorId, DeviceId, SubSysId, Revision uint32; DedicatedVideoMemory, DedicatedSystemMemory, SharedSystemMemory uintptr; AdapterLuid LUID; Flags uint32 /*2 = SOFTWARE*/})`, `QueryInterface` → `IDXGIAdapter3{645967a4-1392-4310-a798-8053ce3e93fd}` slot 14 `QueryVideoMemoryInfo(NodeIndex uint32, Group uint32 /*0 LOCAL, 1 NON_LOCAL*/, *DXGI_QUERY_VIDEO_MEMORY_INFO{Budget, CurrentUsage, AvailableForReservation, CurrentReservation uint64})`. **D3DKMTQueryStatistics** (gdi32) is what System Informer uses for per-node running time; struct is large/WDK-only — not worth it when PDH works.

## 3. CPU
- Total: `kernel32!GetSystemTimes(LPFILETIME idle, kernel, user)` — FILETIME ticks are 100 ns; **kernel includes idle**. busy% = 100·(1 − Δidle/(Δkernel+Δuser)).
- Per core: `NtQuerySystemInformation(8, buf, n*48, &ret)` returns one `SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION` (48 bytes: `IdleTime, KernelTime, UserTime, DpcTime, InterruptTime LARGE_INTEGER; InterruptCount ULONG; +4 pad` — MS docs call the last three `Reserved1[2]`, `Reserved2`) per logical CPU **of the caller's processor group** (≤64). `ret/48` = count. Per-core busy = 1 − ΔIdle/(ΔKernel+ΔUser). >64 CPUs: `NtQuerySystemInformationEx(8, &group USHORT, 2, buf, len, &ret)` per group, groups from `GetActiveProcessorGroupCount()` (gopsutil `perfInfoAllGroups`).
- Name/clock: `HKLM\HARDWARE\DESCRIPTION\System\CentralProcessor\0`: `ProcessorNameString` (REG_SZ, trim spaces), `~MHz` (REG_DWORD, base clock), `VendorIdentifier`, `Identifier`. Logical count `GetActiveProcessorCount(0xFFFF)`; physical cores need `GetLogicalProcessorInformationEx(RelationProcessorCore)` (not in x/sys).
- Task Manager semantics: Windows 8 → Win11 pre-2025 showed `% Processor Utility` (frequency-scaled, >100 % under turbo) on the Performance tab; Windows 11 24H2 **KB5058411 (May 2025)** switched all tabs to `Δcpu_time / (Δelapsed × logical CPUs)`; "CPU Utility" remains an optional Details column. PDH equivalents: `\Processor Information(_Total)\% Processor Utility`, `…\% Processor Time`, `…\% Processor Performance` (instances `"0,0"` = group,index, `_Total`).

## 4. Memory
- `kernel32!GlobalMemoryStatusEx(LPMEMORYSTATUSEX)`: `{ DWORD dwLength /*set to 64*/; DWORD dwMemoryLoad /*0–100*/; ULONGLONG ullTotalPhys, ullAvailPhys, ullTotalPageFile /*commit limit*/, ullAvailPageFile /*commit available*/, ullTotalVirtual, ullAvailVirtual, ullAvailExtendedVirtual /*0*/ }` bytes. Task Manager "In use" = TotalPhys − AvailPhys; "Committed" = (TotalPageFile − AvailPageFile) / TotalPageFile.
- `psapi!GetPerformanceInfo(PPERFORMANCE_INFORMATION, DWORD cb)` (`K32GetPerformanceInfo` in kernel32): `{ DWORD cb; SIZE_T CommitTotal, CommitLimit, CommitPeak, PhysicalTotal, PhysicalAvailable, SystemCache, KernelTotal, KernelPaged, KernelNonpaged, PageSize; DWORD HandleCount, ProcessCount, ThreadCount }` = 104 bytes amd64 (cb padded to 8), 56 on 386. **All SIZE_T memory fields are in pages** → ×PageSize. `SystemCache` ≈ Task Manager "Cached" (standby list + system working set); `KernelPaged/KernelNonpaged` = "Paged pool"/"Non-paged pool"; `HandleCount/ProcessCount/ThreadCount` = the Performance-tab counters.

## 5. Processes
- One call: `NtQuerySystemInformation(SystemProcessInformation=5)`; grow buffer on `STATUS_INFO_LENGTH_MISMATCH` (add slack; `ReturnLength` is for the moment of the call). Walk `NextEntryOffset` (0 = last); each entry is followed by `NumberOfThreads` × `SYSTEM_THREAD_INFORMATION`. x/sys `SYSTEM_PROCESS_INFORMATION` equals phnt `ntexapi.h`: `NextEntryOffset, NumberOfThreads uint32; WorkingSetPrivateSize int64; HardFaultCount, NumberOfThreadsHighWatermark uint32; CycleTime uint64; CreateTime, UserTime, KernelTime int64; ImageName NTUnicodeString /*Length in BYTES; Buffer nil for PID 0*/; BasePriority int32; UniqueProcessID, InheritedFromUniqueProcessID uintptr; HandleCount, SessionID uint32; UniqueProcessKey *uint32; PeakVirtualSize, VirtualSize uintptr; PageFaultCount uint32; PeakWorkingSetSize, WorkingSetSize, QuotaPeakPagedPoolUsage, QuotaPagedPoolUsage, QuotaPeakNonPagedPoolUsage, QuotaNonPagedPoolUsage, PagefileUsage, PeakPagefileUsage, PrivatePageCount uintptr; Read/Write/OtherOperationCount, Read/Write/OtherTransferCount int64`. Units bytes. Task Manager columns: Memory = `WorkingSetPrivateSize`, Commit size = `PagefileUsage`, Handles/Threads, Disk = Δ`ReadTransferCount+WriteTransferCount`. PID 0 = System Idle Process (its KernelTime is total idle), PID 4 = System. Works without privileges and for protected processes (no handles opened). PIDs are recycled: key deltas on `(PID, CreateTime)`.
- Process CPU %: `100 · Δ(KernelTime+UserTime) / Δwall(100 ns) / logicalCPUs` (Task Manager since KB5058411, Process Explorer). Sum over processes ≈ total busy %.
- Toolhelp (`CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS,0)` + `Process32First/Next`, `pe.Size = sizeof`) only gives PID, PPID, thread count, `ExeFile` base name — pair with `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` + `GetProcessTimes` + `GetProcessMemoryInfo` + `QueryFullProcessImageName` per process (one handle per row per tick; `ERROR_ACCESS_DENIED` for protected/system processes — expected; `SeDebugPrivilege` not required for limited rights).
- `GetProcessMemoryInfo(h, *PROCESS_MEMORY_COUNTERS_EX, cb)`: `{ DWORD cb, PageFaultCount; SIZE_T PeakWorkingSetSize, WorkingSetSize, QuotaPeakPagedPoolUsage, QuotaPagedPoolUsage, QuotaPeakNonPagedPoolUsage, QuotaNonPagedPoolUsage, PagefileUsage, PeakPagefileUsage, PrivateUsage /*commit charge*/ }` = 80 bytes amd64; needs `PROCESS_QUERY_LIMITED_INFORMATION` on Win7+. `_EX2` adds `PrivateWorkingSetSize, SharedCommitUsage` but needs Win10/11 22H2 + Sept-2023 CU.
- `QueryFullProcessImageNameW(h, flags, LPWSTR, PDWORD size)`: `size` is in/out WCHAR count (out excludes NUL); flags 0 = Win32 path, `PROCESS_NAME_NATIVE`(1) = `\Device\HarddiskVolumeN\…`.

## 6. Battery
`kernel32!GetSystemPowerStatus(LPSYSTEM_POWER_STATUS)` → `{ BYTE ACLineStatus /*0 offline, 1 online, 255 unknown*/; BYTE BatteryFlag /*bits 1 high>66%, 2 low<33%, 4 critical<5%, 8 charging, 128 no battery; 255 unknown*/; BYTE BatteryLifePercent /*0–100, 255 unknown*/; BYTE SystemStatusFlag /*1 = battery saver*/; DWORD BatteryLifeTime /*s, 0xFFFFFFFF unknown or on AC*/; DWORD BatteryFullLifeTime /*s, 0xFFFFFFFF unknown*/ }` = 12 bytes. Desktop → `BatteryFlag & 128`.

## 7. Network & disk
- `iphlpapi!GetIfTable2(PMIB_IF_TABLE2 *)` (NETIO_STATUS = Win32 error), free with `FreeMibTable`. `MIB_IF_TABLE2 { ULONG NumEntries; MIB_IF_ROW2 Table[]; }` rows start at +8 (8-byte alignment); `MIB_IF_ROW2` = 1352 bytes (x/sys `MibIfRow2` matches on amd64/arm64; 386 gives 1348 → unusable without padding). Use `InOctets/OutOctets` (cumulative bytes), `ReceiveLinkSpeed/TransmitLinkSpeed` (bit/s), `Alias` (friendly, "Ethernet"), `Description` (model). Filter `Type != IF_TYPE_SOFTWARE_LOOPBACK(24)`, `OperStatus == IfOperStatusUp(1)`; `InterfaceAndOperStatusFlags` is one byte of 8 BOOLEAN bitfields, LSB first: bit0 HardwareInterface, bit1 FilterInterface, bit2 ConnectorPresent, bit3 NotAuthenticated, bit4 NotMediaConnected, bit5 Paused, bit6 LowPower, bit7 EndPointInterface — Task Manager lists hardware adapters (bit0/bit2). PDH alternative: `\Network Interface(*)\Bytes Received/sec`, `Bytes Sent/sec`, `Current Bandwidth` (instances are Descriptions with `(`,`)`,`#`,`/` mangled — prefer GetIfTable2).
- Disk rates: PDH `\PhysicalDisk(*)\% Idle Time` (Task Manager "Active time" = 100 − idle), `Disk Read Bytes/sec`, `Disk Write Bytes/sec`, `Avg. Disk sec/Transfer`, `Current Disk Queue Length`; instances `"0 C:"`, `"1 D: E:"`, `_Total`. Or `DeviceIoControl(CreateFile("\\\\.\\PhysicalDrive0" or "\\\\.\\C:", 0, FILE_SHARE_READ|WRITE, nil, OPEN_EXISTING, 0, 0), IOCTL_DISK_PERFORMANCE=0x70020, …)` → `DISK_PERFORMANCE { BytesRead, BytesWritten, ReadTime, WriteTime, IdleTime int64; ReadCount, WriteCount, QueueDepth, SplitCount uint32; QueryTime int64; StorageDeviceNumber uint32; StorageManagerName [8]uint16 }` (gopsutil). Capacity: `GetDiskFreeSpaceEx`.

## 8. Permissions & detection
- Standard user, no manifest/UAC: PDH local queries, NtQuerySystemInformation 5/8, GetIfTable2, registry `HARDWARE\DESCRIPTION` (world-readable), D3DKMT, GetSystemPowerStatus. Per-process handles fail (`ERROR_ACCESS_DENIED`=5) for protected/other-session system processes — degrade to the NtQuery row.
- GPU counters: `PdhAddEnglishCounterW` returns `PDH_CSTATUS_NO_OBJECT` (0xC0000BB8) without a WDDM 2.0 driver or with a corrupt perf registry (`lodctr /R` fixes); `PDH_CSTATUS_NO_INSTANCE`/`PDH_NO_DATA` from the array call when no process is touching the GPU — show 0 %, don't error. `nvml.dll` missing → `proc.Find()` error → skip.
- Battery absent → `BatteryFlag&128`. >64 CPUs → use the Ex path.

## 9. Go sketches (build-verified: `GOOS=windows GOARCH={amd64,arm64,386} go vet && go build` with x/sys v0.33.0)

```go
//go:build windows
package vitalswin // pdh.go

var (
	modpdh                           = windows.NewLazySystemDLL("pdh.dll")
	procPdhOpenQuery                 = modpdh.NewProc("PdhOpenQuery")
	procPdhAddEnglishCounterW        = modpdh.NewProc("PdhAddEnglishCounterW")
	procPdhCollectQueryData          = modpdh.NewProc("PdhCollectQueryData")
	procPdhGetFormattedCounterArrayW = modpdh.NewProc("PdhGetFormattedCounterArrayW")
	procPdhCloseQuery                = modpdh.NewProc("PdhCloseQuery")
)
const (
	PDH_FMT_DOUBLE = 0x200; PDH_CSTATUS_VALID_DATA = 0; PDH_CSTATUS_NEW_DATA = 1
	PDH_CSTATUS_NO_INSTANCE = 0x800007D1; PDH_MORE_DATA = 0x800007D2; PDH_INVALID_DATA = 0xC0000BC6
)
type pdhFmtCounterValueDouble struct { CStatus uint32; _ uint32; Value float64 } // 16 B, union at +8
type pdhFmtCounterValueItemDouble struct { Name *uint16; Fmt pdhFmtCounterValueDouble } // 24 B amd64/arm64 (386: add `_ uint32` after Name)
type PdhError uint32
func (e PdhError) Error() string { return fmt.Sprintf("pdh: status 0x%08X", uint32(e)) }
type Query struct{ h uintptr }
func OpenQuery() (*Query, error) {
	var h uintptr
	if r, _, _ := procPdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&h))); r != 0 { return nil, PdhError(r) }
	return &Query{h: h}, nil
}
func (q *Query) Close() { procPdhCloseQuery.Call(q.h) }
func (q *Query) AddEnglishCounter(path string) (uintptr, error) {
	p, err := windows.UTF16PtrFromString(path); if err != nil { return 0, err }
	var c uintptr
	if r, _, _ := procPdhAddEnglishCounterW.Call(q.h, uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&c))); r != 0 { return 0, PdhError(r) }
	return c, nil
}
func (q *Query) Collect() error { // once per tick; rate counters need two samples
	if r, _, _ := procPdhCollectQueryData.Call(q.h); r != 0 { return PdhError(r) }
	return nil
}
type Item struct { Instance string; Value float64 }
func FormattedArray(counter uintptr) ([]Item, error) {
	var size, count uint32
	var buf []byte
	r, _, _ := procPdhGetFormattedCounterArrayW.Call(counter, PDH_FMT_DOUBLE, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
	for r == PDH_MORE_DATA { // size now = required bytes; loop because instances may appear in between
		buf = make([]byte, size)
		r, _, _ = procPdhGetFormattedCounterArrayW.Call(counter, PDH_FMT_DOUBLE, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buf[0])))
	}
	if r != 0 { return nil, PdhError(r) } // PDH_INVALID_DATA until 2nd Collect; PDH_CSTATUS_NO_INSTANCE when empty
	if count == 0 || len(buf) == 0 { return nil, nil }
	items := unsafe.Slice((*pdhFmtCounterValueItemDouble)(unsafe.Pointer(&buf[0])), count)
	out := make([]Item, 0, count)
	for i := range items {
		if s := items[i].Fmt.CStatus; s != PDH_CSTATUS_VALID_DATA && s != PDH_CSTATUS_NEW_DATA { continue }
		out = append(out, Item{Instance: windows.UTF16PtrToString(items[i].Name), Value: items[i].Fmt.Value}) // copies out of buf
	}
	runtime.KeepAlive(buf)
	return out, nil
}
```

```go
// gpu.go — instance parsing + Task Manager aggregation
const CounterGPUEngineUtil = `\GPU Engine(*)\Utilization Percentage`
type GPUInstance struct { PID uint32; LUID string /*"0x00000000_0x0000F814"*/; Phys, Eng int; EngType string }
func ParseGPUInstance(s string) (g GPUInstance, ok bool) { // windows_exporter utils.go: split on "_"
	parts := strings.Split(s, "_")
	for i := 0; i < len(parts); i++ {
		switch parts[i] {
		case "pid": if i+1 < len(parts) { v, _ := strconv.ParseUint(parts[i+1], 10, 32); g.PID = uint32(v); i++ }
		case "luid": if i+2 < len(parts) { g.LUID = parts[i+1] + "_" + parts[i+2]; ok = true; i += 2 }
		case "phys": if i+1 < len(parts) { g.Phys, _ = strconv.Atoi(parts[i+1]); i++ }
		case "eng": if i+1 < len(parts) { g.Eng, _ = strconv.Atoi(parts[i+1]); i++ }
		case "engtype": g.EngType = strings.Join(parts[i+1:], "_"); i = len(parts)
		}
	}
	return g, ok
}
type GPUUtil struct {
	Adapter   map[string]float64            // luid -> headline % (busiest engine)
	Engines   map[string]map[string]float64 // luid -> "<EngType>/<eng>" -> %
	Processes map[uint32]float64            // pid -> % of its busiest engine
}
func AggregateGPU(items []Item) GPUUtil {
	type key struct { luid string; phys, eng int }
	sum, typ := map[key]float64{}, map[key]string{}
	u := GPUUtil{Adapter: map[string]float64{}, Engines: map[string]map[string]float64{}, Processes: map[uint32]float64{}}
	for _, it := range items {
		g, ok := ParseGPUInstance(it.Instance); if !ok { continue }
		k := key{g.LUID, g.Phys, g.Eng}
		sum[k] += it.Value; typ[k] = g.EngType
		if it.Value > u.Processes[g.PID] { u.Processes[g.PID] = it.Value }
	}
	for k, v := range sum {
		if v > 100 { v = 100 }
		if u.Engines[k.luid] == nil { u.Engines[k.luid] = map[string]float64{} }
		u.Engines[k.luid][typ[k]+"/"+strconv.Itoa(k.eng)] = v
		if v > u.Adapter[k.luid] { u.Adapter[k.luid] = v }
	}
	return u
}
// LUID -> name/VRAM via gdi32 (no COM). NTSTATUS in r1.
var (
	modgdi32 = windows.NewLazySystemDLL("gdi32.dll")
	procD3DKMTEnumAdapters2, procD3DKMTQueryAdapterInfo, procD3DKMTCloseAdapter = modgdi32.NewProc("D3DKMTEnumAdapters2"), modgdi32.NewProc("D3DKMTQueryAdapterInfo"), modgdi32.NewProc("D3DKMTCloseAdapter")
)
type d3dkmtAdapterInfo struct { HAdapter uint32; AdapterLuid windows.LUID; NumOfSources uint32; Present int32 } // 20 B
type d3dkmtEnumAdapters2 struct { NumAdapters uint32; PAdapters *d3dkmtAdapterInfo }
type d3dkmtQueryAdapterInfo struct { HAdapter uint32; Type int32; PrivateDriverData unsafe.Pointer; PrivateDriverDataSize uint32 }
type d3dkmtAdapterRegistryInfo struct { AdapterString, BiosString, DacType, ChipType [260]uint16 }
type d3dkmtSegmentSizeInfo struct { DedicatedVideoMemorySize, DedicatedSystemMemorySize, SharedSystemMemorySize uint64 }
type GPUAdapter struct { Name string; DedicatedVideoBytes, SharedSysBytes uint64 }
func GPUAdapters() (map[string]GPUAdapter, error) {
	var e d3dkmtEnumAdapters2
	if r, _, _ := procD3DKMTEnumAdapters2.Call(uintptr(unsafe.Pointer(&e))); r != 0 { return nil, windows.NTStatus(r) }
	if e.NumAdapters == 0 { return nil, nil }
	ads := make([]d3dkmtAdapterInfo, e.NumAdapters); e.PAdapters = &ads[0]
	if r, _, _ := procD3DKMTEnumAdapters2.Call(uintptr(unsafe.Pointer(&e))); r != 0 { return nil, windows.NTStatus(r) }
	out := map[string]GPUAdapter{}
	for _, a := range ads[:e.NumAdapters] {
		var info d3dkmtAdapterRegistryInfo; var seg d3dkmtSegmentSizeInfo
		q := d3dkmtQueryAdapterInfo{HAdapter: a.HAdapter, Type: 8, PrivateDriverData: unsafe.Pointer(&info), PrivateDriverDataSize: uint32(unsafe.Sizeof(info))}
		procD3DKMTQueryAdapterInfo.Call(uintptr(unsafe.Pointer(&q)))
		q.Type, q.PrivateDriverData, q.PrivateDriverDataSize = 3, unsafe.Pointer(&seg), uint32(unsafe.Sizeof(seg))
		procD3DKMTQueryAdapterInfo.Call(uintptr(unsafe.Pointer(&q)))
		h := a.HAdapter; procD3DKMTCloseAdapter.Call(uintptr(unsafe.Pointer(&h))) // D3DKMT_CLOSEADAPTER{hAdapter}
		out[fmt.Sprintf("0x%08X_0x%08X", uint32(a.AdapterLuid.HighPart), a.AdapterLuid.LowPart)] = GPUAdapter{
			Name: windows.UTF16ToString(info.AdapterString[:]), DedicatedVideoBytes: seg.DedicatedVideoMemorySize, SharedSysBytes: seg.SharedSystemMemorySize}
	}
	return out, nil
}
// tick loop: q,_ := OpenQuery(); c,_ := q.AddEnglishCounter(CounterGPUEngineUtil); q.Collect() /*prime*/
//   every 1 s: q.Collect(); items, err := FormattedArray(c); u := AggregateGPU(items)
```

```go
// cpu.go
var (
	modkernel32, modntdll = windows.NewLazySystemDLL("kernel32.dll"), windows.NewLazySystemDLL("ntdll.dll")
	procGetSystemTimes    = modkernel32.NewProc("GetSystemTimes")
)
type CPUTimes struct{ Idle, Kernel, User uint64 } // 100 ns; Kernel includes Idle
func filetime(f windows.Filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }
func SystemTimes() (CPUTimes, error) {
	var idle, kernel, user windows.Filetime
	r, _, e := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if r == 0 { return CPUTimes{}, e }
	return CPUTimes{filetime(idle), filetime(kernel), filetime(user)}, nil
}
func CPUPercent(a, b CPUTimes) float64 {
	total := (b.Kernel - a.Kernel) + (b.User - a.User); if total == 0 { return 0 }
	return 100 * (1 - float64(b.Idle-a.Idle)/float64(total))
}
type ProcessorPerf struct { IdleTime, KernelTime, UserTime, DpcTime, InterruptTime int64; InterruptCount uint32; _ uint32 } // 48 B
func PerCorePerf() ([]ProcessorPerf, error) { // current processor group (<= 64 CPUs); see §3 for the Ex variant
	n := windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS); if n == 0 { n = 64 }
	buf := make([]ProcessorPerf, n); var ret uint32
	err := windows.NtQuerySystemInformation(windows.SystemProcessorPerformanceInformation, unsafe.Pointer(&buf[0]), uint32(len(buf))*uint32(unsafe.Sizeof(buf[0])), &ret)
	if err != nil { return nil, err }
	return buf[:ret/uint32(unsafe.Sizeof(buf[0]))], nil
}
func CPUName() (name string, baseMHz uint64, err error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE)
	if err != nil { return "", 0, err }
	defer k.Close()
	name, _, err = k.GetStringValue("ProcessorNameString"); baseMHz, _, _ = k.GetIntegerValue("~MHz")
	return strings.TrimSpace(name), baseMHz, err
}
```

```go
// mem.go / power.go
var (
	modpsapi                 = windows.NewLazySystemDLL("psapi.dll")
	procGlobalMemoryStatusEx = modkernel32.NewProc("GlobalMemoryStatusEx")
	procGetPerformanceInfo   = modpsapi.NewProc("GetPerformanceInfo") // or kernel32 "K32GetPerformanceInfo"
	procGetSystemPowerStatus = modkernel32.NewProc("GetSystemPowerStatus")
)
type MemoryStatusEx struct { Length, MemoryLoad uint32; TotalPhys, AvailPhys, TotalPageFile, AvailPageFile, TotalVirtual, AvailVirtual, AvailExtendedVirtual uint64 } // 64 B
func GlobalMemoryStatusEx() (MemoryStatusEx, error) {
	var m MemoryStatusEx; m.Length = uint32(unsafe.Sizeof(m))
	if r, _, e := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); r == 0 { return m, e }
	return m, nil
}
type PerformanceInformation struct { // 104 B amd64/arm64, 56 B 386; page counts -> * PageSize
	Cb uint32
	CommitTotal, CommitLimit, CommitPeak, PhysicalTotal, PhysicalAvailable, SystemCache, KernelTotal, KernelPaged, KernelNonpaged, PageSize uintptr
	HandleCount, ProcessCount, ThreadCount uint32
}
func GetPerformanceInfo() (PerformanceInformation, error) {
	var p PerformanceInformation; p.Cb = uint32(unsafe.Sizeof(p))
	if r, _, e := procGetPerformanceInfo.Call(uintptr(unsafe.Pointer(&p)), uintptr(p.Cb)); r == 0 { return p, e }
	return p, nil
}
type SystemPowerStatus struct { ACLineStatus, BatteryFlag, BatteryLifePercent, SystemStatusFlag uint8; BatteryLifeTime, BatteryFullLifeTime uint32 } // 12 B
func PowerStatus() (SystemPowerStatus, error) {
	var s SystemPowerStatus
	if r, _, e := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&s))); r == 0 { return s, e }
	return s, nil
}
```

```go
// proc.go
type Proc struct {
	PID, PPID, SessionID, Threads, Handles uint32
	Name                                   string // base name; "" for PID 0
	CreateTime, KernelTime, UserTime       int64  // 100 ns
	WorkingSet, PrivateWorkingSet, Commit  uint64 // bytes
	ReadBytes, WriteBytes                  int64
}
func Processes() ([]Proc, error) {
	size := uint32(512 << 10); var buf []byte
	for {
		buf = make([]byte, size); var ret uint32
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&buf[0]), size, &ret)
		if err == nil { break }
		if err != windows.STATUS_INFO_LENGTH_MISMATCH { return nil, err }
		size = ret + 64<<10 // slack for processes created before the retry
	}
	var out []Proc
	for off := uint32(0); ; {
		p := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[off]))
		var name string
		if p.ImageName.Buffer != nil { name = windows.UTF16ToString(unsafe.Slice(p.ImageName.Buffer, p.ImageName.Length/2)) } // Length is bytes
		out = append(out, Proc{PID: uint32(p.UniqueProcessID), PPID: uint32(p.InheritedFromUniqueProcessID), SessionID: p.SessionID,
			Threads: p.NumberOfThreads, Handles: p.HandleCount, Name: name, CreateTime: p.CreateTime, KernelTime: p.KernelTime, UserTime: p.UserTime,
			WorkingSet: uint64(p.WorkingSetSize), PrivateWorkingSet: uint64(p.WorkingSetPrivateSize), Commit: uint64(p.PagefileUsage),
			ReadBytes: p.ReadTransferCount, WriteBytes: p.WriteTransferCount})
		if p.NextEntryOffset == 0 { break }
		off += p.NextEntryOffset
	}
	runtime.KeepAlive(buf)
	return out, nil
}
func ProcCPUPercent(prev, cur Proc, wall time.Duration, logicalCPUs int) float64 { // match rows by (PID, CreateTime)
	d := float64((cur.KernelTime - prev.KernelTime) + (cur.UserTime - prev.UserTime))
	return 100 * d / (float64(wall.Nanoseconds()) / 100) / float64(logicalCPUs)
}
func ExePath(pid uint32) (string, error) { // ERROR_ACCESS_DENIED for protected processes -> use Proc.Name
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid); if err != nil { return "", err }
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH); n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil { return "", err }
	return windows.UTF16ToString(buf[:n]), nil
}
```

```go
// net.go — works with x/sys v0.33.0 (no GetIfTable2Ex there)
var (
	modiphlpapi      = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetIfTable2  = modiphlpapi.NewProc("GetIfTable2")
	procFreeMibTable = modiphlpapi.NewProc("FreeMibTable")
)
type mibIfTable2 struct { NumEntries uint32; _ uint32; Table [1]windows.MibIfRow2 } // rows at +8
type NetCounters struct { Index uint32; Alias, Description string; InOctets, OutOctets, LinkSpeedBps uint64; Hardware, ConnectorPresent bool }
func NetInterfaces() ([]NetCounters, error) {
	var t *mibIfTable2
	if r, _, _ := procGetIfTable2.Call(uintptr(unsafe.Pointer(&t))); r != 0 { return nil, windows.Errno(r) }
	defer procFreeMibTable.Call(uintptr(unsafe.Pointer(t)))
	rows := unsafe.Slice(&t.Table[0], t.NumEntries)
	var out []NetCounters
	for i := range rows {
		r := &rows[i]
		if r.Type == windows.IF_TYPE_SOFTWARE_LOOPBACK || r.OperStatus != windows.IfOperStatusUp { continue }
		out = append(out, NetCounters{Index: r.InterfaceIndex, Alias: windows.UTF16ToString(r.Alias[:]), Description: windows.UTF16ToString(r.Description[:]),
			InOctets: r.InOctets, OutOctets: r.OutOctets, LinkSpeedBps: r.ReceiveLinkSpeed,
			Hardware: r.InterfaceAndOperStatusFlags&0x01 != 0, ConnectorPresent: r.InterfaceAndOperStatusFlags&0x04 != 0})
	}
	return out, nil
}
```

## 10. Pitfalls (checklist)
1. First PDH read after one `PdhCollectQueryData` → `PDH_INVALID_DATA`; the array call returns `PDH_MORE_DATA` first by design — treat both as flow control, not errors. Keep one long-lived query.
2. `szName` points into your buffer: convert to Go strings before the buffer is reused/freed; `KeepAlive`.
3. Task Manager GPU % is max-over-engines of per-engine sums, clamped; per-instance values are already capped at 100 by PDH (`PDH_FMT_NOCAP100` lifts that).
4. LUID strings: upper-case `0x%08X_0x%08X`, HighPart first; engtype compare with `strings.EqualFold`; parse by splitting on `_`, never by fixed offsets.
5. `GetSystemTimes`/`SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION` kernel time includes idle (and DPC/interrupt time); `NtQuerySystemInformation(8)` covers only the current processor group.
6. `GetPerformanceInfo` values are pages; `GlobalMemoryStatusEx` values are bytes; `ullTotalPageFile` is the commit limit, not the page-file size.
7. `NtQuerySystemInformation(5)`: variable-size entries, `NextEntryOffset` walk, `ImageName.Length` in bytes, nil buffer for PID 0, buffer races → add slack; PID reuse → key on `(PID, CreateTime)`.
8. Struct sizes to assert in a test: PDH item 24, `PERFORMANCE_INFORMATION` 104, `MEMORYSTATUSEX` 64, `SYSTEM_POWER_STATUS` 12, processor perf 48, `PROCESS_MEMORY_COUNTERS_EX` 80, `D3DKMT_ADAPTERINFO` 20, `MIB_IF_ROW2` 1352 (amd64/arm64). 386 breaks PDH item, `MIB_IF_ROW2` and SIZE_T fields unless padded — ship amd64/arm64.
9. `QueryFullProcessImageName` buffer is WCHARs and `size` is in/out; `MAX_PATH` (260) is too small for long paths — use `MAX_LONG_PATH`.
10. `~MHz` is base clock; live frequency needs `\Processor Information(_Total)\% Processor Performance` × base or `CallNtPowerInformation(ProcessorInformation)`.
11. Battery: `BatteryLifeTime == 0xFFFFFFFF` on AC; percent 255 = unknown; desktops report `BatteryFlag & 128`.
12. Network: Hyper-V/WSL `vEthernet`, VPN and Bluetooth PAN adapters are not `HardwareInterface`; media-disconnected Wi-Fi is `OperStatus != Up`. Counters are cumulative 64-bit — no wrap handling needed.
13. Reading Task Manager-compatible CPU on pre-KB5058411 Windows 11/10 needs `% Processor Utility`; on patched systems `GetSystemTimes` math matches.

## Unverified
- Exact sentences of Microsoft's "GPUs in the Task Manager" blog (devblogs.microsoft.com blocked here); the busiest-engine rule is corroborated by a search-engine excerpt of the post plus System Informer, tasksmack #1033, RustCat, cubecl, SpecialK. Whether Task Manager groups by physical engine (`eng_n`) or by engine type when a type has several engines (inferred from "Compute_0/Compute_1" UI labels) — the sketch groups by physical engine.
- GPU memory counter units are bytes (windows_exporter emits them unscaled as bytes; no MS doc page found for the GPU counter sets).
- PDH wildcard re-enumeration on every collect (behaviour of typeperf/windows_exporter; no MS doc sentence found).
- KB5058411 / "% Processor Utility" history comes from press (windowslatest, bitsum) — MS pages blocked.
- `nvml.dll` location (`System32` with current drivers; older: `Program Files\NVIDIA Corporation\NVSMI`). DXGI not needing `CoInitializeEx`.
- `SystemCache` ≈ Task Manager "Cached" (MS defines it as standby list + system working set; Task Manager's exact formula is undocumented).

## Sources
- x/sys/windows v0.33.0 / v0.48.0 (`zsyscall_windows.go`, `types_windows.go`, `registry/key.go`, `registry/value.go`) in `$GOMODCACHE`; x/sys v0.34–v0.41 downloaded to check `go` directive and `GetIfTable2Ex` presence.
- MS docs mirror: https://github.com/MicrosoftDocs/sdk-api — `pdh/nf-pdh-pdhgetformattedcounterarrayw.md`, `pdh/ns-pdh-pdh_fmt_countervalue.md`, `winternl/nf-winternl-ntquerysysteminformation.md`, `psapi/ns-psapi-performance_information.md`, `psapi/ns-psapi-process_memory_counters_ex.md`, `psapi/ns-psapi-process_memory_counters_ex2.md`, `psapi/nf-psapi-getprocessmemoryinfo.md`, `sysinfoapi/ns-sysinfoapi-memorystatusex.md`, `winbase/ns-winbase-system_power_status.md`, `processthreadsapi/nf-processthreadsapi-getsystemtimes.md`, `netioapi/ns-netioapi-mib_if_row2.md`.
- Headers: mingw-w64 `pdh.h`, `pdhmsg.h`, `psapi.h`, `winbase.h`, `sysinfoapi.h`, `dxgi.h`, `dxgi1_4.h` (https://github.com/mirror/mingw-w64); phnt `ntexapi.h` (https://github.com/winsiderss/phnt); NVIDIA `nvml.h` (https://github.com/NVIDIA/go-nvml/blob/main/gen/nvml/nvml.h).
- Implementations: gopsutil `cpu/cpu_windows.go`, `process/process_windows.go`, `process/process.go`, `mem/mem_windows.go`, `net/net_windows.go`, `disk/disk_windows.go`, `internal/common/common_windows.go` (https://github.com/shirou/gopsutil); windows_exporter `internal/pdh/{pdh,types,collector}.go`, `internal/collector/gpu/{gpu,types,utils}.go`, `internal/headers/gdi32/{gdi32,types}.go`, `internal/collector/{physical_disk,net,cpu}/types.go` (https://github.com/prometheus-community/windows_exporter); System Informer `plugins/ExtendedTools/gpumon.c` (https://github.com/winsiderss/systeminformer).
- Task Manager GPU rule: https://github.com/mgradwohl/tasksmack/issues/1033, https://github.com/oshi/oshi/pull/2114, https://github.com/crazy-rabbit-software/themia/issues/32, https://github.com/bearice/RustCat (`src/platform/windows/gpu_usage.rs`), https://github.com/SpecialKO/SpecialK (`src/performance/gpu_monitor.cpp`), https://github.com/microsoft/PowerToys (`…/DevHome/Helpers/GPUStats.cs`), search excerpt of https://devblogs.microsoft.com/directx/gpus-in-the-task-manager/.
- Task Manager CPU formula change: https://windowslatest.com/2025/05/26/windows-11-24h2s-task-manager-new-cpu-usage-formula-rolls-out-to-everyone, https://bitsum.com/cpu-utility-vs-cpu-time/.
