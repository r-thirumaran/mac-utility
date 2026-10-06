//go:build windows

package main

import (
	"fmt"
	"math"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
)

// Reach's own SendInput-only injector (no hooks, no shared input package).
var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procSendInput        = user32.NewProc("SendInput")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseEventfMove        = 0x0001
	mouseEventfLeftDown    = 0x0002
	mouseEventfLeftUp      = 0x0004
	mouseEventfRightDown   = 0x0008
	mouseEventfRightUp     = 0x0010
	mouseEventfMiddleDown  = 0x0020
	mouseEventfMiddleUp    = 0x0040
	mouseEventfWheel       = 0x0800
	mouseEventfHWheel      = 0x1000
	mouseEventfVirtualDesk = 0x4000
	mouseEventfAbsolute    = 0x8000

	keyEventfExtendedKey = 0x0001
	keyEventfKeyUp       = 0x0002
	keyEventfUnicode     = 0x0004
	keyEventfScanCode    = 0x0008

	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79

	wheelDelta    = 120
	pixelsPerLine = 40.0 // scroll pixels per wheel notch
)

// mouseInput / keybdInput are MOUSEINPUT / KEYBDINPUT; input is INPUT with
// the union as 32 aligned bytes (40 bytes in all on amd64/arm64).
type mouseInput struct {
	dx, dy      int32
	mouseData   uint32
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type keybdInput struct {
	wVk, wScan  uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type input struct {
	typ uint32
	_   uint32
	u   [4]uint64
}

func mouseEvent(flags uint32, dx, dy int32, data uint32) input {
	var in input
	in.typ = inputMouse
	mi := (*mouseInput)(unsafe.Pointer(&in.u))
	mi.dx, mi.dy, mi.mouseData, mi.dwFlags = dx, dy, data, flags
	return in
}

func keyEvent(vk, scan uint16, flags uint32) input {
	var in input
	in.typ = inputKeyboard
	ki := (*keybdInput)(unsafe.Pointer(&in.u))
	ki.wVk, ki.wScan, ki.dwFlags = vk, scan, flags
	return in
}

// sendInputs injects the events in one SendInput call.
func sendInputs(ins []input) error {
	if len(ins) == 0 {
		return nil
	}
	n, _, e := procSendInput.Call(uintptr(len(ins)), uintptr(unsafe.Pointer(&ins[0])), unsafe.Sizeof(ins[0]))
	if int(n) != len(ins) {
		return fmt.Errorf("SendInput injected %d of %d events: %v", n, len(ins), e)
	}
	return nil
}

func systemMetric(index int) int32 {
	v, _, _ := procGetSystemMetrics.Call(uintptr(index))
	return int32(v)
}

// virtualScreen is the bounding box of all monitors in pixels.
func virtualScreen() (x, y, w, h int32) {
	return systemMetric(smXVirtualScreen), systemMetric(smYVirtualScreen), systemMetric(smCXVirtualScreen), systemMetric(smCYVirtualScreen)
}

// injector implements the mouse and keyboard part of Backend on Windows.
type injector struct {
	mu           sync.Mutex
	accX, accY   float64 // sub-pixel remainders of relative moves
	wheelX       float64 // sub-notch remainders of scrolls, in wheel units
	wheelY       float64
	movedAtLeast bool
}

func (in *injector) MouseMove(dx, dy float64) error {
	in.mu.Lock()
	in.accX += dx
	in.accY += dy
	ix, iy := math.Trunc(in.accX), math.Trunc(in.accY)
	in.accX -= ix
	in.accY -= iy
	in.mu.Unlock()
	if ix == 0 && iy == 0 {
		return nil
	}
	return sendInputs([]input{mouseEvent(mouseEventfMove, int32(ix), int32(iy), 0)})
}

// moveToPixel moves the pointer to an absolute virtual-desktop pixel.
func moveToPixel(px, py float64) error {
	vx, vy, vw, vh := virtualScreen()
	if vw <= 1 || vh <= 1 {
		return fmt.Errorf("no virtual screen")
	}
	ax := (px - float64(vx)) * 65535 / float64(vw-1)
	ay := (py - float64(vy)) * 65535 / float64(vh-1)
	ax = math.Round(clampF(ax, 0, 65535))
	ay = math.Round(clampF(ay, 0, 65535))
	return sendInputs([]input{mouseEvent(mouseEventfMove|mouseEventfAbsolute|mouseEventfVirtualDesk, int32(ax), int32(ay), 0)})
}

func buttonFlags(button string, down bool) (uint32, error) {
	switch button {
	case "left":
		if down {
			return mouseEventfLeftDown, nil
		}
		return mouseEventfLeftUp, nil
	case "right":
		if down {
			return mouseEventfRightDown, nil
		}
		return mouseEventfRightUp, nil
	case "middle":
		if down {
			return mouseEventfMiddleDown, nil
		}
		return mouseEventfMiddleUp, nil
	}
	return 0, bridge.Errorf(bridge.CodeInvalidParams, "unknown button %q", button)
}

func (in *injector) MouseButton(button string, down bool) error {
	f, err := buttonFlags(button, down)
	if err != nil {
		return err
	}
	return sendInputs([]input{mouseEvent(f, 0, 0, 0)})
}

func (in *injector) MouseClick(button string, count int) error {
	d, err := buttonFlags(button, true)
	if err != nil {
		return err
	}
	u, _ := buttonFlags(button, false)
	ins := make([]input, 0, 2*count)
	for i := 0; i < count; i++ {
		ins = append(ins, mouseEvent(d, 0, 0, 0), mouseEvent(u, 0, 0, 0))
	}
	return sendInputs(ins)
}

// Scroll converts pixels to wheel notches (pixelsPerLine px = 120). Positive
// dy scrolls down, which is a negative wheel delta; positive dx scrolls right.
func (in *injector) Scroll(dx, dy float64) error {
	in.mu.Lock()
	in.wheelX += dx / pixelsPerLine * wheelDelta
	in.wheelY -= dy / pixelsPerLine * wheelDelta
	wx, wy := math.Trunc(in.wheelX), math.Trunc(in.wheelY)
	in.wheelX -= wx
	in.wheelY -= wy
	in.mu.Unlock()
	var ins []input
	if wy != 0 {
		ins = append(ins, mouseEvent(mouseEventfWheel, 0, 0, uint32(int32(wy))))
	}
	if wx != 0 {
		ins = append(ins, mouseEvent(mouseEventfHWheel, 0, 0, uint32(int32(wx))))
	}
	return sendInputs(ins)
}

// strokeEvents returns the down or up event of a stroke.
func strokeEvents(k keyStroke, down bool) input {
	var flags uint32
	if !down {
		flags |= keyEventfKeyUp
	}
	if k.VK != 0 {
		return keyEvent(uint16(k.VK), 0, flags)
	}
	flags |= keyEventfScanCode
	if k.Ext {
		flags |= keyEventfExtendedKey
	}
	return keyEvent(0, uint16(k.Scan), flags)
}

// Key presses key with mods held (down && up), or sends one edge.
func (in *injector) Key(key string, mods []string, down, up bool) error {
	k, ok := resolveKey(key)
	if !ok {
		return bridge.Errorf(bridge.CodeInvalidParams, "unknown key %q", key)
	}
	held, ok := resolveMods(mods)
	if !ok {
		return bridge.Errorf(bridge.CodeInvalidParams, "unknown modifier in %v", mods)
	}
	var ins []input
	if down {
		for _, m := range held {
			ins = append(ins, strokeEvents(m, true))
		}
		ins = append(ins, strokeEvents(k, true))
	}
	if up {
		ins = append(ins, strokeEvents(k, false))
		for i := len(held) - 1; i >= 0; i-- {
			ins = append(ins, strokeEvents(held[i], false))
		}
	}
	return sendInputs(ins)
}

// TypeText types text with KEYEVENTF_UNICODE; "\n" presses Enter and "\t"
// Tab so that they reach every app (docs/reach-api.md §2).
func (in *injector) TypeText(text string) error {
	var ins []input
	flush := func() error {
		err := sendInputs(ins)
		ins = ins[:0]
		return err
	}
	for _, r := range text {
		switch r {
		case '\n', '\r', '\t':
			name := "Enter"
			if r == '\t' {
				name = "Tab"
			}
			if r == '\r' {
				continue
			}
			k, _ := resolveKey(name)
			ins = append(ins, strokeEvents(k, true), strokeEvents(k, false))
			continue
		}
		for _, u := range utf16.Encode([]rune{r}) {
			ins = append(ins, keyEvent(0, u, keyEventfUnicode), keyEvent(0, u, keyEventfUnicode|keyEventfKeyUp))
		}
		if len(ins) >= 64 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

// pressAlt taps ALT so that SetForegroundWindow is honoured for our process
// (the classic foreground-lock workaround).
func pressAlt() {
	_ = sendInputs([]input{keyEvent(vkMenu, 0, 0), keyEvent(vkMenu, 0, keyEventfKeyUp)})
}
