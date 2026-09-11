package fs

import "context"

type NopFileSystem struct {
}

func (n NopFileSystem) ReadFile(context.Context, string) ([]byte, error) {
	return []byte{}, nil
}



