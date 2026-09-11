package fs

import (
	"context"
	"os"
)

type OSFileSystem struct {
}

func NewLocalFileSystem() FileSystem {
	return &OSFileSystem{}
}

func (OSFileSystem) ReadFile(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}
