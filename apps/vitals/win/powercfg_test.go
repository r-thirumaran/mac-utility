package main

import (
	"reflect"
	"strings"
	"testing"
)

const powercfgLidFixture = `
Power Scheme GUID: 381b4222-f694-41f0-9685-ff5bb260df2e  (Balanced)
  Subgroup GUID: 4f971e89-eebd-4455-a8de-9e59040e7347  (Power buttons and lid)
    GUID Alias: SUB_BUTTONS
    Power Setting GUID: 5ca83367-6e45-459f-a27b-476b1d01c936  (Lid close action)
      GUID Alias: LIDACTION
      Possible Setting Index: 000
      Possible Setting Friendly Name: Do nothing
      Possible Setting Index: 001
      Possible Setting Friendly Name: Sleep
      Possible Setting Index: 002
      Possible Setting Friendly Name: Hibernate
      Possible Setting Index: 003
      Possible Setting Friendly Name: Shut down
      Current AC Power Setting Index: 0x00000003
      Current DC Power Setting Index: 0x00000001
`

const powercfgVideoFixture = `
Energiesparplan-GUID: 381b4222-f694-41f0-9685-ff5bb260df2e  (Ausbalanciert)
  Untergruppen-GUID: 7516b95f-f776-4464-8c53-06167f40cc99  (Bildschirm)
    GUID-Alias: SUB_VIDEO
    Energieeinstellungs-GUID: 3c0bc021-c8a8-4e07-a973-6b14cbcb2b7e  (Bildschirm ausschalten nach)
      GUID-Alias: VIDEOIDLE
      Minimale Einstellung: 0x00000000
      Maximale Einstellung: 0xffffffff
      Inkrement: 0x00000001
      Einheiten: Sekunden
      Aktuelle Einstellung (Netzbetrieb): 0x00000258
      Aktuelle Einstellung (Akkubetrieb): 0x0000012c
`

func TestParsePowercfgIndex(t *testing.T) {
	ac, dc, err := parsePowercfgIndex(powercfgLidFixture)
	if err != nil || ac != 3 || dc != 1 {
		t.Fatalf("lid = %d/%d, %v", ac, dc, err)
	}
	// Localized output: no English labels, so the last two hex tokens win.
	ac, dc, err = parsePowercfgIndex(powercfgVideoFixture)
	if err != nil || ac != 600 || dc != 300 {
		t.Fatalf("video = %d/%d, %v", ac, dc, err)
	}
	if _, _, err := parsePowercfgIndex("Invalid Parameters -- try \"/?\" for help\n"); err == nil {
		t.Fatal("expected an error without indexes")
	}
	if got := minutesFromSeconds(600); got != 10 {
		t.Fatalf("600 s = %d min", got)
	}
	if got := minutesFromSeconds(0); got != 0 {
		t.Fatalf("0 s = %d min", got)
	}
	if got := minutesFromSeconds(20); got != 1 {
		t.Fatalf("20 s = %d min (must not become never)", got)
	}
	guid, name := parseActiveScheme("Power Scheme GUID: 381b4222-f694-41f0-9685-ff5bb260df2e  (Balanced)\n")
	if guid != "381b4222-f694-41f0-9685-ff5bb260df2e" || name != "Balanced" {
		t.Fatalf("scheme = %q %q", guid, name)
	}
}

func TestActionIndexes(t *testing.T) {
	for i, want := range map[uint32]string{0: "nothing", 1: "sleep", 2: "hibernate", 3: "shutdown", 4: "displayOff", 9: "nothing"} {
		if got := actionFromIndex(i); got != want {
			t.Fatalf("index %d = %q, want %q", i, got, want)
		}
	}
	if i, ok := indexFromAction("shutdown"); !ok || i != 3 {
		t.Fatalf("shutdown = %d %v", i, ok)
	}
	if _, ok := indexFromAction("fly"); ok {
		t.Fatal("unknown action should not map")
	}
}

func TestPlanPowercfg(t *testing.T) {
	changes := flattenChanges(map[string]any{
		"onBattery":    map[string]any{"lidClose": "sleep", "displaySleepMin": float64(5)},
		"onPower":      map[string]any{"systemSleepMin": float64(0), "lidClose": "hibernate"},
		"lidOpenWakes": false,
		"powerButton":  "displayOff",
		"hibernate":    map[string]any{"enabled": true, "mode": "reduced"},
		"powerNap":     true,
		"wakeOnNetwork": true,
	})
	ops, failed := planPowercfg(changes, false)
	byKey := map[string]powercfgOp{}
	for _, op := range ops {
		byKey[op.Key] = op
	}
	if !reflect.DeepEqual(byKey["onBattery.lidClose"].Cmds, [][]string{{"/setdcvalueindex", "SCHEME_CURRENT", "SUB_BUTTONS", "LIDACTION", "1"}}) || !byKey["onBattery.lidClose"].SetActive {
		t.Fatalf("lid op = %+v", byKey["onBattery.lidClose"])
	}
	if !reflect.DeepEqual(byKey["onBattery.displaySleepMin"].Cmds, [][]string{{"/change", "monitor-timeout-dc", "5"}}) {
		t.Fatalf("display op = %+v", byKey["onBattery.displaySleepMin"])
	}
	if !reflect.DeepEqual(byKey["onPower.systemSleepMin"].Cmds, [][]string{{"/change", "standby-timeout-ac", "0"}}) {
		t.Fatalf("standby op = %+v", byKey["onPower.systemSleepMin"])
	}
	if len(byKey["powerButton"].Cmds) != 2 || byKey["powerButton"].Cmds[1][4] != "4" {
		t.Fatalf("power button op = %+v", byKey["powerButton"])
	}
	if byKey["lidOpenWakes"].Cmds[0][4] != "0" {
		t.Fatalf("lid open op = %+v", byKey["lidOpenWakes"])
	}
	if op := byKey["hibernate.enabled"]; !op.Elevated || !reflect.DeepEqual(op.Cmds, [][]string{{"/hibernate", "on"}}) {
		t.Fatalf("hibernate op = %+v", op)
	}
	if op := byKey["hibernate.mode"]; !op.Elevated || op.Cmds[0][2] != "reduced" {
		t.Fatalf("hibernate mode op = %+v", op)
	}
	failedKeys := map[string]ApplyFailure{}
	for _, f := range failed {
		failedKeys[f.Key] = f
	}
	if _, ok := failedKeys["onPower.lidClose"]; !ok {
		t.Fatalf("hibernate lid action should fail while hibernation is off: %v", failed)
	}
	for _, k := range []string{"powerNap", "wakeOnNetwork"} {
		if _, ok := failedKeys[k]; !ok {
			t.Fatalf("%s should be reported unsupported: %v", k, failed)
		}
	}
	if len(ops)+len(failed) != len(changes) {
		t.Fatalf("%d ops + %d failed != %d changes", len(ops), len(failed), len(changes))
	}
	// Invalid values are failures, not ops.
	_, failed = planPowercfg(map[string]any{"onPower.displaySleepMin": "soon", "onBattery.lidClose": "fly", "nope": 1}, true)
	if len(failed) != 3 {
		t.Fatalf("expected 3 failures, got %v", failed)
	}
}

func TestSchtasksArgs(t *testing.T) {
	args, err := schtasksCreateArgs(ScheduleItem{ID: "night", Type: "shutdown", Time: "23:30", Days: []string{"mon", "tue", "fri"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{`/tn Vitals\night`, "/sc weekly", "/d MON,TUE,FRI", "/st 23:30", "/f", "shutdown.exe /s /t 60"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("%q lacks %q", joined, want)
		}
	}
	args, err = schtasksCreateArgs(ScheduleItem{ID: "nap", Type: "sleep", Time: "01:00"}, `C:\Apps\Vitals.exe`)
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(args, " ")
	if !strings.Contains(joined, "/sc daily") || !strings.Contains(joined, `"C:\Apps\Vitals.exe" --power-action sleep`) {
		t.Fatalf("sleep args = %q", joined)
	}
	if _, err := schtasksCreateArgs(ScheduleItem{Type: "sleep", Time: "01:00"}, ""); err == nil {
		t.Fatal("sleep without exe should fail")
	}
	if _, err := schtasksCreateArgs(ScheduleItem{Type: "wake", Time: "01:00"}, "x"); err == nil {
		t.Fatal("wake should be unsupported")
	}
	if _, err := schtasksCreateArgs(ScheduleItem{Type: "restart", Time: "25:00"}, "x"); err == nil {
		t.Fatal("bad time should fail")
	}
	if got := scheduleTaskName("a b/c"); got != `Vitals\a-b-c` {
		t.Fatalf("task name = %q", got)
	}
	if got := strings.Join(schtasksDeleteArgs("x"), " "); got != `/delete /tn Vitals\x /f` {
		t.Fatalf("delete args = %q", got)
	}
}
