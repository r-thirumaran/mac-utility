package main

import "time"

// Sample is one vitals.sample event (docs/ARCHITECTURE.md §5). Rates are
// bytes per second, percentages are 0–100, memory is in bytes. Fields a
// platform cannot measure are zero; the UI builds its breakdowns from
// whichever fields are non-zero (macOS reports app/wired/compressed, Windows
// and Linux report used/cached).
type Sample struct {
	T       int64          `json:"t"`
	CPU     CPUSample      `json:"cpu"`
	Mem     MemSample      `json:"mem"`
	GPU     []GPUSample    `json:"gpu"`
	Net     NetSample      `json:"net"`
	Disk    DiskSample     `json:"disk"`
	Battery *BatterySample `json:"battery"`
}

// CPUSample carries the total and per-logical-core busy percentages.
type CPUSample struct {
	Total   float64   `json:"total"`
	PerCore []float64 `json:"perCore"`
	LoadAvg []float64 `json:"loadAvg,omitempty"`
}

// MemSample is the memory breakdown. Pressure is "normal", "warning" or
// "critical".
type MemSample struct {
	Total      uint64 `json:"total"`
	Used       uint64 `json:"used"`
	App        uint64 `json:"app"`
	Wired      uint64 `json:"wired"`
	Compressed uint64 `json:"compressed"`
	Cached     uint64 `json:"cached"`
	Free       uint64 `json:"free"`
	SwapUsed   uint64 `json:"swapUsed"`
	SwapTotal  uint64 `json:"swapTotal"`
	// Committed and CommitLimit are the Windows commit charge (0 elsewhere).
	Committed   uint64 `json:"committed,omitempty"`
	CommitLimit uint64 `json:"commitLimit,omitempty"`
	Pressure    string `json:"pressure"`
}

// GPUSample is one adapter. Utilization is Task Manager's "GPU %" (the
// busiest engine, 0–100); Engines breaks it down per engine type.
type GPUSample struct {
	Name        string             `json:"name"`
	Utilization float64            `json:"utilization"`
	MemUsed     uint64             `json:"memUsed"`
	MemTotal    uint64             `json:"memTotal"`
	SharedUsed  uint64             `json:"sharedUsed,omitempty"`
	Engines     map[string]float64 `json:"engines,omitempty"`
	Renderer    float64            `json:"renderer,omitempty"`
	Tiler       float64            `json:"tiler,omitempty"`
}

// NetSample is the summed throughput over physical interfaces.
type NetSample struct {
	RxBps float64 `json:"rxBps"`
	TxBps float64 `json:"txBps"`
}

// DiskSample is the summed physical-disk throughput.
type DiskSample struct {
	ReadBps  float64 `json:"readBps"`
	WriteBps float64 `json:"writeBps"`
}

// BatterySample describes the main battery; Present is false on desktops.
type BatterySample struct {
	Present        bool `json:"present"`
	Percent        int  `json:"percent"`
	Charging       bool `json:"charging"`
	OnAC           bool `json:"onAC"`
	TimeToEmptyMin int  `json:"timeToEmptyMin,omitempty"`
}

// Process is one row of vitals.processes. CPU is the percentage of total
// machine capacity used since the previous call (Task Manager semantics).
type Process struct {
	PID      int     `json:"pid"`
	Name     string  `json:"name"`
	CPU      float64 `json:"cpu"`
	MemBytes uint64  `json:"memBytes"`
	Icon     string  `json:"icon,omitempty"`
	User     string  `json:"user,omitempty"`
}

// HostInfo is the static part of app.info.
type HostInfo struct {
	Machine   string `json:"machine"`
	CPUModel  string `json:"cpuModel"`
	Cores     int    `json:"cores"`
	MemTotal  uint64 `json:"memTotal"`
	UptimeSec int64  `json:"uptimeSec"`
	OS        string `json:"os"`
}

// Stats is the platform backend behind the sampler and the process list.
// Sample and Processes compute rates from the previous call, so the first
// call after construction reports zero rates. Kill ends a process; force asks
// for an immediate termination where the platform distinguishes the two
// (SIGKILL on Linux; Windows only has TerminateProcess).
type Stats interface {
	Sample() (Sample, error)
	Processes() ([]Process, error)
	Kill(pid int, force bool) error
	Info() HostInfo
	Close()
}

// rate returns delta/elapsed as a per-second rate, or 0 for a non-positive
// elapsed time or a counter that went backwards.
func rate(cur, prev uint64, elapsed time.Duration) float64 {
	if elapsed <= 0 || cur < prev {
		return 0
	}
	return float64(cur-prev) / elapsed.Seconds()
}

// pct returns 100·busy/total clamped to 0–100.
func pct(busy, total float64) float64 {
	if total <= 0 {
		return 0
	}
	p := 100 * busy / total
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// pressureFromFraction maps a used-memory fraction to the pressure label.
func pressureFromFraction(f float64) string {
	switch {
	case f >= 0.92:
		return "critical"
	case f >= 0.80:
		return "warning"
	default:
		return "normal"
	}
}
