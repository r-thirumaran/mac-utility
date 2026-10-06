//go:build !windows

package input

// NewNative returns the platform input backend. Only Windows has one in the
// Go tree (macOS is the Swift app); everywhere else callers use NewFake.
func NewNative() (Device, error) { return nil, ErrNotSupported }

// SetDPIAware is a no-op off Windows.
func SetDPIAware() {}
