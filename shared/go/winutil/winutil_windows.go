//go:build windows

package winutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	modadvapi32              = windows.NewLazySystemDLL("advapi32.dll")
	procCheckTokenMembership = modadvapi32.NewProc("CheckTokenMembership")
)

// IsElevated reports whether the process token is a member of the
// Administrators group (CheckTokenMembership against the built-in
// Administrators SID), i.e. whether it runs elevated under UAC.
func IsElevated() bool {
	sid, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false
	}
	var isMember int32
	r1, _, _ := procCheckTokenMembership.Call(0, uintptr(unsafe.Pointer(sid)), uintptr(unsafe.Pointer(&isMember)))
	return r1 != 0 && isMember != 0
}

// RelaunchElevated starts a new copy of this executable with the "runas"
// verb (UAC prompt) and the given arguments. The caller normally exits
// afterwards. The user declining the prompt is returned as an error.
func RelaunchElevated(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("winutil: executable path: %w", err)
	}
	escaped := make([]string, len(args))
	for i, a := range args {
		escaped[i] = syscall.EscapeArg(a)
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(strings.Join(escaped, " "))
	cwd, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	if err := windows.ShellExecute(0, verb, file, params, cwd, windows.SW_SHOWNORMAL); err != nil {
		return fmt.Errorf("winutil: relaunch elevated: %w", err)
	}
	return nil
}

// SingleInstance creates the named mutex "Local\<name>". first is false when
// another process already holds it. release frees the mutex; call it on exit.
func SingleInstance(name string) (release func(), first bool, err error) {
	n, err := windows.UTF16PtrFromString(`Local\` + name)
	if err != nil {
		return nil, false, fmt.Errorf("winutil: mutex name: %w", err)
	}
	h, err := windows.CreateMutex(nil, false, n)
	switch {
	case err == nil:
		first = true
	case err == windows.ERROR_ALREADY_EXISTS:
		first = false
	default:
		return nil, false, fmt.Errorf("winutil: create mutex: %w", err)
	}
	release = func() {
		if h != 0 {
			_ = windows.CloseHandle(h)
			h = 0
		}
	}
	return release, first, nil
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// SetAutostart adds (enabled) or removes (disabled) the per-user Run entry
// appName pointing at exePath.
func SetAutostart(appName, exePath string, enabled bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("winutil: open Run key: %w", err)
	}
	defer k.Close()
	if enabled {
		if err := k.SetStringValue(appName, `"`+exePath+`"`); err != nil {
			return fmt.Errorf("winutil: set autostart: %w", err)
		}
		return nil
	}
	if err := k.DeleteValue(appName); err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("winutil: remove autostart: %w", err)
	}
	return nil
}

// Autostart reports whether a Run entry named appName exists.
func Autostart(appName string) bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(appName)
	return err == nil
}

// OpenURL opens url in the default browser (ShellExecute "open").
func OpenURL(url string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(url)
	if err != nil {
		return fmt.Errorf("winutil: url: %w", err)
	}
	if err := windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL); err != nil {
		return fmt.Errorf("winutil: open url: %w", err)
	}
	return nil
}

// RunHidden runs a console program (powercfg, powershell …) without flashing
// a console window and returns its combined output.
func RunHidden(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.CombinedOutput()
}
