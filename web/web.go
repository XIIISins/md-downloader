// Package web holds the library UI's static files (index.html, app.css, app.js).
package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html static
var files embed.FS

// FS returns the UI files, rooted so that "index.html" and "static/app.js" resolve.
func FS() fs.FS { return files }
