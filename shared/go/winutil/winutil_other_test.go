//go:build !windows

package winutil

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestSingleInstance(t *testing.T) {
	name := fmt.Sprintf("winutil-test-%d", os.Getpid())
	rel1, first, err := SingleInstance(name)
	if err != nil || !first {
		t.Fatalf("first: %v %v", first, err)
	}
	rel2, second, err := SingleInstance(name)
	if err != nil || second {
		t.Fatalf("second: first=%v err=%v", second, err)
	}
	rel2()
	rel1()
	rel1()
	rel3, again, err := SingleInstance(name)
	if err != nil || !again {
		t.Fatalf("after release: first=%v err=%v", again, err)
	}
	rel3()
}

func TestStubs(t *testing.T) {
	if err := RelaunchElevated("--x"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("RelaunchElevated: %v", err)
	}
	if err := SetAutostart("App", "/bin/true", true); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("SetAutostart: %v", err)
	}
	if Autostart("App") {
		t.Fatal("Autostart must be false")
	}
	_ = IsElevated()
	out, err := RunHidden("echo", "hidden", "ok")
	if err != nil || strings.TrimSpace(string(out)) != "hidden ok" {
		t.Fatalf("RunHidden: %q %v", out, err)
	}
	if _, err := RunHidden("definitely-not-a-program-xyz"); err == nil {
		t.Fatal("RunHidden with a missing program must fail")
	}
}
