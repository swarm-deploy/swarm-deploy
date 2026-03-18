package web

import (
	"embed"
	"io/fs"
)

func fsSub(src embed.FS, path string) (fs.FS, error) {
	return fs.Sub(src, path)
}
