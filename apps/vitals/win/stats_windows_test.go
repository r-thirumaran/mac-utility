//go:build windows

package main

import (
	"errors"
	"os"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Win32 layouts the stats backend hands to the kernel (amd64/arm64,
// docs/research/windows-system-stats.md §7 item 8).
func TestStatsLayouts(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("64-bit layouts only")
	}
	cases := []struct {
		name      string
		got, want uintptr
	}{
		{"SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION", unsafe.Sizeof(processorPerf{}), 48},
		{"MEMORYSTATUSEX", unsafe.Sizeof(memoryStatusEx{}), 64},
		{"PERFORMANCE_INFORMATION", unsafe.Sizeof(performanceInformation{}), 104},
		{"SYSTEM_POWER_STATUS", unsafe.Sizeof(systemPowerStatus{}), 12},
		{"MIB_IF_ROW2", unsafe.Sizeof(windows.MibIfRow2{}), 1352},
		{"MIB_IF_TABLE2.Table offset", unsafe.Offsetof(mibIfTable2{}.Table), 8},
		{"SYSTEM_PROCESS_INFORMATION", unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{}), 256},
		{"D3DKMT_ADAPTERINFO", unsafe.Sizeof(d3dkmtAdapterInfo{}), 20},
		{"D3DKMT_ENUMADAPTERS2", unsafe.Sizeof(d3dkmtEnumAdapters2{}), 16},
		{"D3DKMT_QUERYADAPTERINFO", unsafe.Sizeof(d3dkmtQueryAdapterInfo{}), 24},
		{"D3DKMT_ADAPTERREGISTRYINFO", unsafe.Sizeof(d3dkmtAdapterRegistryInfo{}), 2080},
		{"D3DKMT_SEGMENTSIZEINFO", unsafe.Sizeof(d3dkmtSegmentSizeInfo{}), 24},
		{"PDH_FMT_COUNTERVALUE", unsafe.Sizeof(pdhFmtCounterValueDouble{}), 16},
		{"PDH_FMT_COUNTERVALUE union offset", unsafe.Offsetof(pdhFmtCounterValueDouble{}.Value), 8},
		{"PDH_FMT_COUNTERVALUE_ITEM_W", unsafe.Sizeof(pdhFmtCounterValueItemDouble{}), 24},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

// TestWin32Reads exercises every raw kernel32/psapi/iphlpapi/ntdll read once
// on the real OS: argument marshalling, BOOL/NTSTATUS conventions and the
// struct layouts above.
func TestWin32Reads(t *testing.T) {
	st, err := getSystemTimes()
	if err != nil || st.Kernel == 0 {
		t.Fatalf("GetSystemTimes: %+v, %v", st, err)
	}
	if st.Kernel < st.Idle {
		t.Errorf("kernel time %d must include idle time %d", st.Kernel, st.Idle)
	}
	m, err := globalMemoryStatusEx()
	if err != nil || m.TotalPhys == 0 || m.AvailPhys > m.TotalPhys || m.MemoryLoad > 100 {
		t.Fatalf("GlobalMemoryStatusEx: %+v, %v", m, err)
	}
	p, err := getPerformanceInfo()
	if err != nil || p.PageSize == 0 || p.ProcessCount == 0 || p.PhysicalTotal == 0 {
		t.Fatalf("GetPerformanceInfo: %+v, %v", p, err)
	}
	// Both derive from the kernel's physical page count; a mismatch beyond
	// rounding means the SIZE_T page fields are being read at the wrong offsets.
	if got := uint64(p.PhysicalTotal) * uint64(p.PageSize); got > m.TotalPhys+m.TotalPhys/64 || got+m.TotalPhys/64 < m.TotalPhys {
		t.Errorf("PhysicalTotal pages = %d bytes, GlobalMemoryStatusEx says %d", got, m.TotalPhys)
	}
	cores, err := perCorePerf()
	if err != nil || len(cores) == 0 || len(cores) > 64 {
		t.Fatalf("SystemProcessorPerformanceInformation: %d cores, %v", len(cores), err)
	}
	for i, c := range cores {
		if c.KernelTime <= 0 || c.KernelTime < c.IdleTime {
			t.Errorf("core %d: kernel %d idle %d", i, c.KernelTime, c.IdleTime)
		}
	}
	if b := readBatteryStatus(); b == nil {
		t.Error("readBatteryStatus returned nil")
	}
	if name, _, err := cpuName(); err != nil || name == "" {
		t.Errorf("cpuName: %q, %v", name, err)
	}
	rx, tx := netTotals()
	t.Logf("net totals rx=%d tx=%d", rx, tx)
	if _, err := gpuAdapters(); err != nil {
		t.Logf("D3DKMTEnumAdapters2: %v (no WDDM adapter is fine)", err)
	}
}

func TestStatsBackend(t *testing.T) {
	s, err := newStats()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	info := s.Info()
	if info.Cores <= 0 || info.MemTotal == 0 || info.UptimeSec <= 0 || info.OS != "windows" {
		t.Fatalf("Info = %+v", info)
	}
	time.Sleep(300 * time.Millisecond)
	sample, err := s.Sample()
	if err != nil {
		t.Fatal(err)
	}
	if sample.Mem.Total != info.MemTotal || sample.Mem.Used == 0 || sample.Mem.Pressure == "" {
		t.Errorf("memory sample %+v", sample.Mem)
	}
	if sample.CPU.Total < 0 || sample.CPU.Total > 100 || len(sample.CPU.PerCore) != info.Cores {
		t.Errorf("cpu sample %+v (cores %d)", sample.CPU, info.Cores)
	}
	for i, c := range sample.CPU.PerCore {
		if c < 0 || c > 100 {
			t.Errorf("core %d = %v", i, c)
		}
	}
	if sample.Battery == nil || sample.GPU == nil {
		t.Errorf("battery/gpu must be present (empty, not nil): %+v %+v", sample.Battery, sample.GPU)
	}
	procs, err := s.Processes()
	if err != nil {
		t.Fatal(err)
	}
	me := os.Getpid()
	found := false
	for _, p := range procs {
		if p.PID == me {
			found = p.Name != "" && p.MemBytes > 0
		}
	}
	if !found {
		t.Errorf("own process %d missing or empty in %d rows", me, len(procs))
	}
	if err := s.Kill(4, true); err == nil {
		t.Error("Kill(4) must be refused")
	}
}

// TestPDH drives pdh.dll through the real counter path: the two-sample
// rule, the single-value read and the PDH_MORE_DATA array negotiation on
// a counter set that always has instances.
func TestPDH(t *testing.T) {
	q, err := openPdhQuery()
	if err != nil {
		t.Fatalf("PdhOpenQuery: %v", err)
	}
	defer q.Close()
	disk, err := q.AddEnglishCounter(counterDiskRead)
	if err != nil {
		t.Skipf("PhysicalDisk counters unavailable: %v", err)
	}
	cpu, err := q.AddEnglishCounter(`\Processor(*)\% Processor Time`)
	if err != nil {
		t.Fatalf("Processor counter: %v", err)
	}
	if err := q.Collect(); err != nil {
		t.Fatalf("first collect: %v", err)
	}
	time.Sleep(250 * time.Millisecond)
	if err := q.Collect(); err != nil {
		t.Fatalf("second collect: %v", err)
	}
	if v, err := formattedValue(disk); err != nil || v < 0 {
		t.Errorf("disk read bytes/sec = %v, %v", v, err)
	}
	items, err := formattedArray(cpu)
	if err != nil || len(items) == 0 {
		t.Fatalf("processor array: %d items, %v", len(items), err)
	}
	total := false
	for _, it := range items {
		if it.Instance == "_Total" {
			total = true
		}
		if it.Value < 0 || it.Value > 100 {
			t.Errorf("%s = %v", it.Instance, it.Value)
		}
	}
	if !total {
		t.Errorf("no _Total instance in %+v", items)
	}
	if gpu, err := q.AddEnglishCounter(counterGPUEngine); err == nil {
		_ = q.Collect()
		time.Sleep(250 * time.Millisecond)
		_ = q.Collect()
		arr, err := formattedArray(gpu)
		var pe pdhError
		if err != nil && !(errors.As(err, &pe) && pe.noData()) {
			t.Errorf("GPU engine array: %v", err)
		}
		for _, it := range arr {
			if _, ok := parseGPUInstance(it.Instance); !ok {
				t.Errorf("unparsable GPU instance %q", it.Instance)
			}
		}
	}
}
