package main

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Sampling interval bounds for vitals.subscribe {intervalMs}.
const (
	minInterval = 250 * time.Millisecond
	maxInterval = 10 * time.Second
)

// clampInterval maps a requested interval to the allowed range; a zero or
// negative request means the default.
func clampInterval(req, def time.Duration) time.Duration {
	if req <= 0 {
		req = def
	}
	if req < minInterval {
		return minInterval
	}
	if req > maxInterval {
		return maxInterval
	}
	return req
}

// normalizeSample fills the fields the UI contract promises are present:
// non-nil slices, a pressure label and a battery object (Present=false on
// desktops) so the page never has to null-check them.
func normalizeSample(s *Sample) {
	if s.CPU.PerCore == nil {
		s.CPU.PerCore = []float64{}
	}
	if s.GPU == nil {
		s.GPU = []GPUSample{}
	}
	if s.Mem.Pressure == "" {
		s.Mem.Pressure = "normal"
	}
	if s.Battery == nil {
		s.Battery = &BatterySample{OnAC: true}
	}
	if s.T == 0 {
		s.T = time.Now().UnixMilli()
	}
}

// sortProcesses orders the list for vitals.processes {sortBy}: "cpu"
// (default) and "mem"/"memory"/"memBytes" descending, "name" ascending
// (case-insensitive), "pid" ascending. Ties break on pid so the order is
// stable between refreshes.
func sortProcesses(list []Process, sortBy string) {
	key := strings.ToLower(strings.TrimSpace(sortBy))
	less := func(a, b Process) bool {
		switch key {
		case "mem", "memory", "membytes":
			if a.MemBytes != b.MemBytes {
				return a.MemBytes > b.MemBytes
			}
		case "name":
			an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name)
			if an != bn {
				return an < bn
			}
		case "pid":
			return a.PID < b.PID
		default: // cpu
			if a.CPU != b.CPU {
				return a.CPU > b.CPU
			}
		}
		return a.PID < b.PID
	}
	sort.SliceStable(list, func(i, j int) bool { return less(list[i], list[j]) })
}

// limitProcesses truncates to limit rows when limit is positive.
func limitProcesses(list []Process, limit int) []Process {
	if list == nil {
		return []Process{}
	}
	if limit > 0 && len(list) > limit {
		return list[:limit]
	}
	return list
}

// sampler is the one shared goroutine behind vitals.subscribe. It runs
// while at least one subscriber exists (and, when clients is set, while at
// least one bridge sink is connected) and hands every sample to emit.
type sampler struct {
	stats   Stats
	emit    func(Sample)
	clients func() int // connected sinks; nil = not checked
	def     time.Duration
	logf    func(format string, a ...any)

	mu       sync.Mutex
	subs     int
	interval time.Duration
	stop     chan struct{}
	done     chan struct{}
	last     *Sample
	samples  uint64
}

func newSampler(stats Stats, def time.Duration, emit func(Sample)) *sampler {
	if def <= 0 {
		def = time.Second
	}
	return &sampler{stats: stats, emit: emit, def: clampInterval(def, def), logf: func(string, ...any) {}}
}

// Subscribe adds a subscriber asking for the given interval and returns the
// interval the shared loop now runs at (the fastest request wins).
func (s *sampler) Subscribe(req time.Duration) time.Duration {
	iv := clampInterval(req, s.def)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs++
	if s.stop == nil {
		s.interval = iv
		s.stop = make(chan struct{})
		s.done = make(chan struct{})
		go s.loop(s.stop, s.done)
		return iv
	}
	if iv < s.interval {
		s.interval = iv
	}
	return s.interval
}

// Unsubscribe removes one subscriber; the loop stops with the last one.
func (s *sampler) Unsubscribe() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subs > 0 {
		s.subs--
	}
	if s.subs == 0 {
		s.stopLocked()
	}
	return s.subs
}

// Reset drops every subscriber (all clients went away) and stops the loop.
func (s *sampler) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs = 0
	s.stopLocked()
}

func (s *sampler) stopLocked() {
	if s.stop != nil {
		close(s.stop)
		s.stop, s.done = nil, nil
	}
}

// Close stops the loop and waits for it to exit.
func (s *sampler) Close() {
	s.mu.Lock()
	done := s.done
	s.subs = 0
	s.stopLocked()
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}

// Subscribers reports the current subscriber count.
func (s *sampler) Subscribers() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.subs
}

// Running reports whether the loop is active.
func (s *sampler) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stop != nil
}

// Interval is the interval the loop currently runs at (0 when stopped).
func (s *sampler) Interval() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop == nil {
		return 0
	}
	return s.interval
}

// Last returns the most recent sample, if any.
func (s *sampler) Last() (Sample, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		return Sample{}, false
	}
	return *s.last, true
}

// Samples counts the samples taken since start.
func (s *sampler) Samples() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.samples
}

func (s *sampler) loop(stop chan struct{}, done chan<- struct{}) {
	defer close(done)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-stop:
			return
		case <-timer.C:
		}
		sample, err := s.stats.Sample()
		if err != nil {
			s.logf("sample: %v", err)
		} else {
			normalizeSample(&sample)
			s.mu.Lock()
			cp := sample
			s.last = &cp
			s.samples++
			s.mu.Unlock()
			s.emit(sample)
		}
		if s.clients != nil && s.clients() == 0 {
			// Every page went away without unsubscribing (closed window,
			// reload): stop until the next subscribe.
			s.mu.Lock()
			if s.stop == stop { // still our run (not restarted meanwhile)
				s.subs = 0
				s.stopLocked()
			}
			s.mu.Unlock()
			return
		}
		s.mu.Lock()
		iv := s.interval
		s.mu.Unlock()
		timer.Reset(iv)
	}
}
