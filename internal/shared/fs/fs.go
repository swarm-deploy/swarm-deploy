package fs

import "context"

type FileSystem interface {
	ReadFile(ctx context.Context, path string) ([]byte, error)
}
