//go:build !windows

package main

import "errors"

// newRealBackend exists only on Windows; other platforms run the fake.
func newRealBackend() (Backend, error) {
	return nil, errors.New("the real backend is Windows-only; run with --fake")
}
