//go:build windows

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/r-thirumaran/mac-utility/shared/go/appdirs"
	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
	"github.com/r-thirumaran/mac-utility/shared/go/winutil"
)

// Windows power backend (docs/research/windows-power-management.md):
// settings are read with `powercfg /q` and written with `powercfg /change`
// and `/set{ac,dc}valueindex` + `/setactive`; `powercfg /h` needs elevation
// and is reported with needsElevation so the UI can offer a relaunch.
// Actions use LockWorkStation, SetSuspendState, SC_MONITORPOWER and
// shutdown.exe; keep-awake is SetThreadExecutionState on a locked OS thread;
// schedules are Task Scheduler tasks under Vitals\.

var (
	modpowrprof               = windows.NewLazySystemDLL("powrprof.dll")
	moduser32                 = windows.NewLazySystemDLL("user32.dll")
	modadvapi32               = windows.NewLazySystemDLL("advapi32.dll")
	procSetSuspendState       = modpowrprof.NewProc("SetSuspendState")
	procGetPwrCapabilities    = modpowrprof.NewProc("GetPwrCapabilities")
	procPowerGetActiveScheme  = modpowrprof.NewProc("PowerGetActiveScheme")
	procPowerReadACValueIndex = modpowrprof.NewProc("PowerReadACValueIndex")
	procPowerReadDCValueIndex = modpowrprof.NewProc("PowerReadDCValueIndex")
	procPowerReadFriendlyName = modpowrprof.NewProc("PowerReadFriendlyName")
	procLockWorkStation       = moduser32.NewProc("LockWorkStation")
	procSendMessageTimeoutW   = moduser32.NewProc("SendMessageTimeoutW")
	procSetThreadExecState    = modkernel32.NewProc("SetThreadExecutionState")
	procAdjustTokenPrivilege  = modadvapi32.NewProc("AdjustTokenPrivileges")
)

// settingGUIDs are the power setting GUIDs behind the powercfg aliases of
// powercfg.go, for the powrprof reads (docs/research/windows-power-management.md §1).
var settingGUIDs = map[string]string{
	subButtons:    "{4F971E89-EEBD-4455-A8DE-9E59040E7347}",
	lidAction:     "{5CA83367-6E45-459F-A27B-476B1D01C936}",
	pButtonAction: "{7648EFA3-DD9C-4E3E-B566-50F929386280}",
	lidOpenWake:   "{99FF10E7-23B1-4C07-A9D1-5C3206D741B4}",
	subVideo:      "{7516B95F-F776-4464-8C53-06167F40CC99}",
	videoIdle:     "{3C0BC021-C8A8-4E07-A973-6B14CBCB2B7E}",
	subSleep:      "{238C9FA8-0AAD-41ED-83F4-97BE242C8F20}",
	standbyIdle:   "{29F6C1DB-86DA-48C5-9FDB-F2B67B1F44DA}",
}

// boolean reads a BOOLEAN (one byte) return value: the rest of the return
// register is undefined, so the whole uintptr must not be compared with 0.
func boolean(r uintptr) bool { return byte(r) != 0 }

const (
	esContinuous      = 0x80000000
	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002
	hwndBroadcast     = 0xFFFF
	wmSysCommand      = 0x0112
	scMonitorPower    = 0xF170
	smtoAbortIfHung   = 0x0002
)

// SYSTEM_POWER_CAPABILITIES (76 bytes, Windows 10 layout).
type systemPowerCapabilities struct {
	PowerButtonPresent, SleepButtonPresent, LidPresent, SystemS1, SystemS2, SystemS3, SystemS4, SystemS5   bool
	HiberFilePresent, FullWake, VideoDimPresent, ApmPresent, UpsPresent, ThermalControl, ProcessorThrottle bool
	ProcessorMinThrottle, ProcessorMaxThrottle                                                             uint8
	FastSystemS4, Hiberboot, WakeAlarmPresent, AoAc, DiskSpinDown                                          bool
	HiberFileType                                                                                          uint8
	AoAcConnectivitySupported                                                                              bool
	_                                                                                                      [6]byte
	SystemBatteriesPresent, BatteriesAreShortTerm                                                          bool
	BatteryScale                                                                                           [3]struct{ Granularity, Capacity uint32 }
	AcOnLineWake, SoftLidWake, RtcWake, MinDeviceWakeState, DefaultLowLatencyWake                          int32
}

type windowsPower struct {
	mu           sync.Mutex
	sysDir       string
	schedulePath string
	awake        *awakeKeeper
}

func newPower(fake bool) Power {
	if fake {
		return newFakePower()
	}
	sysDir, err := windows.GetSystemDirectory()
	if err != nil || sysDir == "" {
		sysDir = `C:\Windows\System32`
	}
	p := &windowsPower{sysDir: sysDir, awake: newAwakeKeeper()}
	if dir, err := appdirs.ConfigDir("Vitals"); err == nil {
		p.schedulePath = filepath.Join(dir, "schedule.json")
	}
	return p
}

// run executes a System32 console tool without a window.
func (p *windowsPower) run(tool string, args ...string) (string, error) {
	out, err := winutil.RunHidden(filepath.Join(p.sysDir, tool), args...)
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text != "" {
			return text, fmt.Errorf("%s %s: %w: %s", tool, strings.Join(args, " "), err, text)
		}
		return text, fmt.Errorf("%s %s: %w", tool, strings.Join(args, " "), err)
	}
	return text, nil
}

// queryIndex reads the AC and DC value indexes of a setting of the active
// scheme: through powrprof (no child process, no localized text), falling
// back to `powercfg /q` and its parser when the API is unavailable.
func (p *windowsPower) queryIndex(sub, setting string) (ac, dc uint32, err error) {
	if ac, dc, err := readIndexAPI(sub, setting); err == nil {
		return ac, dc, nil
	}
	out, err := p.run("powercfg.exe", "/q", schemeCurrent, sub, setting)
	if err != nil {
		return 0, 0, err
	}
	return parsePowercfgIndex(out)
}

// withActiveScheme runs fn with the active scheme GUID from
// PowerGetActiveScheme (freed with LocalFree).
func withActiveScheme(fn func(scheme *windows.GUID) error) error {
	var scheme *windows.GUID
	if r, _, _ := procPowerGetActiveScheme.Call(0, uintptr(unsafe.Pointer(&scheme))); uint32(r) != 0 {
		return fmt.Errorf("PowerGetActiveScheme: %w", windows.Errno(uint32(r)))
	}
	if scheme == nil {
		return errors.New("PowerGetActiveScheme: no scheme")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(scheme)))
	return fn(scheme)
}

// readIndexAPI is PowerReadACValueIndex + PowerReadDCValueIndex for a
// (subgroup, setting) alias pair of powercfg.go.
func readIndexAPI(sub, setting string) (ac, dc uint32, err error) {
	subGUID, err := windows.GUIDFromString(settingGUIDs[sub])
	if err != nil {
		return 0, 0, fmt.Errorf("subgroup %s: %w", sub, err)
	}
	setGUID, err := windows.GUIDFromString(settingGUIDs[setting])
	if err != nil {
		return 0, 0, fmt.Errorf("setting %s: %w", setting, err)
	}
	err = withActiveScheme(func(scheme *windows.GUID) error {
		for _, rd := range []struct {
			proc *windows.LazyProc
			out  *uint32
		}{{procPowerReadACValueIndex, &ac}, {procPowerReadDCValueIndex, &dc}} {
			r, _, _ := rd.proc.Call(0, uintptr(unsafe.Pointer(scheme)), uintptr(unsafe.Pointer(&subGUID)), uintptr(unsafe.Pointer(&setGUID)), uintptr(unsafe.Pointer(rd.out)))
			if uint32(r) != 0 {
				return fmt.Errorf("%s %s/%s: %w", rd.proc.Name, sub, setting, windows.Errno(uint32(r)))
			}
		}
		return nil
	})
	return ac, dc, err
}

// activeSchemeName is the friendly name of the active scheme through
// PowerReadFriendlyName (a NULL subgroup and setting name the scheme).
func activeSchemeName() (string, error) {
	var name string
	err := withActiveScheme(func(scheme *windows.GUID) error {
		var size uint32
		r, _, _ := procPowerReadFriendlyName.Call(0, uintptr(unsafe.Pointer(scheme)), 0, 0, 0, uintptr(unsafe.Pointer(&size)))
		if st := uint32(r); st != 0 && st != uint32(windows.ERROR_MORE_DATA) {
			return fmt.Errorf("PowerReadFriendlyName: %w", windows.Errno(st))
		}
		if size < 2 {
			return errors.New("PowerReadFriendlyName: empty name")
		}
		buf := make([]uint16, size/2+1)
		r, _, _ = procPowerReadFriendlyName.Call(0, uintptr(unsafe.Pointer(scheme)), 0, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
		if uint32(r) != 0 {
			return fmt.Errorf("PowerReadFriendlyName: %w", windows.Errno(uint32(r)))
		}
		name = strings.TrimSpace(windows.UTF16ToString(buf))
		return nil
	})
	return name, err
}

func capabilities() (systemPowerCapabilities, bool) {
	var c systemPowerCapabilities
	if r, _, _ := procGetPwrCapabilities.Call(uintptr(unsafe.Pointer(&c))); !boolean(r) {
		return c, false
	}
	return c, true
}

// readHibernate reads the hiberfile state from the registry (no shell, no
// localization): HibernateEnabled and HiberFileType (1 reduced, 2 full).
func readHibernate() (enabled bool, mode string) {
	mode = "full"
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Power`, registry.QUERY_VALUE)
	if err != nil {
		return false, mode
	}
	defer k.Close()
	if v, _, err := k.GetIntegerValue("HibernateEnabled"); err == nil {
		enabled = v != 0
	}
	if v, _, err := k.GetIntegerValue("HiberFileType"); err == nil && v == 1 {
		mode = "reduced"
	}
	return enabled, mode
}

func (p *windowsPower) Get() (PowerModel, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	caps, capsOK := capabilities()
	hibEnabled, hibMode := readHibernate()
	battery := readBatteryStatus()
	m := PowerModel{
		Hibernate: &HibernateSetting{Enabled: hibEnabled, Mode: hibMode},
		Elevated:  winutil.IsElevated(),
		KeepAwake: p.awake.State(),
	}
	lidChoices := []string{"nothing", "sleep", "shutdown"}
	buttonChoices := []string{"nothing", "sleep", "shutdown", "displayOff"}
	if hibEnabled && (!capsOK || caps.SystemS4) {
		lidChoices = []string{"nothing", "sleep", "hibernate", "shutdown"}
		buttonChoices = powerButtonActions
	}
	m.Supported = PowerSupport{
		Battery:            battery != nil && battery.Present,
		Lid:                !capsOK || caps.LidPresent,
		LidClose:           lidChoices,
		LidOpenWakes:       true,
		Hibernate:          !capsOK || caps.SystemS4,
		HibernateModes:     []string{"full", "reduced"},
		PowerButton:        true,
		PowerButtonActions: buttonChoices,
		DisplaySleep:       true,
		SystemSleep:        true,
		KeepAwake:          true,
		Actions:            powerActions,
		ScheduleTypes:      []string{"sleep", "shutdown", "restart", "hibernate"},
	}
	var errs []string
	if ac, dc, err := p.queryIndex(subButtons, lidAction); err == nil {
		m.OnPower.LidClose, m.OnBattery.LidClose = actionFromIndex(ac), actionFromIndex(dc)
	} else {
		errs = append(errs, err.Error())
	}
	if ac, _, err := p.queryIndex(subButtons, lidOpenWake); err == nil {
		m.LidOpenWakes = ac != 0
	}
	if ac, _, err := p.queryIndex(subButtons, pButtonAction); err == nil {
		m.PowerButton = actionFromIndex(ac)
	}
	if ac, dc, err := p.queryIndex(subVideo, videoIdle); err == nil {
		m.OnPower.DisplaySleepMin, m.OnBattery.DisplaySleepMin = minutesFromSeconds(ac), minutesFromSeconds(dc)
	} else {
		errs = append(errs, err.Error())
	}
	if ac, dc, err := p.queryIndex(subSleep, standbyIdle); err == nil {
		m.OnPower.SystemSleepMin, m.OnBattery.SystemSleepMin = minutesFromSeconds(ac), minutesFromSeconds(dc)
	} else {
		errs = append(errs, err.Error())
	}
	if name, err := activeSchemeName(); err == nil && name != "" {
		m.Scheme = name
	} else if out, err := p.run("powercfg.exe", "/getactivescheme"); err == nil {
		if _, name := parseActiveScheme(out); name != "" {
			m.Scheme = name
		}
	}
	if len(errs) > 0 && m.OnPower.LidClose == "" {
		return m, bridge.Errorf(bridge.CodeInternal, "powercfg: %s", strings.Join(errs, "; "))
	}
	return m, nil
}

func (p *windowsPower) Apply(changes map[string]any) (ApplyResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	hibEnabled, _ := readHibernate()
	flat := flattenChanges(changes)
	// A hibernate toggle in the same batch makes the hibernate choice valid.
	if v, ok := flat["hibernate.enabled"]; ok {
		if b, ok := asBool(v); ok {
			hibEnabled = b
		}
	}
	ops, failed := planPowercfg(flat, hibEnabled)
	res := ApplyResult{Applied: []string{}, Failed: append([]ApplyFailure{}, failed...)}
	elevated := winutil.IsElevated()
	setActive := false
	for _, op := range ops {
		if op.Elevated && !elevated {
			res.Failed = append(res.Failed, ApplyFailure{Key: op.Key, Reason: "needs administrator rights", NeedsElevation: true})
			res.NeedsElevation = true
			continue
		}
		var opErr error
		for _, cmd := range op.Cmds {
			if _, err := p.run("powercfg.exe", cmd...); err != nil {
				opErr = err
				break
			}
		}
		if opErr != nil {
			f := ApplyFailure{Key: op.Key, Reason: opErr.Error()}
			if !elevated && errors.Is(opErr, fs.ErrPermission) || strings.Contains(strings.ToLower(opErr.Error()), "access") {
				f.NeedsElevation = !elevated
				f.Reason = "access denied (a policy may lock this setting)"
				res.NeedsElevation = res.NeedsElevation || f.NeedsElevation
			}
			res.Failed = append(res.Failed, f)
			continue
		}
		setActive = setActive || op.SetActive
		res.Applied = append(res.Applied, op.Key)
	}
	if setActive {
		if _, err := p.run("powercfg.exe", "/setactive", schemeCurrent); err != nil {
			res.Failed = append(res.Failed, ApplyFailure{Key: "scheme", Reason: err.Error()})
		}
	}
	if res.NeedsElevation && len(res.Applied) == 0 {
		return res, &bridge.Error{Code: bridge.CodeNotAuthorized, Message: "changing hibernation needs administrator rights",
			Details: map[string]any{"needsElevation": true, "failed": res.Failed}}
	}
	return res, nil
}

func (p *windowsPower) Action(action string) error {
	switch action {
	case "lock":
		if r, _, e := procLockWorkStation.Call(); r == 0 {
			return fmt.Errorf("LockWorkStation: %w", e)
		}
		return nil
	case "displayOff":
		go func() {
			time.Sleep(400 * time.Millisecond) // let the click's key-up pass
			displayOff()
		}()
		return nil
	case "sleep":
		if err := enablePrivilege("SeShutdownPrivilege"); err != nil {
			return bridge.Errorf(bridge.CodeNotAuthorized, "shutdown privilege: %v", err)
		}
		r, _, e := procSetSuspendState.Call(0, 0, 0)
		if !boolean(r) {
			if errors.Is(e, windows.ERROR_NOT_SUPPORTED) { // Modern Standby: display off is the sleep entry
				displayOff()
				return nil
			}
			return fmt.Errorf("SetSuspendState: %w", e)
		}
		return nil
	case "restart":
		_, err := p.run("shutdown.exe", "/r", "/t", "0")
		return err
	case "shutdown":
		_, err := p.run("shutdown.exe", "/s", "/t", "0")
		return err
	}
	return bridge.Errorf(bridge.CodeInvalidParams, "unknown action %q", action)
}

func displayOff() {
	procSendMessageTimeoutW.Call(hwndBroadcast, wmSysCommand, scMonitorPower, 2, smtoAbortIfHung, 1000, 0)
}

// enablePrivilege turns a privilege on in the process token; the raw call is
// needed because ERROR_NOT_ALL_ASSIGNED arrives with a TRUE return.
func enablePrivilege(name string) error {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok); err != nil {
		return err
	}
	defer tok.Close()
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr(name), &tp.Privileges[0].Luid); err != nil {
		return err
	}
	tp.Privileges[0].Attributes = windows.SE_PRIVILEGE_ENABLED
	r, _, e := procAdjustTokenPrivilege.Call(uintptr(tok), 0, uintptr(unsafe.Pointer(&tp)), 0, 0, 0)
	if r == 0 {
		return e
	}
	if errors.Is(e, windows.ERROR_NOT_ALL_ASSIGNED) {
		return e
	}
	return nil
}

func (p *windowsPower) KeepAwake(enabled bool, mode string) (KeepAwakeState, error) {
	if mode == "" {
		mode = "system"
	}
	if !contains(keepAwakeModes, mode) {
		return KeepAwakeState{}, bridge.Errorf(bridge.CodeInvalidParams, "keep-awake mode must be system or display")
	}
	p.awake.Set(enabled, mode)
	return p.awake.State(), nil
}

func (p *windowsPower) AwakeState() KeepAwakeState { return p.awake.State() }

// awakeKeeper owns the OS thread whose execution state keeps the machine
// awake; the state dies with the thread, so the goroutine stays locked and
// alive for as long as keep-awake is on.
type awakeKeeper struct {
	mu    sync.Mutex
	state KeepAwakeState
	flags chan uintptr
	stop  chan struct{}
}

func newAwakeKeeper() *awakeKeeper { return &awakeKeeper{state: KeepAwakeState{Mode: "system"}} }

func (a *awakeKeeper) State() KeepAwakeState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *awakeKeeper) Set(enabled bool, mode string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = KeepAwakeState{Enabled: enabled, Mode: mode}
	if !enabled {
		if a.stop != nil {
			close(a.stop)
			a.stop, a.flags = nil, nil
		}
		return
	}
	var f uintptr = esContinuous | esSystemRequired
	if mode == "display" {
		f |= esDisplayRequired
	}
	if a.stop == nil {
		a.stop, a.flags = make(chan struct{}), make(chan uintptr, 1)
		go a.loop(a.flags, a.stop, f)
		return
	}
	// Only the latest flags matter: replace a pending update instead of
	// dropping this one.
	select {
	case <-a.flags:
	default:
	}
	a.flags <- f
}

func (a *awakeKeeper) loop(flags <-chan uintptr, stop <-chan struct{}, initial uintptr) {
	runtime.LockOSThread() // never unlocked: the thread owns the ES_ state
	procSetThreadExecState.Call(initial)
	for {
		select {
		case f := <-flags:
			procSetThreadExecState.Call(f)
		case <-stop:
			procSetThreadExecState.Call(esContinuous)
			return
		}
	}
}

// ------------------------------------------------------------- schedules

func (p *windowsPower) loadSchedule() []ScheduleItem {
	var items []ScheduleItem
	if p.schedulePath == "" {
		return []ScheduleItem{}
	}
	if err := appdirs.LoadJSON(p.schedulePath, &items); err != nil || items == nil {
		return []ScheduleItem{}
	}
	return items
}

func (p *windowsPower) ScheduleGet() ([]ScheduleItem, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.loadSchedule(), nil
}

// ScheduleSet replaces every Vitals task: the previously stored items are
// deleted from Task Scheduler, the new enabled ones created, and the list
// persisted next to the config.
func (p *windowsPower) ScheduleSet(items []ScheduleItem) ([]ScheduleItem, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	clean, err := validateSchedule(items, []string{"sleep", "shutdown", "restart", "hibernate"})
	if err != nil {
		return nil, bridge.Errorf(bridge.CodeInvalidParams, "%v", err)
	}
	for _, old := range p.loadSchedule() {
		_, _ = p.run("schtasks.exe", schtasksDeleteArgs(old.ID)...)
	}
	exe, _ := os.Executable()
	var errs []string
	for _, it := range clean {
		if !it.Enabled {
			continue
		}
		args, err := schtasksCreateArgs(it, exe)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if _, err := p.run("schtasks.exe", args...); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if p.schedulePath != "" {
		if err := appdirs.SaveJSON(p.schedulePath, clean); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return clean, bridge.Errorf(bridge.CodeInternal, "schedule: %s", strings.Join(errs, "; "))
	}
	return clean, nil
}

// openSettingsPane deep-links into Windows Settings (ms-settings: URIs).
func openSettingsPane(pane string) error {
	uri := map[string]string{
		"":        "ms-settings:powersleep",
		"power":   "ms-settings:powersleep",
		"sleep":   "ms-settings:powersleep",
		"battery": "ms-settings:batterysaver",
		"startup": "ms-settings:startupapps",
	}[pane]
	if uri == "" {
		uri = "ms-settings:powersleep"
	}
	return winutil.OpenURL(uri)
}
