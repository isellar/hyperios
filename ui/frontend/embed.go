// Package frontend embeds the minimal HyperiOS web UI (a single animated
// status page) so the API server can serve it with no separate build step or
// frontend toolchain. See docs/critiques.md for why this stays intentionally
// minimal for now: the web UI is not v1's primary interface, but a basic
// "is it alive" visual is useful today.
package frontend

import (
	"embed"
	"io/fs"
)

//go:embed index.html style.css app.js
var files embed.FS

// FS returns the embedded frontend files rooted at the embed directory,
// suitable for http.FileServer(http.FS(...)).
func FS() fs.FS {
	return files
}
