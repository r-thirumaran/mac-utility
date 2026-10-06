//go:build windows

package input

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/r-thirumaran/mac-utility/shared/go/winutil"
	"github.com/r-thirumaran/mac-utility/shared/keymap"
)

// Windows backend: WH_MOUSE_LL / WH_KEYBOARD_LL hooks on a dedicated locked
// OS thread with a GetMessageW loop (the same thread owns a hidden top-level
// window that receives WM_DISPLAYCHANGE), SendInput for injection, a blank
// system cursor while the pointer is parked on a remote machine. Not cgo.
//
// Classification of events: anything carrying our dwExtraInfo magic or the
// LLMHF/LLKHF_INJECTED flag is Injected (produced by this process or another
// synthetic source); everything else is physical. Physical mouse moves are
// swallowed (hook returns 1) when the core forwards them, so the hardware
// cursor stays parked and the next MSLLHOOKSTRUCT.pt is again relative to the
// parking spot: the delta is pt − park, no explicit re-centring needed (we
// re-centre defensively anyway).

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	shcore   = windows.NewLazySystemDLL("shcore.dll")

	procSetWindowsHookExW             = user32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx           = user32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx                = user32.NewProc("CallNextHookEx")
	procGetMessageW                   = user32.NewProc("GetMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procPostThreadMessageW            = user32.NewProc("PostThreadMessageW")
	procSendInput                     = user32.NewProc("SendInput")
	procGetCursorPos                  = user32.NewProc("GetCursorPos")
	procSetCursorPos                  = user32.NewProc("SetCursorPos")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procEnumDisplayMonitors           = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW               = user32.NewProc("GetMonitorInfoW")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	procSetSystemCursor               = user32.NewProc("SetSystemCursor")
	procCreateCursor                  = user32.NewProc("CreateCursor")
	procSystemParametersInfoW         = user32.NewProc("SystemParametersInfoW")
	procRegisterClassExW              = user32.NewProc("RegisterClassExW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procGetDoubleClickTime            = user32.NewProc("GetDoubleClickTime")
	procGetAsyncKeyState              = user32.NewProc("GetAsyncKeyState")
	procMapVirtualKeyW                = user32.NewProc("MapVirtualKeyW")
	procGetModuleHandleW              = kernel32.NewProc("GetModuleHandleW")
	procGetDpiForMonitor              = shcore.NewProc("GetDpiForMonitor")
)

// Win32 constants (winuser.h).
const (
	whKeyboardLL = 13
	whMouseLL    = 14

	wmQuit          = 0x0012
	wmDisplayChange = 0x007E
	wmKeyDown       = 0x0100
	wmKeyUp         = 0x0101
	wmSysKeyDown    = 0x0104
	wmSysKeyUp      = 0x0105
	wmMouseMove     = 0x0200
	wmLButtonDown   = 0x0201
	wmLButtonUp     = 0x0202
	wmRButtonDown   = 0x0204
	wmRButtonUp     = 0x0205
	wmMButtonDown   = 0x0207
	wmMButtonUp     = 0x0208
	wmMouseWheel    = 0x020A
	wmXButtonDown   = 0x020B
	wmXButtonUp     = 0x020C
	wmMouseHWheel   = 0x020E

	llmhfInjected = 0x01
	llkhfExtended = 0x01
	llkhfInjected = 0x10
	llkhfUp       = 0x80

	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfMove        = 0x0001
	mouseeventfLeftDown    = 0x0002
	mouseeventfLeftUp      = 0x0004
	mouseeventfRightDown   = 0x0008
	mouseeventfRightUp     = 0x0010
	mouseeventfMiddleDown  = 0x0020
	mouseeventfMiddleUp    = 0x0040
	mouseeventfXDown       = 0x0080
	mouseeventfXUp         = 0x0100
	mouseeventfWheel       = 0x0800
	mouseeventfHWheel      = 0x1000
	mouseeventfVirtualDesk = 0x4000
	mouseeventfAbsolute    = 0x8000

	keyeventfExtendedKey = 0x0001
	keyeventfKeyUp       = 0x0002
	keyeventfUnicode     = 0x0004
	keyeventfScanCode    = 0x0008

	smCxCursor        = 13
	smCyCursor        = 14
	smCxDoubleClk     = 36
	smCyDoubleClk     = 37
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCxVirtualScreen = 78
	smCyVirtualScreen = 79

	monitorinfofPrimary = 0x1
	mdtEffectiveDPI     = 0
	spiSetCursors       = 0x0057
	mapvkVkToVscEx      = 4

	xbutton1 = 0x0001
	xbutton2 = 0x0002

	vkPause = 0x13
)

// magic tags our own SendInput calls (dwExtraInfo) so the hooks classify
// them as injected even on systems that strip the INJECTED flag.
const magic uintptr = 0x45444748 // "EDGH"

// OCR_* ids replaced by the blank cursor while the pointer is parked.
var systemCursorIDs = []uintptr{32512, 32513, 32514, 32515, 32516, 32642, 32643, 32644, 32645, 32646, 32648, 32649, 32650}

type point struct{ X, Y int32 }

type rect struct{ Left, Top, Right, Bottom int32 }

type msllhookstruct struct {
	Pt        point
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type kbdllhookstruct struct {
	VkCode    uint32
	ScanCode  uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type mouseInput struct {
	Dx, Dy    int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type keybdInput struct {
	Vk, Scan  uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

// inputRecord is the INPUT struct: the union begins at the pointer-aligned
// offset after Type (8 on amd64/arm64, 4 on 386) and MOUSEINPUT is its
// largest member, so sizeof is 40 on 64-bit Windows as SendInput expects.
type inputRecord struct {
	Type uint32
	Mi   mouseInput
}

func (r *inputRecord) ki() *keybdInput { return (*keybdInput)(unsafe.Pointer(&r.Mi)) }

type msg struct {
	Hwnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       point
	LPrivate uint32
}

type wndClassExW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

// monitorInfoExW is MONITORINFOEXW: MONITORINFO (40 bytes) + szDevice[32].
type monitorInfoExW struct {
	CbSize    uint32
	RcMonitor rect
	RcWork    rect
	DwFlags   uint32
	SzDevice  [32]uint16
}

// SetDPIAware makes the process per-monitor DPI aware (v2) so every
// coordinate the hooks and SendInput see is in physical pixels. Call it
// before any window or hook exists.
func SetDPIAware() {
	if err := procSetProcessDpiAwarenessContext.Find(); err == nil {
		// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 is the pseudo-handle -4.
		if r, _, _ := procSetProcessDpiAwarenessContext.Call(^uintptr(3)); r != 0 {
			return
		}
	}
	_, _, _ = procSetProcessDPIAware.Call()
}

// clickState tracks multi-click counting for one button.
type clickState struct {
	at    time.Time
	x, y  int32
	count int
}

// winDevice is the Windows Device.
type winDevice struct {
	mu       sync.Mutex
	fn       func(Event) bool
	onDispl  []func()
	threadID uint32
	hooks    [2]uintptr
	hwnd     uintptr
	running  bool
	stopped  chan struct{}

	// cursor bookkeeping (written on the hook thread, read under mu)
	lastX, lastY int32
	havePos      bool
	locked       bool
	parkX, parkY int32
	hidden       bool

	mods     uint8
	keysDown map[uint32]bool
	clicks   [5]clickState
	dblTime  time.Duration
	dblX     int32
	dblY     int32
}

var (
	current atomic.Pointer[winDevice] // the device whose hooks are installed (one per process)

	callbackOnce sync.Once
	mouseCb      uintptr
	kbdCb        uintptr
	wndCb        uintptr
	enumCb       uintptr

	enumMu   sync.Mutex
	enumList []Display
)

// NewNative returns the Windows hook/SendInput backend.
func NewNative() (Device, error) { return newWinDevice(), nil }

func newWinDevice() *winDevice {
	d := &winDevice{keysDown: map[uint32]bool{}, dblTime: 500 * time.Millisecond, dblX: 4, dblY: 4}
	if ms, _, _ := procGetDoubleClickTime.Call(); ms > 0 {
		d.dblTime = time.Duration(ms) * time.Millisecond
	}
	if x := systemMetric(smCxDoubleClk); x > 0 {
		d.dblX = x
	}
	if y := systemMetric(smCyDoubleClk); y > 0 {
		d.dblY = y
	}
	return d
}

func initCallbacks() {
	callbackOnce.Do(func() {
		mouseCb = syscall.NewCallback(mouseProc)
		kbdCb = syscall.NewCallback(keyboardProc)
		wndCb = syscall.NewCallback(wndProc)
		enumCb = syscall.NewCallback(enumProc)
	})
}

// ---------------------------------------------------------------- Capturer

// Start installs the hooks on a locked OS thread and pumps its messages.
func (d *winDevice) Start(fn func(Event) bool) error {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return errors.New("input: already started")
	}
	d.fn = fn
	d.running = true
	d.stopped = make(chan struct{})
	d.mu.Unlock()
	initCallbacks()
	ready := make(chan error, 1)
	go d.loop(ready)
	if err := <-ready; err != nil {
		d.mu.Lock()
		d.running = false
		d.mu.Unlock()
		return err
	}
	return nil
}

func (d *winDevice) loop(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(d.stopped)
	current.Store(d)
	d.threadID = windows.GetCurrentThreadId()
	d.seedModifiers()
	hinst, _, _ := procGetModuleHandleW.Call(0)
	d.hwnd = createHiddenWindow(hinst)
	for i, id := range []uintptr{whMouseLL, whKeyboardLL} {
		cb := mouseCb
		if id == whKeyboardLL {
			cb = kbdCb
		}
		h, _, err := procSetWindowsHookExW.Call(id, cb, hinst, 0)
		if h == 0 {
			for _, hh := range d.hooks[:i] {
				_, _, _ = procUnhookWindowsHookEx.Call(hh)
			}
			d.hooks = [2]uintptr{}
			if d.hwnd != 0 {
				_, _, _ = procDestroyWindow.Call(d.hwnd)
				d.hwnd = 0
			}
			current.Store(nil)
			ready <- fmt.Errorf("input: SetWindowsHookEx(%d): %w", id, err)
			return
		}
		d.hooks[i] = h
	}
	ready <- nil
	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 || int32(r) == -1 || m.Message == wmQuit {
			break
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	for _, h := range d.hooks {
		if h != 0 {
			_, _, _ = procUnhookWindowsHookEx.Call(h)
		}
	}
	d.hooks = [2]uintptr{}
	if d.hwnd != 0 {
		_, _, _ = procDestroyWindow.Call(d.hwnd)
		d.hwnd = 0
	}
	d.showCursor()
	current.Store(nil)
}

// Stop ends the hook thread and restores the cursor.
func (d *winDevice) Stop() {
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return
	}
	d.running = false
	tid, stopped := d.threadID, d.stopped
	d.mu.Unlock()
	_, _, _ = procPostThreadMessageW.Call(uintptr(tid), wmQuit, 0, 0)
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
	}
}

// seedModifiers reads the modifiers held when the hooks start.
func (d *winDevice) seedModifiers() {
	vks := []struct {
		vk  uintptr
		bit uint8
	}{{0xA2, 0x01}, {0xA0, 0x02}, {0xA4, 0x04}, {0x5B, 0x08}, {0xA3, 0x10}, {0xA1, 0x20}, {0xA5, 0x40}, {0x5C, 0x80}}
	var mods uint8
	for _, v := range vks {
		if r, _, _ := procGetAsyncKeyState.Call(v.vk); r&0x8000 != 0 {
			mods |= v.bit
		}
	}
	d.mu.Lock()
	d.mods = mods
	d.mu.Unlock()
}

// LockCursor parks the pointer at the centre of the primary display and
// replaces the system cursors with a blank one (restored on unlock).
func (d *winDevice) LockCursor(lock bool) {
	if lock {
		var cx, cy int32
		for _, s := range d.Displays() {
			if s.Primary {
				cx, cy = int32(s.X+s.W/2), int32(s.Y+s.H/2)
				break
			}
		}
		d.mu.Lock()
		d.locked = true
		d.parkX, d.parkY = cx, cy
		d.lastX, d.lastY, d.havePos = cx, cy, true
		d.mu.Unlock()
		d.MoveTo(int(cx), int(cy))
		d.hideCursor()
		return
	}
	d.mu.Lock()
	d.locked = false
	d.mu.Unlock()
	d.showCursor()
}

func (d *winDevice) hideCursor() {
	d.mu.Lock()
	if d.hidden {
		d.mu.Unlock()
		return
	}
	d.hidden = true
	d.mu.Unlock()
	w := uintptr(int32(systemMetric(smCxCursor)))
	h := uintptr(int32(systemMetric(smCyCursor)))
	if int32(w) <= 0 || int32(h) <= 0 || w%16 != 0 {
		w, h = 32, 32 // the mask rows below are packed, which needs a WORD-aligned width
	}
	n := int(w) * int(h) / 8
	and := make([]byte, n)
	xor := make([]byte, n)
	for i := range and {
		and[i] = 0xFF // AND all ones, XOR all zeros = fully transparent
	}
	hinst, _, _ := procGetModuleHandleW.Call(0)
	for _, id := range systemCursorIDs {
		// SetSystemCursor destroys the handle it is given, so every id needs its own cursor.
		cur, _, _ := procCreateCursor.Call(hinst, 0, 0, w, h, uintptr(unsafe.Pointer(&and[0])), uintptr(unsafe.Pointer(&xor[0])))
		if cur == 0 {
			continue
		}
		_, _, _ = procSetSystemCursor.Call(cur, id)
	}
}

func (d *winDevice) showCursor() {
	d.mu.Lock()
	if !d.hidden {
		d.mu.Unlock()
		return
	}
	d.hidden = false
	d.mu.Unlock()
	_, _, _ = procSystemParametersInfoW.Call(spiSetCursors, 0, 0, 0)
}

// OnDisplaysChanged registers fn for WM_DISPLAYCHANGE.
func (d *winDevice) OnDisplaysChanged(fn func()) {
	d.mu.Lock()
	d.onDispl = append(d.onDispl, fn)
	d.mu.Unlock()
}

// Displays enumerates the monitors in virtual-screen (physical pixel)
// coordinates with their effective DPI scale.
func (d *winDevice) Displays() []Display {
	initCallbacks()
	enumMu.Lock()
	defer enumMu.Unlock()
	enumList = nil
	_, _, _ = procEnumDisplayMonitors.Call(0, 0, enumCb, 0)
	out := append([]Display(nil), enumList...)
	if len(out) == 0 {
		x, y, w, h := virtualScreen()
		out = []Display{{ID: "virtual", X: int(x), Y: int(y), W: int(w), H: int(h), Scale: 1, Primary: true}}
	}
	return out
}

func enumProc(hMonitor, hdc uintptr, lprc *rect, lParam uintptr) uintptr {
	var mi monitorInfoExW
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if r, _, _ := procGetMonitorInfoW.Call(hMonitor, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return 1
	}
	scale := 1.0
	if procGetDpiForMonitor.Find() == nil {
		var dpiX, dpiY uint32
		if hr, _, _ := procGetDpiForMonitor.Call(hMonitor, mdtEffectiveDPI, uintptr(unsafe.Pointer(&dpiX)), uintptr(unsafe.Pointer(&dpiY))); hr == 0 && dpiX > 0 {
			scale = float64(dpiX) / 96
		}
	}
	r := mi.RcMonitor
	enumList = append(enumList, Display{
		ID:      windows.UTF16ToString(mi.SzDevice[:]),
		X:       int(r.Left),
		Y:       int(r.Top),
		W:       int(r.Right - r.Left),
		H:       int(r.Bottom - r.Top),
		Scale:   scale,
		Primary: mi.DwFlags&monitorinfofPrimary != 0,
	})
	return 1
}

// ---------------------------------------------------------------- hooks

func (d *winDevice) deliver(ev Event) bool {
	d.mu.Lock()
	fn := d.fn
	d.mu.Unlock()
	if fn == nil {
		return false
	}
	return fn(ev)
}

// mouseProc is the WH_MOUSE_LL procedure. lParam is typed as the struct
// pointer it is (NewCallback accepts pointer-sized arguments).
func mouseProc(nCode int32, wParam uintptr, ms *msllhookstruct) uintptr {
	d := current.Load()
	if nCode >= 0 && d != nil && ms != nil {
		injected := ms.Flags&llmhfInjected != 0 || ms.ExtraInfo == magic
		if swallow := d.mouseEvent(uint32(wParam), ms, injected); swallow {
			return 1
		}
	}
	r, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, uintptr(unsafe.Pointer(ms)))
	return r
}

func (d *winDevice) mouseEvent(m uint32, ms *msllhookstruct, injected bool) bool {
	ev := Event{Injected: injected, X: int(ms.Pt.X), Y: int(ms.Pt.Y)}
	switch m {
	case wmMouseMove:
		ev.Kind = MouseMove
		d.mu.Lock()
		if d.havePos {
			ev.DX, ev.DY = int(ms.Pt.X-d.lastX), int(ms.Pt.Y-d.lastY)
		}
		locked := d.locked
		d.mu.Unlock()
		swallow := d.deliver(ev) && !injected
		d.mu.Lock()
		if !swallow { // the cursor really moved
			d.lastX, d.lastY, d.havePos = ms.Pt.X, ms.Pt.Y, true
		} else {
			d.lastX, d.lastY = d.parkX, d.parkY
		}
		d.mu.Unlock()
		if swallow && locked {
			d.recentre()
		}
		return swallow
	case wmLButtonDown, wmLButtonUp, wmRButtonDown, wmRButtonUp, wmMButtonDown, wmMButtonUp, wmXButtonDown, wmXButtonUp:
		ev.Kind = MouseButton
		switch m {
		case wmLButtonDown, wmLButtonUp:
			ev.Button = ButtonLeft
		case wmRButtonDown, wmRButtonUp:
			ev.Button = ButtonRight
		case wmMButtonDown, wmMButtonUp:
			ev.Button = ButtonMiddle
		default:
			if uint16(ms.MouseData>>16) == xbutton2 {
				ev.Button = ButtonForward
			} else {
				ev.Button = ButtonBack
			}
		}
		ev.Down = m == wmLButtonDown || m == wmRButtonDown || m == wmMButtonDown || m == wmXButtonDown
		ev.ClickCount = d.clickCount(ev.Button, ev.Down, ms.Pt)
		return d.deliver(ev) && !injected
	case wmMouseWheel:
		ev.Kind = Scroll
		ev.DY = int(int16(ms.MouseData >> 16))
		ev.Flags = ScrollDiscrete
		return d.deliver(ev) && !injected
	case wmMouseHWheel:
		ev.Kind = Scroll
		ev.DX = int(int16(ms.MouseData >> 16))
		ev.Flags = ScrollDiscrete
		return d.deliver(ev) && !injected
	}
	return false
}

// clickCount counts double/triple clicks the way the shell does (same
// button within GetDoubleClickTime and the double-click rectangle).
func (d *winDevice) clickCount(b Button, down bool, pt point) int {
	if int(b) >= len(d.clicks) {
		return 1
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	c := &d.clicks[b]
	if !down {
		if c.count == 0 {
			return 1
		}
		return c.count
	}
	now := time.Now()
	dx, dy := pt.X-c.x, pt.Y-c.y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	if c.count > 0 && now.Sub(c.at) <= d.dblTime && dx <= d.dblX && dy <= d.dblY {
		c.count++
	} else {
		c.count = 1
	}
	c.at, c.x, c.y = now, pt.X, pt.Y
	return c.count
}

// recentre puts the hardware cursor back on the parking spot if a swallowed
// move still leaked (it should not; this is belt and braces).
func (d *winDevice) recentre() {
	var p point
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p))); r == 0 {
		return
	}
	d.mu.Lock()
	px, py := d.parkX, d.parkY
	d.mu.Unlock()
	if p.X != px || p.Y != py {
		_, _, _ = procSetCursorPos.Call(uintptr(px), uintptr(py))
	}
}

// keyboardProc is the WH_KEYBOARD_LL procedure.
func keyboardProc(nCode int32, wParam uintptr, kb *kbdllhookstruct) uintptr {
	d := current.Load()
	if nCode >= 0 && d != nil && kb != nil {
		injected := kb.Flags&llkhfInjected != 0 || kb.ExtraInfo == magic
		if swallow := d.keyEvent(uint32(wParam), kb, injected); swallow {
			return 1
		}
	}
	r, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, uintptr(unsafe.Pointer(kb)))
	return r
}

func (d *winDevice) keyEvent(m uint32, kb *kbdllhookstruct, injected bool) bool {
	up := kb.Flags&llkhfUp != 0 || m == wmKeyUp || m == wmSysKeyUp
	ext := kb.Flags&llkhfExtended != 0
	scan := int(kb.ScanCode & 0xFF)
	if scan == 0 {
		v, _, _ := procMapVirtualKeyW.Call(uintptr(kb.VkCode), mapvkVkToVscEx)
		scan = int(v & 0xFF)
		if v&0xFF00 == 0xE000 {
			ext = true
		}
	}
	if kb.VkCode == vkPause {
		scan, ext = 0x45, false // Pause is E1 1D 45; the hook reports 0x45 without E0
	}
	key, ok := keymap.Default().ByWinScan(scan, ext)
	if !ok {
		return false // a key the table does not know passes through
	}
	ev := Event{Kind: Key, Injected: injected, Wire: key.Wire, Down: !up}
	d.mu.Lock()
	if !injected {
		if bit, isMod := keymap.ModifierBit(key.Wire); isMod {
			if up {
				d.mods &^= bit
			} else {
				d.mods |= bit
			}
		}
		if up {
			delete(d.keysDown, kb.VkCode)
		} else {
			ev.Repeat = d.keysDown[kb.VkCode]
			d.keysDown[kb.VkCode] = true
		}
	}
	ev.Mods = d.mods
	d.mu.Unlock()
	return d.deliver(ev) && !injected
}

// ---------------------------------------------------------------- hidden window

func createHiddenWindow(hinst uintptr) uintptr {
	className, _ := windows.UTF16PtrFromString("EdgehopInputWindow")
	wc := wndClassExW{
		CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		LpfnWndProc:   wndCb,
		HInstance:     hinst,
		LpszClassName: className,
	}
	_, _, _ = procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	title, _ := windows.UTF16PtrFromString("Edgehop")
	// A top-level (not message-only) window: broadcasts such as
	// WM_DISPLAYCHANGE reach hidden top-level windows but not HWND_MESSAGE ones.
	hwnd, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)), 0, 0, 0, 0, 0, 0, 0, hinst, 0)
	return hwnd
}

func wndProc(hwnd uintptr, m uint32, wParam, lParam uintptr) uintptr {
	if m == wmDisplayChange {
		if d := current.Load(); d != nil {
			d.mu.Lock()
			fns := append([]func(){}, d.onDispl...)
			d.mu.Unlock()
			for _, fn := range fns {
				go fn() // never block the hook thread
			}
		}
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(m), wParam, lParam)
	return r
}

// ---------------------------------------------------------------- Injector

func sendInput(recs []inputRecord) {
	if len(recs) == 0 {
		return
	}
	_, _, _ = procSendInput.Call(uintptr(len(recs)), uintptr(unsafe.Pointer(&recs[0])), unsafe.Sizeof(recs[0]))
}

// systemMetric reads one GetSystemMetrics value (an int, so only the low
// 32 bits of the return register are meaningful).
func systemMetric(index uintptr) int32 {
	r, _, _ := procGetSystemMetrics.Call(index)
	return int32(r)
}

// virtualScreen returns the virtual desktop rectangle.
func virtualScreen() (x, y, w, h int32) {
	return systemMetric(smXVirtualScreen), systemMetric(smYVirtualScreen), systemMetric(smCxVirtualScreen), systemMetric(smCyVirtualScreen)
}

// MoveTo warps the cursor with an absolute, virtual-desktop SendInput
// (immune to pointer acceleration); winutil.AbsoluteCoordinate does the
// 0..65535 normalisation so the pixel is hit exactly.
func (d *winDevice) MoveTo(x, y int) {
	vx, vy, vw, vh := virtualScreen()
	if vw <= 0 || vh <= 0 {
		return
	}
	nx := winutil.AbsoluteCoordinate(x, int(vx), int(vw))
	ny := winutil.AbsoluteCoordinate(y, int(vy), int(vh))
	rec := inputRecord{Type: inputMouse}
	rec.Mi = mouseInput{Dx: nx, Dy: ny, Flags: mouseeventfMove | mouseeventfAbsolute | mouseeventfVirtualDesk, ExtraInfo: magic}
	sendInput([]inputRecord{rec})
}

// MoveRelative moves by (dx, dy) from the current position, as an absolute
// move so the remote sender's already-accelerated deltas are not accelerated
// a second time here.
func (d *winDevice) MoveRelative(dx, dy int) {
	x, y := d.CursorPos()
	d.MoveTo(x+dx, y+dy)
}

// Button presses or releases a mouse button.
func (d *winDevice) Button(b Button, down bool, clicks int) {
	var flags uint32
	var data uint32
	switch b {
	case ButtonLeft:
		flags = mouseeventfLeftDown
		if !down {
			flags = mouseeventfLeftUp
		}
	case ButtonRight:
		flags = mouseeventfRightDown
		if !down {
			flags = mouseeventfRightUp
		}
	case ButtonMiddle:
		flags = mouseeventfMiddleDown
		if !down {
			flags = mouseeventfMiddleUp
		}
	case ButtonBack, ButtonForward:
		flags = mouseeventfXDown
		if !down {
			flags = mouseeventfXUp
		}
		data = xbutton1
		if b == ButtonForward {
			data = xbutton2
		}
	default:
		return
	}
	rec := inputRecord{Type: inputMouse}
	rec.Mi = mouseInput{MouseData: data, Flags: flags, ExtraInfo: magic}
	sendInput([]inputRecord{rec})
}

// Scroll injects wheel motion: 120 per notch, positive dy = wheel forward
// (away from the user), positive dx = right.
func (d *winDevice) Scroll(dx, dy int, discrete bool) {
	var recs []inputRecord
	if dy != 0 {
		rec := inputRecord{Type: inputMouse}
		rec.Mi = mouseInput{MouseData: uint32(int32(dy)), Flags: mouseeventfWheel, ExtraInfo: magic}
		recs = append(recs, rec)
	}
	if dx != 0 {
		rec := inputRecord{Type: inputMouse}
		rec.Mi = mouseInput{MouseData: uint32(int32(dx)), Flags: mouseeventfHWheel, ExtraInfo: magic}
		recs = append(recs, rec)
	}
	sendInput(recs)
}

// vkFallback maps wire usages without a scan code to virtual keys.
var vkFallback = map[int]uint16{
	0x48:   vkPause,
	0x10CD: 0xB3, // play/pause
	0x10B5: 0xB0, // next track
	0x10B6: 0xB1, // previous track
	0x10B7: 0xB2, // stop
	0x10E2: 0xAD, // mute
	0x10E9: 0xAF, // volume up
	0x10EA: 0xAE, // volume down
}

// Key presses or releases the key with the given HID usage by scan code
// (the receiver's keyboard layout applies, as the protocol requires).
func (d *winDevice) Key(wire int, down bool, mods uint8) {
	rec := inputRecord{Type: inputKeyboard}
	ki := rec.ki()
	ki.ExtraInfo = magic
	if !down {
		ki.Flags |= keyeventfKeyUp
	}
	if vk, ok := vkFallback[wire]; ok {
		ki.Vk = vk
		sendInput([]inputRecord{rec})
		return
	}
	key, ok := keymap.Default().ByWire(wire)
	if !ok || key.WinScan == 0 {
		return
	}
	ki.Scan = uint16(key.WinScan)
	ki.Flags |= keyeventfScanCode
	if key.WinExt {
		ki.Flags |= keyeventfExtendedKey
	}
	sendInput([]inputRecord{rec})
}

// Text types s verbatim with KEYEVENTF_UNICODE (one down/up per UTF-16 unit).
func (d *winDevice) Text(s string) {
	var recs []inputRecord
	for _, u := range utf16.Encode([]rune(s)) {
		for _, up := range []bool{false, true} {
			rec := inputRecord{Type: inputKeyboard}
			ki := rec.ki()
			ki.Scan = u
			ki.Flags = keyeventfUnicode
			if up {
				ki.Flags |= keyeventfKeyUp
			}
			ki.ExtraInfo = magic
			recs = append(recs, rec)
		}
	}
	sendInput(recs)
}

// CursorPos returns the hardware cursor position.
func (d *winDevice) CursorPos() (int, int) {
	var p point
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p))); r == 0 {
		d.mu.Lock()
		defer d.mu.Unlock()
		return int(d.lastX), int(d.lastY)
	}
	return int(p.X), int(p.Y)
}
