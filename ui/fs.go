package ui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var embeddedFiles embed.FS

var FS fs.FS

func init() {
	dist, err := fs.Sub(embeddedFiles, "dist")
	if err != nil {
		panic(err)
	}

	FS = dist
}
