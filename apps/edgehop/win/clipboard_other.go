//go:build !windows

package main

import "github.com/r-thirumaran/mac-utility/apps/edgehop/core"

// newClipboard returns the in-memory clipboard used by the Linux test build.
func newClipboard() core.Clipboard { return &core.MemClipboard{} }
