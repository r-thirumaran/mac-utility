package core

import (
	"testing"
	"time"
)

var shakeStart = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

// feed plays a list of deltas spaced ms apart and returns how often the
// detector fired.
func feed(s *ShakeDetector, at time.Time, step time.Duration, deltas ...int) (int, time.Time) {
	fired := 0
	for _, dx := range deltas {
		if s.Feed(dx, 0, at) {
			fired++
		}
		at = at.Add(step)
	}
	return fired, at
}

func TestShakeFiresOnFourReversals(t *testing.T) {
	s := NewShakeDetector()
	// Five 60-unit strokes alternating direction = four reversals, inside the
	// 600 ms window.
	n, _ := feed(s, shakeStart, 50*time.Millisecond, 60, -60, 60, -60, 60)
	if n != 1 {
		t.Fatalf("fired %d times, want 1", n)
	}
}

func TestShakeNeedsLongEnoughStrokes(t *testing.T) {
	s := NewShakeDetector()
	// 20-unit wiggles never reach ShakeMinStroke.
	if n, _ := feed(s, shakeStart, 20*time.Millisecond, 20, -20, 20, -20, 20, -20, 20, -20, 20); n != 0 {
		t.Fatalf("short strokes fired %d times", n)
	}
	// One long stroke is not a shake either.
	s = NewShakeDetector()
	if n, _ := feed(s, shakeStart, 20*time.Millisecond, 100, 100, 100, 100); n != 0 {
		t.Fatal("a straight drag fired")
	}
}

func TestShakeWindowExpires(t *testing.T) {
	s := NewShakeDetector()
	// Reversals 300 ms apart: by the fourth, the first two have aged out of
	// the 600 ms window.
	if n, _ := feed(s, shakeStart, 300*time.Millisecond, 60, -60, 60, -60, 60); n != 0 {
		t.Fatalf("slow wiggle fired %d times", n)
	}
}

func TestShakeCooldown(t *testing.T) {
	s := NewShakeDetector()
	n, at := feed(s, shakeStart, 50*time.Millisecond, 60, -60, 60, -60, 60)
	if n != 1 {
		t.Fatalf("first shake fired %d times", n)
	}
	// Immediately shaking again does nothing.
	n, at = feed(s, at, 50*time.Millisecond, -60, 60, -60, 60, -60)
	if n != 0 {
		t.Fatalf("fired during the cooldown (%d)", n)
	}
	// After the cooldown it fires again.
	at = at.Add(ShakeCooldown)
	if n, _ = feed(s, at, 50*time.Millisecond, 60, -60, 60, -60, 60); n != 1 {
		t.Fatalf("fired %d times after the cooldown", n)
	}
}

func TestShakeJitterBreaksTheChain(t *testing.T) {
	s := NewShakeDetector()
	// Two good reversals, then a 5-unit stroke: the stroke after it is too
	// short to count, which resets the chain, so nothing fires.
	if n, _ := feed(s, shakeStart, 30*time.Millisecond, 60, -60, 60, -5, 60, -60, 60); n != 0 {
		t.Fatalf("jitter still fired (%d)", n)
	}
}

func TestShakeResetAndZeroDeltas(t *testing.T) {
	s := NewShakeDetector()
	_, at := feed(s, shakeStart, 20*time.Millisecond, 60, -60, 60)
	s.Reset()
	if n, _ := feed(s, at, 20*time.Millisecond, -60, 60); n != 0 {
		t.Fatal("fired after Reset")
	}
	// Vertical-only moves are ignored entirely.
	if s.Feed(0, 500, at) {
		t.Fatal("a vertical move fired")
	}
	// Custom thresholds are honoured.
	c := &ShakeDetector{Window: time.Second, MinStroke: 10, MinReversals: 2, Cooldown: time.Second}
	if n, _ := feed(c, shakeStart, 10*time.Millisecond, 10, -10, 10); n != 1 {
		t.Fatal("custom detector did not fire")
	}
}
