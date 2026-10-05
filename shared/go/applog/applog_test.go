package applog

import (
	"runtime"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "vitals.log")
	lg, closeFn, err := OpenFile(path, 300)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 40)
	for i := 0; i < 30; i++ {
		lg.Printf("%02d %s", i, line)
	}
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}
	if err := closeFn(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	main, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if len(main) > 300 || len(backup) > 300 || len(backup) == 0 {
		t.Fatalf("sizes main=%d backup=%d", len(main), len(backup))
	}
	if !strings.Contains(string(main), "29 "+line) {
		t.Fatalf("last line missing from current file: %q", main)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 2 {
		t.Fatalf("expected exactly log + one backup, got %v", entries)
	}
	st, _ := os.Stat(path)
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 { // Windows has no POSIX modes
		t.Fatalf("mode %o", st.Mode().Perm())
	}

	// Reopening appends to the existing file and keeps rotating.
	lg2, close2, err := OpenFile(path, 300)
	if err != nil {
		t.Fatal(err)
	}
	lg2.Print("appended")
	close2()
	main2, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(main2), string(main)) || !strings.Contains(string(main2), "appended") {
		t.Fatal("reopen did not append")
	}
}

func TestOpenUsesLogDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_STATE_HOME", filepath.Join(tmp, "state"))
	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "local"))
	t.Setenv("APPDATA", filepath.Join(tmp, "roaming"))
	lg, closeFn, err := Open("Edgehop")
	if err != nil {
		t.Fatal(err)
	}
	lg.Print("hello")
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(tmp, "*", "*", "edgehop.log"))
	if len(matches) != 1 {
		t.Fatalf("log file not found under %s: %v", tmp, matches)
	}
	data, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(data), "hello") {
		t.Fatalf("content %q", data)
	}
}

func TestWriteAfterClose(t *testing.T) {
	r, err := newRotating(filepath.Join(t.TempDir(), "a.log"), 10)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if _, err := r.Write([]byte("x")); err == nil {
		t.Fatal("write after close must fail")
	}
}
