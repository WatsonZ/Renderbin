// Package web embeds the built SvelteKit frontend (web/build) so the
// backend binary can serve API and static assets from a single process.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// FS returns the embedded frontend build output rooted at dist/, ready to
// be served with http.FileServer.
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
