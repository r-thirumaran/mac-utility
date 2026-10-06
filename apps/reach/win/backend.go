package main

import "image"

// Display is one row of screen.displays.
type Display struct {
	Index   int     `json:"index"`
	W       int     `json:"w"`
	H       int     `json:"h"`
	Scale   float64 `json:"scale"`
	Primary bool    `json:"primary"`
	Name    string  `json:"name"`
}

// AppInfo is one installed app (apps.list).
type AppInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	IconB64 string `json:"iconB64,omitempty"`
}

// RunningApp is one row of apps.running.
type RunningApp struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	PID    int    `json:"pid"`
	Active bool   `json:"active"`
}

// WindowInfo is one row of windows.list.
type WindowInfo struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	App     string `json:"app"`
	PID     int    `json:"pid"`
	Display int    `json:"display"`
}

// Backend is what the bridge drives: the real Windows implementation
// (backend_windows.go) or the recording fake (backend_fake.go) used by the
// Linux tests and by --fake. Methods return *bridge.Error to choose a
// docs/reach-api.md error code; any other error is reported as "internal".
// Coordinates follow docs/reach-api.md §2: display is an index into
// Displays(), x/y are fractions 0..1 of that display.
type Backend interface {
	// Platform is "windows" or "linux".
	Platform() string
	// Permissions reports OS permission switches (macOS has them; Windows and
	// the fake report none).
	Permissions() map[string]bool
	// HostInfo returns the host name, the user name and the uptime in seconds.
	HostInfo() (hostname, user string, uptimeSec int64)

	Displays() ([]Display, error)
	// Capture grabs display at native resolution; stream.go scales and encodes.
	Capture(display int) (*image.RGBA, error)

	MouseMove(dx, dy float64) error
	MouseMoveTo(display int, x, y float64) error
	MouseButton(button string, down bool) error
	MouseClick(button string, count int) error
	Scroll(dx, dy float64) error

	TypeText(text string) error
	// Key presses (down && up), presses down or releases key, a DOM code
	// name of keys.go, with mods held for a press.
	Key(key string, mods []string, down, up bool) error
	Media(action string) error
	VolumeGet() (level int, muted bool, err error)
	VolumeSet(level int) error

	AppsList(icons bool) ([]AppInfo, error)
	AppsRunning() ([]RunningApp, error)
	AppLaunch(id string) error
	AppFocus(id string, pid int) error
	AppQuit(pid int, force bool) error
	WindowsList() ([]WindowInfo, error)
	WindowFocus(id string) error

	Power(action string) error
	ClipboardGet() (string, error)
	ClipboardSet(text string) error
	OpenURL(url string) error

	LaunchAtLogin() bool
	SetLaunchAtLogin(enabled bool) error
}

// Action is one recorded injection of the fake backend (reach.debug.actions).
type Action map[string]any

// actionRecorder is implemented by the fake backend; the bridge exposes its
// buffer as the test-only reach.debug.actions method.
type actionRecorder interface {
	Actions() []Action
	ClearActions()
}

// Valid parameter vocabularies of docs/reach-api.md §2.
var (
	mouseButtons = map[string]bool{"left": true, "right": true, "middle": true}
	mediaActions = map[string]bool{"playPause": true, "next": true, "previous": true, "volumeUp": true, "volumeDown": true, "mute": true}
	powerActions = map[string]bool{"lock": true, "sleep": true, "displayOff": true, "restart": true, "shutdown": true}
	modNames     = map[string]bool{"ctrl": true, "shift": true, "alt": true, "meta": true, "cmdOrCtrl": true}
)
