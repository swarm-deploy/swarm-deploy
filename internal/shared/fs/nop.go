package fs

import (
	"context"
	"os"
)

type NopFileSystem struct {
}

func (NopFileSystem) ReadFile(context.Context, string) ([]byte, error) {
	return []byte{}, nil
}

func (NopFileSystem) WriteFile(context.Context, string, []byte, os.FileMode) error {
	return nil
}

func (NopFileSystem) CreateDirectory(context.Context, string, os.FileMode) error {
	return nil
}

func (NopFileSystem) Rename(context.Context, string, string) error {
	return nil
}
