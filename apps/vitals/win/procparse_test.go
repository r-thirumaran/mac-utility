package main

import (
	"math"
	"testing"
)

const procStatFixture = `cpu  39665 0 10438 1026772 977 0 2677 2416 0 0
cpu0 9793 0 2607 256391 229 0 1588 657 0 0
cpu1 9711 0 2801 256686 278 0 723 620 0 0
cpu2 10000 0 2500 256000 200 0 200 600 0 0
cpu3 10161 0 2530 257695 270 0 166 539 0 0
intr 12345 0 0
ctxt 987654
btime 1700000000
processes 25000
procs_running 1
procs_blocked 0
`

func TestParseProcStat(t *testing.T) {
	total, cores, err := parseProcStat(procStatFixture)
	if err != nil {
		t.Fatal(err)
	}
	if total.User != 39665 || total.Idle != 1026772 || total.Steal != 2416 {
		t.Fatalf("total = %+v", total)
	}
	if len(cores) != 4 || cores[3].User != 10161 {
		t.Fatalf("cores = %+v", cores)
	}
	prev := cpuTimes{User: 100, System: 50, Idle: 850}
	cur := cpuTimes{User: 130, System: 60, Idle: 910}
	if got := busyPercent(prev, cur); math.Abs(got-40) > 1e-9 {
		t.Fatalf("busy = %v, want 40", got)
	}
	if got := busyPercent(cur, cur); got != 0 {
		t.Fatalf("busy with no elapsed = %v", got)
	}
	if _, _, err := parseProcStat("intr 1 2 3\n"); err == nil {
		t.Fatal("expected an error without a cpu line")
	}
}

const meminfoFixture = `MemTotal:       16480968 kB
MemFree:        13825908 kB
MemAvailable:   15718836 kB
Buffers:           50068 kB
Cached:          2006500 kB
SwapCached:            0 kB
SwapTotal:       2097148 kB
SwapFree:        1048576 kB
SReclaimable:     123456 kB
HugePages_Total:       0
`

func TestParseMeminfo(t *testing.T) {
	m := parseMeminfo(meminfoFixture)
	if m["MemTotal"] != 16480968*1024 {
		t.Fatalf("MemTotal = %d", m["MemTotal"])
	}
	if m["HugePages_Total"] != 0 {
		t.Fatalf("HugePages_Total = %d", m["HugePages_Total"])
	}
	s := memFromMeminfo(m)
	if s.Total != 16480968*1024 || s.Used != (16480968-15718836)*1024 {
		t.Fatalf("used = %d total = %d", s.Used, s.Total)
	}
	if s.Cached != (2006500+50068+123456)*1024 {
		t.Fatalf("cached = %d", s.Cached)
	}
	if s.SwapTotal != 2097148*1024 || s.SwapUsed != (2097148-1048576)*1024 {
		t.Fatalf("swap = %d/%d", s.SwapUsed, s.SwapTotal)
	}
	if s.Pressure != "normal" {
		t.Fatalf("pressure = %q", s.Pressure)
	}
	if got := pressureFromFraction(0.85); got != "warning" {
		t.Fatalf("pressure(0.85) = %q", got)
	}
	if got := pressureFromFraction(0.97); got != "critical" {
		t.Fatalf("pressure(0.97) = %q", got)
	}
}

const netDevFixture = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 170533882   28831    0    0    0     0          0         0 170533882   28831    0    0    0     0       0          0
  eth0: 1000 10 0 0 0 0 0 0 2000 20 0 0 0 0 0 0
 wlan0: 500 5 0 0 0 0 0 0 250 2 0 0 0 0 0 0
docker0: 99999 1 0 0 0 0 0 0 99999 1 0 0 0 0 0 0
veth1a: 99999 1 0 0 0 0 0 0 99999 1 0 0 0 0 0 0
`

func TestParseNetDev(t *testing.T) {
	rx, tx := parseNetDev(netDevFixture)
	if rx != 1500 || tx != 2250 {
		t.Fatalf("rx/tx = %d/%d", rx, tx)
	}
}

const diskstatsFixture = `   7       0 loop0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
   8       0 sda 1000 0 2000 10 500 0 4000 20 0 0 0 0 0 0 0 0 0
   8       1 sda1 900 0 1800 9 450 0 3600 18 0 0 0 0 0 0 0 0 0
 259       0 nvme0n1 10 0 100 1 5 0 200 1 0 0 0 0 0 0 0 0 0
 253       0 dm-0 10 0 100 1 5 0 200 1 0 0 0 0 0 0 0 0 0
`

func TestParseDiskstats(t *testing.T) {
	r, w := parseDiskstats(diskstatsFixture)
	if r != (2000+100)*512 || w != (4000+200)*512 {
		t.Fatalf("read/write = %d/%d", r, w)
	}
}

func TestParsePidStat(t *testing.T) {
	line := "25036 (my (odd) name) R 25022 25036 25022 0 -1 4194304 83 0 0 0 12 34 0 0 20 0 1 0 271017 2920448 365 18446744073709551615 94061291741184 0 0 0 0 0 0 0 0 0 0 0 17 1 0 0 0 0 0 0 0 0 0 0 0 0 0"
	p, err := parsePidStat(line)
	if err != nil {
		t.Fatal(err)
	}
	if p.Comm != "my (odd) name" || p.State != 'R' || p.UTime != 12 || p.STime != 34 || p.StartTime != 271017 || p.RSSPages != 365 {
		t.Fatalf("pidStat = %+v", p)
	}
	if _, err := parsePidStat("1 (x) R"); err == nil {
		t.Fatal("expected an error for a short line")
	}
}

func TestParseMisc(t *testing.T) {
	if got := parseLoadavg("0.09 0.18 0.26 1/104 25035\n"); len(got) != 3 || got[1] != 0.18 {
		t.Fatalf("loadavg = %v", got)
	}
	if got := parseUptime("2710.17 10267.75\n"); got != 2710.17 {
		t.Fatalf("uptime = %v", got)
	}
	cpuinfo := "processor\t: 0\nvendor_id\t: GenuineIntel\nmodel name\t: Intel(R) Xeon(R) Processor @ 2.10GHz\nprocessor\t: 1\nmodel name\t: other\n"
	if got := parseCPUModel(cpuinfo); got != "Intel(R) Xeon(R) Processor @ 2.10GHz" {
		t.Fatalf("cpu model = %q", got)
	}
	if got := parseCPUModel("Hardware\t: BCM2835\nRevision\t: a02082\n"); got != "BCM2835" {
		t.Fatalf("arm cpu model = %q", got)
	}
}

func TestBatteryFromUevents(t *testing.T) {
	ac := parseUevent("POWER_SUPPLY_NAME=AC\nPOWER_SUPPLY_TYPE=Mains\nPOWER_SUPPLY_ONLINE=0\n")
	bat := parseUevent("POWER_SUPPLY_NAME=BAT0\nPOWER_SUPPLY_TYPE=Battery\nPOWER_SUPPLY_STATUS=Discharging\nPOWER_SUPPLY_PRESENT=1\nPOWER_SUPPLY_CAPACITY=73\nPOWER_SUPPLY_ENERGY_NOW=30000000\nPOWER_SUPPLY_POWER_NOW=10000000\n")
	b := batteryFromUevents([]map[string]string{ac, bat})
	if !b.Present || b.Percent != 73 || b.Charging || b.OnAC || b.TimeToEmptyMin != 180 {
		t.Fatalf("battery = %+v", *b)
	}
	none := batteryFromUevents(nil)
	if none.Present || !none.OnAC {
		t.Fatalf("desktop battery = %+v", *none)
	}
	charging := batteryFromUevents([]map[string]string{parseUevent("POWER_SUPPLY_TYPE=Battery\nPOWER_SUPPLY_STATUS=Charging\nPOWER_SUPPLY_ENERGY_NOW=50\nPOWER_SUPPLY_ENERGY_FULL=100\n")})
	if !charging.Present || charging.Percent != 50 || !charging.Charging || !charging.OnAC {
		t.Fatalf("charging battery = %+v", *charging)
	}
}
