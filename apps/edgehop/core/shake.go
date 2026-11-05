package core

import "time"

// Shake to locate the pointer (docs/edgehop-protocol.md §6.3 `locate`): the
// machine that receives the physical mouse events watches the raw deltas,
// whether or not it is forwarding them.

// Shake detection constants.
const (
	// ShakeWindow is the sliding window the reversals must fall into.
	ShakeWindow = 600 * time.Millisecond
	// ShakeMinStroke is how far (world units) the pointer must travel
	// between two reversals for the reversal to count.
	ShakeMinStroke = 40
	// ShakeMinReversals is how many counting reversals fire the locate.
	ShakeMinReversals = 4
	// ShakeCooldown is the quiet time after a locate fired.
	ShakeCooldown = 1500 * time.Millisecond
)

// ShakeDetector spots a horizontal shake in raw pointer deltas. It is pure:
// feed it every physical mouse delta with the time it happened and it
// reports the single moment the gesture completes. Not safe for concurrent
// use; the Node calls it under its lock.
type ShakeDetector struct {
	// Window, MinStroke, MinReversals and Cooldown default to the constants
	// above when zero.
	Window       time.Duration
	MinStroke    int
	MinReversals int
	Cooldown     time.Duration

	dir       int // current horizontal direction: -1, 0 or +1
	travel    int // |distance| travelled since the last direction change
	reversals []time.Time
	lastFire  time.Time
}

// NewShakeDetector returns a detector with the spec defaults.
func NewShakeDetector() *ShakeDetector {
	return &ShakeDetector{Window: ShakeWindow, MinStroke: ShakeMinStroke, MinReversals: ShakeMinReversals, Cooldown: ShakeCooldown}
}

func (s *ShakeDetector) window() time.Duration {
	if s.Window <= 0 {
		return ShakeWindow
	}
	return s.Window
}

func (s *ShakeDetector) minStroke() int {
	if s.MinStroke <= 0 {
		return ShakeMinStroke
	}
	return s.MinStroke
}

func (s *ShakeDetector) minReversals() int {
	if s.MinReversals <= 0 {
		return ShakeMinReversals
	}
	return s.MinReversals
}

func (s *ShakeDetector) cooldown() time.Duration {
	if s.Cooldown <= 0 {
		return ShakeCooldown
	}
	return s.Cooldown
}

// Reset forgets the gesture in progress (not the cooldown).
func (s *ShakeDetector) Reset() {
	s.dir, s.travel, s.reversals = 0, 0, nil
}

// Feed takes one raw pointer delta. It returns true exactly once per
// completed shake: at least MinReversals direction changes, each after a
// stroke of MinStroke units or more, inside Window, and no closer than
// Cooldown to the previous locate.
func (s *ShakeDetector) Feed(dx, dy int, now time.Time) bool {
	_ = dy // only horizontal reversals count
	if dx == 0 {
		return false
	}
	dir := 1
	if dx < 0 {
		dir = -1
	}
	switch {
	case s.dir == 0:
		s.dir, s.travel = dir, abs(dx)
	case dir == s.dir:
		s.travel += abs(dx)
	default:
		if s.travel >= s.minStroke() {
			s.reversals = append(s.reversals, now)
		} else {
			// Jitter: the stroke was too short to count as a shake leg, and
			// it breaks the chain.
			s.reversals = nil
		}
		s.dir, s.travel = dir, abs(dx)
	}
	// Drop reversals that fell out of the sliding window.
	cutoff := now.Add(-s.window())
	keep := s.reversals[:0]
	for _, t := range s.reversals {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	s.reversals = keep
	if len(s.reversals) < s.minReversals() {
		return false
	}
	if !s.lastFire.IsZero() && now.Sub(s.lastFire) < s.cooldown() {
		return false
	}
	s.lastFire = now
	s.dir, s.travel, s.reversals = 0, 0, nil
	return true
}
