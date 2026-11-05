//go:build windows

package input

import (
	"math"
	"runtime"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Shake-to-locate overlay (docs/edgehop-protocol.md §6.3): a borderless,
// click-through, always-on-top layered window that draws a ring shrinking
// from locateMaxRadius to locateMinRadius around the cursor for about a
// second and then fades out. One overlay at a time.
//
// The ring is painted by hand into a 32-bit premultiplied DIB and shown with
// UpdateLayeredWindow, so there is no WM_PAINT, no background and no cgo.

// Ring geometry and timing (§6.3).
const (
	locateMaxRadius = 140
	locateMinRadius = 28
	locateStroke    = 6 // accent band, px
	locateOutline   = 2 // white outline on each side of the band, px
	locateDuration  = 1000 * time.Millisecond
	locateFadeFrom  = 700 * time.Millisecond // alpha ramps down after this
	locateFrame     = 16 * time.Millisecond
)

// locateAccent is the ring colour (BGR; a warm blue that reads on any
// wallpaper).
var locateAccent = [3]byte{0xFF, 0x8A, 0x1F} // B, G, R

var (
	gdi32 = windows.NewLazySystemDLL("gdi32.dll")

	procUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procShowWindow          = user32.NewProc("ShowWindow")
	procPeekMessageW        = user32.NewProc("PeekMessageW")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")

	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
)

// Win32 constants for the overlay.
const (
	wsExLayered     = 0x00080000
	wsExTransparent = 0x00000020
	wsExTopmost     = 0x00000008
	wsExToolWindow  = 0x00000080
	wsExNoActivate  = 0x08000000
	wsPopup         = 0x80000000

	swpNoActivate  = 0x0010
	swpNoSize      = 0x0001
	swpShowWindow  = 0x0040
	hwndTopmost    = ^uintptr(0) // (HWND)-1
	swShowNoActive = 4
	swHide         = 0

	ulwAlpha     = 0x00000002
	acSrcOver    = 0x00
	acSrcAlpha   = 0x01
	biRGB        = 0
	dibRGBColors = 0

	pmRemove = 0x0001
)

type blendFunction struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}

type size struct{ Cx, Cy int32 }

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// locateSide is the window's edge length: the largest ring plus its outline
// with a pixel of slack all round.
const locateSide = 2*(locateMaxRadius+locateStroke/2+locateOutline+1) + 1

// locateBusy keeps a single overlay at a time.
var locateBusy atomic.Bool

// Locate shows the shake-to-locate overlay around the current cursor. It
// returns at once; the animation runs on its own locked OS thread and is a
// no-op while an overlay is already up.
func Locate() {
	if !locateBusy.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer locateBusy.Store(false)
		runLocateOverlay()
	}()
}

// runLocateOverlay owns the overlay window for one animation.
func runLocateOverlay() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hinst, _, _ := procGetModuleHandleW.Call(0)
	hwnd := createLocateWindow(hinst)
	if hwnd == 0 {
		return
	}
	defer procDestroyWindow.Call(hwnd)

	screen, _, _ := procGetDC.Call(0)
	if screen == 0 {
		return
	}
	defer procReleaseDC.Call(0, screen)
	memDC, _, _ := procCreateCompatibleDC.Call(screen)
	if memDC == 0 {
		return
	}
	defer procDeleteDC.Call(memDC)

	bi := bitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:       locateSide,
		Height:      -locateSide, // top-down
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
	}
	var bits unsafe.Pointer
	bmp, _, _ := procCreateDIBSection.Call(memDC, uintptr(unsafe.Pointer(&bi)), dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		return
	}
	defer procDeleteObject.Call(bmp)
	old, _, _ := procSelectObject.Call(memDC, bmp)
	defer procSelectObject.Call(memDC, old)

	px := unsafe.Slice((*byte)(bits), locateSide*locateSide*4)
	procShowWindow.Call(hwnd, swShowNoActive)

	start := time.Now()
	for {
		elapsed := time.Since(start)
		if elapsed >= locateDuration {
			break
		}
		t := float64(elapsed) / float64(locateDuration)
		radius := locateMaxRadius - t*(locateMaxRadius-locateMinRadius)
		alpha := 1.0
		if elapsed > locateFadeFrom {
			alpha = 1 - float64(elapsed-locateFadeFrom)/float64(locateDuration-locateFadeFrom)
		}
		drawLocateRing(px, radius, alpha)

		var cur point
		procGetCursorPos.Call(uintptr(unsafe.Pointer(&cur)))
		origin := point{cur.X - locateSide/2, cur.Y - locateSide/2}
		src := point{0, 0}
		sz := size{locateSide, locateSide}
		blend := blendFunction{BlendOp: acSrcOver, SourceConstantAlpha: 255, AlphaFormat: acSrcAlpha}
		procUpdateLayeredWindow.Call(hwnd, screen,
			uintptr(unsafe.Pointer(&origin)), uintptr(unsafe.Pointer(&sz)),
			memDC, uintptr(unsafe.Pointer(&src)), 0,
			uintptr(unsafe.Pointer(&blend)), ulwAlpha)
		procSetWindowPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0, swpNoActivate|swpNoSize|swpShowWindow)

		pumpLocateMessages(hwnd)
		time.Sleep(locateFrame)
	}
	procShowWindow.Call(hwnd, swHide)
}

// createLocateWindow makes the layered, click-through, topmost popup.
func createLocateWindow(hinst uintptr) uintptr {
	className, _ := windows.UTF16PtrFromString("EdgehopLocateOverlay")
	wc := wndClassExW{
		CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		LpfnWndProc:   wndCb,
		HInstance:     hinst,
		LpszClassName: className,
	}
	// A second RegisterClassExW for the same name fails harmlessly.
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	title, _ := windows.UTF16PtrFromString("Edgehop locate")
	hwnd, _, _ := procCreateWindowExW.Call(
		wsExLayered|wsExTransparent|wsExTopmost|wsExToolWindow|wsExNoActivate,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		wsPopup, 0, 0, locateSide, locateSide, 0, 0, hinst, 0)
	return hwnd
}

// pumpLocateMessages drains the overlay's queue so Windows does not consider
// the window hung.
func pumpLocateMessages(hwnd uintptr) {
	var m msg
	for {
		r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), hwnd, 0, 0, pmRemove)
		if r == 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// drawLocateRing paints the ring into a premultiplied BGRA top-down bitmap:
// the accent band is locateStroke px wide with a locateOutline px white
// edging on both sides, so it reads on any wallpaper.
func drawLocateRing(px []byte, radius, alpha float64) {
	for i := range px {
		px[i] = 0
	}
	if alpha <= 0 {
		return
	}
	if alpha > 1 {
		alpha = 1
	}
	const c = locateSide / 2
	half := float64(locateStroke) / 2
	outer := half + locateOutline
	lo, hi := radius-outer-1, radius+outer+1
	yStart, yEnd := int(math.Floor(float64(c)-hi)), int(math.Ceil(float64(c)+hi))
	if yStart < 0 {
		yStart = 0
	}
	if yEnd > locateSide-1 {
		yEnd = locateSide - 1
	}
	for y := yStart; y <= yEnd; y++ {
		dy := float64(y - c)
		row := y * locateSide * 4
		for x := 0; x < locateSide; x++ {
			dx := float64(x - c)
			d := math.Hypot(dx, dy)
			if d < lo || d > hi {
				continue
			}
			off := math.Abs(d - radius)
			var r, g, b byte
			var a float64
			switch {
			case off <= half:
				b, g, r = locateAccent[0], locateAccent[1], locateAccent[2]
				a = alpha
			case off <= outer:
				b, g, r = 0xFF, 0xFF, 0xFF
				a = alpha
			default:
				// One pixel of feathering at both rims.
				b, g, r = 0xFF, 0xFF, 0xFF
				a = alpha * (1 - (off - outer))
			}
			if a <= 0 {
				continue
			}
			i := row + x*4
			px[i+0] = premul(b, a)
			px[i+1] = premul(g, a)
			px[i+2] = premul(r, a)
			px[i+3] = byte(a * 255)
		}
	}
}

func premul(v byte, a float64) byte { return byte(float64(v) * a) }
