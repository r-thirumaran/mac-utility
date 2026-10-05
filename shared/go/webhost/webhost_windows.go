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
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procLoadImageW          = user32.NewProc("LoadImageW")
	procSendMessageW        = user32.NewProc("SendMessageW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
)

const (
	imageIcon        = 1
	lrLoadFromFile   = 0x0010
	lrDefaultSize    = 0x0040
	wmSetIcon        = 0x0080
	iconSmall        = 0
	iconBig          = 1
	swRestore        = 9
	defaultWinWidth  = 1000
	defaultWinHeight = 700
)

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
	width, height := w.c.Width, w.c.Height
	if width <= 0 {
		width = defaultWinWidth
	}
	if height <= 0 {
		height = defaultWinHeight
	}
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
