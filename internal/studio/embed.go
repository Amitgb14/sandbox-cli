package studio

import (
	"embed"
	"io/fs"
)

// The built UI, when this binary was built with one: `make studio` exports the
// app in studio/ into ui/, and a release build always does. A plain `go build`
// has only ui/.keep, and Studio then serves a page saying how to build it.
//
//go:embed all:ui
var uiFiles embed.FS

// EmbeddedUI is the UI built into this binary, or nil.
func EmbeddedUI() fs.FS {
	sub, err := fs.Sub(uiFiles, "ui")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
