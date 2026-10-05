package input

import (
	"sync"
)

// Call is one recorded Injector/Capturer call of a FakeDevice.
type Call struct {
	Op   string `json:"op"`
	Args []int  `json:"args,omitempty"`
	Text string `json:"text,omitempty"`
}

// FakeDevice is an in-memory Capturer + Injector: it keeps a cursor, a
// programmable display list and a log of every injected call, and lets a
// test (or the --fake-input bridge) feed physical events with Emit. It is
// the Linux backend and the backend of the core simulations.
//
// Semantics mirror the Windows backend: a physical MouseMove carries a delta;
// the cursor moves by that delta (clamped to the desktop union) unless the
// capture callback swallows the event, in which case the cursor stays where
// it is — exactly what returning 1 from a WH_MOUSE_LL hook does. Injected
// moves always move the cursor and are reported back through the callback
// with Injected=true, as the hook would see them.
type FakeDevice struct {
	mu        sync.Mutex
	fn        func(Event) bool
	x, y      int
	displays  []Display
	locked    bool
	log       []Call
	onDisplay []func()
	// MaxLog caps the recorded calls (0 = 10000).
	MaxLog int
}

// NewFake returns a device showing displays (default: one 1920×1080 primary
// at the origin) with the cursor at the centre of the primary display.
func NewFake(displays ...Display) *FakeDevice {
	if len(displays) == 0 {
		displays = []Display{{ID: "fake-1", X: 0, Y: 0, W: 1920, H: 1080, Scale: 1, Primary: true}}
	}
	d := &FakeDevice{displays: append([]Display(nil), displays...)}
	d.x, d.y = d.centre()
	return d
}

func (d *FakeDevice) centre() (int, int) {
	for _, s := range d.displays {
		if s.Primary {
			return s.X + s.W/2, s.Y + s.H/2
		}
	}
	if len(d.displays) > 0 {
		s := d.displays[0]
		return s.X + s.W/2, s.Y + s.H/2
	}
	return 0, 0
}

// clamp keeps a point inside the bounding box of the displays, like the OS
// does for the hardware cursor.
func (d *FakeDevice) clamp(x, y int) (int, int) {
	if len(d.displays) == 0 {
		return x, y
	}
	minX, minY := d.displays[0].X, d.displays[0].Y
	maxX, maxY := d.displays[0].X+d.displays[0].W, d.displays[0].Y+d.displays[0].H
	for _, s := range d.displays[1:] {
		if s.X < minX {
			minX = s.X
		}
		if s.Y < minY {
			minY = s.Y
		}
		if s.X+s.W > maxX {
			maxX = s.X + s.W
		}
		if s.Y+s.H > maxY {
			maxY = s.Y + s.H
		}
	}
	if x < minX {
		x = minX
	}
	if x > maxX-1 {
		x = maxX - 1
	}
	if y < minY {
		y = minY
	}
	if y > maxY-1 {
		y = maxY - 1
	}
	return x, y
}

func (d *FakeDevice) record(c Call) {
	max := d.MaxLog
	if max <= 0 {
		max = 10000
	}
	if len(d.log) >= max {
		d.log = d.log[len(d.log)-max+1:]
	}
	d.log = append(d.log, c)
}

// ---------------------------------------------------------------- Capturer

// Start installs fn as the capture callback.
func (d *FakeDevice) Start(fn func(Event) bool) error {
	d.mu.Lock()
	d.fn = fn
	d.mu.Unlock()
	return nil
}

// Stop removes the callback.
func (d *FakeDevice) Stop() {
	d.mu.Lock()
	d.fn = nil
	d.mu.Unlock()
}

// LockCursor records the lock and parks the cursor at the primary centre.
func (d *FakeDevice) LockCursor(lock bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.locked = lock
	v := 0
	if lock {
		v = 1
		d.x, d.y = d.centre()
	}
	d.record(Call{Op: "LockCursor", Args: []int{v}})
}

// Locked reports the last LockCursor value.
func (d *FakeDevice) Locked() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.locked
}

// Displays returns a copy of the display list.
func (d *FakeDevice) Displays() []Display {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Display(nil), d.displays...)
}

// OnDisplaysChanged registers fn to run after SetDisplays.
func (d *FakeDevice) OnDisplaysChanged(fn func()) {
	d.mu.Lock()
	d.onDisplay = append(d.onDisplay, fn)
	d.mu.Unlock()
}

// SetDisplays replaces the monitors and notifies listeners.
func (d *FakeDevice) SetDisplays(displays []Display) {
	d.mu.Lock()
	d.displays = append([]Display(nil), displays...)
	d.x, d.y = d.clamp(d.x, d.y)
	fns := append([]func(){}, d.onDisplay...)
	d.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// ---------------------------------------------------------------- Injector

func (d *FakeDevice) deliver(ev Event) bool {
	d.mu.Lock()
	fn := d.fn
	d.mu.Unlock()
	if fn == nil {
		return false
	}
	return fn(ev)
}

// MoveRelative moves the cursor by (dx, dy), clamped, and reports the
// injected move.
func (d *FakeDevice) MoveRelative(dx, dy int) {
	d.mu.Lock()
	d.x, d.y = d.clamp(d.x+dx, d.y+dy)
	x, y := d.x, d.y
	d.record(Call{Op: "MoveRelative", Args: []int{dx, dy}})
	d.mu.Unlock()
	d.deliver(Event{Kind: MouseMove, Injected: true, X: x, Y: y, DX: dx, DY: dy})
}

// MoveTo warps the cursor to (x, y), clamped, and reports the injected move.
func (d *FakeDevice) MoveTo(x, y int) {
	d.mu.Lock()
	ox, oy := d.x, d.y
	d.x, d.y = d.clamp(x, y)
	nx, ny := d.x, d.y
	d.record(Call{Op: "MoveTo", Args: []int{x, y}})
	d.mu.Unlock()
	d.deliver(Event{Kind: MouseMove, Injected: true, X: nx, Y: ny, DX: nx - ox, DY: ny - oy})
}

// Button records a button change and reports it as an injected event.
func (d *FakeDevice) Button(b Button, down bool, clicks int) {
	d.mu.Lock()
	x, y := d.x, d.y
	d.record(Call{Op: "Button", Args: []int{int(b), boolInt(down), clicks}})
	d.mu.Unlock()
	d.deliver(Event{Kind: MouseButton, Injected: true, X: x, Y: y, Button: b, Down: down, ClickCount: clicks})
}

// Scroll records a scroll and reports it as an injected event.
func (d *FakeDevice) Scroll(dx, dy int, discrete bool) {
	d.mu.Lock()
	x, y := d.x, d.y
	d.record(Call{Op: "Scroll", Args: []int{dx, dy, boolInt(discrete)}})
	d.mu.Unlock()
	var flags uint8
	if discrete {
		flags = ScrollDiscrete
	}
	d.deliver(Event{Kind: Scroll, Injected: true, X: x, Y: y, DX: dx, DY: dy, Flags: flags})
}

// Key records a key change and reports it as an injected event.
func (d *FakeDevice) Key(wire int, down bool, mods uint8) {
	d.mu.Lock()
	d.record(Call{Op: "Key", Args: []int{wire, boolInt(down), int(mods)}})
	d.mu.Unlock()
	d.deliver(Event{Kind: Key, Injected: true, Wire: wire, Down: down, Mods: mods})
}

// Text records typed text and reports it as an injected event.
func (d *FakeDevice) Text(s string) {
	d.mu.Lock()
	d.record(Call{Op: "Text", Text: s})
	d.mu.Unlock()
	d.deliver(Event{Kind: KeyText, Injected: true, Text: s})
}

// CursorPos returns the cursor.
func (d *FakeDevice) CursorPos() (int, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.x, d.y
}

// SetCursor warps the cursor without reporting an event (test setup).
func (d *FakeDevice) SetCursor(x, y int) {
	d.mu.Lock()
	d.x, d.y = d.clamp(x, y)
	d.mu.Unlock()
}

// ---------------------------------------------------------------- physical

// Emit feeds a physical event. For MouseMove the cursor advances by
// (DX, DY) (or warps to X, Y when both DX and DY are zero and X or Y is set)
// unless the callback swallows the event; the delivered event carries the
// resulting absolute position. Returns the callback's swallow decision.
func (d *FakeDevice) Emit(ev Event) bool {
	ev.Injected = false
	d.mu.Lock()
	if ev.Kind == MouseMove {
		var nx, ny int
		if ev.DX == 0 && ev.DY == 0 && (ev.X != 0 || ev.Y != 0) {
			nx, ny = d.clamp(ev.X, ev.Y)
			ev.DX, ev.DY = nx-d.x, ny-d.y
		} else {
			nx, ny = d.clamp(d.x+ev.DX, d.y+ev.DY)
		}
		ev.X, ev.Y = nx, ny
		fn := d.fn
		d.mu.Unlock()
		swallow := false
		if fn != nil {
			swallow = fn(ev)
		}
		d.mu.Lock()
		if !swallow && !d.locked {
			d.x, d.y = nx, ny
		}
		d.mu.Unlock()
		return swallow
	}
	if ev.Kind == MouseButton || ev.Kind == Scroll {
		ev.X, ev.Y = d.x, d.y
	}
	fn := d.fn
	d.mu.Unlock()
	if fn == nil {
		return false
	}
	return fn(ev)
}

// Log returns a copy of the recorded calls.
func (d *FakeDevice) Log() []Call {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Call(nil), d.log...)
}

// ClearLog forgets the recorded calls.
func (d *FakeDevice) ClearLog() {
	d.mu.Lock()
	d.log = nil
	d.mu.Unlock()
}

// Count returns how many recorded calls have the given op.
func (d *FakeDevice) Count(op string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, c := range d.log {
		if c.Op == op {
			n++
		}
	}
	return n
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
