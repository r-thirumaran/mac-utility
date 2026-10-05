package appdirs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDirsPerPlatform(t *testing.T) {
	j := filepath.Join
	cases := []struct {
		name   string
		goos   string
		env    map[string]string
		config string
		state  string
		log    string
	}{
		{"windows", "windows", map[string]string{"APPDATA": `C:\U\Roaming`, "LOCALAPPDATA": `C:\U\Local`},
			j(`C:\U\Roaming`, "Vitals"), j(`C:\U\Local`, "Vitals"), j(`C:\U\Local`, "Vitals")},
		{"windows fallback", "windows", map[string]string{"USERPROFILE": `C:\Users\t`},
			j(`C:\Users\t`, "AppData", "Roaming", "Vitals"), j(`C:\Users\t`, "AppData", "Local", "Vitals"), j(`C:\Users\t`, "AppData", "Local", "Vitals")},
		{"darwin", "darwin", map[string]string{"HOME": "/Users/t"},
			"/Users/t/Library/Application Support/Vitals", "/Users/t/Library/Application Support/Vitals", "/Users/t/Library/Logs/Vitals"},
		{"linux xdg", "linux", map[string]string{"HOME": "/home/t", "XDG_CONFIG_HOME": "/cfg", "XDG_STATE_HOME": "/st"},
			"/cfg/vitals", "/st/vitals", "/st/vitals"},
		{"linux defaults", "linux", map[string]string{"HOME": "/home/t"},
			"/home/t/.config/vitals", "/home/t/.local/state/vitals", "/home/t/.local/state/vitals"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := dirs(tc.goos, envOf(tc.env), "Vitals")
			if err != nil {
				t.Fatal(err)
			}
			if d.config != tc.config || d.state != tc.state || d.log != tc.log {
				t.Fatalf("got %+v", d)
			}
		})
	}
	if _, err := dirs("linux", envOf(nil), "Vitals"); err == nil {
		t.Fatal("no HOME must fail")
	}
	if _, err := dirs("windows", envOf(nil), "Vitals"); err == nil {
		t.Fatal("no APPDATA/USERPROFILE must fail")
	}
	if _, err := dirs("linux", envOf(map[string]string{"HOME": "/h"}), ""); err == nil {
		t.Fatal("empty app must fail")
	}
}

func TestRealDirsCreated(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "cfg"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(tmp, "state"))
	t.Setenv("APPDATA", filepath.Join(tmp, "roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "local"))
	for _, f := range []func(string) (string, error){ConfigDir, StateDir, LogDir} {
		d, err := f("Edgehop")
		if err != nil {
			t.Fatal(err)
		}
		st, err := os.Stat(d)
		if err != nil || !st.IsDir() {
			t.Fatalf("%s not created: %v", d, err)
		}
		if !strings.HasPrefix(d, tmp) {
			t.Fatalf("%s outside temp", d)
		}
		if runtime.GOOS != "windows" && st.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode %o", d, st.Mode().Perm())
		}
	}
}

type cfg struct {
	Name  string   `json:"name"`
	Port  int      `json:"port"`
	Peers []string `json:"peers"`
}

func TestSaveLoadJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	var missing cfg
	if err := LoadJSON(path, &missing); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	in := cfg{Name: "A", Port: 24800, Peers: []string{"x"}}
	if err := SaveJSON(path, in); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", st.Mode().Perm())
	}
	var out cfg
	if err := LoadJSON(path, &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "A" || out.Port != 24800 || len(out.Peers) != 1 {
		t.Fatalf("round trip %+v", out)
	}
	// Overwrite keeps a single file and no temp leftovers.
	in.Port = 1
	if err := SaveJSON(path, in); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadJSON(path, &out); err == nil {
		t.Fatal("corrupt file must fail")
	}
	if err := SaveJSON(path, make(chan int)); err == nil {
		t.Fatal("unencodable value must fail")
	}
}
