package main

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Pure helpers for the Windows power backend (docs/research/windows-power-
// management.md): powercfg output parsing, action index mapping and the
// translation of model changes into powercfg / schtasks command lines. No
// build tag, so the mapping is unit-tested on Linux.

// powercfg aliases (resolved by powercfg itself; GUIDs listed for reference).
const (
	subButtons    = "SUB_BUTTONS"   // 4f971e89-eebd-4455-a8de-9e59040e7347
	lidAction     = "LIDACTION"     // 5ca83367-6e45-459f-a27b-476b1d01c936
	pButtonAction = "PBUTTONACTION" // 7648efa3-dd9c-4e3e-b566-50f929386280
	lidOpenWake   = "LIDOPENWAKE"   // 99ff10e7-23b1-4c07-a9d1-5c3206d741b4
	subVideo      = "SUB_VIDEO"     // 7516b95f-f776-4464-8c53-06167f40cc99
	videoIdle     = "VIDEOIDLE"     // 3c0bc021-c8a8-4e07-a973-6b14cbcb2b7e
	subSleep      = "SUB_SLEEP"     // 238c9fa8-0aad-41ed-83f4-97be242c8f20
	standbyIdle   = "STANDBYIDLE"   // 29f6c1db-86da-48c5-9fdb-f2b67b1f44da
	schemeCurrent = "SCHEME_CURRENT"
)

var (
	acIndexRE  = regexp.MustCompile(`(?i)Current AC Power Setting Index:\s*(0x[0-9a-f]+)`)
	dcIndexRE  = regexp.MustCompile(`(?i)Current DC Power Setting Index:\s*(0x[0-9a-f]+)`)
	hexTokenRE = regexp.MustCompile(`0x[0-9A-Fa-f]{8}`)
	schemeRE   = regexp.MustCompile(`(?i)Power Scheme GUID:\s*([0-9a-f-]{36})\s*\(([^)]*)\)`)
)

// parsePowercfgIndex extracts the AC and DC value indexes from the output of
// `powercfg /q SCHEME_CURRENT <sub> <setting>`. The English labels are tried
// first; on a localized Windows the last two 8-digit hex tokens are AC then
// DC (the labels are translated, the numbers are not).
func parsePowercfgIndex(out string) (ac, dc uint32, err error) {
	if a, d := acIndexRE.FindStringSubmatch(out), dcIndexRE.FindStringSubmatch(out); a != nil && d != nil {
		av, err1 := strconv.ParseUint(a[1][2:], 16, 32)
		dv, err2 := strconv.ParseUint(d[1][2:], 16, 32)
		if err1 == nil && err2 == nil {
			return uint32(av), uint32(dv), nil
		}
	}
	m := hexTokenRE.FindAllString(out, -1)
	if len(m) < 2 {
		return 0, 0, fmt.Errorf("powercfg: no setting indexes in output: %q", strings.TrimSpace(out))
	}
	av, _ := strconv.ParseUint(m[len(m)-2][2:], 16, 32)
	dv, _ := strconv.ParseUint(m[len(m)-1][2:], 16, 32)
	return uint32(av), uint32(dv), nil
}

// parseActiveScheme returns the GUID and friendly name from
// `powercfg /getactivescheme`.
func parseActiveScheme(out string) (guid, name string) {
	m := schemeRE.FindStringSubmatch(out)
	if m == nil {
		return "", ""
	}
	return m[1], strings.TrimSpace(m[2])
}

// Action indexes shared by LIDACTION / PBUTTONACTION: 0 do nothing, 1
// sleep, 2 hibernate, 3 shut down, 4 turn off the display (buttons only).
var actionByIndex = map[uint32]string{0: "nothing", 1: "sleep", 2: "hibernate", 3: "shutdown", 4: "displayOff"}

func actionFromIndex(i uint32) string {
	if a, ok := actionByIndex[i]; ok {
		return a
	}
	return "nothing"
}

func indexFromAction(a string) (uint32, bool) {
	for i, name := range actionByIndex {
		if name == a {
			return i, true
		}
	}
	return 0, false
}

// minutesFromSeconds converts a timeout index (seconds, 0 = never) to the
// model's minutes, rounding to the nearest minute but never rounding a
// non-zero timeout down to "never".
func minutesFromSeconds(sec uint32) int {
	if sec == 0 {
		return 0
	}
	m := int((sec + 30) / 60)
	if m == 0 {
		m = 1
	}
	return m
}

// powercfgOp is one planned write: the commands to run, whether the scheme
// must be re-activated afterwards and whether the command needs elevation.
type powercfgOp struct {
	Key       string
	Cmds      [][]string
	SetActive bool
	Elevated  bool
}

// planPowercfg translates flattened, validated changes into powercfg
// operations. Keys Windows cannot map are returned as failures. lidHibernate
// reports whether the hibernate index is valid for lid/button actions
// (false when the hiberfile is off).
func planPowercfg(changes map[string]any, lidHibernate bool) (ops []powercfgOp, failed []ApplyFailure) {
	timers := map[string]string{
		"onBattery.displaySleepMin": "monitor-timeout-dc",
		"onBattery.systemSleepMin":  "standby-timeout-dc",
		"onPower.displaySleepMin":   "monitor-timeout-ac",
		"onPower.systemSleepMin":    "standby-timeout-ac",
	}
	for _, key := range sortedKeys(changes) {
		v, err := validateChange(key, changes[key])
		if err != nil {
			failed = append(failed, ApplyFailure{Key: key, Reason: err.Error()})
			continue
		}
		switch {
		case timers[key] != "":
			ops = append(ops, powercfgOp{Key: key, Cmds: [][]string{{"/change", timers[key], strconv.Itoa(v.(int))}}})
		case key == "onBattery.lidClose" || key == "onPower.lidClose":
			action := v.(string)
			if action == "displayOff" {
				failed = append(failed, ApplyFailure{Key: key, Reason: "the lid cannot turn off the display on Windows"})
				continue
			}
			if action == "hibernate" && !lidHibernate {
				failed = append(failed, ApplyFailure{Key: key, Reason: "hibernation is disabled; enable it first"})
				continue
			}
			idx, _ := indexFromAction(action)
			verb := "/setacvalueindex"
			if key == "onBattery.lidClose" {
				verb = "/setdcvalueindex"
			}
			ops = append(ops, powercfgOp{Key: key, SetActive: true, Cmds: [][]string{{verb, schemeCurrent, subButtons, lidAction, strconv.Itoa(int(idx))}}})
		case key == "powerButton":
			action := v.(string)
			if action == "hibernate" && !lidHibernate {
				failed = append(failed, ApplyFailure{Key: key, Reason: "hibernation is disabled; enable it first"})
				continue
			}
			idx, _ := indexFromAction(action)
			n := strconv.Itoa(int(idx))
			ops = append(ops, powercfgOp{Key: key, SetActive: true, Cmds: [][]string{
				{"/setacvalueindex", schemeCurrent, subButtons, pButtonAction, n},
				{"/setdcvalueindex", schemeCurrent, subButtons, pButtonAction, n},
			}})
		case key == "lidOpenWakes":
			n := "0"
			if v.(bool) {
				n = "1"
			}
			ops = append(ops, powercfgOp{Key: key, SetActive: true, Cmds: [][]string{
				{"/setacvalueindex", schemeCurrent, subButtons, lidOpenWake, n},
				{"/setdcvalueindex", schemeCurrent, subButtons, lidOpenWake, n},
			}})
		case key == "hibernate.enabled":
			state := "off"
			if v.(bool) {
				state = "on"
			}
			ops = append(ops, powercfgOp{Key: key, Elevated: true, Cmds: [][]string{{"/hibernate", state}}})
		case key == "hibernate.mode":
			mode := v.(string)
			if mode != "full" && mode != "reduced" {
				failed = append(failed, ApplyFailure{Key: key, Reason: "hibernate mode must be full or reduced"})
				continue
			}
			ops = append(ops, powercfgOp{Key: key, Elevated: true, Cmds: [][]string{{"/hibernate", "/type", mode}}})
		default:
			failed = append(failed, ApplyFailure{Key: key, Reason: "not supported on Windows"})
		}
	}
	return ops, failed
}

// schtasksCreateArgs builds the `schtasks /create` arguments for one
// recurring schedule item. exe is this program's path (used for the sleep
// action, which has no built-in command line). The task name is
// "Vitals\<id>" so the items live in their own Task Scheduler folder.
func schtasksCreateArgs(it ScheduleItem, exe string) ([]string, error) {
	var run string
	switch it.Type {
	case "shutdown":
		run = `shutdown.exe /s /t 60 /c "Vitals scheduled shutdown"`
	case "restart":
		run = `shutdown.exe /r /t 60 /c "Vitals scheduled restart"`
	case "hibernate":
		run = `shutdown.exe /h`
	case "sleep":
		if exe == "" {
			return nil, errors.New("sleep schedule needs the Vitals executable path")
		}
		run = `"` + exe + `" --power-action sleep`
	default:
		return nil, fmt.Errorf("unsupported schedule type %q", it.Type)
	}
	if !timeRE.MatchString(it.Time) {
		return nil, fmt.Errorf("bad time %q", it.Time)
	}
	args := []string{"/create", "/tn", scheduleTaskName(it.ID), "/tr", run, "/st", it.Time, "/f"}
	if len(it.Days) == 0 || len(it.Days) == 7 {
		args = append(args, "/sc", "daily")
	} else {
		days := make([]string, 0, len(it.Days))
		for _, d := range it.Days {
			days = append(days, strings.ToUpper(d))
		}
		args = append(args, "/sc", "weekly", "/d", strings.Join(days, ","))
	}
	return args, nil
}

// schtasksDeleteArgs removes the task of a schedule item.
func schtasksDeleteArgs(id string) []string {
	return []string{"/delete", "/tn", scheduleTaskName(id), "/f"}
}

// scheduleTaskName sanitizes an item id into a task name.
func scheduleTaskName(id string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '-'
	}, id)
	if clean == "" {
		clean = "item"
	}
	return `Vitals\` + clean
}
