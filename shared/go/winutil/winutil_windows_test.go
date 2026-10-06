//go:build windows

package winutil

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestSingleInstanceMutex(t *testing.T) {
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

func TestRunHidden(t *testing.T) {
	out, err := RunHidden("cmd.exe", "/c", "echo", "hidden")
	if err != nil || strings.TrimSpace(string(out)) != "hidden" {
		t.Fatalf("RunHidden = %q, %v", out, err)
	}
	if _, err := RunHidden("cmd.exe", "/c", "exit", "3"); err == nil {
		t.Fatal("non-zero exit must be an error")
	}
}

func TestAutostartAndElevation(t *testing.T) {
	if Autostart("mu-winutil-no-such-entry") {
		t.Fatal("missing Run entry reported present")
	}
	t.Logf("elevated: %v", IsElevated())
}
