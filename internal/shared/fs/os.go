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

func (OSFileSystem) WriteFile(_ context.Context, path string, payload []byte, mode os.FileMode) error {
	return os.WriteFile(path, payload, mode)
}

func (OSFileSystem) CreateDirectory(_ context.Context, path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (OSFileSystem) Rename(_ context.Context, oldPath string, newPath string) error {
	return os.Rename(oldPath, newPath)
}
