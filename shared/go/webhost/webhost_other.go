//go:build !windows

package webhost

import (
	"context"

	"github.com/r-thirumaran/mac-utility/shared/go/winutil"
)

// window is the non-Windows stand-in: there is no native window, "Open" and
// the initial show hand the URL to xdg-open/open and ignore failures.
type window struct {
	url string
}

func newWindow(ctx context.Context, c Config, url string) *window {
	return &window{url: url}
}

// onReady opens the browser once (NoUI=false); failures are ignored.
func (w *window) onReady() { w.open() }

func (w *window) open() { _ = winutil.OpenURL(w.url) }

func (w *window) close() {}
