package main

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

// buildLnk assembles a minimal MS-SHLLINK file: header, LinkInfo with a
// local base path, Unicode string data and an icon environment block.
func buildLnk(t *testing.T, base, suffix, args, iconLoc string, iconIndex int32, envTarget string) []byte {
	t.Helper()
	hdr := make([]byte, lnkHeaderSize)
	binary.LittleEndian.PutUint32(hdr, lnkHeaderSize)
	flags := uint32(lnkHasLinkInfo | lnkIsUnicode)
	if args != "" {
		flags |= lnkHasArguments
	}
	if iconLoc != "" {
		flags |= lnkHasIconLocation
	}
	binary.LittleEndian.PutUint32(hdr[0x14:], flags)
	binary.LittleEndian.PutUint32(hdr[0x38:], uint32(iconIndex))
	out := hdr

	// LinkInfo: 0x1C header, VolumeIDAndLocalBasePath, offsets to the strings.
	li := make([]byte, 0x1C)
	lbp := append([]byte(base), 0)
	cps := append([]byte(suffix), 0)
	binary.LittleEndian.PutUint32(li[4:], 0x1C)
	binary.LittleEndian.PutUint32(li[8:], lnkVolumeIDAndLocalBasePath)
	binary.LittleEndian.PutUint32(li[16:], uint32(len(li)))
	li = append(li, lbp...)
	binary.LittleEndian.PutUint32(li[24:], uint32(len(li)))
	li = append(li, cps...)
	binary.LittleEndian.PutUint32(li, uint32(len(li)))
	out = append(out, li...)

	str := func(s string) []byte {
		u := utf16.Encode([]rune(s))
		b := make([]byte, 2+2*len(u))
		binary.LittleEndian.PutUint16(b, uint16(len(u)))
		for i, c := range u {
			binary.LittleEndian.PutUint16(b[2+2*i:], c)
		}
		return b
	}
	if args != "" {
		out = append(out, str(args)...)
	}
	if iconLoc != "" {
		out = append(out, str(iconLoc)...)
	}
	if envTarget != "" {
		blk := make([]byte, 8+260+520)
		binary.LittleEndian.PutUint32(blk, uint32(len(blk)))
		binary.LittleEndian.PutUint32(blk[4:], lnkEnvironmentBlock)
		copy(blk[8:], envTarget)
		for i, c := range utf16.Encode([]rune(envTarget)) {
			binary.LittleEndian.PutUint16(blk[8+260+2*i:], c)
		}
		out = append(out, blk...)
	}
	return append(out, 0, 0, 0, 0) // terminal block
}

func TestParseLnk(t *testing.T) {
	t.Setenv("ProgramFiles", `C:\Program Files`)
	data := buildLnk(t, `C:\Apps\Tool`, `tool.exe`, "--flag", `%ProgramFiles%\Tool\tool.ico`, 3, "")
	info, err := parseLnk(data)
	if err != nil {
		t.Fatal(err)
	}
	if info.Target != `C:\Apps\Tool\tool.exe` {
		t.Errorf("target = %q", info.Target)
	}
	if info.Args != "--flag" {
		t.Errorf("args = %q", info.Args)
	}
	if info.IconPath != `C:\Program Files\Tool\tool.ico` || info.IconIndex != 3 {
		t.Errorf("icon = %q #%d", info.IconPath, info.IconIndex)
	}

	// Without LinkInfo the environment block supplies the target.
	noLI := buildLnk(t, "", "", "", "", 0, `%ProgramFiles%\Env\env.exe`)
	binary.LittleEndian.PutUint32(noLI[0x14:], lnkIsUnicode)
	// Strip the LinkInfo structure (flags no longer announce it).
	liSize := int(binary.LittleEndian.Uint32(noLI[lnkHeaderSize:]))
	noLI = append(noLI[:lnkHeaderSize:lnkHeaderSize], noLI[lnkHeaderSize+liSize:]...)
	info, err = parseLnk(noLI)
	if err != nil {
		t.Fatal(err)
	}
	if info.Target != `C:\Program Files\Env\env.exe` {
		t.Errorf("env target = %q", info.Target)
	}

	for _, bad := range [][]byte{nil, []byte("not a link"), make([]byte, 10)} {
		if _, err := parseLnk(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	if got := expandWinEnv(`%NOPE_UNSET%\x`); got != `%NOPE_UNSET%\x` {
		t.Errorf("unknown var expanded to %q", got)
	}
}

func TestFakeApps(t *testing.T) {
	fb := newFakeBackend()
	fb.appsDir = t.TempDir()
	apps, err := fb.AppsList(true)
	if err != nil || len(apps) == 0 {
		t.Fatalf("apps = %v err=%v", apps, err)
	}
	for _, a := range apps {
		if a.IconB64 == "" || a.ID == "" || a.Name == "" {
			t.Fatalf("incomplete app %+v", a)
		}
	}
	running, _ := fb.AppsRunning()
	if len(running) != 3 || !running[0].Active {
		t.Fatalf("running = %v", running)
	}
	if err := fb.AppFocus("", running[1].PID); err != nil {
		t.Fatal(err)
	}
	running, _ = fb.AppsRunning()
	if running[0].Active || !running[1].Active {
		t.Fatalf("focus did not move: %v", running)
	}
	if err := fb.AppQuit(running[2].PID, false); err != nil {
		t.Fatal(err)
	}
	if running, _ = fb.AppsRunning(); len(running) != 2 {
		t.Fatalf("quit did not remove: %v", running)
	}
	if err := fb.AppQuit(1, true); err == nil {
		t.Fatal("quit of an unknown pid succeeded")
	}
}
