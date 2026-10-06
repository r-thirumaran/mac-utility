//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
	"github.com/r-thirumaran/mac-utility/shared/go/winutil"
)

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	powrprof = windows.NewLazySystemDLL("powrprof.dll")

	procGetTickCount64   = kernel32.NewProc("GetTickCount64")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalFree       = kernel32.NewProc("GlobalFree")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	procGlobalSize       = kernel32.NewProc("GlobalSize")
	procLockWorkStation  = user32.NewProc("LockWorkStation")
	procSendMessageTOW   = user32.NewProc("SendMessageTimeoutW")
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procGetClipboardData = user32.NewProc("GetClipboardData")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procSetSuspendState  = powrprof.NewProc("SetSuspendState")
)

const (
	hwndBroadcast   = 0xFFFF
	wmSysCommand    = 0x0112
	scMonitorPower  = 0xF170
	monitorOff      = 2
	smtoAbortIfHung = 0x0002
	cfUnicodeText   = 13
	gmemMoveable    = 0x0002
	autostartName   = "Reach"
	maxClipboard    = 4 << 20
)

// winBackend is the real backend: SendInput injection (input_windows.go),
// GDI capture (screen_windows.go), Start Menu apps (apps_windows.go), Core
// Audio volume (volume_windows.go) and the power/clipboard calls below.
type winBackend struct {
	injector
	iconMu    sync.Mutex
	iconCache map[string]string
}

func newRealBackend() (Backend, error) {
	setDPIAware()
	return &winBackend{iconCache: map[string]string{}}, nil
}

func (w *winBackend) Platform() string             { return "windows" }
func (w *winBackend) Permissions() map[string]bool { return map[string]bool{} }

func (w *winBackend) HostInfo() (string, string, int64) {
	host, _ := os.Hostname()
	name := os.Getenv("USERNAME")
	if name == "" {
		if u, err := user.Current(); err == nil {
			name = u.Username
		}
	}
	ms, _, _ := procGetTickCount64.Call()
	return host, name, int64(ms / 1000)
}

func (w *winBackend) Media(action string) error {
	key, ok := mediaKeyOf[action]
	if !ok {
		return bridge.Errorf(bridge.CodeInvalidParams, "unknown media action %q", action)
	}
	return w.Key(key, nil, true, true)
}

// Power runs the action; the slow ones run detached so the bridge answers.
func (w *winBackend) Power(action string) error {
	switch action {
	case "lock":
		if r, _, e := procLockWorkStation.Call(); r == 0 {
			return fmt.Errorf("LockWorkStation: %v", e)
		}
	case "sleep":
		go suspend()
	case "displayOff":
		go displayOff()
	case "restart":
		if out, err := winutil.RunHidden(systemTool("shutdown.exe"), "/r", "/t", "0"); err != nil {
			return fmt.Errorf("shutdown /r: %v: %s", err, out)
		}
	case "shutdown":
		if out, err := winutil.RunHidden(systemTool("shutdown.exe"), "/s", "/t", "0"); err != nil {
			return fmt.Errorf("shutdown /s: %v: %s", err, out)
		}
	default:
		return bridge.Errorf(bridge.CodeInvalidParams, "unknown power action %q", action)
	}
	return nil
}

// systemTool is the full path of a System32 program (no PATH lookup).
func systemTool(name string) string {
	dir, err := windows.GetSystemDirectory()
	if err != nil || dir == "" {
		dir = `C:\Windows\System32`
	}
	return filepath.Join(dir, name)
}

// suspend puts the machine to sleep: SetSuspendState needs SeShutdownPrivilege
// enabled in the token (interactive users hold it disabled) and returns a
// BOOLEAN, of which only the low byte is defined. Modern Standby machines
// report ERROR_NOT_SUPPORTED; there, turning the display off is the sleep
// entry (docs/research/windows-power-management.md §6).
func suspend() {
	_ = enableShutdownPrivilege()
	r, _, e := procSetSuspendState.Call(0, 0, 0)
	if byte(r) == 0 && errors.Is(e, windows.ERROR_NOT_SUPPORTED) {
		displayOff()
	}
}

// displayOff broadcasts SC_MONITORPOWER with a timeout so a hung window
// cannot stall the call.
func displayOff() {
	_, _, _ = procSendMessageTOW.Call(hwndBroadcast, wmSysCommand, scMonitorPower, monitorOff, smtoAbortIfHung, 1000, 0)
}

func enableShutdownPrivilege() error {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok); err != nil {
		return err
	}
	defer tok.Close()
	name, _ := windows.UTF16PtrFromString("SeShutdownPrivilege")
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	if err := windows.LookupPrivilegeValue(nil, name, &tp.Privileges[0].Luid); err != nil {
		return err
	}
	tp.Privileges[0].Attributes = windows.SE_PRIVILEGE_ENABLED
	return windows.AdjustTokenPrivileges(tok, false, &tp, 0, nil, nil)
}

// openClipboard retries briefly because another app may hold the clipboard.
func openClipboard() error {
	for i := 0; i < 10; i++ {
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("clipboard is busy")
}

func (w *winBackend) ClipboardGet() (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := openClipboard(); err != nil {
		return "", err
	}
	defer procCloseClipboard.Call()
	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", nil // no text on the clipboard
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return "", fmt.Errorf("GlobalLock failed")
	}
	defer procGlobalUnlock.Call(h)
	size, _, _ := procGlobalSize.Call(h)
	if size > maxClipboard {
		size = maxClipboard
	}
	units := unsafe.Slice((*uint16)(ptrOf(p)), size/2)
	n := 0
	for n < len(units) && units[n] != 0 {
		n++
	}
	return string(utf16.Decode(units[:n])), nil
}

func (w *winBackend) ClipboardSet(text string) error {
	if len(text) > maxClipboard {
		return bridge.Errorf(bridge.CodeInvalidParams, "text too large")
	}
	units := utf16.Encode([]rune(text))
	units = append(units, 0)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := openClipboard(); err != nil {
		return err
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	size := uintptr(len(units) * 2)
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return fmt.Errorf("GlobalAlloc failed")
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("GlobalLock failed")
	}
	copy(unsafe.Slice((*uint16)(ptrOf(p)), len(units)), units)
	procGlobalUnlock.Call(h)
	if r, _, e := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("SetClipboardData: %v", e)
	}
	return nil // the system owns h now
}

// ptrOf turns a pointer returned by a Win32 call (GlobalLock) into an
// unsafe.Pointer; the memory is not managed by the Go GC.
func ptrOf(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }

func (w *winBackend) OpenURL(url string) error { return winutil.OpenURL(url) }

func (w *winBackend) LaunchAtLogin() bool { return winutil.Autostart(autostartName) }

func (w *winBackend) SetLaunchAtLogin(enabled bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return winutil.SetAutostart(autostartName, exe, enabled)
}
