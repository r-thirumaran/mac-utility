package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
	"github.com/r-thirumaran/mac-utility/shared/go/httpserve"
	"github.com/r-thirumaran/mac-utility/shared/go/tray"
	"github.com/r-thirumaran/mac-utility/shared/go/webhost"
	"github.com/r-thirumaran/mac-utility/shared/go/winutil"
)

// caps advertised by /api/info.
var agentCaps = []string{"screen", "apps", "media", "power", "clipboard"}

// agent ties the config, the backend, pairing, discovery and the two
// dispatchers together. The LAN server (main.go) serves phones and the local
// settings page; webhost's own loopback server serves the WebView2 window on
// Windows with the same methods.
type agent struct {
	id         string
	version    string
	cfg        *store
	backend    Backend
	pair       *pairer
	disc       *discovery
	localToken string
	port       int
	fake       bool
	debug      bool
	noUI       bool
	logger     *log.Logger
	start      time.Time
	quit       func()

	dLAN, dLocal *bridge.Dispatcher

	trayMu sync.Mutex
	tray   *tray.Tray

	connsMu sync.Mutex
	conns   map[context.Context]*connInfo
}

// connInfo is one WebSocket on the LAN server: the local settings page
// (local token) or a paired phone (pairing token).
type connInfo struct {
	c      *httpserve.Conn
	local  bool
	token  string
	device string

	mu     sync.Mutex
	stream *streamer
}

func newAgent(cfg *store, b Backend, localToken, version string, logger *log.Logger, fake, debug bool) *agent {
	snap := cfg.Snapshot()
	a := &agent{
		id: snap.AgentID, version: version, cfg: cfg, backend: b, pair: newPairer(),
		disc: newDiscovery(snap.AgentID), localToken: localToken, fake: fake, debug: debug,
		logger: logger, start: time.Now(), conns: map[context.Context]*connInfo{},
		dLAN: bridge.New(), dLocal: bridge.New(),
	}
	a.registerMethods(a.dLAN)
	a.registerMethods(a.dLocal)
	return a
}

func (a *agent) logf(format string, args ...any) {
	if a.logger != nil {
		a.logger.Printf(format, args...)
	}
}

func (a *agent) debugf(format string, args ...any) {
	if a.debug {
		a.logf(format, args...)
	}
}

// ---------------------------------------------------------------- info

func (a *agent) name() string { return a.cfg.Snapshot().Name }

// infoPublic is /api/info.
func (a *agent) infoPublic() map[string]any {
	return map[string]any{
		"id": a.id, "name": a.name(), "platform": a.backend.Platform(), "version": a.version,
		"pairingRequired": true, "caps": agentCaps,
	}
}

// infoFull is the `info` method and event.
func (a *agent) infoFull() map[string]any {
	m := a.infoPublic()
	host, user, up := a.backend.HostInfo()
	m["hostname"], m["user"], m["uptimeSec"] = host, user, up
	m["permissions"] = a.backend.Permissions()
	m["port"] = a.port
	return m
}

// ---------------------------------------------------------------- auth

// tokenOf returns the token of a request: ?token= or the mu_token cookie.
func tokenOf(r *http.Request) string {
	if q := r.URL.Query().Get("token"); q != "" {
		return q
	}
	if ck, err := r.Cookie(webhost.CookieName); err == nil {
		return ck.Value
	}
	return ""
}

func (a *agent) isLocalToken(tok string) bool {
	return tok != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(a.localToken)) == 1
}

// authorize is the LAN server's Auth: the PWA and /api/ are open; /ws
// needs the local token or a pairing token (401 otherwise).
func (a *agent) authorize(r *http.Request) bool {
	if r.URL.Path != "/ws" {
		return true
	}
	tok := tokenOf(r)
	if a.isLocalToken(tok) {
		return true
	}
	_, ok := a.cfg.FindToken(tok)
	return ok
}

// cookieSetter mirrors webhost: a GET / with the local token sets the
// mu_token cookie so the settings page survives a reload without ?token=.
func (a *agent) cookieSetter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" && a.isLocalToken(r.URL.Query().Get("token")) {
			if ck, err := r.Cookie(webhost.CookieName); err != nil || ck.Value != a.localToken {
				http.SetCookie(w, &http.Cookie{Name: webhost.CookieName, Value: a.localToken, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------- connections

// onWS classifies an accepted socket, registers it and sends the `info` event.
func (a *agent) onWS(c *httpserve.Conn) {
	tok := tokenOf(c.Request())
	ci := &connInfo{c: c, token: tok}
	if a.isLocalToken(tok) {
		ci.local = true
	} else if p, ok := a.cfg.FindToken(tok); ok {
		ci.device = p.Device
		a.cfg.Touch(tok)
	} else {
		c.Close()
		return
	}
	ctx := c.Context()
	a.connsMu.Lock()
	a.conns[ctx] = ci
	a.connsMu.Unlock()
	if ci.local {
		a.debugf("ws: local settings page connected")
	} else {
		a.logf("ws: %q connected from %s", ci.device, c.Request().RemoteAddr)
	}
	_ = c.SendJSON(map[string]any{"event": "info", "data": a.infoFull()})
	go func() {
		<-ctx.Done()
		a.removeConn(ctx)
	}()
}

func (a *agent) removeConn(ctx context.Context) {
	a.connsMu.Lock()
	ci := a.conns[ctx]
	delete(a.conns, ctx)
	a.connsMu.Unlock()
	if ci == nil {
		return
	}
	ci.stopStream()
	if !ci.local {
		a.logf("ws: %q disconnected", ci.device)
	}
}

func (a *agent) connOf(ctx context.Context) *connInfo {
	a.connsMu.Lock()
	defer a.connsMu.Unlock()
	return a.conns[ctx]
}

// isLocal reports whether a request may use the reach.* methods: a local
// socket on the LAN server, or any socket of webhost's loopback server
// (which only the local token opens, so it is never in the table).
func (a *agent) isLocal(ctx context.Context) bool {
	ci := a.connOf(ctx)
	return ci == nil || ci.local
}

func (a *agent) phoneConnCount() int {
	a.connsMu.Lock()
	defer a.connsMu.Unlock()
	n := 0
	for _, ci := range a.conns {
		if !ci.local {
			n++
		}
	}
	return n
}

// emitLocal pushes an event to the settings page(s) only: webhost's
// loopback sockets through dLocal and the local sockets of the LAN server.
func (a *agent) emitLocal(event string, data any) {
	a.dLocal.Emit(event, data)
	frame := map[string]any{"event": event, "data": data}
	a.connsMu.Lock()
	var locals []*connInfo
	for _, ci := range a.conns {
		if ci.local {
			locals = append(locals, ci)
		}
	}
	a.connsMu.Unlock()
	for _, ci := range locals {
		_ = ci.c.SendJSON(frame)
	}
}

// closeConnsWithToken drops the sockets of revoked pairings.
func (a *agent) closeConnsWithToken(tokens []string) {
	a.connsMu.Lock()
	var victims []*connInfo
	for _, ci := range a.conns {
		for _, t := range tokens {
			if ci.token == t {
				victims = append(victims, ci)
			}
		}
	}
	a.connsMu.Unlock()
	for _, ci := range victims {
		ci.c.Close()
	}
}

func (ci *connInfo) stopStream() {
	ci.mu.Lock()
	s := ci.stream
	ci.stream = nil
	ci.mu.Unlock()
	if s != nil {
		s.stop()
	}
}

// showCode shows a pairing code on the laptop: tray notification plus the
// reach.pairCode event for the settings page.
func (a *agent) showCode(code string, expires time.Time, device string) {
	a.trayMu.Lock()
	t := a.tray
	a.trayMu.Unlock()
	if t != nil {
		body := "Pairing code " + code + " — valid for 2 minutes"
		if device != "" {
			body = device + ": pairing code " + code + " — valid for 2 minutes"
		}
		t.Notify("Reach", body)
	} else if a.noUI {
		// Headless runs have no tray or window to show the code in.
		fmt.Fprintf(os.Stderr, "reach: pairing code %s (valid for 2 minutes)\n", code)
	}
	a.emitLocal("reach.pairCode", map[string]any{"code": code, "expiresAt": expires.UTC().Format(time.RFC3339), "device": device})
}

// setTray remembers the running tray (webhost.Config.OnTray).
func (a *agent) setTray(t *tray.Tray) {
	a.trayMu.Lock()
	a.tray = t
	a.trayMu.Unlock()
}

// trayItems are the app's menu entries between "Open Reach" and "Quit".
func (a *agent) trayItems() []tray.Item {
	return []tray.Item{
		{Title: "Show pairing code", OnClick: func() {
			code, expires, ok, err := a.pair.request("")
			if err != nil || !ok {
				return
			}
			a.showCode(code, expires, "")
		}},
		{Title: "Launch at login", Checked: a.cfg.Snapshot().LaunchAtLogin, OnClick: func() {
			cur := a.cfg.Snapshot().LaunchAtLogin
			if err := a.setLaunchAtLogin(!cur); err != nil {
				a.logf("launch at login: %v", err)
			}
		}},
	}
}

func (a *agent) setLaunchAtLogin(enabled bool) error {
	if err := a.backend.SetLaunchAtLogin(enabled); err != nil {
		if errors.Is(err, winutil.ErrNotSupported) {
			return bridge.Errorf(bridge.CodeNotSupported, "launch at login is not available on this platform")
		}
		return err
	}
	if err := a.cfg.Update(func(c *Config) { c.LaunchAtLogin = enabled }); err != nil {
		return err
	}
	a.trayMu.Lock()
	t := a.tray
	a.trayMu.Unlock()
	if t != nil {
		items := t.Items()
		for i := range items {
			if items[i].Title == "Launch at login" {
				items[i].Checked = enabled
			}
		}
		t.SetItems(items)
	}
	return nil
}

// runningWatcher pushes apps.running to phones when the list changes (≤ 1/s).
func (a *agent) runningWatcher(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var last []byte
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if a.phoneConnCount() == 0 {
			last = nil
			continue
		}
		list, err := a.backend.AppsRunning()
		if err != nil {
			continue
		}
		b, err := json.Marshal(list)
		if err != nil || bytes.Equal(b, last) {
			continue
		}
		last = b
		a.dLAN.Emit("apps.running", list)
	}
}

// ---------------------------------------------------------------- methods

type pointParams struct {
	Display *int     `json:"display"`
	X       *float64 `json:"x"`
	Y       *float64 `json:"y"`
}

// registerMethods installs every method of docs/reach-api.md §2 and §5.
func (a *agent) registerMethods(d *bridge.Dispatcher) {
	b := a.backend
	reg := func(name string, h bridge.Handler) {
		d.Register(name, func(ctx context.Context, raw json.RawMessage) (any, error) {
			if a.debug {
				t0 := time.Now()
				res, err := h(ctx, raw)
				a.logf("%s → %v (%s)", name, errString(err), time.Since(t0).Round(time.Microsecond))
				return res, err
			}
			return h(ctx, raw)
		})
	}
	local := func(name string, h bridge.Handler) {
		reg(name, func(ctx context.Context, raw json.RawMessage) (any, error) {
			if !a.isLocal(ctx) {
				return nil, bridge.Errorf(bridge.CodeNotAuthorized, "%s is only available to the agent's own settings page", name)
			}
			return h(ctx, raw)
		})
	}

	reg("info", func(ctx context.Context, raw json.RawMessage) (any, error) { return a.infoFull(), nil })
	reg("ping", func(ctx context.Context, raw json.RawMessage) (any, error) {
		return map[string]any{"t": time.Now().UnixMilli()}, nil
	})

	// screen
	reg("screen.displays", func(ctx context.Context, raw json.RawMessage) (any, error) { return b.Displays() })
	reg("screen.frame", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[streamParams](raw)
		if err != nil {
			return nil, err
		}
		p = p.normalize()
		img, err := b.Capture(p.Display)
		if err != nil {
			return nil, err
		}
		frame, w, h, err := encodeFrame(img, p)
		if err != nil {
			return nil, err
		}
		return map[string]any{"display": p.Display, "w": w, "h": h, "jpegB64": base64.StdEncoding.EncodeToString(frame[frameHeader:])}, nil
	})
	reg("screen.stream.start", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[streamParams](raw)
		if err != nil {
			return nil, err
		}
		p = p.normalize()
		ci := a.connOf(ctx)
		if ci == nil {
			return nil, bridge.Errorf(bridge.CodeNotSupported, "screen streams need the WebSocket transport")
		}
		if _, err := b.Capture(p.Display); err != nil {
			return nil, err // report needs_permission / invalid display now rather than silently
		}
		ci.stopStream()
		s := startStream(ci.c.Context(), b, p, ci.c.SendBinary, a.logf)
		ci.mu.Lock()
		ci.stream = s
		ci.mu.Unlock()
		a.debugf("stream: started display %d ≤%dpx q%.1f %dfps", p.Display, p.MaxWidth, p.Quality, p.FPS)
		return map[string]any{}, nil
	})
	reg("screen.stream.stop", func(ctx context.Context, raw json.RawMessage) (any, error) {
		if ci := a.connOf(ctx); ci != nil {
			ci.stopStream()
		}
		return map[string]any{}, nil
	})

	// mouse
	reg("mouse.move", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[struct{ DX, DY float64 }](raw)
		if err != nil {
			return nil, err
		}
		return nil, b.MouseMove(p.DX, p.DY)
	})
	reg("mouse.moveTo", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[pointParams](raw)
		if err != nil {
			return nil, err
		}
		if p.X == nil || p.Y == nil {
			return nil, bridge.Errorf(bridge.CodeInvalidParams, "x and y are required")
		}
		return nil, b.MouseMoveTo(displayOf(p), clamp01(*p.X), clamp01(*p.Y))
	})
	reg("mouse.click", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[struct {
			Button string `json:"button"`
			Count  int    `json:"count"`
			pointParams
		}](raw)
		if err != nil {
			return nil, err
		}
		button, err := buttonOf(p.Button)
		if err != nil {
			return nil, err
		}
		count := p.Count
		if count == 0 {
			count = 1
		}
		if count != 1 && count != 2 {
			return nil, bridge.Errorf(bridge.CodeInvalidParams, "count must be 1 or 2")
		}
		if p.X != nil && p.Y != nil {
			if err := b.MouseMoveTo(displayOf(p.pointParams), clamp01(*p.X), clamp01(*p.Y)); err != nil {
				return nil, err
			}
		}
		return nil, b.MouseClick(button, count)
	})
	mouseButton := func(down bool) bridge.Handler {
		return func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, err := bridge.Params[struct {
				Button string `json:"button"`
			}](raw)
			if err != nil {
				return nil, err
			}
			button, err := buttonOf(p.Button)
			if err != nil {
				return nil, err
			}
			return nil, b.MouseButton(button, down)
		}
	}
	reg("mouse.down", mouseButton(true))
	reg("mouse.up", mouseButton(false))
	reg("mouse.scroll", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[struct{ DX, DY float64 }](raw)
		if err != nil {
			return nil, err
		}
		return nil, b.Scroll(p.DX, p.DY)
	})

	// keyboard
	reg("key.text", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[struct {
			Text string `json:"text"`
		}](raw)
		if err != nil {
			return nil, err
		}
		if p.Text == "" {
			return map[string]any{}, nil
		}
		return nil, b.TypeText(p.Text)
	})
	reg("key.press", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[struct {
			Key  string   `json:"key"`
			Mods []string `json:"mods"`
		}](raw)
		if err != nil {
			return nil, err
		}
		for _, m := range p.Mods {
			if !modNames[m] {
				return nil, bridge.Errorf(bridge.CodeInvalidParams, "unknown modifier %q", m)
			}
		}
		return nil, b.Key(p.Key, p.Mods, true, true)
	})
	keyEdge := func(down, up bool) bridge.Handler {
		return func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, err := bridge.Params[struct {
				Key string `json:"key"`
			}](raw)
			if err != nil {
				return nil, err
			}
			return nil, b.Key(p.Key, nil, down, up)
		}
	}
	reg("key.down", keyEdge(true, false))
	reg("key.up", keyEdge(false, true))

	// media & volume
	reg("media", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[struct {
			Action string `json:"action"`
		}](raw)
		if err != nil {
			return nil, err
		}
		if !mediaActions[p.Action] {
			return nil, bridge.Errorf(bridge.CodeInvalidParams, "unknown media action %q", p.Action)
		}
		return nil, b.Media(p.Action)
	})
	reg("volume.get", func(ctx context.Context, raw json.RawMessage) (any, error) {
		level, muted, err := b.VolumeGet()
		if err != nil {
			return nil, err
		}
		return map[string]any{"level": level, "muted": muted}, nil
	})
	reg("volume.set", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[struct {
			Level *int `json:"level"`
		}](raw)
		if err != nil {
			return nil, err
		}
		if p.Level == nil || *p.Level < 0 || *p.Level > 100 {
			return nil, bridge.Errorf(bridge.CodeInvalidParams, "level must be 0..100")
		}
		return nil, b.VolumeSet(*p.Level)
	})

	// apps & windows
	reg("apps.list", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[struct {
			Icons bool `json:"icons"`
		}](raw)
		if err != nil {
			return nil, err
		}
		apps, err := b.AppsList(p.Icons)
		if err != nil {
			return nil, err
		}
		if apps == nil {
			apps = []AppInfo{}
		}
		return apps, nil
	})
	reg("apps.running", func(ctx context.Context, raw json.RawMessage) (any, error) {
		list, err := b.AppsRunning()
		if err != nil {
			return nil, err
		}
		if list == nil {
			list = []RunningApp{}
		}
		return list, nil
	})
	reg("apps.launch", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[struct {
			ID string `json:"id"`
		}](raw)
		if err != nil {
			return nil, err
		}
		if p.ID == "" {
			return nil, bridge.Errorf(bridge.CodeInvalidParams, "id is required")
		}
		return nil, b.AppLaunch(p.ID)
	})
	reg("apps.focus", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[struct {
			ID  string `json:"id"`
			PID int    `json:"pid"`
		}](raw)
		if err != nil {
			return nil, err
		}
		if p.ID == "" && p.PID == 0 {
			return nil, bridge.Errorf(bridge.CodeInvalidParams, "id or pid is required")
		}
		return nil, b.AppFocus(p.ID, p.PID)
	})
	reg("apps.quit", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[struct {
			PID   int  `json:"pid"`
			Force bool `json:"force"`
		}](raw)
		if err != nil {
			return nil, err
		}
		if p.PID <= 0 {
			return nil, bridge.Errorf(bridge.CodeInvalidParams, "pid is required")
		}
		return nil, b.AppQuit(p.PID, p.Force)
	})
	reg("windows.list", func(ctx context.Context, raw json.RawMessage) (any, error) {
		list, err := b.WindowsList()
		if err != nil {
			return nil, err
		}
		if list == nil {
			list = []WindowInfo{}
		}
		return list, nil
	})
	reg("windows.focus", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[struct {
			ID string `json:"id"`
		}](raw)
		if err != nil {
			return nil, err
		}
		if p.ID == "" {
			return nil, bridge.Errorf(bridge.CodeInvalidParams, "id is required")
		}
		return nil, b.WindowFocus(p.ID)
	})

	// power, clipboard, URLs
	reg("power", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[struct {
			Action string `json:"action"`
		}](raw)
		if err != nil {
			return nil, err
		}
		if !powerActions[p.Action] {
			return nil, bridge.Errorf(bridge.CodeInvalidParams, "unknown power action %q", p.Action)
		}
		a.logf("power: %s", p.Action)
		return nil, b.Power(p.Action)
	})
	reg("clipboard.get", func(ctx context.Context, raw json.RawMessage) (any, error) {
		text, err := b.ClipboardGet()
		if err != nil {
			return nil, err
		}
		return map[string]any{"text": text}, nil
	})
	reg("clipboard.set", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[struct {
			Text string `json:"text"`
		}](raw)
		if err != nil {
			return nil, err
		}
		return nil, b.ClipboardSet(p.Text)
	})
	reg("open.url", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[struct {
			URL string `json:"url"`
		}](raw)
		if err != nil {
			return nil, err
		}
		u := strings.TrimSpace(p.URL)
		if !strings.HasPrefix(strings.ToLower(u), "http://") && !strings.HasPrefix(strings.ToLower(u), "https://") {
			return nil, bridge.Errorf(bridge.CodeInvalidParams, "only http(s) URLs can be opened")
		}
		return nil, b.OpenURL(u)
	})

	// local settings page (docs/reach-api.md §5)
	local("reach.state", func(ctx context.Context, raw json.RawMessage) (any, error) { return a.state(), nil })
	local("reach.settings.set", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[struct {
			Name          *string `json:"name"`
			Port          *int    `json:"port"`
			LaunchAtLogin *bool   `json:"launchAtLogin"`
		}](raw)
		if err != nil {
			return nil, err
		}
		restart := false
		if p.Name != nil {
			n := strings.TrimSpace(*p.Name)
			if n == "" || len(n) > maxDeviceName {
				return nil, bridge.Errorf(bridge.CodeInvalidParams, "name must be 1–%d characters", maxDeviceName)
			}
			if err := a.cfg.Update(func(c *Config) { c.Name = n }); err != nil {
				return nil, err
			}
			a.disc.rename(n)
		}
		if p.Port != nil {
			if *p.Port < 1 || *p.Port > 65535 {
				return nil, bridge.Errorf(bridge.CodeInvalidParams, "port must be 1–65535")
			}
			if err := a.cfg.Update(func(c *Config) { c.Port = *p.Port }); err != nil {
				return nil, err
			}
			restart = *p.Port != a.port
		}
		if p.LaunchAtLogin != nil {
			if err := a.setLaunchAtLogin(*p.LaunchAtLogin); err != nil {
				return nil, err
			}
		}
		st := a.state()
		st["restartRequired"] = restart
		return st, nil
	})
	local("reach.pairings.list", func(ctx context.Context, raw json.RawMessage) (any, error) {
		return a.cfg.PublicPairings(), nil
	})
	local("reach.pairings.revoke", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[struct {
			Device string `json:"device"`
		}](raw)
		if err != nil {
			return nil, err
		}
		tokens, err := a.cfg.Revoke(p.Device)
		if err != nil {
			return nil, err
		}
		if len(tokens) == 0 {
			return nil, bridge.Errorf(bridge.CodeNotFound, "no pairing for %q", p.Device)
		}
		a.closeConnsWithToken(tokens)
		a.logf("revoked pairing of %q", p.Device)
		return map[string]any{"pairings": a.cfg.PublicPairings()}, nil
	})
	local("reach.pair.showCode", func(ctx context.Context, raw json.RawMessage) (any, error) {
		code, expires, ok, err := a.pair.request("")
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, bridge.Errorf(bridge.CodeNotAuthorized, "pairing is locked for %d s after repeated wrong codes", int(time.Until(expires).Seconds()))
		}
		a.showCode(code, expires, "")
		return map[string]any{"code": code, "expiresAt": expires.UTC().Format(time.RFC3339)}, nil
	})

	// common app.* methods (docs/ARCHITECTURE.md §4)
	local("app.info", func(ctx context.Context, raw json.RawMessage) (any, error) {
		host, _, _ := b.HostInfo()
		return map[string]any{"name": "Reach", "version": a.version, "platform": b.Platform(), "machine": host, "permissions": b.Permissions()}, nil
	})
	local("app.openURL", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := bridge.Params[struct {
			URL string `json:"url"`
		}](raw)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(p.URL, "http://") && !strings.HasPrefix(p.URL, "https://") {
			return nil, bridge.Errorf(bridge.CodeInvalidParams, "only http(s) URLs can be opened")
		}
		return nil, b.OpenURL(p.URL)
	})
	local("app.openSettingsPane", func(ctx context.Context, raw json.RawMessage) (any, error) {
		return nil, bridge.Errorf(bridge.CodeNotSupported, "no OS permission panes on this platform")
	})
	local("app.quit", func(ctx context.Context, raw json.RawMessage) (any, error) {
		if a.quit != nil {
			go a.quit()
		}
		return map[string]any{}, nil
	})

	// test-only: the fake backend's injection log
	if rec, ok := b.(actionRecorder); ok {
		reg("reach.debug.actions", func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, err := bridge.Params[struct {
				Clear bool `json:"clear"`
			}](raw)
			if err != nil {
				return nil, err
			}
			actions := rec.Actions()
			if p.Clear {
				rec.ClearActions()
			}
			if actions == nil {
				actions = []Action{}
			}
			return map[string]any{"actions": actions}, nil
		})
	}
}

// state is reach.state.
func (a *agent) state() map[string]any {
	snap := a.cfg.Snapshot()
	host, _, _ := a.backend.HostInfo()
	urls := lanURLs(a.port)
	if urls == nil {
		urls = []string{}
	}
	peers := a.disc.list()
	if peers == nil {
		peers = []peer{}
	}
	return map[string]any{
		"name": snap.Name, "port": a.port, "configuredPort": snap.Port, "agentId": a.id,
		"platform": a.backend.Platform(), "version": a.version, "hostname": host, "fake": a.fake,
		"pairings": a.cfg.PublicPairings(), "permissions": a.backend.Permissions(), "peers": peers,
		"launchAtLogin": snap.LaunchAtLogin, "urls": urls,
	}
}

func displayOf(p pointParams) int {
	if p.Display == nil {
		return 0
	}
	return *p.Display
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func buttonOf(s string) (string, error) {
	if s == "" {
		return "left", nil
	}
	if !mouseButtons[s] {
		return "", bridge.Errorf(bridge.CodeInvalidParams, "unknown button %q", s)
	}
	return s, nil
}

func errString(err error) string {
	if err == nil {
		return "ok"
	}
	var be *bridge.Error
	if errors.As(err, &be) {
		return be.Code
	}
	return fmt.Sprintf("internal: %v", err)
}
