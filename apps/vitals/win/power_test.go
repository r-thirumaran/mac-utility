package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFlattenChanges(t *testing.T) {
	var changes map[string]any
	if err := json.Unmarshal([]byte(`{"onBattery":{"lidClose":"sleep","systemSleepMin":15},"lidOpenWakes":true,"hibernate":{"enabled":false},"onPower.displaySleepMin":10}`), &changes); err != nil {
		t.Fatal(err)
	}
	flat := flattenChanges(changes)
	want := map[string]any{
		"onBattery.lidClose":       "sleep",
		"onBattery.systemSleepMin": float64(15),
		"lidOpenWakes":             true,
		"hibernate.enabled":        false,
		"onPower.displaySleepMin":  float64(10),
	}
	if !reflect.DeepEqual(flat, want) {
		t.Fatalf("flat = %#v", flat)
	}
	if keys := sortedKeys(flat); keys[0] != "hibernate.enabled" || keys[len(keys)-1] != "onPower.displaySleepMin" {
		t.Fatalf("sorted keys = %v", keys)
	}
}

func TestValidateAndApplyToModel(t *testing.T) {
	m := PowerModel{}
	cases := map[string]any{
		"onBattery.displaySleepMin": float64(5),
		"onBattery.systemSleepMin":  "15",
		"onBattery.lidClose":        "shutdown",
		"onPower.displaySleepMin":   float64(0),
		"onPower.systemSleepMin":    float64(30),
		"onPower.lidClose":          "nothing",
		"powerButton":               "displayOff",
		"lidOpenWakes":              true,
		"wakeOnNetwork":             "off",
		"powerNap":                  true,
		"lowPowerMode":              float64(1),
		"hibernate.enabled":         true,
		"hibernate.mode":            "reduced",
	}
	for k, v := range cases {
		if err := applyToModel(&m, k, v); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
	}
	if m.OnBattery != (PowerSource{5, 15, "shutdown"}) || m.OnPower != (PowerSource{0, 30, "nothing"}) {
		t.Fatalf("sources = %+v %+v", m.OnBattery, m.OnPower)
	}
	if m.PowerButton != "displayOff" || !m.LidOpenWakes || m.WakeOnNetwork || m.PowerNap == nil || !*m.PowerNap || m.LowPowerMode == nil || !*m.LowPowerMode {
		t.Fatalf("flags = %+v", m)
	}
	if m.Hibernate == nil || !m.Hibernate.Enabled || m.Hibernate.Mode != "reduced" {
		t.Fatalf("hibernate = %+v", m.Hibernate)
	}
	bad := []struct {
		k string
		v any
	}{
		{"onBattery.displaySleepMin", float64(-1)},
		{"onBattery.displaySleepMin", "soon"},
		{"onPower.lidClose", "fly"},
		{"powerButton", float64(3)},
		{"lidOpenWakes", "maybe"},
		{"hibernate.mode", float64(2)},
		{"made.up", true},
	}
	for _, c := range bad {
		if _, err := validateChange(c.k, c.v); err == nil {
			t.Fatalf("%s=%v should be rejected", c.k, c.v)
		}
	}
}

func TestValidateSchedule(t *testing.T) {
	items, err := validateSchedule([]ScheduleItem{
		{Type: "Shutdown", Time: "23:00", Days: []string{"Friday", "mon", "MON"}},
		{Type: "sleep", Time: "01:30", Days: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}, Enabled: true},
		{ID: "keep", Type: "restart", Time: "06:00"},
	}, []string{"shutdown", "sleep", "restart"})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Type != "shutdown" || !reflect.DeepEqual(items[0].Days, []string{"mon", "fri"}) || items[0].ID == "" {
		t.Fatalf("item 0 = %+v", items[0])
	}
	if len(items[1].Days) != 0 || !items[1].Enabled {
		t.Fatalf("every day should normalize to an empty list: %+v", items[1])
	}
	if items[2].ID != "keep" || len(items[2].Days) != 0 {
		t.Fatalf("item 2 = %+v", items[2])
	}
	for _, bad := range []ScheduleItem{
		{Type: "wake", Time: "01:00"},
		{Type: "sleep", Time: "1:00"},
		{Type: "sleep", Time: "24:00"},
		{Type: "sleep", Time: "10:00", Days: []string{"funday"}},
	} {
		if _, err := validateSchedule([]ScheduleItem{bad}, []string{"shutdown", "sleep", "restart"}); err == nil {
			t.Fatalf("%+v should be rejected", bad)
		}
	}
}

func TestFakePowerRoundTrip(t *testing.T) {
	p := newFakePower()
	m, err := p.Get()
	if err != nil || !m.Fake || m.OnBattery.LidClose == "" || len(m.Supported.LidClose) == 0 {
		t.Fatalf("get = %+v, %v", m, err)
	}
	res, err := p.Apply(map[string]any{"onBattery": map[string]any{"lidClose": "shutdown"}, "lidOpenWakes": false, "bogus": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Applied, []string{"lidOpenWakes", "onBattery.lidClose"}) || len(res.Failed) != 1 || res.Failed[0].Key != "bogus" {
		t.Fatalf("apply = %+v", res)
	}
	m, _ = p.Get()
	if m.OnBattery.LidClose != "shutdown" || m.LidOpenWakes {
		t.Fatalf("model after apply = %+v", m)
	}
	if _, err := p.KeepAwake(true, "display"); err != nil {
		t.Fatal(err)
	}
	m, _ = p.Get()
	if !m.KeepAwake.Enabled || m.KeepAwake.Mode != "display" {
		t.Fatalf("keep awake = %+v", m.KeepAwake)
	}
	if _, err := p.KeepAwake(true, "forever"); err == nil {
		t.Fatal("bad keep-awake mode should fail")
	}
	if err := p.Action("lock"); err != nil || p.lastAction != "lock" {
		t.Fatalf("action = %v, last %q", err, p.lastAction)
	}
	if err := p.Action("explode"); err == nil {
		t.Fatal("bad action should fail")
	}
	items, err := p.ScheduleSet([]ScheduleItem{{Type: "shutdown", Time: "22:00", Days: []string{"sat", "sun"}, Enabled: true}})
	if err != nil || len(items) != 1 || items[0].ID == "" {
		t.Fatalf("schedule set = %+v, %v", items, err)
	}
	got, _ := p.ScheduleGet()
	if !reflect.DeepEqual(got, items) {
		t.Fatalf("schedule get = %+v", got)
	}
}
