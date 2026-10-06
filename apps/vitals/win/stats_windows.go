//go:build windows

package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Windows stats backend (docs/research/windows-system-stats.md): CPU from
// GetSystemTimes + NtQuerySystemInformation(8), memory from
// GlobalMemoryStatusEx + GetPerformanceInfo, GPU and disk rates from PDH,
// processes from NtQuerySystemInformation(5), network from GetIfTable2 and
// battery from GetSystemPowerStatus. Nothing here needs elevation.

var (
	modkernel32              = windows.NewLazySystemDLL("kernel32.dll")
	modpsapi                 = windows.NewLazySystemDLL("psapi.dll")
	modiphlpapi              = windows.NewLazySystemDLL("iphlpapi.dll")
	modgdi32                 = windows.NewLazySystemDLL("gdi32.dll")
	procGetSystemTimes       = modkernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx = modkernel32.NewProc("GlobalMemoryStatusEx")
	procGetSystemPowerStatus = modkernel32.NewProc("GetSystemPowerStatus")
	procGetTickCount64       = modkernel32.NewProc("GetTickCount64")
	procGetPerformanceInfo   = modpsapi.NewProc("GetPerformanceInfo")
	procGetIfTable2          = modiphlpapi.NewProc("GetIfTable2")
	procFreeMibTable         = modiphlpapi.NewProc("FreeMibTable")
	procD3DKMTEnumAdapters2  = modgdi32.NewProc("D3DKMTEnumAdapters2")
	procD3DKMTQueryAdapter   = modgdi32.NewProc("D3DKMTQueryAdapterInfo")
	procD3DKMTCloseAdapter   = modgdi32.NewProc("D3DKMTCloseAdapter")
)

const (
	counterGPUEngine    = `\GPU Engine(*)\Utilization Percentage`
	counterGPUDedicated = `\GPU Adapter Memory(*)\Dedicated Usage`
	counterGPUShared    = `\GPU Adapter Memory(*)\Shared Usage`
	counterDiskRead     = `\PhysicalDisk(_Total)\Disk Read Bytes/sec`
	counterDiskWrite    = `\PhysicalDisk(_Total)\Disk Write Bytes/sec`
)

// ---------------------------------------------------------------- structs

// systemTimes are 100 ns FILETIME ticks; Kernel includes Idle.
type systemTimes struct{ Idle, Kernel, User uint64 }

func filetimeU64(f windows.Filetime) uint64 {
	return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime)
}

// SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION, 48 bytes.
type processorPerf struct {
	IdleTime, KernelTime, UserTime, DpcTime, InterruptTime int64
	InterruptCount                                         uint32
	_                                                      uint32
}

// MEMORYSTATUSEX, 64 bytes.
type memoryStatusEx struct {
	Length, MemoryLoad                                                                                   uint32
	TotalPhys, AvailPhys, TotalPageFile, AvailPageFile, TotalVirtual, AvailVirtual, AvailExtendedVirtual uint64
}

// PERFORMANCE_INFORMATION, 104 bytes on amd64/arm64; SIZE_T fields are pages.
type performanceInformation struct {
	Cb                                                                                               uint32
	CommitTotal, CommitLimit, CommitPeak, PhysicalTotal, PhysicalAvailable, SystemCache, KernelTotal uintptr
	KernelPaged, KernelNonpaged, PageSize                                                            uintptr
	HandleCount, ProcessCount, ThreadCount                                                           uint32
}

// SYSTEM_POWER_STATUS, 12 bytes.
type systemPowerStatus struct {
	ACLineStatus, BatteryFlag, BatteryLifePercent, SystemStatusFlag uint8
	BatteryLifeTime, BatteryFullLifeTime                            uint32
}

// MIB_IF_TABLE2: NumEntries then rows at +8.
type mibIfTable2 struct {
	NumEntries uint32
	_          uint32
	Table      [1]windows.MibIfRow2
}

type d3dkmtAdapterInfo struct {
	HAdapter     uint32
	AdapterLuid  windows.LUID
	NumOfSources uint32
	Present      int32
}

type d3dkmtEnumAdapters2 struct {
	NumAdapters uint32
	PAdapters   *d3dkmtAdapterInfo
}

type d3dkmtQueryAdapterInfo struct {
	HAdapter              uint32
	Type                  int32
	PrivateDriverData     unsafe.Pointer
	PrivateDriverDataSize uint32
}

type d3dkmtAdapterRegistryInfo struct {
	AdapterString, BiosString, DacType, ChipType [260]uint16
}

type d3dkmtSegmentSizeInfo struct {
	DedicatedVideoMemorySize, DedicatedSystemMemorySize, SharedSystemMemorySize uint64
}

type gpuAdapter struct {
	Name                string
	DedicatedVideoBytes uint64
	SharedSysBytes      uint64
}

// ---------------------------------------------------------------- backend

type windowsStats struct {
	mu          sync.Mutex
	info        HostInfo
	logicalCPUs int
	pageSize    uint64

	prevTimes systemTimes
	prevCores []processorPerf
	prevNetRx uint64
	prevNetTx uint64
	prevAt    time.Time

	pdh          *pdhQuery
	cGPUEngine   uintptr
	cGPUDed      uintptr
	cGPUShared   uintptr
	cDiskRead    uintptr
	cDiskWrite   uintptr
	gpuAvailable bool
	adapters     map[string]gpuAdapter // luid → name/VRAM
	gpuFallback  string

	procPrev map[procKey]procTimes
	procAt   time.Time
}

type procKey struct {
	pid    uint32
	create int64
}

type procTimes struct{ kernel, user int64 }

func newStats() (Stats, error) {
	s := &windowsStats{procPrev: map[procKey]procTimes{}}
	s.logicalCPUs = int(windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS))
	if s.logicalCPUs <= 0 {
		s.logicalCPUs = runtime.NumCPU()
	}
	host, _ := os.Hostname()
	s.info = HostInfo{Machine: host, OS: "windows", Cores: s.logicalCPUs}
	if name, _, err := cpuName(); err == nil && name != "" {
		s.info.CPUModel = name
	}
	if m, err := globalMemoryStatusEx(); err == nil {
		s.info.MemTotal = m.TotalPhys
	}
	if p, err := getPerformanceInfo(); err == nil && p.PageSize > 0 {
		s.pageSize = uint64(p.PageSize)
	} else {
		s.pageSize = 4096
	}
	if t, err := getSystemTimes(); err == nil {
		s.prevTimes = t
	}
	if cores, err := perCorePerf(); err == nil {
		s.prevCores = cores
		if len(cores) > 0 {
			s.info.Cores = len(cores)
		}
	}
	s.prevNetRx, s.prevNetTx = netTotals()
	s.prevAt = time.Now()
	s.openCounters()
	s.adapters, _ = gpuAdapters()
	s.gpuFallback = gpuNameFromRegistry()
	return s, nil
}

// openCounters opens the long-lived PDH query; a missing counter set (no
// WDDM 2.0 driver) just disables that reading.
func (s *windowsStats) openCounters() {
	q, err := openPdhQuery()
	if err != nil {
		return
	}
	s.pdh = q
	if c, err := q.AddEnglishCounter(counterGPUEngine); err == nil {
		s.cGPUEngine = c
		s.gpuAvailable = true
	}
	s.cGPUDed, _ = q.AddEnglishCounter(counterGPUDedicated)
	s.cGPUShared, _ = q.AddEnglishCounter(counterGPUShared)
	s.cDiskRead, _ = q.AddEnglishCounter(counterDiskRead)
	s.cDiskWrite, _ = q.AddEnglishCounter(counterDiskWrite)
	_ = q.Collect() // prime; rate counters need two samples
}

func (s *windowsStats) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pdh != nil {
		s.pdh.Close()
		s.pdh = nil
	}
}

func (s *windowsStats) Info() HostInfo {
	info := s.info
	if r, _, _ := procGetTickCount64.Call(); r != 0 {
		info.UptimeSec = int64(r / 1000)
	}
	return info
}

func (s *windowsStats) Sample() (Sample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(s.prevAt)
	out := Sample{T: now.UnixMilli(), GPU: []GPUSample{}}

	// CPU
	if t, err := getSystemTimes(); err == nil {
		total := float64(t.Kernel-s.prevTimes.Kernel) + float64(t.User-s.prevTimes.User)
		idle := float64(t.Idle - s.prevTimes.Idle)
		out.CPU.Total = pct(total-idle, total)
		s.prevTimes = t
	} else {
		return out, fmt.Errorf("GetSystemTimes: %w", err)
	}
	if cores, err := perCorePerf(); err == nil {
		out.CPU.PerCore = make([]float64, len(cores))
		for i := range cores {
			if i < len(s.prevCores) {
				p, c := s.prevCores[i], cores[i]
				total := float64(c.KernelTime-p.KernelTime) + float64(c.UserTime-p.UserTime)
				out.CPU.PerCore[i] = pct(total-float64(c.IdleTime-p.IdleTime), total)
			}
		}
		s.prevCores = cores
	}

	// Memory
	if m, err := globalMemoryStatusEx(); err == nil {
		out.Mem.Total = m.TotalPhys
		out.Mem.Free = m.AvailPhys
		if m.TotalPhys > m.AvailPhys {
			out.Mem.Used = m.TotalPhys - m.AvailPhys
		}
		committed := m.TotalPageFile - m.AvailPageFile
		out.Mem.Committed, out.Mem.CommitLimit = committed, m.TotalPageFile
		if m.TotalPageFile > m.TotalPhys {
			out.Mem.SwapTotal = m.TotalPageFile - m.TotalPhys
		}
		if committed > out.Mem.Used {
			out.Mem.SwapUsed = committed - out.Mem.Used
			if out.Mem.SwapUsed > out.Mem.SwapTotal {
				out.Mem.SwapUsed = out.Mem.SwapTotal
			}
		}
		switch {
		case m.MemoryLoad >= 92:
			out.Mem.Pressure = "critical"
		case m.MemoryLoad >= 80:
			out.Mem.Pressure = "warning"
		default:
			out.Mem.Pressure = "normal"
		}
	}
	if p, err := getPerformanceInfo(); err == nil {
		out.Mem.Cached = uint64(p.SystemCache) * uint64(p.PageSize)
	}

	// PDH: GPU + disk
	if s.pdh != nil && s.pdh.Collect() == nil {
		out.GPU = s.readGPU()
		if s.cDiskRead != 0 {
			out.Disk.ReadBps, _ = formattedValue(s.cDiskRead)
		}
		if s.cDiskWrite != 0 {
			out.Disk.WriteBps, _ = formattedValue(s.cDiskWrite)
		}
	}

	// Network
	rx, tx := netTotals()
	out.Net = NetSample{RxBps: rate(rx, s.prevNetRx, elapsed), TxBps: rate(tx, s.prevNetTx, elapsed)}
	s.prevNetRx, s.prevNetTx = rx, tx

	out.Battery = readBatteryStatus()
	s.prevAt = now
	return out, nil
}

// readGPU aggregates the engine counters per adapter and joins the adapter
// names and memory sizes. Adapters without any active instance report 0 %.
func (s *windowsStats) readGPU() []GPUSample {
	if !s.gpuAvailable {
		return []GPUSample{}
	}
	items, err := formattedArray(s.cGPUEngine)
	if err != nil && !err.(pdhError).noData() {
		return []GPUSample{}
	}
	u := aggregateGPU(items)
	dedicated := map[string]uint64{}
	shared := map[string]uint64{}
	if s.cGPUDed != 0 {
		if arr, err := formattedArray(s.cGPUDed); err == nil {
			for _, it := range arr {
				if luid, ok := parseLUIDInstance(it.Instance); ok {
					dedicated[luid] += uint64(it.Value)
				}
			}
		}
	}
	if s.cGPUShared != 0 {
		if arr, err := formattedArray(s.cGPUShared); err == nil {
			for _, it := range arr {
				if luid, ok := parseLUIDInstance(it.Instance); ok {
					shared[luid] += uint64(it.Value)
				}
			}
		}
	}
	// Every adapter we know about, plus any LUID the counters mention.
	seen := map[string]bool{}
	var luids []string
	for _, l := range sortedLUIDs(u.Adapter) {
		seen[l] = true
		luids = append(luids, l)
	}
	for l := range s.adapters {
		if !seen[l] {
			seen[l] = true
			luids = append(luids, l)
		}
	}
	for l := range dedicated {
		if !seen[l] {
			seen[l] = true
			luids = append(luids, l)
		}
	}
	out := make([]GPUSample, 0, len(luids))
	for _, l := range luids {
		a, known := s.adapters[l]
		name := a.Name
		if name == "" {
			name = s.gpuFallback
			if name == "" {
				name = "GPU " + l
			}
		}
		if isBasicRenderDriver(name) {
			continue
		}
		g := GPUSample{Name: name, Utilization: u.Adapter[l], Engines: u.Engines[l], MemUsed: dedicated[l], SharedUsed: shared[l]}
		if known {
			g.MemTotal = a.DedicatedVideoBytes
			if g.MemTotal == 0 { // integrated: report the shared budget instead
				g.MemTotal = a.SharedSysBytes
				g.MemUsed = shared[l]
			}
		}
		out = append(out, g)
	}
	return out
}

// Processes walks SystemProcessInformation and computes per-process CPU %
// against the previous walk keyed by (PID, CreateTime).
func (s *windowsStats) Processes() ([]Process, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.procPrev) == 0 || time.Since(s.procAt) > 15*time.Second {
		if _, err := s.scanProcesses(); err != nil {
			return nil, err
		}
		time.Sleep(250 * time.Millisecond)
	}
	return s.scanProcesses()
}

func (s *windowsStats) scanProcesses() ([]Process, error) {
	size := uint32(512 << 10)
	var buf []byte
	for i := 0; i < 8; i++ {
		buf = make([]byte, size)
		var ret uint32
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&buf[0]), size, &ret)
		if err == nil {
			break
		}
		if err != windows.STATUS_INFO_LENGTH_MISMATCH {
			return nil, fmt.Errorf("NtQuerySystemInformation: %w", err)
		}
		size = ret + 64<<10
		buf = nil
	}
	if buf == nil {
		return nil, fmt.Errorf("NtQuerySystemInformation: buffer never fit")
	}
	now := time.Now()
	wall := float64(now.Sub(s.procAt).Nanoseconds()) / 100 * float64(max(1, s.logicalCPUs))
	next := make(map[procKey]procTimes, len(s.procPrev)+16)
	var out []Process
	for off := uint32(0); ; {
		if int(off)+int(unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{})) > len(buf) {
			break
		}
		p := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[off]))
		pid := uint32(p.UniqueProcessID)
		if pid != 0 {
			name := ""
			if p.ImageName.Buffer != nil && p.ImageName.Length > 0 {
				name = windows.UTF16ToString(unsafe.Slice(p.ImageName.Buffer, p.ImageName.Length/2))
			}
			if name == "" && pid == 4 {
				name = "System"
			}
			key := procKey{pid, p.CreateTime}
			cur := procTimes{p.KernelTime, p.UserTime}
			next[key] = cur
			row := Process{PID: int(pid), Name: name, MemBytes: uint64(max(0, p.WorkingSetPrivateSize))}
			if prev, ok := s.procPrev[key]; ok && wall > 0 {
				d := float64(cur.kernel-prev.kernel) + float64(cur.user-prev.user)
				row.CPU = pct(d, wall)
			}
			out = append(out, row)
		}
		if p.NextEntryOffset == 0 {
			break
		}
		off += p.NextEntryOffset
	}
	runtime.KeepAlive(buf)
	s.procPrev, s.procAt = next, now
	return out, nil
}

// Kill terminates the process. Windows has no graceful variant without a
// window handle, so force is accepted for interface symmetry and ignored.
func (s *windowsStats) Kill(pid int, force bool) error {
	if pid <= 4 || pid == os.Getpid() {
		return fmt.Errorf("refusing to end pid %d", pid)
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("OpenProcess %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		return fmt.Errorf("TerminateProcess %d: %w", pid, err)
	}
	return nil
}

// ------------------------------------------------------------- Win32 reads

func getSystemTimes() (systemTimes, error) {
	var idle, kernel, user windows.Filetime
	r, _, e := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if r == 0 {
		return systemTimes{}, e
	}
	return systemTimes{filetimeU64(idle), filetimeU64(kernel), filetimeU64(user)}, nil
}

// perCorePerf returns one entry per logical CPU of the current processor
// group (≤ 64).
func perCorePerf() ([]processorPerf, error) {
	n := windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS)
	if n == 0 || n > 64 {
		n = 64
	}
	buf := make([]processorPerf, n)
	var ret uint32
	err := windows.NtQuerySystemInformation(windows.SystemProcessorPerformanceInformation, unsafe.Pointer(&buf[0]), uint32(len(buf))*uint32(unsafe.Sizeof(buf[0])), &ret)
	if err != nil {
		return nil, err
	}
	return buf[:ret/uint32(unsafe.Sizeof(buf[0]))], nil
}

func globalMemoryStatusEx() (memoryStatusEx, error) {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	if r, _, e := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); r == 0 {
		return m, e
	}
	return m, nil
}

func getPerformanceInfo() (performanceInformation, error) {
	var p performanceInformation
	p.Cb = uint32(unsafe.Sizeof(p))
	if r, _, e := procGetPerformanceInfo.Call(uintptr(unsafe.Pointer(&p)), uintptr(p.Cb)); r == 0 {
		return p, e
	}
	return p, nil
}

func readBatteryStatus() *BatterySample {
	var st systemPowerStatus
	if r, _, _ := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&st))); r == 0 {
		return &BatterySample{OnAC: true}
	}
	b := &BatterySample{OnAC: st.ACLineStatus != 0}
	if st.BatteryFlag&128 != 0 || st.BatteryFlag == 255 {
		return b
	}
	b.Present = true
	if st.BatteryLifePercent != 255 {
		b.Percent = int(st.BatteryLifePercent)
	}
	b.Charging = st.BatteryFlag&8 != 0
	if st.BatteryLifeTime != 0xFFFFFFFF && !b.OnAC {
		b.TimeToEmptyMin = int(st.BatteryLifeTime / 60)
	}
	return b
}

// netTotals sums the octet counters of the up, non-loopback hardware
// interfaces (what Task Manager lists).
func netTotals() (rx, tx uint64) {
	var t *mibIfTable2
	if r, _, _ := procGetIfTable2.Call(uintptr(unsafe.Pointer(&t))); r != 0 || t == nil {
		return 0, 0
	}
	defer procFreeMibTable.Call(uintptr(unsafe.Pointer(t)))
	rows := unsafe.Slice(&t.Table[0], t.NumEntries)
	for i := range rows {
		r := &rows[i]
		if r.Type == windows.IF_TYPE_SOFTWARE_LOOPBACK || r.OperStatus != windows.IfOperStatusUp {
			continue
		}
		hardware := r.InterfaceAndOperStatusFlags&0x01 != 0
		connector := r.InterfaceAndOperStatusFlags&0x04 != 0
		if !hardware && !connector {
			continue
		}
		rx += r.InOctets
		tx += r.OutOctets
	}
	return rx, tx
}

func cpuName() (name string, baseMHz uint64, err error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE)
	if err != nil {
		return "", 0, err
	}
	defer k.Close()
	name, _, err = k.GetStringValue("ProcessorNameString")
	baseMHz, _, _ = k.GetIntegerValue("~MHz")
	return strings.Join(strings.Fields(name), " "), baseMHz, err
}

// gpuNameFromRegistry reads the first display adapter's DriverDesc as a
// fallback when D3DKMT enumeration fails.
func gpuNameFromRegistry() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}\0000`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	name, _, err := k.GetStringValue("DriverDesc")
	if err != nil {
		return ""
	}
	return name
}

// gpuAdapters maps LUID keys (as PDH prints them) to adapter names and
// memory sizes through gdi32's D3DKMT entry points.
func gpuAdapters() (map[string]gpuAdapter, error) {
	if procD3DKMTEnumAdapters2.Find() != nil {
		return nil, procD3DKMTEnumAdapters2.Find()
	}
	var e d3dkmtEnumAdapters2
	if r, _, _ := procD3DKMTEnumAdapters2.Call(uintptr(unsafe.Pointer(&e))); r != 0 {
		return nil, windows.NTStatus(r)
	}
	if e.NumAdapters == 0 {
		return nil, nil
	}
	ads := make([]d3dkmtAdapterInfo, e.NumAdapters)
	e.PAdapters = &ads[0]
	if r, _, _ := procD3DKMTEnumAdapters2.Call(uintptr(unsafe.Pointer(&e))); r != 0 {
		return nil, windows.NTStatus(r)
	}
	out := map[string]gpuAdapter{}
	for _, a := range ads[:e.NumAdapters] {
		var info d3dkmtAdapterRegistryInfo
		var seg d3dkmtSegmentSizeInfo
		q := d3dkmtQueryAdapterInfo{HAdapter: a.HAdapter, Type: 8, PrivateDriverData: unsafe.Pointer(&info), PrivateDriverDataSize: uint32(unsafe.Sizeof(info))}
		procD3DKMTQueryAdapter.Call(uintptr(unsafe.Pointer(&q)))
		q.Type, q.PrivateDriverData, q.PrivateDriverDataSize = 3, unsafe.Pointer(&seg), uint32(unsafe.Sizeof(seg))
		procD3DKMTQueryAdapter.Call(uintptr(unsafe.Pointer(&q)))
		h := a.HAdapter
		procD3DKMTCloseAdapter.Call(uintptr(unsafe.Pointer(&h)))
		key := fmt.Sprintf("0x%08X_0x%08X", uint32(a.AdapterLuid.HighPart), a.AdapterLuid.LowPart)
		out[key] = gpuAdapter{
			Name:                strings.TrimSpace(windows.UTF16ToString(info.AdapterString[:])),
			DedicatedVideoBytes: seg.DedicatedVideoMemorySize,
			SharedSysBytes:      seg.SharedSystemMemorySize,
		}
	}
	return out, nil
}
