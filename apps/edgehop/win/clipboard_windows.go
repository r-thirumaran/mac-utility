//go:build windows

package main

import (
	"runtime"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/r-thirumaran/mac-utility/apps/edgehop/core"
)

// winClipboard is the system text clipboard (CF_UNICODETEXT) through user32
// and kernel32: OpenClipboard is retried briefly because another process may
// hold it for a moment. The clipboard is owned by the thread that opened it,
// so each operation pins its goroutine to one OS thread until CloseClipboard
// (otherwise a rescheduled goroutine would leave it open for every process).
type winClipboard struct{}

var (
	clipUser32           = windows.NewLazySystemDLL("user32.dll")
	clipKernel32         = windows.NewLazySystemDLL("kernel32.dll")
	procOpenClipboard    = clipUser32.NewProc("OpenClipboard")
	procCloseClipboard   = clipUser32.NewProc("CloseClipboard")
	procEmptyClipboard   = clipUser32.NewProc("EmptyClipboard")
	procGetClipboardData = clipUser32.NewProc("GetClipboardData")
	procSetClipboardData = clipUser32.NewProc("SetClipboardData")
	procGlobalAlloc      = clipKernel32.NewProc("GlobalAlloc")
	procGlobalFree       = clipKernel32.NewProc("GlobalFree")
	procGlobalLock       = clipKernel32.NewProc("GlobalLock")
	procGlobalUnlock     = clipKernel32.NewProc("GlobalUnlock")
	procGlobalSize       = clipKernel32.NewProc("GlobalSize")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
	maxClipboard  = 512 << 10 // bytes of UTF-16 forwarded at most (protocol frames are ≤ 1 MiB)
)

func newClipboard() core.Clipboard { return winClipboard{} }

// ptr turns a memory address returned by GlobalLock into a pointer (the
// usual indirection that keeps go vet's unsafe.Pointer check satisfied).
func ptr(addr uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&addr)) }

func openClipboard() bool {
	for i := 0; i < 10; i++ {
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// Text returns the clipboard text, ok=false when it holds no text.
func (winClipboard) Text() (string, bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if !openClipboard() {
		return "", false
	}
	defer procCloseClipboard.Call()
	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", false
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return "", false
	}
	defer procGlobalUnlock.Call(h)
	size, _, _ := procGlobalSize.Call(h)
	if size > maxClipboard {
		size = maxClipboard
	}
	n := int(size / 2)
	buf := unsafe.Slice((*uint16)(ptr(p)), n)
	end := 0
	for end < n && buf[end] != 0 {
		end++
	}
	return string(utf16.Decode(buf[:end])), true
}

// SetText replaces the clipboard with s.
func (winClipboard) SetText(s string) {
	u := utf16.Encode([]rune(s))
	u = append(u, 0)
	size := uintptr(len(u) * 2)
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return
	}
	copy(unsafe.Slice((*uint16)(ptr(p)), len(u)), u)
	procGlobalUnlock.Call(h)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if !openClipboard() {
		procGlobalFree.Call(h)
		return
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	if r, _, _ := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		procGlobalFree.Call(h) // the system owns the handle only on success
	}
}
