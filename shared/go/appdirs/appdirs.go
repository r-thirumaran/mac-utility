// Package appdirs resolves the per-app directories of docs/ARCHITECTURE.md §8
// and reads/writes JSON files atomically with private permissions.
//
//	            config                              state / logs
//	windows     %APPDATA%\<App>                     %LOCALAPPDATA%\<App>
//	darwin      ~/Library/Application Support/<App> same / ~/Library/Logs/<App>
//	linux       $XDG_CONFIG_HOME/<app>              $XDG_STATE_HOME/<app>
//
// The app name is used as given on Windows and macOS ("Vitals") and
// lower-cased on Linux ("vitals"). Directories are created (0700) on first use.
package appdirs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ConfigDir returns the directory for config.json and pairing keys.
func ConfigDir(app string) (string, error) {
	d, err := dirs(runtime.GOOS, os.Getenv, app)
	if err != nil {
		return "", err
	}
	return ensure(d.config)
}

// StateDir returns the directory for mutable state (caches, databases).
func StateDir(app string) (string, error) {
	d, err := dirs(runtime.GOOS, os.Getenv, app)
	if err != nil {
		return "", err
	}
	return ensure(d.state)
}

// LogDir returns the directory for log files.
func LogDir(app string) (string, error) {
	d, err := dirs(runtime.GOOS, os.Getenv, app)
	if err != nil {
		return "", err
	}
	return ensure(d.log)
}

type dirSet struct{ config, state, log string }

// dirs computes the three directories for goos from the environment; it is
// separated from the OS so tests can cover every platform.
func dirs(goos string, env func(string) string, app string) (dirSet, error) {
	if app == "" {
		return dirSet{}, errors.New("appdirs: empty app name")
	}
	home := env("HOME")
	if goos == "windows" && home == "" {
		home = env("USERPROFILE")
	}
	switch goos {
	case "windows":
		appData, local := env("APPDATA"), env("LOCALAPPDATA")
		if appData == "" {
			if home == "" {
				return dirSet{}, errors.New("appdirs: neither APPDATA nor USERPROFILE is set")
			}
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		if local == "" {
			if home == "" {
				return dirSet{}, errors.New("appdirs: neither LOCALAPPDATA nor USERPROFILE is set")
			}
			local = filepath.Join(home, "AppData", "Local")
		}
		return dirSet{
			config: filepath.Join(appData, app),
			state:  filepath.Join(local, app),
			log:    filepath.Join(local, app),
		}, nil
	case "darwin":
		if home == "" {
			return dirSet{}, errors.New("appdirs: HOME is not set")
		}
		support := filepath.Join(home, "Library", "Application Support", app)
		return dirSet{config: support, state: support, log: filepath.Join(home, "Library", "Logs", app)}, nil
	default:
		name := strings.ToLower(app)
		config, state := env("XDG_CONFIG_HOME"), env("XDG_STATE_HOME")
		if config == "" || state == "" {
			if home == "" {
				return dirSet{}, errors.New("appdirs: HOME is not set")
			}
			if config == "" {
				config = filepath.Join(home, ".config")
			}
			if state == "" {
				state = filepath.Join(home, ".local", "state")
			}
		}
		return dirSet{
			config: filepath.Join(config, name),
			state:  filepath.Join(state, name),
			log:    filepath.Join(state, name),
		}, nil
	}
}

func ensure(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("appdirs: create %s: %w", dir, err)
	}
	return dir, nil
}

// LoadJSON decodes the file at path into v. A missing file returns an error
// satisfying errors.Is(err, fs.ErrNotExist).
func LoadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("appdirs: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("appdirs: parse %s: %w", path, err)
	}
	return nil
}

// SaveJSON writes v as indented JSON to path with mode 0600, creating the
// parent directory, via a temporary file in the same directory that is fsynced
// and renamed into place so a crash never leaves a truncated file.
func SaveJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("appdirs: encode %s: %w", path, err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("appdirs: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("appdirs: temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		tmp.Close()
		cleanup()
		return fmt.Errorf("appdirs: chmod: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("appdirs: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("appdirs: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("appdirs: close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("appdirs: rename: %w", err)
	}
	return nil
}
