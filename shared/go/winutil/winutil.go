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

// AbsoluteCoordinate converts a virtual-desktop pixel p on an axis that
// starts at origin and is size pixels long into the 0..65535 value SendInput
// takes with MOUSEEVENTF_ABSOLUTE|MOUSEEVENTF_VIRTUALDESK. Windows maps a
// normalised value n back to a pixel as (n*size)>>16 (the documented
// "65535 is the last pixel" reading amounts to round(n*(size-1)/65535));
// the two agree on a half-pixel-wide band for every pixel and the value
// returned is the centre of that band, n = 65536*(p+1/4+p/(2*size))/size,
// so either mapping yields p with a quarter pixel of margin. A plain
// p*65535/(size-1) misses low pixels by one under the first mapping. Pixels
// outside the axis clamp to its ends; a non-positive size yields 0.
func AbsoluteCoordinate(p, origin, size int) int32 {
	if size <= 0 {
		return 0
	}
	q, sz := int64(p-origin), int64(size)
	// round(65536 * ((4q+1)*size + 2q) / (4*size*size))
	n := (65536*((4*q+1)*sz+2*q) + 2*sz*sz) / (4 * sz * sz)
	if n < 0 {
		n = 0
	}
	if n > 65535 {
		n = 65535
	}
	return int32(n)
}
