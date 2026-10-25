// Package web embeds the library UI's static files.
package web

import _ "embed"

// IndexHTML is the single-page library UI.
//
//go:embed index.html
var IndexHTML []byte
