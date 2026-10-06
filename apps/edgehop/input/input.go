// Package input is Edgehop's platform abstraction for capturing physical
// mouse/keyboard events and injecting synthetic ones. The core state machine
// (apps/edgehop/core) only ever talks to the two interfaces below; fake.go
// implements them in memory for tests and the Linux build, windows.go with
// low-level hooks and SendInput.
//
// Coordinates are desktop-union pixels, origin top-left, y down, exactly as
// the `displays` field of docs/edgehop-protocol.md §6 describes them.
package input

import "errors"

// ErrNotSupported is returned by NewNative on platforms without a backend.
var ErrNotSupported = errors.New("input: no native backend on this platform")

// Kind classifies an Event.
type Kind int

// Event kinds.
const (
	MouseMove   Kind = iota // X, Y absolute after the move; DX, DY the raw delta
	MouseButton             // Button, Down, ClickCount
	Scroll                  // DX, DY (120 = one wheel notch, or pixels when !Discrete), Flags
	Key                     // Wire (HID usage, docs/edgehop-protocol.md §5), Down, Mods
	KeyText                 // Text typed verbatim (dead keys, emoji)
)

// String names the kind for logs.
func (k Kind) String() string {
	switch k {
	case MouseMove:
		return "mouse_move"
	case MouseButton:
		return "mouse_button"
	case Scroll:
		return "scroll"
	case Key:
		return "key"
	case KeyText:
		return "key_text"
	}
	return "unknown"
}

// Button is a mouse button as numbered on the wire (§5 mouse_button).
type Button uint8

// Wire button numbers.
const (
	ButtonLeft    Button = 0
	ButtonRight   Button = 1
	ButtonMiddle  Button = 2
	ButtonBack    Button = 3
	ButtonForward Button = 4
)

// Scroll flags (§5 scroll).
const (
	ScrollDiscrete uint8 = 1 << 0 // one wheel notch = 120
	ScrollMomentum uint8 = 1 << 1 // trackpad momentum phase
)

// Event is one captured (or injected) input event.
type Event struct {
	Kind Kind
	// Injected is true when the event was produced by this process's own
	// Injector (or any synthetic source the OS flags), never by hardware.
	Injected bool
	// X, Y is the absolute cursor position after the event (pointer events).
	X, Y int
	// DX, DY is the relative delta: raw motion for MouseMove, wheel units or
	// pixels for Scroll.
	DX, DY int
	// Button and Down describe MouseButton events; Down is also the key state
	// for Key events.
	Button     Button
	Down       bool
	ClickCount int
	// Repeat marks an auto-repeat key down (wire state 2).
	Repeat bool
	// Wire is the HID usage of a Key event (shared/keymap wire value).
	Wire int
	// Mods is the HID modifier byte at the time of the event (bit0 LCtrl …
	// bit7 RGUI).
	Mods uint8
	// Flags carries the Scroll flags.
	Flags uint8
	// Text is the KeyText payload.
	Text string
}

// Display is one monitor in desktop-union coordinates.
type Display struct {
	ID      string  `json:"id"`
	X       int     `json:"x"`
	Y       int     `json:"y"`
	W       int     `json:"w"`
	H       int     `json:"h"`
	Scale   float64 `json:"scale"`
	Primary bool    `json:"primary"`
}

// Capturer observes the local input devices and the display configuration.
type Capturer interface {
	// Start installs the capture and calls fn for every event, physical or
	// injected. fn returns true to swallow the event (the OS never sees it).
	// fn must return quickly: on Windows it runs on the hook thread.
	Start(fn func(Event) (swallow bool)) error
	// Stop removes the capture.
	Stop()
	// LockCursor parks the cursor at the centre of the primary display and
	// hides it (lock=true) or restores it (lock=false). Best effort.
	LockCursor(lock bool)
	// Displays returns the current monitors.
	Displays() []Display
	// OnDisplaysChanged registers a callback for monitor changes.
	OnDisplaysChanged(fn func())
}

// Injector synthesises input on the local machine.
type Injector interface {
	MoveRelative(dx, dy int)
	MoveTo(x, y int)
	Button(b Button, down bool, clicks int)
	Scroll(dx, dy int, discrete bool)
	// Key presses or releases the key with the given HID wire usage; mods is
	// the sender's modifier byte, used only for platform-specific fix-ups.
	Key(wire int, down bool, mods uint8)
	Text(s string)
	CursorPos() (int, int)
}

// Device is a Capturer that is also an Injector (every platform backend).
type Device interface {
	Capturer
	Injector
}
