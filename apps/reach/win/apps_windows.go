//go:build windows

package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
)

var (
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	dwmapi  = windows.NewLazySystemDLL("dwmapi.dll")

	procSHGetFileInfoW     = shell32.NewProc("SHGetFileInfoW")
	procSHDefExtractIconW  = shell32.NewProc("SHDefExtractIconW")
	procGetIconInfo        = user32.NewProc("GetIconInfo")
	procDestroyIcon        = user32.NewProc("DestroyIcon")
	procGetWindowTextW     = user32.NewProc("GetWindowTextW")
	procGetWindowTextLenW  = user32.NewProc("GetWindowTextLengthW")
	procGetWindowLongPtrW  = user32.NewProc("GetWindowLongPtrW")
	procGetWindow          = user32.NewProc("GetWindow")
	procIsIconic           = user32.NewProc("IsIconic")
	procShowWindow         = user32.NewProc("ShowWindow")
	procSetForegroundWnd   = user32.NewProc("SetForegroundWindow")
	procBringWindowToTop   = user32.NewProc("BringWindowToTop")
	procPostMessageW       = user32.NewProc("PostMessageW")
	procDwmGetWindowAttrib = dwmapi.NewProc("DwmGetWindowAttribute")
)

const (
	shgfiIcon        = 0x100
	shgfiLargeIcon   = 0x0
	gwlExStyle       = ^uintptr(19) // GWL_EXSTYLE = -20
	wsExToolWindow   = 0x00000080
	gwOwner          = 4
	swRestore        = 9
	swShowNormal     = 1
	wmClose          = 0x0010
	dwmwaCloaked     = 14
	iconSize         = 64
	processTerminate = 0x0001
)

// shFileInfo is SHFILEINFOW.
type shFileInfo struct {
	hIcon         uintptr
	iIcon         int32
	dwAttributes  uint32
	szDisplayName [260]uint16
	szTypeName    [80]uint16
}

// iconInfo is ICONINFO.
type iconInfo struct {
	fIcon    int32
	xHotspot uint32
	yHotspot uint32
	hbmMask  uintptr
	hbmColor uintptr
}

var (
	windowsMu  sync.Mutex
	windowsOut []uintptr
	windowCB   = windows.NewCallback(func(hwnd, lparam uintptr) uintptr {
		windowsOut = append(windowsOut, hwnd)
		return 1
	})
)

// startMenuDirs are the folders whose .lnk shortcuts make up apps.list.
func startMenuDirs() []string {
	var dirs []string
	if pd := os.Getenv("ProgramData"); pd != "" {
		dirs = append(dirs, filepath.Join(pd, `Microsoft\Windows\Start Menu\Programs`))
	}
	if ad := os.Getenv("APPDATA"); ad != "" {
		dirs = append(dirs, filepath.Join(ad, `Microsoft\Windows\Start Menu\Programs`))
	}
	return dirs
}

// skipShortcut filters uninstallers, help files and web links out of the
// Start Menu listing.
func skipShortcut(name string, info lnkInfo) bool {
	l := strings.ToLower(name)
	for _, bad := range []string{"uninstall", "uninstal", "remove ", "help", "readme", "read me", "release notes", "documentation", "website", "web site", "license", "licence", "eula", "faq", "manual", "support"} {
		if strings.Contains(l, bad) {
			return true
		}
	}
	t := strings.ToLower(info.Target)
	base := filepath.Base(t)
	if strings.HasPrefix(base, "unins") || base == "msiexec.exe" || strings.Contains(base, "uninstall") {
		return true
	}
	switch filepath.Ext(t) {
	case ".txt", ".url", ".htm", ".html", ".chm", ".pdf", ".rtf", ".log", ".ini":
		return true
	}
	return false
}

// AppsList walks the Start Menu folders (recursively), de-duplicates by
// name and optionally extracts 64 px icons (cached per shortcut mtime).
func (w *winBackend) AppsList(icons bool) ([]AppInfo, error) {
	type entry struct {
		app  AppInfo
		info lnkInfo
		mod  time.Time
	}
	seen := map[string]bool{}
	var entries []entry
	for _, dir := range startMenuDirs() {
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".lnk") {
				return nil
			}
			name := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
			key := strings.ToLower(name)
			if seen[key] {
				return nil
			}
			var info lnkInfo
			if data, err := os.ReadFile(path); err == nil {
				info, _ = parseLnk(data)
			}
			if skipShortcut(name, info) {
				return nil
			}
			seen[key] = true
			var mod time.Time
			if st, err := d.Info(); err == nil {
				mod = st.ModTime()
			}
			entries = append(entries, entry{app: AppInfo{ID: path, Name: name}, info: info, mod: mod})
			return nil
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].app.Name) < strings.ToLower(entries[j].app.Name)
	})
	out := make([]AppInfo, len(entries))
	for i, e := range entries {
		out[i] = e.app
	}
	if !icons {
		return out, nil
	}
	w.withCOM(func() {
		for i, e := range entries {
			out[i].IconB64 = w.cachedIcon(e.app.ID, e.mod, e.info)
		}
	})
	return out, nil
}

// withCOM runs fn on a locked thread with COM initialised (SHGetFileInfo
// needs it).
func (w *winBackend) withCOM(fn func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err == nil || isSFalse(err) {
		defer windows.CoUninitialize()
	}
	fn()
}

func isSFalse(err error) bool {
	e, ok := err.(windows.Errno)
	return ok && e == 1
}

func (w *winBackend) cachedIcon(path string, mod time.Time, info lnkInfo) string {
	key := path + "|" + mod.Format(time.RFC3339Nano)
	w.iconMu.Lock()
	if b64, ok := w.iconCache[key]; ok {
		w.iconMu.Unlock()
		return b64
	}
	w.iconMu.Unlock()
	b64 := ""
	if img, err := shortcutIcon(path, info); err == nil {
		var buf bytes.Buffer
		if png.Encode(&buf, img) == nil {
			b64 = base64.StdEncoding.EncodeToString(buf.Bytes())
		}
	}
	w.iconMu.Lock()
	w.iconCache[key] = b64
	w.iconMu.Unlock()
	return b64
}

// shortcutIcon extracts the icon of a shortcut: SHDefExtractIconW at 64 px
// from the icon location or the target, falling back to the shell's large
// (32 px) icon of the .lnk itself through SHGetFileInfoW.
func shortcutIcon(lnkPath string, info lnkInfo) (*image.RGBA, error) {
	candidates := []struct {
		file  string
		index int32
	}{}
	if info.IconPath != "" {
		candidates = append(candidates, struct {
			file  string
			index int32
		}{info.IconPath, info.IconIndex})
	}
	if info.Target != "" {
		switch strings.ToLower(filepath.Ext(info.Target)) {
		case ".exe", ".dll", ".ico":
			candidates = append(candidates, struct {
				file  string
				index int32
			}{info.Target, 0})
		}
	}
	for _, c := range candidates {
		p, err := windows.UTF16PtrFromString(c.file)
		if err != nil {
			continue
		}
		var hLarge uintptr
		hr, _, _ := procSHDefExtractIconW.Call(uintptr(unsafe.Pointer(p)), uintptr(c.index), 0, uintptr(unsafe.Pointer(&hLarge)), 0, iconSize)
		if hr == 0 && hLarge != 0 {
			img, err := iconToRGBA(hLarge)
			procDestroyIcon.Call(hLarge)
			if err == nil {
				return img, nil
			}
		}
	}
	p, err := windows.UTF16PtrFromString(lnkPath)
	if err != nil {
		return nil, err
	}
	var sfi shFileInfo
	r, _, _ := procSHGetFileInfoW.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&sfi)), unsafe.Sizeof(sfi), shgfiIcon|shgfiLargeIcon)
	if r == 0 || sfi.hIcon == 0 {
		return nil, fmt.Errorf("no icon for %s", lnkPath)
	}
	defer procDestroyIcon.Call(sfi.hIcon)
	return iconToRGBA(sfi.hIcon)
}

// iconToRGBA renders an HICON to a 64×64 RGBA image: GetIconInfo →
// GetDIBits of the colour bitmap (mask-derived alpha for icons without one).
func iconToRGBA(hIcon uintptr) (*image.RGBA, error) {
	var ii iconInfo
	if r, _, e := procGetIconInfo.Call(hIcon, uintptr(unsafe.Pointer(&ii))); r == 0 {
		return nil, fmt.Errorf("GetIconInfo: %v", e)
	}
	if ii.hbmMask != 0 {
		defer procDeleteObject.Call(ii.hbmMask)
	}
	if ii.hbmColor != 0 {
		defer procDeleteObject.Call(ii.hbmColor)
	}
	src := ii.hbmColor
	if src == 0 {
		src = ii.hbmMask
	}
	var bm gdiBitmap
	if r, _, _ := procGetObjectW.Call(src, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm))); r == 0 {
		return nil, fmt.Errorf("GetObject failed")
	}
	width, height := int(bm.bmWidth), int(bm.bmHeight)
	if ii.hbmColor == 0 {
		height /= 2 // a monochrome icon stacks XOR over AND in one bitmap
	}
	if width <= 0 || height <= 0 || width > 1024 || height > 1024 {
		return nil, fmt.Errorf("odd icon size %dx%d", width, height)
	}
	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		return nil, fmt.Errorf("GetDC failed")
	}
	defer procReleaseDC.Call(0, hdc)
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	if ii.hbmColor != 0 {
		if err := readDIBits(hdc, ii.hbmColor, width, height, img.Pix); err != nil {
			return nil, err
		}
		bgraToRGBAKeepAlpha(img.Pix)
	}
	hasAlpha := false
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0 {
			hasAlpha = true
			break
		}
	}
	if !hasAlpha && ii.hbmMask != 0 {
		mask := make([]byte, width*height*4)
		if err := readDIBits(hdc, ii.hbmMask, width, height, mask); err == nil {
			for i := 0; i+3 < len(mask); i += 4 {
				if mask[i] == 0 { // mask black = opaque
					img.Pix[i+3] = 0xff
				} else {
					img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 0, 0, 0, 0
				}
			}
			if ii.hbmColor == 0 {
				for i := 0; i+3 < len(mask); i += 4 {
					if img.Pix[i+3] != 0 {
						img.Pix[i], img.Pix[i+1], img.Pix[i+2] = 0x20, 0x20, 0x20
					}
				}
			}
		}
	} else if !hasAlpha {
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = 0xff
		}
	}
	return resizeRGBA(img, iconSize, iconSize), nil
}

func bgraToRGBAKeepAlpha(pix []byte) {
	for i := 0; i+3 < len(pix); i += 4 {
		pix[i], pix[i+2] = pix[i+2], pix[i]
	}
}

// resizeRGBA resamples bilinearly to w×h (returns src when it already fits).
func resizeRGBA(src *image.RGBA, w, h int) *image.RGBA {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	if sw == w && sh == h {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		fy := (float64(y)+0.5)*float64(sh)/float64(h) - 0.5
		y0 := int(fy)
		if y0 < 0 {
			y0 = 0
		}
		y1 := y0 + 1
		if y1 >= sh {
			y1 = sh - 1
		}
		wy := fy - float64(y0)
		if wy < 0 {
			wy = 0
		}
		for x := 0; x < w; x++ {
			fx := (float64(x)+0.5)*float64(sw)/float64(w) - 0.5
			x0 := int(fx)
			if x0 < 0 {
				x0 = 0
			}
			x1 := x0 + 1
			if x1 >= sw {
				x1 = sw - 1
			}
			wx := fx - float64(x0)
			if wx < 0 {
				wx = 0
			}
			o := dst.PixOffset(x, y)
			for c := 0; c < 4; c++ {
				p00 := float64(src.Pix[src.PixOffset(x0, y0)+c])
				p10 := float64(src.Pix[src.PixOffset(x1, y0)+c])
				p01 := float64(src.Pix[src.PixOffset(x0, y1)+c])
				p11 := float64(src.Pix[src.PixOffset(x1, y1)+c])
				v := (p00*(1-wx)+p10*wx)*(1-wy) + (p01*(1-wx)+p11*wx)*wy
				dst.Pix[o+c] = uint8(v + 0.5)
			}
		}
	}
	return dst
}

// AppLaunch opens a Start Menu shortcut with ShellExecuteW "open".
func (w *winBackend) AppLaunch(id string) error {
	if !strings.EqualFold(filepath.Ext(id), ".lnk") || !underStartMenu(id) {
		return bridge.Errorf(bridge.CodeInvalidParams, "id must be a Start Menu shortcut from apps.list")
	}
	if _, err := os.Stat(id); err != nil {
		return bridge.Errorf(bridge.CodeNotFound, "shortcut not found")
	}
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(id)
	if err != nil {
		return err
	}
	if err := windows.ShellExecute(0, verb, file, nil, nil, swShowNormal); err != nil {
		return fmt.Errorf("ShellExecute: %w", err)
	}
	return nil
}

func underStartMenu(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for _, dir := range startMenuDirs() {
		rel, err := filepath.Rel(dir, abs)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- windows

// topWindow is one visible, titled, uncloaked top-level window.
type topWindow struct {
	hwnd  uintptr
	title string
	pid   uint32
}

// listWindows enumerates the alt-tab style window list.
func listWindows() []topWindow {
	windowsMu.Lock()
	defer windowsMu.Unlock()
	windowsOut = windowsOut[:0]
	_ = windows.EnumWindows(windowCB, nil)
	out := make([]topWindow, 0, len(windowsOut))
	for _, h := range windowsOut {
		if !windows.IsWindowVisible(windows.HWND(h)) {
			continue
		}
		if ex, _, _ := procGetWindowLongPtrW.Call(h, gwlExStyle); ex&wsExToolWindow != 0 {
			continue
		}
		if owner, _, _ := procGetWindow.Call(h, gwOwner); owner != 0 {
			continue
		}
		var cloaked uint32
		if r, _, _ := procDwmGetWindowAttrib.Call(h, dwmwaCloaked, uintptr(unsafe.Pointer(&cloaked)), 4); r == 0 && cloaked != 0 {
			continue
		}
		n, _, _ := procGetWindowTextLenW.Call(h)
		if n == 0 {
			continue
		}
		buf := make([]uint16, n+1)
		procGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), n+1)
		title := windows.UTF16ToString(buf)
		if strings.TrimSpace(title) == "" {
			continue
		}
		var pid uint32
		windows.GetWindowThreadProcessId(windows.HWND(h), &pid)
		if pid == 0 || pid == uint32(os.Getpid()) {
			continue
		}
		out = append(out, topWindow{hwnd: h, title: title, pid: pid})
	}
	return out
}

// processImage returns the executable path of pid ("" when inaccessible).
func processImage(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

func appNameOf(image string, fallback string) string {
	if image == "" {
		return fallback
	}
	base := strings.TrimSuffix(filepath.Base(image), filepath.Ext(image))
	if base == "" {
		return fallback
	}
	return strings.ToUpper(base[:1]) + base[1:]
}

func (w *winBackend) AppsRunning() ([]RunningApp, error) {
	wins := listWindows()
	fg := uint32(0)
	if h := windows.GetForegroundWindow(); h != 0 {
		windows.GetWindowThreadProcessId(h, &fg)
	}
	seen := map[uint32]bool{}
	var out []RunningApp
	for _, tw := range wins {
		if seen[tw.pid] {
			continue
		}
		seen[tw.pid] = true
		img := processImage(tw.pid)
		id := img
		if id == "" {
			id = "pid:" + strconv.Itoa(int(tw.pid))
		}
		out = append(out, RunningApp{ID: id, Name: appNameOf(img, tw.title), PID: int(tw.pid), Active: tw.pid == fg})
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// focusWindow restores and raises hwnd.
func focusWindow(hwnd uintptr) error {
	if r, _, _ := procIsIconic.Call(hwnd); r != 0 {
		procShowWindow.Call(hwnd, swRestore)
	}
	pressAlt()
	procBringWindowToTop.Call(hwnd)
	if r, _, _ := procSetForegroundWnd.Call(hwnd); r == 0 {
		return bridge.Errorf(bridge.CodeInternal, "SetForegroundWindow refused")
	}
	return nil
}

func (w *winBackend) AppFocus(id string, pid int) error {
	for _, tw := range listWindows() {
		if (pid != 0 && int(tw.pid) == pid) || (id != "" && strings.EqualFold(processImage(tw.pid), id)) {
			return focusWindow(tw.hwnd)
		}
	}
	return bridge.Errorf(bridge.CodeNotFound, "no window for app %q / pid %d", id, pid)
}

func (w *winBackend) AppQuit(pid int, force bool) error {
	if force {
		h, err := windows.OpenProcess(processTerminate, false, uint32(pid))
		if err != nil {
			return bridge.Errorf(bridge.CodeNotFound, "open process %d: %v", pid, err)
		}
		defer windows.CloseHandle(h)
		if err := windows.TerminateProcess(h, 1); err != nil {
			return fmt.Errorf("TerminateProcess: %w", err)
		}
		return nil
	}
	closed := 0
	for _, tw := range listWindows() {
		if int(tw.pid) == pid {
			procPostMessageW.Call(tw.hwnd, wmClose, 0, 0)
			closed++
		}
	}
	if closed == 0 {
		return bridge.Errorf(bridge.CodeNotFound, "no window for pid %d", pid)
	}
	return nil
}

func (w *winBackend) WindowsList() ([]WindowInfo, error) {
	mons, _ := enumMonitors()
	wins := listWindows()
	out := make([]WindowInfo, 0, len(wins))
	for _, tw := range wins {
		img := processImage(tw.pid)
		out = append(out, WindowInfo{ID: strconv.FormatUint(uint64(tw.hwnd), 10), Title: tw.title, App: appNameOf(img, tw.title), PID: int(tw.pid), Display: displayIndexOfWindow(tw.hwnd, mons)})
	}
	return out, nil
}

func (w *winBackend) WindowFocus(id string) error {
	h, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return bridge.Errorf(bridge.CodeInvalidParams, "bad window id %q", id)
	}
	for _, tw := range listWindows() {
		if tw.hwnd == uintptr(h) {
			return focusWindow(tw.hwnd)
		}
	}
	return bridge.Errorf(bridge.CodeNotFound, "no window %s", id)
}
