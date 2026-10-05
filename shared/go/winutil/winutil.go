// Package winutil wraps the handful of Win32 calls the Windows apps need:
// elevation check and relaunch, single-instance mutex, autostart registry
// entry, opening a URL and running a console tool without a window.
//
// Every function exists on every platform so callers compile everywhere; on
// non-Windows systems IsElevated reports root, SingleInstance uses a lock
// file, OpenURL uses xdg-open/open, RunHidden is a plain exec and the rest
// return ErrNotSupported.
package winutil

import "errors"

// ErrNotSupported is returned by Windows-only operations on other platforms.
var ErrNotSupported = errors.New("winutil: not supported on this platform")
