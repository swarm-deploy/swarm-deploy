package modelstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/model"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

const fileModePrivate = 0o600

// FileStore persists recommendations in a JSON file.
type FileStore struct {
	mu   sync.RWMutex
	path string
	fs   fs.FileSystem
	data file
}

type file struct {
	List   []model.Recommendation `json:"list"`
	Stacks map[string][]int       `json:"stacks"` // map[stack_name][]index - index from List
}

// NewFileStore creates a file-backed recommendation store and loads current state from disk.
func NewFileStore(ctx context.Context, path string, filesystem fs.FileSystem) (*FileStore, error) {
	store := &FileStore{
		path: path,
		fs:   filesystem,
		data: file{
			List:   []model.Recommendation{},
			Stacks: map[string][]int{},
		},
	}

	err := store.load(ctx)
	if err != nil {
		return nil, err
	}

	return store, nil
}

// List returns recommendations matching filter.
func (f *FileStore) List(_ context.Context, filter ListFilter) ([]model.Recommendation, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if filter.Stack == "" {
		return limitRecommendations(f.data.List, filter.Limit), nil
	}

	indexes := f.data.Stacks[filter.Stack]
	recommendations := make([]model.Recommendation, 0, len(indexes))
	for _, index := range indexes {
		if index < 0 || index >= len(f.data.List) {
			continue
		}

		recommendations = append(recommendations, f.data.List[index])
		if filter.Limit > 0 && len(recommendations) >= filter.Limit {
			break
		}
	}

	return recommendations, nil
}

func limitRecommendations(recommendations []model.Recommendation, limit int) []model.Recommendation {
	if limit <= 0 || len(recommendations) <= limit {
		return recommendations
	}

	return recommendations[:limit]
}

// UpdateStack replaces recommendations for stack and persists updated state.
func (f *FileStore) UpdateStack(ctx context.Context, stack string, recommendations []model.Recommendation) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	data := file{
		List:   make([]model.Recommendation, 0, len(f.data.List)+len(recommendations)),
		Stacks: map[string][]int{},
	}

	for existingStack, indexes := range f.data.Stacks {
		if existingStack == stack {
			continue
		}

		for _, index := range indexes {
			if index < 0 || index >= len(f.data.List) {
				continue
			}

			data.Stacks[existingStack] = append(data.Stacks[existingStack], len(data.List))
			data.List = append(data.List, f.data.List[index])
		}
	}

	for _, recommendation := range recommendations {
		recommendation.Subject.Stack = stack
		data.Stacks[stack] = append(data.Stacks[stack], len(data.List))
		data.List = append(data.List, recommendation)
	}

	f.data = data

	err := f.flushLocked(ctx)
	if err != nil {
		return fmt.Errorf("flush recommendations store: %w", err)
	}

	return nil
}

func (f *FileStore) load(ctx context.Context) error {
	err := f.fs.CreateDirectory(ctx, filepath.Dir(f.path), 0o755) //nolint:mnd // default directory permissions
	if err != nil {
		return fmt.Errorf("create recommendations store dir: %w", err)
	}

	payload, err := f.fs.ReadFile(ctx, f.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("read recommendations store file: %w", err)
	}
	if len(payload) == 0 {
		return nil
	}

	var decoded file
	if unmarshalErr := json.Unmarshal(payload, &decoded); unmarshalErr != nil {
		return fmt.Errorf("decode recommendations store file: %w", unmarshalErr)
	}
	if decoded.List == nil {
		decoded.List = []model.Recommendation{}
	}
	if decoded.Stacks == nil {
		decoded.Stacks = map[string][]int{}
	}

	f.data = decoded
	return nil
}

func (f *FileStore) flushLocked(ctx context.Context) error {
	payload, err := json.Marshal(f.data)
	if err != nil {
		return fmt.Errorf("encode recommendations store file: %w", err)
	}

	tmpPath := fmt.Sprintf("%s.tmp", f.path)
	writeErr := f.fs.WriteFile(ctx, tmpPath, payload, fileModePrivate)
	if writeErr != nil {
		return fmt.Errorf("write recommendations store temp file: %w", writeErr)
	}
	renameErr := f.fs.Rename(ctx, tmpPath, f.path)
	if renameErr != nil {
		return fmt.Errorf("replace recommendations store file: %w", renameErr)
	}

	return nil
}
