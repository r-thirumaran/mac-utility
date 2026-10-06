//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// linuxStats reads /proc and /sys so the Linux test build shows live numbers
// through the same bridge the Windows exe uses.
type linuxStats struct {
	mu        sync.Mutex
	clkTck    float64
	pageSize  uint64
	info      HostInfo
	prevStat  cpuTimes
	prevCores []cpuTimes
	prevNetRx uint64
	prevNetTx uint64
	prevDiskR uint64
	prevDiskW uint64
	prevAt    time.Time
	procPrev  map[int]procSnap
	procAt    time.Time
}

type procSnap struct {
	start uint64
	ticks uint64
}

func newStats() (Stats, error) {
	s := &linuxStats{clkTck: 100, pageSize: uint64(os.Getpagesize()), procPrev: map[int]procSnap{}}
	host, _ := os.Hostname()
	s.info = HostInfo{Machine: host, OS: "linux", Cores: runtime.NumCPU()}
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		s.info.CPUModel = parseCPUModel(string(b))
	}
	if b, err := os.ReadFile("/proc/stat"); err == nil {
		if total, cores, err := parseProcStat(string(b)); err == nil {
			s.prevStat, s.prevCores = total, cores
			if len(cores) > 0 {
				s.info.Cores = len(cores)
			}
		}
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		s.info.MemTotal = parseMeminfo(string(b))["MemTotal"]
	}
	s.info.UptimeSec = int64(readUptime())
	s.prevNetRx, s.prevNetTx = readNetDev()
	s.prevDiskR, s.prevDiskW = readDiskstats()
	s.prevAt = time.Now()
	return s, nil
}

func (s *linuxStats) Info() HostInfo {
	info := s.info
	info.UptimeSec = int64(readUptime())
	return info
}

func (s *linuxStats) Close() {}

func readUptime() float64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	return parseUptime(string(b))
}

func readNetDev() (uint64, uint64) {
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	return parseNetDev(string(b))
}

func readDiskstats() (uint64, uint64) {
	b, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		return 0, 0
	}
	return parseDiskstats(string(b))
}

func (s *linuxStats) Sample() (Sample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(s.prevAt)
	out := Sample{T: now.UnixMilli(), GPU: []GPUSample{}}

	if b, err := os.ReadFile("/proc/stat"); err == nil {
		total, cores, err := parseProcStat(string(b))
		if err != nil {
			return out, err
		}
		out.CPU.Total = busyPercent(s.prevStat, total)
		out.CPU.PerCore = make([]float64, len(cores))
		for i := range cores {
			if i < len(s.prevCores) {
				out.CPU.PerCore[i] = busyPercent(s.prevCores[i], cores[i])
			}
		}
		s.prevStat, s.prevCores = total, cores
	} else {
		return out, fmt.Errorf("read /proc/stat: %w", err)
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		out.CPU.LoadAvg = parseLoadavg(string(b))
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		out.Mem = memFromMeminfo(parseMeminfo(string(b)))
	}
	if p := readPressure(); p != "" {
		out.Mem.Pressure = p
	}
	rx, tx := readNetDev()
	out.Net = NetSample{RxBps: rate(rx, s.prevNetRx, elapsed), TxBps: rate(tx, s.prevNetTx, elapsed)}
	s.prevNetRx, s.prevNetTx = rx, tx
	dr, dw := readDiskstats()
	out.Disk = DiskSample{ReadBps: rate(dr, s.prevDiskR, elapsed), WriteBps: rate(dw, s.prevDiskW, elapsed)}
	s.prevDiskR, s.prevDiskW = dr, dw
	out.Battery = readBattery()
	s.prevAt = now
	return out, nil
}

// readPressure maps the kernel's PSI memory "some" average to the label the
// UI badges (the meminfo heuristic stays in effect when PSI is unavailable).
func readPressure() string {
	b, err := os.ReadFile("/proc/pressure/memory")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "some ") {
			continue
		}
		for _, f := range strings.Fields(line) {
			if v, ok := strings.CutPrefix(f, "avg10="); ok {
				if x, err := strconv.ParseFloat(v, 64); err == nil {
					switch {
					case x >= 25:
						return "critical"
					case x >= 5:
						return "warning"
					default:
						return "normal"
					}
				}
			}
		}
	}
	return ""
}

func readBattery() *BatterySample {
	dirs, err := filepath.Glob("/sys/class/power_supply/*")
	if err != nil || len(dirs) == 0 {
		return &BatterySample{OnAC: true}
	}
	var supplies []map[string]string
	for _, d := range dirs {
		if b, err := os.ReadFile(filepath.Join(d, "uevent")); err == nil {
			supplies = append(supplies, parseUevent(string(b)))
		}
	}
	return batteryFromUevents(supplies)
}

// Processes lists every process with CPU deltas since the previous call;
// the first call (or one after a long pause) samples twice 250 ms apart.
func (s *linuxStats) Processes() ([]Process, error) {
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

func (s *linuxStats) scanProcesses() ([]Process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}
	now := time.Now()
	elapsedTicks := now.Sub(s.procAt).Seconds() * s.clkTck * float64(max(1, s.info.Cores))
	next := make(map[int]procSnap, len(entries))
	out := make([]Process, 0, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		st, err := parsePidStat(string(b))
		if err != nil {
			continue
		}
		ticks := st.UTime + st.STime
		next[pid] = procSnap{start: st.StartTime, ticks: ticks}
		p := Process{PID: pid, Name: st.Comm, MemBytes: uint64(max(0, st.RSSPages)) * s.pageSize}
		if prev, ok := s.procPrev[pid]; ok && prev.start == st.StartTime && elapsedTicks > 0 && ticks >= prev.ticks {
			p.CPU = pct(float64(ticks-prev.ticks), elapsedTicks)
		}
		if st.State == 'Z' {
			p.Name += " <defunct>"
		}
		out = append(out, p)
	}
	s.procPrev, s.procAt = next, now
	return out, nil
}

// Kill sends SIGTERM, or SIGKILL when force is set.
func (s *linuxStats) Kill(pid int, force bool) error {
	if pid <= 1 || pid == os.Getpid() {
		return fmt.Errorf("refusing to kill pid %d", pid)
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	if err := syscall.Kill(pid, sig); err != nil {
		return fmt.Errorf("kill %d: %w", pid, err)
	}
	return nil
}
