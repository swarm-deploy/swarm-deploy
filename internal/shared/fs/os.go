package fs

import (
	"context"
	"os"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

type OSFileSystem struct {
}

func NewLocalFileSystem() FileSystem {
	return &OSFileSystem{}
}

func (OSFileSystem) ReadFile(_ context.Context, path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, faults.WrapIO(err)
	}

	return content, nil
}

func (OSFileSystem) WriteFile(_ context.Context, path string, payload []byte, mode os.FileMode) error {
	return faults.WrapIO(os.WriteFile(path, payload, mode))
}

func (OSFileSystem) CreateDirectory(_ context.Context, path string, perm os.FileMode) error {
	return faults.WrapIO(os.MkdirAll(path, perm))
}

func (OSFileSystem) Rename(_ context.Context, oldPath string, newPath string) error {
	return faults.WrapIO(os.Rename(oldPath, newPath))
}
