//go:build windows

package main

import (
	"testing"
	"unsafe"
)

func TestPowerLayouts(t *testing.T) {
	var c systemPowerCapabilities
	if s := unsafe.Sizeof(c); s != 76 {
		t.Errorf("sizeof SYSTEM_POWER_CAPABILITIES = %d, want 76", s)
	}
	if o := unsafe.Offsetof(c.BatteryScale); o != 32 {
		t.Errorf("BatteryScale offset = %d, want 32", o)
	}
	if o := unsafe.Offsetof(c.AcOnLineWake); o != 56 {
		t.Errorf("AcOnLineWake offset = %d, want 56", o)
	}
	if o := unsafe.Offsetof(c.HiberFileType); o != 22 {
		t.Errorf("HiberFileType offset = %d, want 22", o)
	}
}

// TestPowerReads runs the powrprof reads on the real OS and cross-checks the
// API value indexes against the parsed `powercfg /q` text.
func TestPowerReads(t *testing.T) {
	if _, ok := capabilities(); !ok {
		t.Error("GetPwrCapabilities failed")
	}
	enabled, mode := readHibernate()
	t.Logf("hibernate enabled=%v mode=%s", enabled, mode)
	ac, dc, err := readIndexAPI(subVideo, videoIdle)
	if err != nil {
		t.Fatalf("PowerReadACValueIndex/DC: %v", err)
	}
	p := newPower(false).(*windowsPower)
	if out, err := p.run("powercfg.exe", "/q", schemeCurrent, subVideo, videoIdle); err == nil {
		pac, pdc, perr := parsePowercfgIndex(out)
		if perr != nil {
			t.Errorf("powercfg /q parse: %v", perr)
		} else if pac != ac || pdc != dc {
			t.Errorf("API says AC=%d DC=%d, powercfg /q says AC=%d DC=%d", ac, dc, pac, pdc)
		}
	} else {
		t.Logf("powercfg /q unavailable: %v", err)
	}
	if name, err := activeSchemeName(); err != nil || name == "" {
		t.Errorf("PowerReadFriendlyName: %q, %v", name, err)
	}
	if err := enablePrivilege("SeShutdownPrivilege"); err != nil {
		t.Logf("SeShutdownPrivilege: %v", err)
	}
	m, err := p.Get()
	if err != nil {
		t.Logf("power.get: %v", err)
	}
	if len(m.Supported.Actions) == 0 || m.Hibernate == nil || m.Scheme == "" {
		t.Errorf("power model %+v", m)
	}
	st, err := p.KeepAwake(true, "display")
	if err != nil || !st.Enabled || st.Mode != "display" {
		t.Fatalf("keep awake on: %+v, %v", st, err)
	}
	st, err = p.KeepAwake(true, "system")
	if err != nil || !st.Enabled || st.Mode != "system" {
		t.Fatalf("keep awake switch: %+v, %v", st, err)
	}
	st, err = p.KeepAwake(false, "")
	if err != nil || st.Enabled {
		t.Fatalf("keep awake off: %+v, %v", st, err)
	}
	if _, err := p.ScheduleGet(); err != nil {
		t.Error(err)
	}
}
