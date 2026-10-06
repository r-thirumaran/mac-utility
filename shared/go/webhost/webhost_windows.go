//go:build windows

package webhost

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/webviewloader"
	"golang.org/x/sys/windows"

	"github.com/r-thirumaran/mac-utility/shared/go/winutil"
)

var (
	user32                            = windows.NewLazySystemDLL("user32.dll")
	procLoadImageW                    = user32.NewProc("LoadImageW")
	procSendMessageW                  = user32.NewProc("SendMessageW")
	procShowWindow                    = user32.NewProc("ShowWindow")
	procSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	procGetDpiForSystem               = user32.NewProc("GetDpiForSystem")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
)

const (
	imageIcon        = 1
	lrLoadFromFile   = 0x0010
	lrDefaultSize    = 0x0040
	wmSetIcon        = 0x0080
	iconSmall        = 0
	iconBig          = 1
	swRestore        = 9
	smCxScreen       = 0
	smCyScreen       = 1
	defaultWinWidth  = 1000
	defaultWinHeight = 700
)

// setDPIAware opts the process into per-monitor DPI awareness (v2) before
// the tray and the window exist. Without it Windows renders the WebView2
// page at 96 dpi and bitmap-stretches it on every display scaled above
// 100 % (most laptops), which looks blurry. Apps that already set it
// (Edgehop, Reach) make this a no-op; the pseudo handle -4 is
// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2.
func setDPIAware() {
	if procSetProcessDpiAwarenessContext.Find() == nil {
		if r, _, _ := procSetProcessDpiAwarenessContext.Call(^uintptr(3)); r != 0 {
			return
		}
	}
	_, _, _ = procSetProcessDPIAware.Call()
}

// windowSize converts the logical (96 dpi) size the app asked for to the
// physical pixels a DPI-aware window needs on the primary display, and
// keeps it inside that display so go-webview2's centring never places the
// window off-screen.
func windowSize(width, height int) (int, int) {
	if width <= 0 {
		width = defaultWinWidth
	}
	if height <= 0 {
		height = defaultWinHeight
	}
	dpi := 96
	if procGetDpiForSystem.Find() == nil {
		if r, _, _ := procGetDpiForSystem.Call(); uint32(r) > 0 {
			dpi = int(uint32(r))
		}
	}
	width, height = width*dpi/96, height*dpi/96
	if r, _, _ := procGetSystemMetrics.Call(smCxScreen); int32(r) > 0 && width > int(int32(r)) {
		width = int(int32(r))
	}
	if r, _, _ := procGetSystemMetrics.Call(smCyScreen); int32(r) > 0 && height > int(int32(r)) {
		height = int(int32(r))
	}
	return width, height
}

// window owns the WebView2 window. It lives on a dedicated locked OS thread
// (loop) because the Win32 message pump must run where the window was
// created; the tray owns the main thread.
type window struct {
	c      Config
	url    string
	openCh chan struct{}

	mu   sync.Mutex
	wv   webview2.WebView
	hwnd uintptr
}

func newWindow(ctx context.Context, c Config, url string) *window {
	setDPIAware() // before the tray (main thread) or this window exists
	w := &window{c: c, url: url, openCh: make(chan struct{}, 1)}
	go w.loop(ctx)
	return w
}

// onReady runs once the tray exists.
func (w *window) onReady() {
	if w.c.ShowOnStart {
		w.open()
	}
}

// open shows the window: raises it when it exists, otherwise asks the loop
// to create one. Safe from any goroutine.
func (w *window) open() {
	w.mu.Lock()
	wv, hwnd := w.wv, w.hwnd
	w.mu.Unlock()
	if wv != nil {
		wv.Dispatch(func() { raise(hwnd) })
		return
	}
	select {
	case w.openCh <- struct{}{}:
	default:
	}
}

// close terminates the window's message loop if a window is open.
func (w *window) close() {
	w.mu.Lock()
	wv := w.wv
	w.mu.Unlock()
	if wv != nil {
		wv.Dispatch(wv.Terminate)
	}
}

func (w *window) loop(ctx context.Context) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.openCh:
		}
		w.show(ctx)
	}
}

// show creates the WebView2 window and pumps its messages until it closes.
// Without a usable WebView2 runtime the URL opens in the default browser.
func (w *window) show(ctx context.Context) {
	if !webviewAvailable() {
		_ = winutil.OpenURL(w.url)
		return
	}
	width, height := windowSize(w.c.Width, w.c.Height)
	dataPath := dataDir(w.c.Title)
	wv := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  dataPath,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  w.c.Title,
			Width:  uint(width),
			Height: uint(height),
			Center: true,
		},
	})
	if wv == nil {
		_ = winutil.OpenURL(w.url)
		return
	}
	hwnd := uintptr(wv.Window())
	w.mu.Lock()
	w.wv, w.hwnd = wv, hwnd
	w.mu.Unlock()
	applyIcon(hwnd, dataPath, w.c.IconICO)
	wv.Navigate(w.url)
	if ctx.Err() != nil {
		wv.Dispatch(wv.Terminate)
	}
	wv.Run()
	w.mu.Lock()
	w.wv, w.hwnd = nil, 0
	w.mu.Unlock()
}

func webviewAvailable() bool {
	v, err := webviewloader.GetInstalledVersion()
	return err == nil && v != ""
}

// dataDir is %LOCALAPPDATA%\<Title>\webview.
func dataDir(title string) string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		if d, err := os.UserCacheDir(); err == nil {
			base = d
		} else {
			base = os.TempDir()
		}
	}
	return filepath.Join(base, title, "webview")
}

func raise(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	_, _, _ = procShowWindow.Call(hwnd, swRestore)
	_, _, _ = procSetForegroundWindow.Call(hwnd)
}

// applyIcon writes the .ico next to the WebView2 data and sets it as the
// window's big and small icon. Best effort.
func applyIcon(hwnd uintptr, dataPath string, ico []byte) {
	if hwnd == 0 || len(ico) == 0 {
		return
	}
	if err := os.MkdirAll(dataPath, 0o700); err != nil {
		return
	}
	path := filepath.Join(dataPath, "icon.ico")
	if err := os.WriteFile(path, ico, 0o600); err != nil {
		return
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return
	}
	h, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(p)), imageIcon, 0, 0, lrLoadFromFile|lrDefaultSize)
	if h == 0 {
		return
	}
	_, _, _ = procSendMessageW.Call(hwnd, wmSetIcon, iconBig, h)
	_, _, _ = procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, h)
}
