package fs

import (
	"context"
)

type ReadFunc func(ctx context.Context, path string) ([]byte, error)

func ReadWithCache(reader ReadFunc) ReadFunc {
	cache := map[string][]byte{}

	return func(ctx context.Context, path string) ([]byte, error) {
		if content, ok := cache[path]; ok {
			return content, nil
		}

		content, err := reader(ctx, path)
		if err != nil {
			return nil, err
		}

		cache[path] = content

		return content, nil
	}
}
