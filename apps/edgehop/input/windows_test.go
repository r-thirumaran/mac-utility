//go:build windows

package input

import (
	"testing"
	"unsafe"
)

// The Win32 ABI layouts SendInput and the hooks rely on (amd64/arm64).
func TestWin32Layouts(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		if s := unsafe.Sizeof(inputRecord{}); s != 40 {
			t.Fatalf("sizeof INPUT = %d, want 40", s)
		}
		if o := unsafe.Offsetof(inputRecord{}.Mi); o != 8 {
			t.Fatalf("INPUT union offset = %d, want 8", o)
		}
		if s := unsafe.Sizeof(mouseInput{}); s != 32 {
			t.Fatalf("sizeof MOUSEINPUT = %d, want 32", s)
		}
		if s := unsafe.Sizeof(keybdInput{}); s != 24 {
			t.Fatalf("sizeof KEYBDINPUT = %d, want 24", s)
		}
		if s := unsafe.Sizeof(msllhookstruct{}); s != 32 {
			t.Fatalf("sizeof MSLLHOOKSTRUCT = %d, want 32", s)
		}
		if s := unsafe.Sizeof(kbdllhookstruct{}); s != 24 {
			t.Fatalf("sizeof KBDLLHOOKSTRUCT = %d, want 24", s)
		}
		if s := unsafe.Sizeof(wndClassExW{}); s != 80 {
			t.Fatalf("sizeof WNDCLASSEXW = %d, want 80", s)
		}
	}
	if s := unsafe.Sizeof(monitorInfoExW{}); s != 104 {
		t.Fatalf("sizeof MONITORINFOEXW = %d, want 104", s)
	}
}

func TestDisplaysAndMetrics(t *testing.T) {
	d := newWinDevice()
	ds := d.Displays()
	if len(ds) == 0 {
		t.Fatal("no displays")
	}
	primary := 0
	for _, s := range ds {
		if s.W <= 0 || s.H <= 0 || s.Scale <= 0 {
			t.Fatalf("bad display %+v", s)
		}
		if s.Primary {
			primary++
		}
	}
	if primary != 1 {
		t.Fatalf("%d primary displays", primary)
	}
	x, y := d.CursorPos()
	_, _, w, h := virtualScreen()
	if w <= 0 || h <= 0 {
		t.Fatalf("virtual screen %dx%d", w, h)
	}
	_ = x
	_ = y
}
