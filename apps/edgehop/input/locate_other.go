//go:build !windows

package input

// Locate is the shake-to-locate overlay (docs/edgehop-protocol.md §6.3).
// Only the Windows backend draws one: macOS has its own shake-to-locate and
// the Linux build is for tests.
func Locate() {}
