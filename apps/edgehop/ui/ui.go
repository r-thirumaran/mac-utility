// Package ui embeds the Edgehop web UI (index.html, app.js, layout.js,
// pairing.js, app.css) for the Go backend to serve. The page references the
// shared kit with ./shared/… paths, so the server mounts shared/ui at
// /shared/ next to this file system (apps/edgehop/win/overlayfs.go).
package ui

import (
	"embed"
	"io/fs"
)

//go:embed static
var embedded embed.FS

// Static is the UI with index.html at its root.
var Static fs.FS = mustSub(embedded, "static")

func mustSub(f embed.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return s
}
