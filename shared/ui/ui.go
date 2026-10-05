// Package sharedui embeds the shared web UI kit (shared/ui/README.md) so a Go
// app can serve it at /shared/ next to its own static files. Only the kit's
// stylesheets and modules are embedded; the demo page is not.
package sharedui

import "embed"

// FS holds base.css, charts.js, ui.js, native.js and demo.js at its root.
//
//go:embed *.css *.js
var FS embed.FS
