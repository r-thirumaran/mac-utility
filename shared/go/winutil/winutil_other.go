//go:build !windows

package winutil

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
)

// IsElevated reports whether the process runs as root.
func IsElevated() bool { return os.Geteuid() == 0 }

// RelaunchElevated is not supported outside Windows.
func RelaunchElevated(args ...string) error { return ErrNotSupported }

// SingleInstance takes an exclusive flock on <temp dir>/<name>.lock. first is
// false when another process holds the lock. release unlocks the file.
func SingleInstance(name string) (release func(), first bool, err error) {
	path := filepath.Join(os.TempDir(), name+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("winutil: lock file: %w", err)
	}
	switch err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); {
	case err == nil:
		first = true
	case errors.Is(err, syscall.EWOULDBLOCK):
		first = false
	default:
		f.Close()
		return nil, false, fmt.Errorf("winutil: flock: %w", err)
	}
	release = func() {
		if f != nil {
			if first {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			}
			_ = f.Close()
			f = nil
		}
	}
	return release, first, nil
}

// SetAutostart is not supported outside Windows.
func SetAutostart(appName, exePath string, enabled bool) error { return ErrNotSupported }

// Autostart always reports false outside Windows.
func Autostart(appName string) bool { return false }

// OpenURL opens url with xdg-open (Linux) or open (macOS).
func OpenURL(url string) error {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	cmd := exec.Command(opener, url)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("winutil: open url: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// RunHidden runs the program and returns its combined output.
func RunHidden(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}
