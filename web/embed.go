// Package web embeds the built front end (the output of `npm run build` in this directory).
// web/dist/.gitkeep is tracked so the server compiles even when the front end was not built.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the build output rooted at dist: index.html at the top, hashed files under assets/.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is a literal directory name embedded above
	}
	return sub
}
