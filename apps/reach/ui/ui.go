// Package ui embeds the Reach phone app (the PWA) and the agent's own settings
// page — the same files — for the Go agent to serve on the LAN. The pages
// reference the shared kit with ./shared/… paths, so the server mounts
// shared/ui at /shared/ next to this file system (see apps/reach/win/overlayfs.go).
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
