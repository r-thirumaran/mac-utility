package main

import (
	"bufio"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// Pure parsers for the Linux /proc and /sys files behind stats_linux.go.
// They take file contents, so they are unit-tested with fixtures on every
// platform and vetted in the Windows build.

// cpuTimes are the jiffies columns of a /proc/stat "cpu" line.
type cpuTimes struct {
	User, Nice, System, Idle, IOWait, IRQ, SoftIRQ, Steal uint64
}

func (c cpuTimes) idle() uint64 { return c.Idle + c.IOWait }
func (c cpuTimes) total() uint64 {
	return c.User + c.Nice + c.System + c.Idle + c.IOWait + c.IRQ + c.SoftIRQ + c.Steal
}

// busyPercent is the busy share between two readings of one CPU line.
func busyPercent(prev, cur cpuTimes) float64 {
	total := float64(cur.total()) - float64(prev.total())
	idle := float64(cur.idle()) - float64(prev.idle())
	return pct(total-idle, total)
}

// parseProcStat returns the aggregate "cpu" line and one entry per "cpuN"
// line of /proc/stat, in order.
func parseProcStat(text string) (total cpuTimes, perCore []cpuTimes, err error) {
	sc := bufio.NewScanner(strings.NewReader(text))
	found := false
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || !strings.HasPrefix(f[0], "cpu") {
			continue
		}
		var t cpuTimes
		cols := []*uint64{&t.User, &t.Nice, &t.System, &t.Idle, &t.IOWait, &t.IRQ, &t.SoftIRQ, &t.Steal}
		for i, p := range cols {
			if i+1 < len(f) {
				*p, _ = strconv.ParseUint(f[i+1], 10, 64)
			}
		}
		if f[0] == "cpu" {
			total, found = t, true
		} else {
			perCore = append(perCore, t)
		}
	}
	if !found {
		return total, nil, errors.New("procparse: no cpu line in /proc/stat")
	}
	return total, perCore, nil
}

// parseMeminfo returns every /proc/meminfo field in bytes.
func parseMeminfo(text string) map[string]uint64 {
	out := map[string]uint64{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		key, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		v, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		if len(f) > 1 && strings.EqualFold(f[1], "kB") {
			v *= 1024
		}
		out[strings.TrimSpace(key)] = v
	}
	return out
}

// memFromMeminfo derives the MemSample fields from /proc/meminfo values.
func memFromMeminfo(m map[string]uint64) MemSample {
	total := m["MemTotal"]
	avail, ok := m["MemAvailable"]
	if !ok {
		avail = m["MemFree"] + m["Buffers"] + m["Cached"]
	}
	var used uint64
	if total > avail {
		used = total - avail
	}
	s := MemSample{
		Total:     total,
		Used:      used,
		Cached:    m["Cached"] + m["Buffers"] + m["SReclaimable"],
		Free:      m["MemFree"],
		SwapTotal: m["SwapTotal"],
	}
	if m["SwapTotal"] >= m["SwapFree"] {
		s.SwapUsed = m["SwapTotal"] - m["SwapFree"]
	}
	if total > 0 {
		s.Pressure = pressureFromFraction(float64(used) / float64(total))
	} else {
		s.Pressure = "normal"
	}
	return s
}

// parseNetDev sums the receive and transmit byte counters of every
// interface in /proc/net/dev except the loopback and virtual ones.
func parseNetDev(text string) (rx, tx uint64) {
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if !countNetInterface(name) {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(f[0], 10, 64)
		t, _ := strconv.ParseUint(f[8], 10, 64)
		rx += r
		tx += t
	}
	return rx, tx
}

// countNetInterface filters loopback, bridges, container and tunnel
// interfaces so traffic is not counted twice.
func countNetInterface(name string) bool {
	if name == "lo" {
		return false
	}
	for _, p := range []string{"ifb", "veth", "docker", "br-", "virbr", "vmnet", "tun", "tap", "lxc", "cni", "flannel", "dummy"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

// wholeDiskRE matches block devices that are whole disks (not partitions,
// loops, RAM disks or device-mapper volumes), so bytes are counted once.
var wholeDiskRE = regexp.MustCompile(`^(sd[a-z]+|vd[a-z]+|hd[a-z]+|xvd[a-z]+|nvme\d+n\d+|mmcblk\d+)$`)

// parseDiskstats sums sectors read and written (×512) over whole disks.
func parseDiskstats(text string) (readBytes, writeBytes uint64) {
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		// major minor name reads_completed reads_merged sectors_read ms_reading
		// writes_completed writes_merged sectors_written …
		if len(f) < 10 || !wholeDiskRE.MatchString(f[2]) {
			continue
		}
		r, _ := strconv.ParseUint(f[5], 10, 64)
		w, _ := strconv.ParseUint(f[9], 10, 64)
		readBytes += r * 512
		writeBytes += w * 512
	}
	return readBytes, writeBytes
}

// pidStat is the subset of /proc/<pid>/stat Vitals uses.
type pidStat struct {
	Comm      string
	State     byte
	UTime     uint64 // clock ticks
	STime     uint64
	StartTime uint64 // clock ticks since boot
	RSSPages  int64
}

// parsePidStat parses /proc/<pid>/stat; the command name may contain spaces
// and parentheses, so the fields are located from the last ')'.
func parsePidStat(text string) (pidStat, error) {
	var p pidStat
	open := strings.IndexByte(text, '(')
	closeIdx := strings.LastIndexByte(text, ')')
	if open < 0 || closeIdx < open {
		return p, errors.New("procparse: malformed stat line")
	}
	p.Comm = text[open+1 : closeIdx]
	f := strings.Fields(text[closeIdx+1:])
	// f[0] = state (field 3), utime = field 14 → f[11], stime → f[12],
	// starttime = field 22 → f[19], rss = field 24 → f[21].
	if len(f) < 22 {
		return p, errors.New("procparse: short stat line")
	}
	p.State = f[0][0]
	p.UTime, _ = strconv.ParseUint(f[11], 10, 64)
	p.STime, _ = strconv.ParseUint(f[12], 10, 64)
	p.StartTime, _ = strconv.ParseUint(f[19], 10, 64)
	p.RSSPages, _ = strconv.ParseInt(f[21], 10, 64)
	return p, nil
}

// parseLoadavg returns the three load averages of /proc/loadavg.
func parseLoadavg(text string) []float64 {
	f := strings.Fields(text)
	if len(f) < 3 {
		return nil
	}
	out := make([]float64, 3)
	for i := 0; i < 3; i++ {
		out[i], _ = strconv.ParseFloat(f[i], 64)
	}
	return out
}

// parseUptime returns the first number of /proc/uptime in seconds.
func parseUptime(text string) float64 {
	f := strings.Fields(text)
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

// parseCPUModel returns the first "model name" (x86) or "Hardware"/"Model"
// (ARM) value of /proc/cpuinfo.
func parseCPUModel(text string) string {
	fallback := ""
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch key {
		case "model name":
			return val
		case "Hardware", "Model":
			if fallback == "" {
				fallback = val
			}
		}
	}
	return fallback
}

// parseUevent parses a /sys/class/power_supply/<x>/uevent file into its
// POWER_SUPPLY_* keys (without the prefix).
func parseUevent(text string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		out[strings.TrimPrefix(k, "POWER_SUPPLY_")] = strings.TrimSpace(v)
	}
	return out
}

// batteryFromUevents builds the battery sample from the uevent maps of every
// power supply. ac reports whether any Mains/USB supply is online.
func batteryFromUevents(supplies []map[string]string) *BatterySample {
	b := &BatterySample{OnAC: true}
	var battery map[string]string
	acKnown := false
	for _, u := range supplies {
		switch u["TYPE"] {
		case "Battery":
			if battery == nil && u["PRESENT"] != "0" {
				battery = u
			}
		case "Mains", "USB", "USB_PD", "USB_C", "Wireless":
			if !acKnown {
				b.OnAC = false
				acKnown = true
			}
			if u["ONLINE"] == "1" {
				b.OnAC = true
			}
		}
	}
	if battery == nil {
		return b
	}
	b.Present = true
	if v, err := strconv.Atoi(battery["CAPACITY"]); err == nil {
		b.Percent = v
	} else if now, err := strconv.ParseFloat(battery["ENERGY_NOW"], 64); err == nil {
		if full, err := strconv.ParseFloat(battery["ENERGY_FULL"], 64); err == nil && full > 0 {
			b.Percent = int(100*now/full + 0.5)
		}
	}
	status := battery["STATUS"]
	b.Charging = status == "Charging"
	if !acKnown {
		b.OnAC = status != "Discharging"
	}
	if status == "Discharging" {
		if v, err := strconv.ParseFloat(battery["TIME_TO_EMPTY_NOW"], 64); err == nil && v > 0 {
			b.TimeToEmptyMin = int(v / 60)
		} else if now, err := strconv.ParseFloat(battery["ENERGY_NOW"], 64); err == nil {
			if power, err := strconv.ParseFloat(battery["POWER_NOW"], 64); err == nil && power > 0 {
				b.TimeToEmptyMin = int(now / power * 60)
			}
		}
	}
	return b
}
