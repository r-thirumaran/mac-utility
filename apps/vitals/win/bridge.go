package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
	"github.com/r-thirumaran/mac-utility/shared/go/winutil"
)

// app owns the backends and serves the bridge methods of docs/ARCHITECTURE.md
// §5. Every handler is registered by register(); the method ↔ handler table
// is in apps/vitals/README.md.
type app struct {
	stats   Stats
	power   Power
	sampler *sampler
	log     *log.Logger
	debug   bool
	version string
	fake    bool // --fake-power
	// quit ends the app (app.quit, and after an elevated relaunch).
	quit func()
}

type appOptions struct {
	Stats     Stats
	Power     Power
	FakePower bool
	Log       *log.Logger
	Debug     bool
	Version   string
	Interval  time.Duration
	Quit      func()
}

func newApp(o appOptions) *app {
	a := &app{stats: o.Stats, power: o.Power, fake: o.FakePower, log: o.Log, debug: o.Debug, version: o.Version, quit: o.Quit}
	if a.log == nil {
		a.log = log.New(os.Stderr, "", log.LstdFlags)
	}
	if a.quit == nil {
		a.quit = func() {}
	}
	a.sampler = newSampler(o.Stats, o.Interval, nil)
	a.sampler.logf = a.log.Printf
	return a
}

// debugf logs only with --debug.
func (a *app) debugf(format string, args ...any) {
	if a.debug {
		a.log.Printf(format, args...)
	}
}

// platform is the app.info platform value: "windows" or "linux" (the macOS
// backend is Swift and reports "macos" itself).
func platform() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	default:
		return runtime.GOOS
	}
}

// machineInfo is app.info.machine.
type machineInfo struct {
	Hostname  string `json:"hostname"`
	CPUModel  string `json:"cpuModel"`
	Cores     int    `json:"cores"`
	MemTotal  uint64 `json:"memTotal"`
	OS        string `json:"os"`
	UptimeSec int64  `json:"uptimeSec"`
}

func machineFromHost(h HostInfo) machineInfo {
	return machineInfo{Hostname: h.Machine, CPUModel: h.CPUModel, Cores: h.Cores, MemTotal: h.MemTotal, OS: h.OS, UptimeSec: h.UptimeSec}
}

// appInfo is the app.info result.
type appInfo struct {
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	Platform    string         `json:"platform"`
	Machine     machineInfo    `json:"machine"`
	Permissions map[string]any `json:"permissions"`
	Elevated    bool           `json:"elevated"`
	FakePower   bool           `json:"fakePower,omitempty"`
}

// register wires every method onto d and points the sampler at its sinks.
func (a *app) register(d *bridge.Dispatcher) {
	a.sampler.emit = func(s Sample) { d.Emit("vitals.sample", s) }
	a.sampler.clients = d.SinkCount

	d.Register("app.info", a.handleInfo)
	d.Register("app.openURL", a.handleOpenURL)
	d.Register("app.quit", a.handleQuit)
	d.Register("app.openSettingsPane", a.handleOpenSettingsPane)

	d.Register("vitals.subscribe", a.handleSubscribe)
	d.Register("vitals.unsubscribe", a.handleUnsubscribe)
	d.Register("vitals.processes", a.handleProcesses)
	d.Register("vitals.killProcess", a.handleKillProcess)

	d.Register("power.get", a.handlePowerGet)
	d.Register("power.apply", a.handlePowerApply)
	d.Register("power.relaunchElevated", a.handleRelaunchElevated)
	d.Register("power.action", a.handlePowerAction)
	d.Register("power.schedule.get", a.handleScheduleGet)
	d.Register("power.schedule.set", a.handleScheduleSet)
	d.Register("power.keepAwake", func(ctx context.Context, raw json.RawMessage) (any, error) {
		return a.handleKeepAwake(ctx, raw, d)
	})
	d.Register("power.keepAwake.get", a.handleKeepAwakeGet)
}

// ---------------------------------------------------------------- app.*

func (a *app) handleInfo(ctx context.Context, raw json.RawMessage) (any, error) {
	return appInfo{
		Name:        appName,
		Version:     a.version,
		Platform:    platform(),
		Machine:     machineFromHost(a.stats.Info()),
		Permissions: map[string]any{},
		Elevated:    winutil.IsElevated(),
		FakePower:   a.fake,
	}, nil
}

// openableURL accepts the schemes the UI may hand to the OS.
func openableURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" {
		return "", bridge.Errorf(bridge.CodeInvalidParams, "url must be absolute")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto", "ms-settings":
		return u.String(), nil
	}
	return "", bridge.Errorf(bridge.CodeInvalidParams, "unsupported url scheme %q", u.Scheme)
}

func (a *app) handleOpenURL(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := bridge.Params[struct {
		URL string `json:"url"`
	}](raw)
	if err != nil {
		return nil, err
	}
	u, err := openableURL(p.URL)
	if err != nil {
		return nil, err
	}
	if err := winutil.OpenURL(u); err != nil {
		return nil, bridge.Errorf(bridge.CodeInternal, "open url: %v", err)
	}
	return map[string]any{}, nil
}

func (a *app) handleQuit(ctx context.Context, raw json.RawMessage) (any, error) {
	a.log.Printf("quit requested from the UI")
	go func() {
		time.Sleep(150 * time.Millisecond) // let the response reach the page
		a.quit()
	}()
	return map[string]any{}, nil
}

func (a *app) handleOpenSettingsPane(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := bridge.Params[struct {
		Pane string `json:"pane"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if err := openSettingsPane(p.Pane); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}

// ------------------------------------------------------------- vitals.*

func (a *app) handleSubscribe(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := bridge.Params[struct {
		IntervalMs int `json:"intervalMs"`
	}](raw)
	if err != nil {
		return nil, err
	}
	iv := a.sampler.Subscribe(time.Duration(p.IntervalMs) * time.Millisecond)
	a.debugf("subscribe: %d subscribers, interval %s", a.sampler.Subscribers(), iv)
	return map[string]any{"intervalMs": iv.Milliseconds(), "subscribers": a.sampler.Subscribers()}, nil
}

func (a *app) handleUnsubscribe(ctx context.Context, raw json.RawMessage) (any, error) {
	n := a.sampler.Unsubscribe()
	a.debugf("unsubscribe: %d subscribers left", n)
	return map[string]any{"subscribers": n}, nil
}

func (a *app) handleProcesses(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := bridge.Params[struct {
		SortBy string `json:"sortBy"`
		Limit  int    `json:"limit"`
	}](raw)
	if err != nil {
		return nil, err
	}
	list, err := a.stats.Processes()
	if err != nil {
		return nil, bridge.Errorf(bridge.CodeInternal, "processes: %v", err)
	}
	sortProcesses(list, p.SortBy)
	return limitProcesses(list, p.Limit), nil
}

func (a *app) handleKillProcess(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := bridge.Params[struct {
		PID   int  `json:"pid"`
		Force bool `json:"force"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if p.PID <= 0 {
		return nil, bridge.Errorf(bridge.CodeInvalidParams, "pid must be positive")
	}
	if err := a.stats.Kill(p.PID, p.Force); err != nil {
		a.log.Printf("kill %d: %v", p.PID, err)
		if errors.Is(err, os.ErrPermission) {
			return nil, bridge.Errorf(bridge.CodeNotAuthorized, "cannot end process %d: access denied", p.PID)
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, bridge.Errorf(bridge.CodeInvalidParams, "no process with pid %d", p.PID)
		}
		return nil, bridge.Errorf(bridge.CodeInternal, "%v", err)
	}
	a.log.Printf("ended process %d (force=%v)", p.PID, p.Force)
	return map[string]any{"pid": p.PID}, nil
}

// -------------------------------------------------------------- power.*

// passError keeps a *bridge.Error's code and wraps anything else as internal.
func passError(err error, what string) error {
	var be *bridge.Error
	if errors.As(err, &be) {
		return be
	}
	return bridge.Errorf(bridge.CodeInternal, "%s: %v", what, err)
}

func (a *app) handlePowerGet(ctx context.Context, raw json.RawMessage) (any, error) {
	m, err := a.power.Get()
	if err != nil {
		return nil, passError(err, "power.get")
	}
	m.Elevated = winutil.IsElevated()
	if m.Supported.LidClose == nil {
		m.Supported.LidClose = []string{}
	}
	if m.Supported.Actions == nil {
		m.Supported.Actions = []string{}
	}
	if m.Supported.ScheduleTypes == nil {
		m.Supported.ScheduleTypes = []string{}
	}
	return m, nil
}

func (a *app) handlePowerApply(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := bridge.Params[struct {
		Changes map[string]any `json:"changes"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if len(p.Changes) == 0 {
		return nil, bridge.Errorf(bridge.CodeInvalidParams, "changes must not be empty")
	}
	res, err := a.power.Apply(p.Changes)
	if err != nil {
		a.log.Printf("power.apply: %v", err)
		return nil, passError(err, "power.apply")
	}
	if res.Applied == nil {
		res.Applied = []string{}
	}
	if res.Failed == nil {
		res.Failed = []ApplyFailure{}
	}
	a.log.Printf("power.apply: applied %v, failed %d", res.Applied, len(res.Failed))
	return res, nil
}

func (a *app) handleRelaunchElevated(ctx context.Context, raw json.RawMessage) (any, error) {
	if runtime.GOOS != "windows" {
		return nil, bridge.Errorf(bridge.CodeNotSupported, "elevation is only available on Windows")
	}
	if winutil.IsElevated() {
		return map[string]any{"alreadyElevated": true}, nil
	}
	// The new copy waits briefly for the single-instance mutex (main.go), so
	// this process only has to exit after the prompt was accepted.
	if err := winutil.RelaunchElevated(); err != nil {
		a.log.Printf("relaunch elevated: %v", err)
		return nil, bridge.Errorf(bridge.CodeNotAuthorized, "elevation was declined: %v", err)
	}
	a.log.Printf("relaunched elevated; exiting")
	go func() {
		time.Sleep(300 * time.Millisecond)
		a.quit()
	}()
	return map[string]any{"relaunched": true}, nil
}

func (a *app) handlePowerAction(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := bridge.Params[struct {
		Action string `json:"action"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if !contains(powerActions, p.Action) {
		return nil, bridge.Errorf(bridge.CodeInvalidParams, "action must be one of %s", strings.Join(powerActions, ", "))
	}
	a.log.Printf("power.action %s", p.Action)
	if err := a.power.Action(p.Action); err != nil {
		return nil, passError(err, "power.action")
	}
	return map[string]any{"action": p.Action}, nil
}

func (a *app) handleScheduleGet(ctx context.Context, raw json.RawMessage) (any, error) {
	items, err := a.power.ScheduleGet()
	if err != nil {
		return nil, passError(err, "power.schedule.get")
	}
	if items == nil {
		items = []ScheduleItem{}
	}
	return map[string]any{"items": items}, nil
}

func (a *app) handleScheduleSet(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := bridge.Params[struct {
		Items []ScheduleItem `json:"items"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if p.Items == nil {
		p.Items = []ScheduleItem{}
	}
	items, err := a.power.ScheduleSet(p.Items)
	if err != nil {
		return nil, passError(err, "power.schedule.set")
	}
	if items == nil {
		items = []ScheduleItem{}
	}
	a.log.Printf("power.schedule.set: %d items", len(items))
	return map[string]any{"items": items}, nil
}

func (a *app) handleKeepAwake(ctx context.Context, raw json.RawMessage, d *bridge.Dispatcher) (any, error) {
	p, err := bridge.Params[struct {
		Enabled bool   `json:"enabled"`
		Mode    string `json:"mode"`
	}](raw)
	if err != nil {
		return nil, err
	}
	st, err := a.power.KeepAwake(p.Enabled, p.Mode)
	if err != nil {
		return nil, passError(err, "power.keepAwake")
	}
	a.log.Printf("keep awake: enabled=%v mode=%s", st.Enabled, st.Mode)
	d.Emit("power.keepAwake", st)
	return st, nil
}

func (a *app) handleKeepAwakeGet(ctx context.Context, raw json.RawMessage) (any, error) {
	return a.power.AwakeState(), nil
}
