package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
	"github.com/r-thirumaran/mac-utility/shared/go/winutil"
)

// rpc sends one request through the dispatcher and decodes the response.
type rpc struct {
	t *testing.T
	d *bridge.Dispatcher
}

type rpcErr struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

func (r rpc) call(method string, params any, out any) *rpcErr {
	r.t.Helper()
	req := map[string]any{"id": 1, "method": method}
	if params != nil {
		req["params"] = params
	}
	raw, _ := json.Marshal(req)
	resp := r.d.Handle(context.Background(), raw)
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcErr         `json:"error"`
	}
	if err := json.Unmarshal(resp, &env); err != nil {
		r.t.Fatalf("%s: bad response %s: %v", method, resp, err)
	}
	if env.Error != nil {
		return env.Error
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			r.t.Fatalf("%s: decode result %s: %v", method, env.Result, err)
		}
	}
	return nil
}

func (r rpc) mustCall(method string, params any, out any) {
	r.t.Helper()
	if e := r.call(method, params, out); e != nil {
		r.t.Fatalf("%s: unexpected error %s: %s", method, e.Code, e.Message)
	}
}

func newTestApp(t *testing.T) (*app, *bridge.Dispatcher, *fakeStats, *fakePower, rpc) {
	t.Helper()
	stats := newFakeStats()
	power := newFakePower()
	quit := make(chan struct{}, 1)
	a := newApp(appOptions{
		Stats: stats, Power: power, FakePower: true, Version: "1.2.3", Interval: 250 * time.Millisecond,
		Log:  log.New(os.Stderr, "", 0),
		Quit: func() { quit <- struct{}{} },
	})
	d := bridge.New()
	a.register(d)
	t.Cleanup(a.sampler.Close)
	return a, d, stats, power, rpc{t, d}
}

func TestRegisteredMethods(t *testing.T) {
	_, d, _, _, _ := newTestApp(t)
	want := []string{
		"app.info", "app.openURL", "app.quit", "app.openSettingsPane",
		"vitals.subscribe", "vitals.unsubscribe", "vitals.processes", "vitals.killProcess",
		"power.get", "power.apply", "power.relaunchElevated", "power.action",
		"power.schedule.get", "power.schedule.set", "power.keepAwake", "power.keepAwake.get",
	}
	have := map[string]bool{}
	for _, m := range d.Methods() {
		have[m] = true
	}
	for _, m := range want {
		if !have[m] {
			t.Errorf("method %s not registered", m)
		}
	}
	if len(have) != len(want) {
		t.Errorf("registered %d methods, want %d: %v", len(have), len(want), d.Methods())
	}
}

func TestAppInfo(t *testing.T) {
	_, _, _, _, r := newTestApp(t)
	var info struct {
		Name        string         `json:"name"`
		Version     string         `json:"version"`
		Platform    string         `json:"platform"`
		Machine     machineInfo    `json:"machine"`
		Permissions map[string]any `json:"permissions"`
		FakePower   bool           `json:"fakePower"`
	}
	r.mustCall("app.info", nil, &info)
	if info.Name != "Vitals" || info.Version != "1.2.3" {
		t.Fatalf("info = %+v", info)
	}
	if info.Platform != platform() || (info.Platform != "windows" && info.Platform != "linux") {
		t.Fatalf("platform = %q", info.Platform)
	}
	if info.Machine.Hostname != "testbox" || info.Machine.Cores != 2 || info.Machine.MemTotal != 1000 || info.Machine.CPUModel != "Fake CPU" || info.Machine.OS != "linux" || info.Machine.UptimeSec != 42 {
		t.Fatalf("machine = %+v", info.Machine)
	}
	if info.Permissions == nil || len(info.Permissions) != 0 {
		t.Fatalf("permissions = %v", info.Permissions)
	}
	if !info.FakePower {
		t.Fatal("fakePower flag missing")
	}
}

func TestOpenURLValidation(t *testing.T) {
	_, _, _, _, r := newTestApp(t)
	for _, bad := range []string{"", "javascript:alert(1)", "file:///etc/passwd", "not a url"} {
		if e := r.call("app.openURL", map[string]any{"url": bad}, nil); e == nil || e.Code != bridge.CodeInvalidParams {
			t.Errorf("openURL(%q) → %v, want invalid_params", bad, e)
		}
	}
	for _, good := range []string{"https://example.com/x?y=1", "mailto:someone@example.com", "ms-settings:powersleep"} {
		if _, err := openableURL(good); err != nil {
			t.Errorf("openableURL(%q): %v", good, err)
		}
	}
}

func TestQuitCallsBack(t *testing.T) {
	stats := newFakeStats()
	quit := make(chan struct{}, 1)
	a := newApp(appOptions{Stats: stats, Power: newFakePower(), Quit: func() { quit <- struct{}{} }, Log: log.New(os.Stderr, "", 0)})
	d := bridge.New()
	a.register(d)
	defer a.sampler.Close()
	r := rpc{t, d}
	r.mustCall("app.quit", nil, nil)
	select {
	case <-quit:
	case <-time.After(2 * time.Second):
		t.Fatal("quit callback not invoked")
	}
}

func TestSubscribeEmitsSamples(t *testing.T) {
	a, d, _, _, r := newTestApp(t)
	sink := bridge.NewChanSink(16)
	remove := d.AddSink(sink)
	defer remove()
	var res struct {
		IntervalMs  int `json:"intervalMs"`
		Subscribers int `json:"subscribers"`
	}
	r.mustCall("vitals.subscribe", map[string]any{"intervalMs": 100}, &res)
	if res.IntervalMs != 250 || res.Subscribers != 1 {
		t.Fatalf("subscribe → %+v (100 ms must clamp to 250)", res)
	}
	select {
	case ev := <-sink.C():
		var frame struct {
			Event string `json:"event"`
			Data  Sample `json:"data"`
		}
		if err := json.Unmarshal(ev, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Event != "vitals.sample" || frame.Data.CPU.Total != 12.5 || len(frame.Data.CPU.PerCore) != 2 {
			t.Fatalf("event = %s", ev)
		}
		if !strings.Contains(string(ev), `"gpu":[]`) || !strings.Contains(string(ev), `"battery":{`) {
			t.Fatalf("sample must carry gpu [] and a battery object: %s", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no vitals.sample event")
	}
	r.mustCall("vitals.unsubscribe", nil, &res)
	if res.Subscribers != 0 || a.sampler.Running() {
		t.Fatalf("after unsubscribe: %+v running=%v", res, a.sampler.Running())
	}
}

func TestProcessesAndKill(t *testing.T) {
	_, _, stats, _, r := newTestApp(t)
	var list []Process
	r.mustCall("vitals.processes", map[string]any{"sortBy": "mem", "limit": 2}, &list)
	if len(list) != 2 || list[0].PID != 5 || list[1].PID != 7 {
		t.Fatalf("processes(mem, 2) = %+v", list)
	}
	r.mustCall("vitals.processes", map[string]any{}, &list)
	if len(list) != 4 || list[0].PID != 2 {
		t.Fatalf("processes(default) = %+v", list)
	}
	var killed struct {
		PID int `json:"pid"`
	}
	r.mustCall("vitals.killProcess", map[string]any{"pid": 7, "force": true}, &killed)
	if killed.PID != 7 || len(stats.killed) != 1 || stats.killed[0] != 7 || !stats.forced[0] {
		t.Fatalf("kill recorded %v forced %v", stats.killed, stats.forced)
	}
	if e := r.call("vitals.killProcess", map[string]any{"pid": 0}, nil); e == nil || e.Code != bridge.CodeInvalidParams {
		t.Fatalf("pid 0 → %v", e)
	}
	if e := r.call("vitals.killProcess", map[string]any{"pid": 999}, nil); e == nil || e.Code != bridge.CodeNotAuthorized {
		t.Fatalf("permission error → %v", e)
	}
	if e := r.call("vitals.killProcess", map[string]any{"pid": 404}, nil); e == nil || e.Code != bridge.CodeInvalidParams {
		t.Fatalf("missing process → %v", e)
	}
}

func TestPowerGetApplyRoundTrip(t *testing.T) {
	_, _, _, _, r := newTestApp(t)
	var m PowerModel
	r.mustCall("power.get", nil, &m)
	if m.OnBattery.LidClose != "sleep" || !m.Fake || m.Elevated != winutil.IsElevated() {
		t.Fatalf("model = %+v", m)
	}
	if m.Supported.LidClose == nil || m.Supported.Actions == nil || m.Supported.ScheduleTypes == nil {
		t.Fatal("supported lists must encode as arrays")
	}
	if e := r.call("power.apply", map[string]any{"changes": map[string]any{}}, nil); e == nil || e.Code != bridge.CodeInvalidParams {
		t.Fatalf("empty changes → %v", e)
	}
	var res ApplyResult
	r.mustCall("power.apply", map[string]any{"changes": map[string]any{
		"onBattery": map[string]any{"lidClose": "nothing", "displaySleepMin": 2},
		"hibernate": map[string]any{"enabled": false},
		"bogus":     1,
	}}, &res)
	if strings.Join(res.Applied, ",") != "hibernate.enabled,onBattery.displaySleepMin,onBattery.lidClose" {
		t.Fatalf("applied = %v", res.Applied)
	}
	if len(res.Failed) != 1 || res.Failed[0].Key != "bogus" {
		t.Fatalf("failed = %+v", res.Failed)
	}
	r.mustCall("power.get", nil, &m)
	if m.OnBattery.LidClose != "nothing" || m.OnBattery.DisplaySleepMin != 2 || m.Hibernate.Enabled {
		t.Fatalf("changes not reflected: %+v", m)
	}
}

func TestPowerActionAndRelaunch(t *testing.T) {
	_, _, _, power, r := newTestApp(t)
	if e := r.call("power.action", map[string]any{"action": "explode"}, nil); e == nil || e.Code != bridge.CodeInvalidParams {
		t.Fatalf("bad action → %v", e)
	}
	r.mustCall("power.action", map[string]any{"action": "lock"}, nil)
	if power.lastAction != "lock" {
		t.Fatalf("lastAction = %q", power.lastAction)
	}
	e := r.call("power.relaunchElevated", nil, nil)
	switch {
	case e == nil && !winutil.IsElevated():
		t.Fatal("relaunch must not silently succeed")
	case e != nil && e.Code != bridge.CodeNotSupported && e.Code != bridge.CodeNotAuthorized:
		t.Fatalf("relaunch → %s: %s", e.Code, e.Message)
	}
}

func TestScheduleAndKeepAwake(t *testing.T) {
	_, d, _, _, r := newTestApp(t)
	var got struct {
		Items []ScheduleItem `json:"items"`
	}
	r.mustCall("power.schedule.get", nil, &got)
	if got.Items == nil || len(got.Items) != 0 {
		t.Fatalf("initial schedule = %v", got.Items)
	}
	r.mustCall("power.schedule.set", map[string]any{"items": []map[string]any{
		{"type": "shutdown", "time": "23:30", "days": []string{"Mon", "fri"}, "enabled": true},
	}}, &got)
	if len(got.Items) != 1 || got.Items[0].ID == "" || strings.Join(got.Items[0].Days, ",") != "mon,fri" {
		t.Fatalf("schedule.set → %+v", got.Items)
	}
	if e := r.call("power.schedule.set", map[string]any{"items": []map[string]any{{"type": "wake", "time": "08:00"}}}, nil); e == nil || e.Code != bridge.CodeInvalidParams {
		t.Fatalf("unsupported type → %v", e)
	}

	sink := bridge.NewChanSink(4)
	remove := d.AddSink(sink)
	defer remove()
	var st KeepAwakeState
	r.mustCall("power.keepAwake", map[string]any{"enabled": true, "mode": "display"}, &st)
	if !st.Enabled || st.Mode != "display" {
		t.Fatalf("keepAwake → %+v", st)
	}
	select {
	case ev := <-sink.C():
		if !strings.HasPrefix(string(ev), `{"event":"power.keepAwake"`) {
			t.Fatalf("event = %s", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no power.keepAwake event")
	}
	r.mustCall("power.keepAwake.get", nil, &st)
	if !st.Enabled || st.Mode != "display" {
		t.Fatalf("keepAwake.get → %+v", st)
	}
	if e := r.call("power.keepAwake", map[string]any{"enabled": true, "mode": "forever"}, nil); e == nil || e.Code != bridge.CodeInvalidParams {
		t.Fatalf("bad mode → %v", e)
	}
	r.mustCall("power.keepAwake", map[string]any{"enabled": false}, &st)
	if st.Enabled {
		t.Fatal("keep-awake still on")
	}
}

func TestPassError(t *testing.T) {
	be := bridge.Errorf(bridge.CodeNotSupported, "nope")
	if got := passError(be, "x"); got != be {
		t.Fatalf("bridge errors pass through, got %v", got)
	}
	var out *bridge.Error
	if err := passError(fmt.Errorf("wrapped: %w", be), "x"); err != be {
		t.Fatalf("wrapped bridge error not unwrapped: %v", err)
	}
	err := passError(fmt.Errorf("disk on fire"), "power.get")
	if ok := asBridgeError(err, &out); !ok || out.Code != bridge.CodeInternal || !strings.Contains(out.Message, "power.get") {
		t.Fatalf("plain error → %v", err)
	}
}

func asBridgeError(err error, out **bridge.Error) bool {
	be, ok := err.(*bridge.Error)
	if ok {
		*out = be
	}
	return ok
}
