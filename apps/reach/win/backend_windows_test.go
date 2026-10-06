//go:build windows

package main

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"testing"
	"unsafe"
)

// The Win32 layouts of the injector, the capture and the icon extraction
// (amd64/arm64).
func TestReachLayouts(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("64-bit layouts only")
	}
	cases := []struct {
		name      string
		got, want uintptr
	}{
		{"INPUT", unsafe.Sizeof(input{}), 40},
		{"INPUT union offset", unsafe.Offsetof(input{}.u), 8},
		{"MOUSEINPUT", unsafe.Sizeof(mouseInput{}), 32},
		{"KEYBDINPUT", unsafe.Sizeof(keybdInput{}), 24},
		{"BITMAPINFOHEADER", unsafe.Sizeof(bitmapInfoHeader{}), 40},
		{"BITMAPINFO", unsafe.Sizeof(bitmapInfo{}), 44},
		{"BITMAP", unsafe.Sizeof(gdiBitmap{}), 32},
		{"MONITORINFOEXW", unsafe.Sizeof(monitorInfoEx{}), 104},
		{"SHFILEINFOW", unsafe.Sizeof(shFileInfo{}), 696},
		{"ICONINFO", unsafe.Sizeof(iconInfo{}), 32},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	notch := int32(-120)
	m := mouseEvent(mouseEventfWheel, 1, -2, uint32(notch))
	mi := (*mouseInput)(unsafe.Pointer(&m.u))
	if m.typ != inputMouse || mi.dx != 1 || mi.dy != -2 || int32(mi.mouseData) != -120 || mi.dwFlags != mouseEventfWheel {
		t.Errorf("mouse INPUT = %+v", *mi)
	}
	k := keyEvent(0, 0x1c, keyEventfScanCode|keyEventfKeyUp)
	ki := (*keybdInput)(unsafe.Pointer(&k.u))
	if k.typ != inputKeyboard || ki.wVk != 0 || ki.wScan != 0x1c || ki.dwFlags != keyEventfScanCode|keyEventfKeyUp {
		t.Errorf("keyboard INPUT = %+v", *ki)
	}
}

// TestReachBackendLive runs the read-only parts of the real backend on the
// OS: monitors, a GDI capture, the Start Menu walk with icon extraction, the
// window list and a clipboard round trip. Nothing is injected.
func TestReachBackendLive(t *testing.T) {
	b, err := newRealBackend()
	if err != nil {
		t.Fatal(err)
	}
	w := b.(*winBackend)
	ds, err := w.Displays()
	if err != nil || len(ds) == 0 {
		t.Fatalf("displays: %v, %v", ds, err)
	}
	primary := 0
	for _, d := range ds {
		if d.W <= 0 || d.H <= 0 || d.Scale <= 0 {
			t.Errorf("bad display %+v", d)
		}
		if d.Primary {
			primary++
		}
	}
	if primary != 1 || !ds[0].Primary {
		t.Errorf("primary display must come first, once: %+v", ds)
	}
	img, err := w.Capture(0)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if img.Bounds().Dx() != ds[0].W || img.Bounds().Dy() != ds[0].H {
		t.Errorf("capture %v for display %+v", img.Bounds(), ds[0])
	}
	for i := 3; i < len(img.Pix); i += 4 * 977 {
		if img.Pix[i] != 0xff {
			t.Fatalf("pixel %d alpha %d after the BGRA swizzle", i/4, img.Pix[i])
		}
	}
	if _, err := w.Capture(len(ds)); err == nil {
		t.Error("capture of a missing display succeeded")
	}
	host, user, up := w.HostInfo()
	if host == "" || user == "" || up <= 0 {
		t.Errorf("host info %q %q %d", host, user, up)
	}
	apps, err := w.AppsList(true)
	if err != nil {
		t.Fatalf("apps.list: %v", err)
	}
	icons := 0
	for _, a := range apps {
		if a.ID == "" || a.Name == "" || !underStartMenu(a.ID) {
			t.Errorf("bad app %+v", a)
		}
		if a.IconB64 == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(a.IconB64)
		if err != nil {
			t.Fatalf("%s: icon base64: %v", a.Name, err)
		}
		im, err := png.Decode(bytes.NewReader(raw))
		if err != nil || im.Bounds().Dx() != iconSize || im.Bounds().Dy() != iconSize {
			t.Fatalf("%s: icon png: %v %v", a.Name, err, im)
		}
		icons++
	}
	t.Logf("%d Start Menu apps, %d with icons", len(apps), icons)
	if len(apps) > 0 && icons == 0 {
		t.Error("no shortcut yielded an icon")
	}
	if _, err := w.AppsRunning(); err != nil {
		t.Error(err)
	}
	if _, err := w.WindowsList(); err != nil {
		t.Error(err)
	}
	if err := w.AppLaunch(`C:\nowhere\x.lnk`); err == nil {
		t.Error("launch outside the Start Menu succeeded")
	}
	const text = "Reach clipboard ✓ 测试 \U0001F600"
	if err := w.ClipboardSet(text); err != nil {
		t.Skipf("clipboard unavailable: %v", err)
	}
	if got, err := w.ClipboardGet(); err != nil || got != text {
		t.Errorf("clipboard = %q, %v", got, err)
	}
	if level, muted, err := w.VolumeGet(); err != nil {
		t.Logf("volume unavailable: %v", err)
	} else if level < 0 || level > 100 {
		t.Errorf("volume %d muted=%v", level, muted)
	}
}
