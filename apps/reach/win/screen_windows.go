//go:build windows

package main

import (
	"fmt"
	"image"
	"sort"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
)

var (
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")
	shcore = windows.NewLazySystemDLL("shcore.dll")

	procGetDC                         = user32.NewProc("GetDC")
	procReleaseDC                     = user32.NewProc("ReleaseDC")
	procEnumDisplayMonitors           = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW               = user32.NewProc("GetMonitorInfoW")
	procMonitorFromWindow             = user32.NewProc("MonitorFromWindow")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	procGetDpiForMonitor              = shcore.NewProc("GetDpiForMonitor")

	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procGetObjectW             = gdi32.NewProc("GetObjectW")
)

const (
	srcCopy                  = 0x00CC0020
	captureBlt               = 0x40000000
	dibRGBColors             = 0
	biRGB                    = 0
	monitorInfoPrimary       = 1
	monitorDefaultToNearest  = 2
	mdtEffectiveDPI          = 0
	dpiAwarenessPerMonitorV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
)

type rect struct{ left, top, right, bottom int32 }

func (r rect) w() int32 { return r.right - r.left }
func (r rect) h() int32 { return r.bottom - r.top }

// monitorInfoEx is MONITORINFOEXW.
type monitorInfoEx struct {
	cbSize    uint32
	rcMonitor rect
	rcWork    rect
	dwFlags   uint32
	szDevice  [32]uint16
}

// bitmapInfoHeader is BITMAPINFOHEADER; bitmapInfo adds the (unused) first
// palette entry so the struct is a valid BITMAPINFO.
type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type bitmapInfo struct {
	header bitmapInfoHeader
	colors [1]uint32
}

// gdiBitmap is BITMAP (GetObjectW).
type gdiBitmap struct {
	bmType       int32
	bmWidth      int32
	bmHeight     int32
	bmWidthBytes int32
	bmPlanes     uint16
	bmBitsPixel  uint16
	bmBits       uintptr
}

// monitor is one physical display.
type monitor struct {
	handle  uintptr
	rc      rect
	primary bool
	name    string
	scale   float64
}

var (
	monitorsMu  sync.Mutex
	monitorsOut []uintptr
	monitorCB   = windows.NewCallback(func(hMonitor, hdc, lprc, lparam uintptr) uintptr {
		monitorsOut = append(monitorsOut, hMonitor)
		return 1
	})
)

// setDPIAware opts the process into per-monitor DPI so GetDC(0) captures
// and metrics are in physical pixels.
func setDPIAware() {
	if procSetProcessDpiAwarenessContext.Find() == nil {
		if r, _, _ := procSetProcessDpiAwarenessContext.Call(dpiAwarenessPerMonitorV2); r != 0 {
			return
		}
	}
	_, _, _ = procSetProcessDPIAware.Call()
}

// enumMonitors lists the monitors, primary first, then left to right.
func enumMonitors() ([]monitor, error) {
	monitorsMu.Lock()
	defer monitorsMu.Unlock()
	monitorsOut = monitorsOut[:0]
	if r, _, e := procEnumDisplayMonitors.Call(0, 0, monitorCB, 0); r == 0 {
		return nil, fmt.Errorf("EnumDisplayMonitors: %v", e)
	}
	out := make([]monitor, 0, len(monitorsOut))
	for _, h := range monitorsOut {
		var mi monitorInfoEx
		mi.cbSize = uint32(unsafe.Sizeof(mi))
		if r, _, _ := procGetMonitorInfoW.Call(h, uintptr(unsafe.Pointer(&mi))); r == 0 {
			continue
		}
		m := monitor{handle: h, rc: mi.rcMonitor, primary: mi.dwFlags&monitorInfoPrimary != 0, name: windows.UTF16ToString(mi.szDevice[:]), scale: 1}
		if procGetDpiForMonitor.Find() == nil {
			var dx, dy uint32
			if r, _, _ := procGetDpiForMonitor.Call(h, mdtEffectiveDPI, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r == 0 && dx > 0 {
				m.scale = float64(dx) / 96
			}
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].primary != out[j].primary {
			return out[i].primary
		}
		if out[i].rc.left != out[j].rc.left {
			return out[i].rc.left < out[j].rc.left
		}
		return out[i].rc.top < out[j].rc.top
	})
	if len(out) == 0 {
		return nil, fmt.Errorf("no monitors")
	}
	return out, nil
}

func (w *winBackend) Displays() ([]Display, error) {
	mons, err := enumMonitors()
	if err != nil {
		return nil, err
	}
	out := make([]Display, len(mons))
	for i, m := range mons {
		out[i] = Display{Index: i, W: int(m.rc.w()), H: int(m.rc.h()), Scale: m.scale, Primary: m.primary, Name: m.name}
	}
	return out, nil
}

func (w *winBackend) monitorAt(display int) (monitor, error) {
	mons, err := enumMonitors()
	if err != nil {
		return monitor{}, err
	}
	if display < 0 || display >= len(mons) {
		return monitor{}, bridge.Errorf(bridge.CodeInvalidParams, "no display %d", display)
	}
	return mons[display], nil
}

// Capture grabs one display with BitBlt(SRCCOPY|CAPTUREBLT) + GetDIBits
// (top-down 32 bpp) into an RGBA image.
func (w *winBackend) Capture(display int) (*image.RGBA, error) {
	m, err := w.monitorAt(display)
	if err != nil {
		return nil, err
	}
	width, height := int(m.rc.w()), int(m.rc.h())
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("display %d has no area", display)
	}
	hdcScreen, _, _ := procGetDC.Call(0)
	if hdcScreen == 0 {
		return nil, fmt.Errorf("GetDC failed")
	}
	defer procReleaseDC.Call(0, hdcScreen)
	hdcMem, _, _ := procCreateCompatibleDC.Call(hdcScreen)
	if hdcMem == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(hdcMem)
	hbm, _, _ := procCreateCompatibleBitmap.Call(hdcScreen, uintptr(width), uintptr(height))
	if hbm == 0 {
		return nil, fmt.Errorf("CreateCompatibleBitmap failed")
	}
	defer procDeleteObject.Call(hbm)
	old, _, _ := procSelectObject.Call(hdcMem, hbm)
	r, _, e := procBitBlt.Call(hdcMem, 0, 0, uintptr(width), uintptr(height), hdcScreen, uintptr(m.rc.left), uintptr(m.rc.top), srcCopy|captureBlt)
	// The bitmap must not be selected into a DC while GetDIBits reads it.
	procSelectObject.Call(hdcMem, old)
	if r == 0 {
		return nil, fmt.Errorf("BitBlt: %v", e)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	if err := readDIBits(hdcMem, hbm, width, height, img.Pix); err != nil {
		return nil, err
	}
	bgraToRGBA(img.Pix)
	return img, nil
}

// readDIBits copies an hbm (not selected anywhere) as top-down 32 bpp BGRA.
func readDIBits(hdc, hbm uintptr, width, height int, dst []byte) error {
	var bmi bitmapInfo
	bmi.header = bitmapInfoHeader{biSize: uint32(unsafe.Sizeof(bmi.header)), biWidth: int32(width), biHeight: -int32(height), biPlanes: 1, biBitCount: 32, biCompression: biRGB}
	lines, _, e := procGetDIBits.Call(hdc, hbm, 0, uintptr(height), uintptr(unsafe.Pointer(&dst[0])), uintptr(unsafe.Pointer(&bmi)), dibRGBColors)
	if int(lines) != height {
		return fmt.Errorf("GetDIBits copied %d of %d lines: %v", lines, height, e)
	}
	return nil
}

func bgraToRGBA(pix []byte) {
	for i := 0; i+3 < len(pix); i += 4 {
		pix[i], pix[i+2] = pix[i+2], pix[i]
		pix[i+3] = 0xff
	}
}

func (w *winBackend) MouseMoveTo(display int, x, y float64) error {
	m, err := w.monitorAt(display)
	if err != nil {
		return err
	}
	px := float64(m.rc.left) + x*float64(m.rc.w()-1)
	py := float64(m.rc.top) + y*float64(m.rc.h()-1)
	return moveToPixel(px, py)
}

// displayIndexOfWindow maps a window to its display index.
func displayIndexOfWindow(hwnd uintptr, mons []monitor) int {
	h, _, _ := procMonitorFromWindow.Call(hwnd, monitorDefaultToNearest)
	for i, m := range mons {
		if m.handle == h {
			return i
		}
	}
	return 0
}
