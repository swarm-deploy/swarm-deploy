package fs

import (
	"context"
	"os"
)

type FileSystem interface {
	// ReadFile reads and returns file content by path.
	ReadFile(ctx context.Context, path string) ([]byte, error)

	// WriteFile writes payload to path with mode.
	WriteFile(ctx context.Context, path string, payload []byte, mode os.FileMode) error

	// CreateDirectory creates directory path with perm.
	CreateDirectory(ctx context.Context, path string, perm os.FileMode) error

	// Rename renames oldPath to newPath.
	Rename(ctx context.Context, oldPath string, newPath string) error
}
