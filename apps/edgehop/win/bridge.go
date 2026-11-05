package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/r-thirumaran/mac-utility/apps/edgehop/core"
	"github.com/r-thirumaran/mac-utility/apps/edgehop/input"
	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
	"github.com/r-thirumaran/mac-utility/shared/go/tray"
	"github.com/r-thirumaran/mac-utility/shared/go/winutil"
)

// autostartName is the HKCU Run value name.
const autostartName = "Edgehop"

// logKeep is how many log lines edgehop.log.recent returns.
const logKeep = 300

// codeInvalidLayout is the bridge error code for a layout that breaks the
// canvas rules (docs/edgehop-protocol.md §6 `layout`).
const codeInvalidLayout = "invalid_layout"

// uiState is the edgehop.state payload: the core state plus what only the
// app layer knows (the pairing in progress, launch at login, the version).
type uiState struct {
	core.State
	Pairing       *core.PairingState `json:"pairing,omitempty"`
	LaunchAtLogin bool               `json:"launchAtLogin"`
	Version       string             `json:"version"`
	FakeInput     bool               `json:"fakeInput"`
}

// uiSettings is edgehop.settings.get/set: the core settings plus the name
// and the autostart flag.
type uiSettings struct {
	core.Settings
	Name          string `json:"name"`
	LaunchAtLogin bool   `json:"launchAtLogin"`
}

// app wires the Node, the Transport and the bridge.
type app struct {
	node    *core.Node
	tr      *core.Transport
	store   *core.PeerStore
	fake    *input.FakeDevice // nil without --fake-input
	d       *bridge.Dispatcher
	logger  *log.Logger
	version string
	exe     string
	debug   bool
	quit    func()

	mu      sync.Mutex
	logs    []core.LogEntry
	pairing *core.PairingState
	tray    *tray.Tray
}

// register installs every bridge method (docs/ARCHITECTURE.md §4, §5).
func (a *app) register() {
	d := a.d
	d.Register("app.info", a.appInfo)
	d.Register("app.openURL", a.appOpenURL)
	d.Register("app.quit", func(context.Context, json.RawMessage) (any, error) {
		go a.quit()
		return nil, nil
	})
	d.Register("app.openSettingsPane", a.appOpenSettingsPane)
	d.Register("edgehop.state", func(context.Context, json.RawMessage) (any, error) { return a.state(), nil })
	d.Register("edgehop.layout.set", a.layoutSet)
	d.Register("edgehop.pair.start", a.pairStart)
	d.Register("edgehop.pair.confirm", a.pairConfirm)
	d.Register("edgehop.pair.cancel", func(context.Context, json.RawMessage) (any, error) {
		a.tr.PairCancel()
		return nil, nil
	})
	d.Register("edgehop.unpair", a.unpair)
	d.Register("edgehop.settings.get", func(context.Context, json.RawMessage) (any, error) { return a.settings(), nil })
	d.Register("edgehop.settings.set", a.settingsSet)
	d.Register("edgehop.switchTo", a.switchTo)
	d.Register("edgehop.log.recent", func(context.Context, json.RawMessage) (any, error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		return map[string]any{"entries": append([]core.LogEntry{}, a.logs...)}, nil
	})
	if a.fake != nil {
		d.Register("edgehop.debug.emit", a.debugEmit)
		d.Register("edgehop.debug.injected", a.debugInjected)
		d.Register("edgehop.debug.displays", a.debugDisplays)
	}
}

// ---------------------------------------------------------------- state & events

func (a *app) state() uiState {
	a.mu.Lock()
	p := a.pairing
	a.mu.Unlock()
	return uiState{State: a.node.State(), Pairing: p, LaunchAtLogin: winutil.Autostart(autostartName), Version: a.version, FakeInput: a.fake != nil}
}

func (a *app) emitState() { a.d.Emit("edgehop.state", a.state()) }

// pump forwards the node's events to the UI, keeps the log ring and the
// tray in step. It returns when the node stops.
func (a *app) pump() {
	for ev := range a.node.Events() {
		a.handleEvent(ev)
	}
}

// handleEvent routes one node or transport event.
func (a *app) handleEvent(ev core.Event) {
	switch ev.Name {
	case "edgehop.state":
		a.d.Emit("edgehop.state", a.state())
		a.updateTray()
	case "edgehop.log":
		if e, ok := ev.Data.(core.LogEntry); ok {
			a.addLog(e)
			a.d.Emit("edgehop.log", e)
		}
	case "edgehop.pairing":
		if p, ok := ev.Data.(core.PairingState); ok {
			a.mu.Lock()
			a.pairing = &p
			a.mu.Unlock()
			a.d.Emit(ev.Name, p)
			a.emitState()
		}
	case "edgehop.pairCode":
		if pc, ok := ev.Data.(core.PairCode); ok {
			a.logf("info", "pairing request from %s: code shown in the window", pc.Peer.Name)
			if a.tray != nil {
				a.tray.Notify("Edgehop pairing", pc.Peer.Name+" wants to pair — open Edgehop for the code")
			}
		}
		a.d.Emit(ev.Name, ev.Data)
	default:
		a.d.Emit(ev.Name, ev.Data)
	}
}

func (a *app) addLog(e core.LogEntry) {
	a.mu.Lock()
	a.logs = append(a.logs, e)
	if len(a.logs) > logKeep {
		a.logs = a.logs[len(a.logs)-logKeep:]
	}
	a.mu.Unlock()
}

// logf logs through the file logger and the UI (level debug only with --debug).
func (a *app) logf(level, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if level == "debug" && !a.debug {
		return
	}
	if a.logger != nil {
		a.logger.Printf("%s %s", level, msg)
	}
	e := core.LogEntry{Level: level, Msg: msg, TS: time.Now().UnixMilli()}
	a.addLog(e)
	a.d.Emit("edgehop.log", e)
}

// nodeLogf is Config.Logf: the node already emits edgehop.log itself.
func (a *app) nodeLogf(level, msg string) {
	if level == "debug" && !a.debug {
		return
	}
	if a.logger != nil {
		a.logger.Printf("%s %s", level, msg)
	}
}

// ---------------------------------------------------------------- tray

// trayItems builds the app section of the tray menu: launch at login, a
// separator, then one "Switch to" entry per paired machine (disabled while
// offline or active). Keeping offline peers listed means the pool of menu
// items only changes on pair/unpair.
func (a *app) trayItems() []tray.Item {
	st := a.node.State()
	items := []tray.Item{{
		Title: "Launch at login", Checked: winutil.Autostart(autostartName),
		OnClick: func() {
			enabled := !winutil.Autostart(autostartName)
			if err := winutil.SetAutostart(autostartName, a.exe, enabled); err != nil {
				a.logf("warn", "launch at login: %v", err)
			}
			a.emitState()
			a.updateTray()
		},
	}, {Separator: true}}
	peers := append([]core.Machine(nil), st.Peers...)
	sort.Slice(peers, func(i, j int) bool { return peers[i].Name < peers[j].Name })
	for _, p := range peers {
		if !p.Paired {
			continue
		}
		id := p.ID
		items = append(items, tray.Item{
			Title:    "Switch to " + p.Name,
			Disabled: p.Status != "online" || p.Active,
			OnClick: func() {
				if err := a.node.Claim(id); err != nil {
					a.logf("warn", "switch: %v", err)
				}
			},
		})
	}
	if !st.Self.Active {
		items = append(items, tray.Item{Title: "Take control back here", OnClick: func() { _ = a.node.Claim(st.Self.ID) }})
	}
	return items
}

func (a *app) setTray(t *tray.Tray) {
	a.mu.Lock()
	a.tray = t
	a.mu.Unlock()
	a.updateTray()
}

// updateTray rewrites the app section between the shell's "Open" and "Quit".
func (a *app) updateTray() {
	a.mu.Lock()
	t := a.tray
	a.mu.Unlock()
	if t == nil {
		return
	}
	cur := t.Items()
	var head, tail []tray.Item
	if len(cur) >= 2 { // "Open Edgehop", separator
		head = cur[:2]
	}
	for i := len(cur) - 1; i >= 0; i-- {
		if cur[i].Separator {
			tail = cur[i:]
			break
		}
	}
	mine := a.trayItems()
	items := append(append(append([]tray.Item{}, head...), mine...), tail...)
	t.SetItems(items)
	st := a.node.State()
	tip := "Edgehop — " + st.Self.Name
	if !st.Self.Active {
		for _, p := range st.Peers {
			if p.Active {
				tip = "Edgehop — controlling " + p.Name
			}
		}
	}
	t.SetTooltip(tip)
}

// ---------------------------------------------------------------- handlers

func (a *app) appInfo(context.Context, json.RawMessage) (any, error) {
	st := a.node.State()
	return map[string]any{
		"name": "Edgehop", "version": a.version, "platform": core.Platform(),
		"machine": st.Self.Name, "machineId": st.Self.ID, "permissions": st.Permissions,
		"fakeInput": a.fake != nil, "goos": runtime.GOOS,
	}, nil
}

func (a *app) appOpenURL(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := bridge.Params[struct {
		URL string `json:"url"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(p.URL, "http://") && !strings.HasPrefix(p.URL, "https://") {
		return nil, bridge.Errorf(bridge.CodeInvalidParams, "only http(s) URLs")
	}
	if err := winutil.OpenURL(p.URL); err != nil {
		return nil, bridge.Errorf(bridge.CodeInternal, "%v", err)
	}
	return nil, nil
}

// settingsPanes maps the UI's pane names to ms-settings: URIs.
var settingsPanes = map[string]string{
	"":         "ms-settings:",
	"firewall": "ms-settings:windowsdefender",
	"network":  "ms-settings:network",
	"startup":  "ms-settings:startupapps",
	"mouse":    "ms-settings:mousetouchpad",
	"keyboard": "ms-settings:typing",
}

func (a *app) appOpenSettingsPane(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := bridge.Params[struct {
		Pane string `json:"pane"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" {
		return nil, bridge.Errorf(bridge.CodeNotSupported, "no settings panes on %s", runtime.GOOS)
	}
	uri, ok := settingsPanes[p.Pane]
	if !ok {
		return nil, bridge.Errorf(bridge.CodeInvalidParams, "unknown pane %q", p.Pane)
	}
	if err := winutil.OpenURL(uri); err != nil {
		return nil, bridge.Errorf(bridge.CodeInternal, "%v", err)
	}
	return nil, nil
}

// layoutSet takes a canvas ({machines:[{id,x,y}]}) or, for one release, a
// legacy tile grid ({tiles:[{id,col,row}]}); the node converts between the
// two and derives the other model. It answers with the new state, whose
// `layout` field holds the stamped layout.
func (a *app) layoutSet(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := bridge.Params[struct {
		Layout core.Layout `json:"layout"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if err := a.node.SetLayout(p.Layout); err != nil {
		return nil, bridge.Errorf(codeInvalidLayout, "%v", err)
	}
	a.emitState()
	return a.state(), nil
}

func (a *app) pairStart(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := bridge.Params[struct {
		PeerID  string `json:"peerId"`
		Address string `json:"address"`
	}](raw)
	if err != nil {
		return nil, err
	}
	p.Address = strings.TrimSpace(p.Address)
	if p.PeerID == "" && p.Address == "" {
		return nil, bridge.Errorf(bridge.CodeInvalidParams, "peerId or address required")
	}
	if p.PeerID != "" && p.PeerID == a.node.ID() {
		return nil, bridge.Errorf(bridge.CodeInvalidParams, "cannot pair with this machine")
	}
	// Record the placeholder stage before the transport runs: its own
	// connecting/waiting_code events may arrive within a millisecond and
	// must not be overwritten by a later "connecting".
	a.mu.Lock()
	prev := a.pairing
	a.pairing = &core.PairingState{Stage: "connecting", PeerID: p.PeerID, Address: p.Address}
	a.mu.Unlock()
	if err := a.tr.PairStart(p.PeerID, p.Address); err != nil {
		a.mu.Lock()
		a.pairing = prev
		a.mu.Unlock()
		return nil, bridge.Errorf(bridge.CodeInternal, "%v", err)
	}
	a.emitState()
	return nil, nil
}

func (a *app) pairConfirm(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := bridge.Params[struct {
		PeerID string `json:"peerId"`
		Code   string `json:"code"`
	}](raw)
	if err != nil {
		return nil, err
	}
	code := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, p.Code)
	if len(code) != 6 {
		return nil, bridge.Errorf(bridge.CodeInvalidParams, "the code has 6 digits")
	}
	if err := a.tr.PairConfirm(p.PeerID, code); err != nil {
		return nil, bridge.Errorf(bridge.CodeInternal, "%v", err)
	}
	return nil, nil
}

func (a *app) unpair(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := bridge.Params[struct {
		PeerID string `json:"peerId"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if p.PeerID == "" {
		return nil, bridge.Errorf(bridge.CodeInvalidParams, "peerId required")
	}
	a.tr.Unpair(p.PeerID)
	if err := a.node.Forget(p.PeerID); err != nil {
		return nil, bridge.Errorf(bridge.CodeInternal, "%v", err)
	}
	return nil, nil
}

func (a *app) settings() uiSettings {
	return uiSettings{Settings: a.node.Settings(), Name: a.node.State().Self.Name, LaunchAtLogin: winutil.Autostart(autostartName)}
}

// settingsSet merges the given keys into the settings (a partial object is fine).
func (a *app) settingsSet(_ context.Context, raw json.RawMessage) (any, error) {
	cur := a.settings()
	if err := json.Unmarshal(raw, &cur); err != nil && len(raw) > 0 {
		return nil, bridge.Errorf(bridge.CodeInvalidParams, "invalid params: %v", err)
	}
	var probe map[string]json.RawMessage
	_ = json.Unmarshal(raw, &probe)
	if _, ok := probe["name"]; ok {
		if err := a.node.SetName(cur.Name); err != nil {
			return nil, bridge.Errorf(bridge.CodeInvalidParams, "%v", err)
		}
	}
	if err := a.node.SetSettings(cur.Settings); err != nil {
		return nil, bridge.Errorf(bridge.CodeInternal, "%v", err)
	}
	if _, ok := probe["launchAtLogin"]; ok && cur.LaunchAtLogin != winutil.Autostart(autostartName) {
		if err := winutil.SetAutostart(autostartName, a.exe, cur.LaunchAtLogin); err != nil {
			if errors.Is(err, winutil.ErrNotSupported) {
				return nil, bridge.Errorf(bridge.CodeNotSupported, "launch at login is only available on Windows")
			}
			return nil, bridge.Errorf(bridge.CodeInternal, "%v", err)
		}
	}
	a.emitState()
	a.updateTray()
	return a.settings(), nil
}

func (a *app) switchTo(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := bridge.Params[struct {
		PeerID string `json:"peerId"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if p.PeerID == "" {
		p.PeerID = a.node.ID()
	}
	if err := a.node.Claim(p.PeerID); err != nil {
		return nil, bridge.Errorf(bridge.CodeInvalidParams, "%v", err)
	}
	return nil, nil
}

// ---------------------------------------------------------------- debug (fake input)

// debugEvent is the JSON form of an input.Event for edgehop.debug.emit.
type debugEvent struct {
	Kind   string `json:"kind"` // mouse_move | mouse_button | scroll | key | key_text
	X      int    `json:"x"`
	Y      int    `json:"y"`
	DX     int    `json:"dx"`
	DY     int    `json:"dy"`
	Button int    `json:"button"`
	Down   bool   `json:"down"`
	Clicks int    `json:"clicks"`
	Repeat bool   `json:"repeat"`
	Wire   int    `json:"wire"`
	Mods   int    `json:"mods"`
	Flags  int    `json:"flags"`
	Text   string `json:"text"`
}

func (e debugEvent) event() (input.Event, error) {
	ev := input.Event{X: e.X, Y: e.Y, DX: e.DX, DY: e.DY, Button: input.Button(e.Button), Down: e.Down, ClickCount: e.Clicks, Repeat: e.Repeat, Wire: e.Wire, Mods: uint8(e.Mods), Flags: uint8(e.Flags), Text: e.Text}
	switch e.Kind {
	case "mouse_move":
		ev.Kind = input.MouseMove
	case "mouse_button":
		ev.Kind = input.MouseButton
		if ev.ClickCount == 0 {
			ev.ClickCount = 1
		}
	case "scroll":
		ev.Kind = input.Scroll
	case "key":
		ev.Kind = input.Key
	case "key_text":
		ev.Kind = input.KeyText
	default:
		return ev, fmt.Errorf("unknown kind %q", e.Kind)
	}
	return ev, nil
}

func (a *app) debugEmit(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := bridge.Params[struct {
		Event  debugEvent   `json:"event"`
		Events []debugEvent `json:"events"`
	}](raw)
	if err != nil {
		return nil, err
	}
	evs := p.Events
	if p.Event.Kind != "" {
		evs = append([]debugEvent{p.Event}, evs...)
	}
	swallowed := make([]bool, 0, len(evs))
	for _, de := range evs {
		ev, err := de.event()
		if err != nil {
			return nil, bridge.Errorf(bridge.CodeInvalidParams, "%v", err)
		}
		swallowed = append(swallowed, a.fake.Emit(ev))
	}
	x, y := a.fake.CursorPos()
	return map[string]any{"swallowed": swallowed, "cursor": map[string]int{"x": x, "y": y}}, nil
}

func (a *app) debugInjected(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := bridge.Params[struct {
		Clear bool `json:"clear"`
	}](raw)
	if err != nil {
		return nil, err
	}
	calls := a.fake.Log()
	if calls == nil {
		calls = []input.Call{}
	}
	if p.Clear {
		a.fake.ClearLog()
	}
	x, y := a.fake.CursorPos()
	return map[string]any{"calls": calls, "cursor": map[string]int{"x": x, "y": y}, "locked": a.fake.Locked()}, nil
}

func (a *app) debugDisplays(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := bridge.Params[struct {
		Displays []input.Display `json:"displays"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if len(p.Displays) > 0 {
		a.fake.SetDisplays(p.Displays)
	}
	return map[string]any{"displays": a.fake.Displays()}, nil
}
