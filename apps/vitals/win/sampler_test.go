package main

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

// fakeStats is a deterministic Stats backend for the sampler and bridge tests.
type fakeStats struct {
	mu      sync.Mutex
	samples int
	killed  []int
	forced  []bool
	fail    bool
	procs   []Process
}

func newFakeStats() *fakeStats {
	return &fakeStats{procs: []Process{
		{PID: 10, Name: "zed", CPU: 1.5, MemBytes: 300},
		{PID: 2, Name: "Alpha", CPU: 40, MemBytes: 100},
		{PID: 7, Name: "beta", CPU: 40, MemBytes: 900},
		{PID: 5, Name: "gamma", CPU: 0, MemBytes: 900},
	}}
}

func (f *fakeStats) Sample() (Sample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return Sample{}, errors.New("boom")
	}
	f.samples++
	return Sample{CPU: CPUSample{Total: 12.5, PerCore: []float64{10, 15}}, Mem: MemSample{Total: 1000, Used: 400, Cached: 100}}, nil
}

func (f *fakeStats) Processes() ([]Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Process(nil), f.procs...), nil
}

func (f *fakeStats) Kill(pid int, force bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch pid {
	case 999:
		return os.ErrPermission
	case 404:
		return os.ErrNotExist
	}
	f.killed = append(f.killed, pid)
	f.forced = append(f.forced, force)
	return nil
}

func (f *fakeStats) Info() HostInfo {
	return HostInfo{Machine: "testbox", CPUModel: "Fake CPU", Cores: 2, MemTotal: 1000, UptimeSec: 42, OS: "linux"}
}

func (f *fakeStats) Close() {}

func (f *fakeStats) sampleCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.samples
}

func TestClampInterval(t *testing.T) {
	def := time.Second
	cases := []struct {
		in, want time.Duration
	}{
		{0, time.Second},
		{-5, time.Second},
		{10 * time.Millisecond, minInterval},
		{500 * time.Millisecond, 500 * time.Millisecond},
		{time.Minute, maxInterval},
	}
	for _, c := range cases {
		if got := clampInterval(c.in, def); got != c.want {
			t.Errorf("clampInterval(%s) = %s, want %s", c.in, got, c.want)
		}
	}
	if got := clampInterval(0, 50*time.Millisecond); got != minInterval {
		t.Errorf("a too-small default is clamped too: %s", got)
	}
}

func TestNormalizeSample(t *testing.T) {
	var s Sample
	normalizeSample(&s)
	if s.CPU.PerCore == nil || s.GPU == nil {
		t.Fatal("slices must be non-nil so they encode as []")
	}
	if s.Mem.Pressure != "normal" {
		t.Fatalf("pressure = %q", s.Mem.Pressure)
	}
	if s.Battery == nil || !s.Battery.OnAC || s.Battery.Present {
		t.Fatalf("battery = %+v", s.Battery)
	}
	if s.T == 0 {
		t.Fatal("timestamp not set")
	}
	kept := Sample{T: 5, Mem: MemSample{Pressure: "critical"}, Battery: &BatterySample{Present: true, Percent: 50}}
	normalizeSample(&kept)
	if kept.T != 5 || kept.Mem.Pressure != "critical" || kept.Battery.Percent != 50 {
		t.Fatalf("existing values changed: %+v", kept)
	}
}

func TestSortAndLimitProcesses(t *testing.T) {
	pids := func(list []Process) []int {
		out := make([]int, len(list))
		for i, p := range list {
			out[i] = p.PID
		}
		return out
	}
	eq := func(a, b []int) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	base := newFakeStats().procs
	cases := map[string][]int{
		"":       {2, 7, 10, 5}, // cpu desc, ties by pid
		"cpu":    {2, 7, 10, 5},
		"mem":    {5, 7, 10, 2}, // mem desc, ties by pid
		"memory": {5, 7, 10, 2},
		"name":   {2, 7, 5, 10}, // case-insensitive
		"pid":    {2, 5, 7, 10},
	}
	for key, want := range cases {
		list := append([]Process(nil), base...)
		sortProcesses(list, key)
		if got := pids(list); !eq(got, want) {
			t.Errorf("sortBy %q = %v, want %v", key, got, want)
		}
	}
	if got := limitProcesses(append([]Process(nil), base...), 2); len(got) != 2 {
		t.Fatalf("limit 2 → %d rows", len(got))
	}
	if got := limitProcesses(append([]Process(nil), base...), 0); len(got) != 4 {
		t.Fatalf("limit 0 keeps everything, got %d", len(got))
	}
	if got := limitProcesses(nil, 5); got == nil {
		t.Fatal("nil list must become an empty slice")
	}
}

func TestSamplerLifecycle(t *testing.T) {
	stats := newFakeStats()
	var mu sync.Mutex
	var got []Sample
	s := newSampler(stats, 300*time.Millisecond, func(smp Sample) {
		mu.Lock()
		got = append(got, smp)
		mu.Unlock()
	})
	if s.Running() || s.Interval() != 0 {
		t.Fatal("must be idle before the first subscriber")
	}
	if iv := s.Subscribe(0); iv != 300*time.Millisecond {
		t.Fatalf("default interval = %s", iv)
	}
	if iv := s.Subscribe(time.Hour); iv != 300*time.Millisecond {
		t.Fatalf("a slower subscriber must not slow the loop: %s", iv)
	}
	if iv := s.Subscribe(250 * time.Millisecond); iv != 250*time.Millisecond {
		t.Fatalf("a faster subscriber speeds the loop up: %s", iv)
	}
	if s.Subscribers() != 3 || !s.Running() {
		t.Fatalf("subs=%d running=%v", s.Subscribers(), s.Running())
	}
	deadline := time.Now().Add(3 * time.Second)
	for stats.sampleCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if stats.sampleCount() < 2 {
		t.Fatal("the loop did not sample twice")
	}
	last, ok := s.Last()
	if !ok || last.CPU.Total != 12.5 || last.Mem.Pressure != "normal" || last.Battery == nil {
		t.Fatalf("last sample = %+v (ok %v)", last, ok)
	}
	mu.Lock()
	n := len(got)
	mu.Unlock()
	if n < 2 {
		t.Fatalf("emit called %d times", n)
	}
	s.Unsubscribe()
	s.Unsubscribe()
	if !s.Running() {
		t.Fatal("still one subscriber: must keep running")
	}
	if left := s.Unsubscribe(); left != 0 || s.Running() {
		t.Fatalf("after the last unsubscribe: left=%d running=%v", left, s.Running())
	}
	if s.Unsubscribe() != 0 {
		t.Fatal("unsubscribe never goes negative")
	}
	// A fresh subscription restarts the loop and samples again.
	before := stats.sampleCount()
	s.Subscribe(0)
	deadline = time.Now().Add(3 * time.Second)
	for stats.sampleCount() == before && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if stats.sampleCount() == before {
		t.Fatal("restart did not sample")
	}
	s.Close()
	if s.Running() || s.Subscribers() != 0 {
		t.Fatal("Close must stop and reset")
	}
}

func TestSamplerStopsWithoutClients(t *testing.T) {
	stats := newFakeStats()
	var clients int32 = 1
	var mu sync.Mutex
	s := newSampler(stats, 250*time.Millisecond, func(Sample) {})
	s.clients = func() int { mu.Lock(); defer mu.Unlock(); return int(clients) }
	s.Subscribe(0)
	deadline := time.Now().Add(2 * time.Second)
	for stats.sampleCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	clients = 0
	mu.Unlock()
	deadline = time.Now().Add(2 * time.Second)
	for s.Running() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.Running() || s.Subscribers() != 0 {
		t.Fatalf("loop must stop once every sink is gone: running=%v subs=%d", s.Running(), s.Subscribers())
	}
}

func TestSamplerKeepsGoingAfterSampleError(t *testing.T) {
	stats := newFakeStats()
	stats.fail = true
	var logged int
	var mu sync.Mutex
	s := newSampler(stats, 250*time.Millisecond, func(Sample) {})
	s.logf = func(string, ...any) { mu.Lock(); logged++; mu.Unlock() }
	s.Subscribe(0)
	time.Sleep(350 * time.Millisecond)
	stats.mu.Lock()
	stats.fail = false
	stats.mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for stats.sampleCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	s.Close()
	mu.Lock()
	defer mu.Unlock()
	if logged == 0 {
		t.Fatal("errors must be logged")
	}
	if stats.sampleCount() == 0 {
		t.Fatal("the loop must survive a failed sample")
	}
}
