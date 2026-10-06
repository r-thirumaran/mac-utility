package main

import "testing"

func TestResolveKey(t *testing.T) {
	cases := []struct {
		name string
		scan int
		ext  bool
		vk   int
		dom  string
	}{
		{"KeyA", 0x1e, false, 0, "KeyA"},
		{"a", 0x1e, false, 0, "KeyA"},
		{"A", 0x1e, false, 0, "KeyA"},
		{"Enter", 0x1c, false, 0, "Enter"},
		{"enter", 0x1c, false, 0, "Enter"},
		{"Return", 0x1c, false, 0, "Enter"},
		{"Escape", 0x01, false, 0, "Escape"},
		{"esc", 0x01, false, 0, "Escape"},
		{"Tab", 0x0f, false, 0, "Tab"},
		{"Backspace", 0x0e, false, 0, "Backspace"},
		{"Space", 0x39, false, 0, "Space"},
		{"ArrowLeft", 0x4b, true, 0, "ArrowLeft"},
		{"ArrowUp", 0x48, true, 0, "ArrowUp"},
		{"F5", 0x3f, false, 0, "F5"},
		{"Delete", 0x53, true, 0, "Delete"},
		{"MetaLeft", 0x5b, true, 0, "MetaLeft"},
		{"win", 0x5b, true, 0, "MetaLeft"},
		{"ControlLeft", 0x1d, false, 0, "ControlLeft"},
		{"ctrl", 0x1d, false, 0, "ControlLeft"},
		{"AltLeft", 0x38, false, 0, "AltLeft"},
		{"ShiftLeft", 0x2a, false, 0, "ShiftLeft"},
		{"Digit1", 0x02, false, 0, "Digit1"},
		{"1", 0x02, false, 0, "Digit1"},
		{"/", 0x35, false, 0, "Slash"},
		{"MediaPlayPause", 0, false, 0xB3, "MediaPlayPause"},
		{"MediaTrackNext", 0, false, 0xB0, "MediaTrackNext"},
		{"MediaTrackPrevious", 0, false, 0xB1, "MediaTrackPrevious"},
		{"AudioVolumeUp", 0, false, 0xAF, "AudioVolumeUp"},
		{"AudioVolumeDown", 0, false, 0xAE, "AudioVolumeDown"},
		{"AudioVolumeMute", 0, false, 0xAD, "AudioVolumeMute"},
	}
	for _, tc := range cases {
		k, ok := resolveKey(tc.name)
		if !ok {
			t.Errorf("%q: not resolved", tc.name)
			continue
		}
		if k.Scan != tc.scan || k.Ext != tc.ext || k.VK != tc.vk || k.Name != tc.dom {
			t.Errorf("%q: got scan=%#x ext=%v vk=%#x name=%q, want scan=%#x ext=%v vk=%#x name=%q", tc.name, k.Scan, k.Ext, k.VK, k.Name, tc.scan, tc.ext, tc.vk, tc.dom)
		}
	}
	for _, bad := range []string{"", "BrightnessUp", "Hyper", "KeyÄ", "!!"} {
		if _, ok := resolveKey(bad); ok {
			t.Errorf("%q resolved unexpectedly", bad)
		}
	}
	mods, ok := resolveMods([]string{"cmdOrCtrl", "shift", "alt", "meta"})
	if !ok || len(mods) != 4 || mods[0].Scan != 0x1d || mods[1].Scan != 0x2a || mods[2].Scan != 0x38 || mods[3].Scan != 0x5b {
		t.Fatalf("mods = %+v ok=%v", mods, ok)
	}
	if _, ok := resolveMods([]string{"hyper"}); ok {
		t.Fatal("hyper accepted")
	}
	for _, action := range []string{"playPause", "next", "previous", "volumeUp", "volumeDown", "mute"} {
		if _, ok := resolveKey(mediaKeyOf[action]); !ok {
			t.Errorf("media action %q has no key", action)
		}
	}
}
