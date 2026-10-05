// Package applog opens the per-app log file of docs/ARCHITECTURE.md §8
// (%LOCALAPPDATA%\<App>\<app>.log on Windows, the XDG state dir on Linux)
// with size-based rotation: when the file would exceed MaxSize it is renamed
// to <app>.log.1 (replacing the previous backup) and a new file is started.
// When stdout is a terminal the log is also written to stderr so `--no-ui`
// runs show it live.
package applog

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/r-thirumaran/mac-utility/shared/go/appdirs"
)

// MaxSize is the rotation threshold in bytes.
const MaxSize = 5 << 20

// Open creates (or appends to) <LogDir(app)>/<app>.log and returns a logger,
// a function that flushes and closes the file, and any error.
func Open(app string) (*log.Logger, func() error, error) {
	dir, err := appdirs.LogDir(app)
	if err != nil {
		return nil, nil, err
	}
	return OpenFile(filepath.Join(dir, strings.ToLower(app)+".log"), MaxSize)
}

// OpenFile is Open for an explicit path and rotation size.
func OpenFile(path string, maxSize int64) (*log.Logger, func() error, error) {
	w, err := newRotating(path, maxSize)
	if err != nil {
		return nil, nil, err
	}
	var out io.Writer = w
	if isTerminal(os.Stdout) {
		out = io.MultiWriter(w, os.Stderr)
	}
	return log.New(out, "", log.LstdFlags|log.Lmicroseconds), w.Close, nil
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

type rotating struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64
}

func newRotating(path string, max int64) (*rotating, error) {
	if max <= 0 {
		max = MaxSize
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("applog: create dir: %w", err)
	}
	r := &rotating{path: path, max: max}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotating) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("applog: open %s: %w", r.path, err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("applog: stat: %w", err)
	}
	r.f, r.size = f, st.Size()
	return nil
}

// Write implements io.Writer, rotating first when p would push the file past
// the limit. A single write larger than the limit goes into a fresh file.
func (r *rotating) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, os.ErrClosed
	}
	if r.size > 0 && r.size+int64(len(p)) > r.max {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotating) rotate() error {
	if err := r.f.Close(); err != nil {
		return fmt.Errorf("applog: close for rotation: %w", err)
	}
	r.f = nil
	backup := r.path + ".1"
	_ = os.Remove(backup)
	if err := os.Rename(r.path, backup); err != nil {
		return fmt.Errorf("applog: rotate: %w", err)
	}
	return r.open()
}

// Close syncs and closes the file. Later writes return os.ErrClosed.
func (r *rotating) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	_ = r.f.Sync()
	err := r.f.Close()
	r.f = nil
	return err
}
