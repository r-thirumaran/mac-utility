package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/r-thirumaran/mac-utility/apps/edgehop/core"
	"github.com/r-thirumaran/mac-utility/apps/edgehop/input"
	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
	"github.com/r-thirumaran/mac-utility/shared/go/securechan"
	"github.com/r-thirumaran/mac-utility/shared/go/tray"
)

// newTestApp wires an app exactly like run() does, with a fake device, a
// memory store and Bonjour off, on a free port.
func newTestApp(t *testing.T, name string) (*app, *bridge.ChanSink) {
	t.Helper()
	id, err := securechan.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	store := core.NewMemoryStore()
	fake := input.NewFake()
	d := bridge.New()
	a := &app{store: store, fake: fake, d: d, version: "test", exe: "/x/edgehop"}
	a.tr = core.NewTransport(core.TransportConfig{Self: id, Name: name, Plat: core.Platform(), Store: store, Port: 0, DisableMDNS: true, Emit: a.handleEvent, Logf: a.nodeLogf})
	a.node = core.New(core.Config{ID: id.ID(), Name: name, Plat: core.Platform(), Version: "test", Store: store, Capturer: fake, Injector: fake, Transport: a.tr, Clipboard: &core.MemClipboard{}, Logf: a.nodeLogf})
	a.tr.Bind(a.node)
	a.register()
	ctx, cancel := context.WithCancel(context.Background())
	a.quit = cancel
	if err := a.tr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	a.node.SetPort(a.tr.Port())
	if err := a.node.Start(); err != nil {
		t.Fatal(err)
	}
	go a.pump()
	sink := bridge.NewChanSink(256)
	d.AddSink(sink)
	t.Cleanup(func() { a.node.Stop(); a.tr.Stop(); cancel() })
	return a, sink
}

// call runs a bridge request and decodes the result into out.
func call(t *testing.T, a *app, method string, params any, out any) *bridge.Error {
	t.Helper()
	p, _ := json.Marshal(params)
	req, _ := json.Marshal(map[string]any{"id": 1, "method": method, "params": json.RawMessage(p)})
	resp := a.d.Handle(context.Background(), req)
	var r struct {
		Result json.RawMessage `json:"result"`
		Error  *bridge.Error   `json:"error"`
	}
	if err := json.Unmarshal(resp, &r); err != nil {
		t.Fatalf("%s: bad response %s", method, resp)
	}
	if r.Error != nil {
		return r.Error
	}
	if out != nil {
		if err := json.Unmarshal(r.Result, out); err != nil {
			t.Fatalf("%s: decode result %s: %v", method, r.Result, err)
		}
	}
	return nil
}

func TestBridgeMethods(t *testing.T) {
	a, sink := newTestApp(t, "Bridge")
	var info map[string]any
	if e := call(t, a, "app.info", nil, &info); e != nil {
		t.Fatal(e)
	}
	if info["name"] != "Edgehop" || info["machine"] != "Bridge" || info["platform"] != core.Platform() || info["fakeInput"] != true {
		t.Fatalf("app.info = %v", info)
	}
	var st uiState
	if e := call(t, a, "edgehop.state", nil, &st); e != nil {
		t.Fatal(e)
	}
	if !st.Self.Active || st.Port == 0 || st.Version != "test" || !st.FakeInput || st.Peers == nil || st.Layout.Tiles == nil {
		t.Fatalf("state = %+v", st)
	}
	// settings round trip with a partial object
	var s uiSettings
	if e := call(t, a, "edgehop.settings.set", map[string]any{"switchDelayMs": 250, "clipboardSync": false}, &s); e != nil {
		t.Fatal(e)
	}
	if s.SwitchDelayMs != 250 || s.ClipboardSync || !s.SwapCmdCtrl || s.Name != "Bridge" {
		t.Fatalf("settings = %+v", s)
	}
	if e := call(t, a, "edgehop.settings.set", map[string]any{"name": "Renamed"}, &s); e != nil || s.Name != "Renamed" {
		t.Fatalf("rename: %+v %v", s, e)
	}
	if e := call(t, a, "edgehop.settings.set", map[string]any{"name": "  "}, nil); e == nil || e.Code != bridge.CodeInvalidParams {
		t.Fatalf("empty name accepted: %v", e)
	}
	if e := call(t, a, "edgehop.settings.get", nil, &s); e != nil || s.SwitchDelayMs != 250 {
		t.Fatalf("settings.get = %+v %v", s, e)
	}
	// layout
	var lr struct {
		Layout core.Layout `json:"layout"`
	}
	if e := call(t, a, "edgehop.layout.set", map[string]any{"layout": map[string]any{"tiles": []map[string]any{{"id": a.node.ID(), "col": 2, "row": 2}}}}, &lr); e != nil {
		t.Fatal(e)
	}
	if len(lr.Layout.Tiles) != 1 || lr.Layout.UpdatedAt == 0 {
		t.Fatalf("layout = %+v", lr.Layout)
	}
	if e := call(t, a, "edgehop.layout.set", map[string]any{"layout": map[string]any{"tiles": []map[string]any{{"id": "x", "col": 1, "row": 1}, {"id": "y", "col": 1, "row": 1}}}}, nil); e == nil || e.Code != bridge.CodeInvalidParams {
		t.Fatalf("overlapping tiles accepted: %v", e)
	}
	// pairing parameter validation
	if e := call(t, a, "edgehop.pair.start", map[string]any{}, nil); e == nil || e.Code != bridge.CodeInvalidParams {
		t.Fatalf("pair.start without target: %v", e)
	}
	if e := call(t, a, "edgehop.pair.confirm", map[string]any{"code": "12"}, nil); e == nil || e.Code != bridge.CodeInvalidParams {
		t.Fatalf("short code accepted: %v", e)
	}
	if e := call(t, a, "edgehop.pair.confirm", map[string]any{"code": "123456"}, nil); e == nil || !strings.Contains(e.Message, "no pairing") {
		t.Fatalf("confirm without pairing: %v", e)
	}
	if e := call(t, a, "edgehop.unpair", map[string]any{}, nil); e == nil || e.Code != bridge.CodeInvalidParams {
		t.Fatalf("unpair without id: %v", e)
	}
	if e := call(t, a, "edgehop.switchTo", map[string]any{"peerId": "nobody"}, nil); e == nil {
		t.Fatal("switch to an unknown peer succeeded")
	}
	if e := call(t, a, "app.openURL", map[string]any{"url": "file:///etc/passwd"}, nil); e == nil || e.Code != bridge.CodeInvalidParams {
		t.Fatalf("non-http URL accepted: %v", e)
	}
	// debug: a physical move on the lone (active) machine is not swallowed
	var em struct {
		Swallowed []bool         `json:"swallowed"`
		Cursor    map[string]int `json:"cursor"`
	}
	if e := call(t, a, "edgehop.debug.emit", map[string]any{"event": map[string]any{"kind": "mouse_move", "dx": 10, "dy": -5}}, &em); e != nil {
		t.Fatal(e)
	}
	if len(em.Swallowed) != 1 || em.Swallowed[0] || em.Cursor["x"] != 970 || em.Cursor["y"] != 535 {
		t.Fatalf("emit = %+v", em)
	}
	if e := call(t, a, "edgehop.debug.emit", map[string]any{"event": map[string]any{"kind": "bogus"}}, nil); e == nil {
		t.Fatal("bogus kind accepted")
	}
	var inj struct {
		Calls  []input.Call `json:"calls"`
		Locked bool         `json:"locked"`
	}
	a.fake.MoveTo(5, 5)
	if e := call(t, a, "edgehop.debug.injected", map[string]any{"clear": true}, &inj); e != nil || len(inj.Calls) != 1 || inj.Calls[0].Op != "MoveTo" || inj.Locked {
		t.Fatalf("injected = %+v %v", inj, e)
	}
	if e := call(t, a, "edgehop.debug.injected", nil, &inj); e != nil || len(inj.Calls) != 0 {
		t.Fatalf("log not cleared: %+v", inj)
	}
	var dd struct {
		Displays []input.Display `json:"displays"`
	}
	if e := call(t, a, "edgehop.debug.displays", map[string]any{"displays": []map[string]any{{"id": "a", "w": 800, "h": 600, "primary": true}}}, &dd); e != nil || len(dd.Displays) != 1 {
		t.Fatalf("displays = %+v %v", dd, e)
	}
	// log ring and events
	a.logf("info", "hello from the test")
	var lg struct {
		Entries []core.LogEntry `json:"entries"`
	}
	if e := call(t, a, "edgehop.log.recent", nil, &lg); e != nil || len(lg.Entries) == 0 || lg.Entries[len(lg.Entries)-1].Msg != "hello from the test" {
		t.Fatalf("log.recent = %+v %v", lg, e)
	}
	deadline := time.After(5 * time.Second)
	seen := map[string]bool{}
	for !(seen["edgehop.state"] && seen["edgehop.log"]) {
		select {
		case f := <-sink.C():
			var ev struct {
				Event string `json:"event"`
			}
			_ = json.Unmarshal(f, &ev)
			seen[ev.Event] = true
		case <-deadline:
			t.Fatalf("events not seen: %v", seen)
		}
	}
	// unknown method
	if e := call(t, a, "edgehop.nope", nil, nil); e == nil || e.Code != bridge.CodeNotFound {
		t.Fatalf("unknown method: %v", e)
	}
}

func TestTrayMenu(t *testing.T) {
	a, _ := newTestApp(t, "Tray")
	items := a.trayItems()
	if len(items) < 2 || items[0].Title != "Launch at login" || !items[1].Separator {
		t.Fatalf("items = %+v", items)
	}
	// Paired peers get a "Switch to" entry, disabled while offline.
	other, _ := securechan.NewIdentity()
	if err := a.store.Trust(securechan.PeerInfo{ID: other.ID(), Name: "Desk PC", Plat: "windows", StaticPub: other.Pub}); err != nil {
		t.Fatal(err)
	}
	items = a.trayItems()
	found := false
	for _, it := range items {
		if it.Title == "Switch to Desk PC" {
			found = true
			if !it.Disabled {
				t.Fatal("offline peer must be disabled")
			}
		}
	}
	if !found {
		t.Fatalf("no switch entry: %+v", items)
	}
	// updateTray keeps the shell's Open/Quit around the app items.
	done := make(chan struct{})
	go tray.Run(tray.Config{Items: []tray.Item{{Title: "Open Edgehop"}, {Separator: true}, {Title: "old"}, {Separator: true}, {Title: "Quit"}}}, func(tr *tray.Tray) {
		a.setTray(tr)
		got := tr.Items()
		if got[0].Title != "Open Edgehop" || got[len(got)-1].Title != "Quit" || got[2].Title != "Launch at login" {
			t.Errorf("tray items = %+v", got)
		}
		if !strings.HasPrefix(tr.Tooltip(), "Edgehop — Tray") {
			t.Errorf("tooltip = %q", tr.Tooltip())
		}
		tr.Quit()
		close(done)
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("tray did not run")
	}
}
