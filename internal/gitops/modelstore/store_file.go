package modelstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/swarm-deploy/swarm-deploy/internal/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

const fileModePrivate = 0o600

// FileStore persists runtime state in a JSON file.
type FileStore struct {
	mu    sync.RWMutex
	path  string
	fs    fs.FileSystem
	state model.Runtime
}

// NewFileStore creates a file-backed runtime state store and loads current state from disk.
func NewFileStore(ctx context.Context, path string, filesystem fs.FileSystem) (*FileStore, error) {
	s := &FileStore{
		path: path,
		fs:   filesystem,
		state: model.Runtime{
			Stacks:   map[string]model.Stack{},
			Networks: map[string]model.Network{},
		},
	}

	if err := s.load(ctx); err != nil {
		return nil, err
	}

	return s, nil
}

// Get returns a snapshot copy of current runtime state.
func (s *FileStore) Get() model.Runtime {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.state.Clone()
}

// Update applies state mutation and persists updated runtime state to disk.
func (s *FileStore) Update(fn func(*model.Runtime)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	slog.Info("[file-state-store] updating", slog.Any("state", s.state))

	fn(&s.state)

	if err := s.flush(); err != nil {
		slog.Error(
			"[file-state-store] failed to persist runtime state",
			slog.String("path", s.path),
			slog.Any("err", err),
		)
		return
	}
}

func (s *FileStore) Stop() {}

func (s *FileStore) load(ctx context.Context) error {
	if err := s.fs.CreateDirectory(ctx, filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create runtime state dir: %w", err)
	}

	payload, err := s.fs.ReadFile(ctx, s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("read runtime state file: %w", err)
	}
	if len(payload) == 0 {
		return nil
	}

	var decoded model.Runtime
	if unmarshalErr := json.Unmarshal(payload, &decoded); unmarshalErr != nil {
		return fmt.Errorf("decode runtime state file: %w", unmarshalErr)
	}
	if decoded.Stacks == nil {
		decoded.Stacks = map[string]model.Stack{}
	}
	if decoded.Networks == nil {
		decoded.Networks = map[string]model.Network{}
	}

	s.state = decoded
	return nil
}

func (s *FileStore) flush() error {
	slog.Info("[file-state-store] flushing", slog.Any("state", s.state), slog.String("path", s.path))

	payload, err := json.Marshal(s.state)
	if err != nil {
		return fmt.Errorf("encode runtime state file: %w", err)
	}

	if writeErr := s.fs.WriteFile(context.Background(), s.path, payload, fileModePrivate); writeErr != nil {
		return fmt.Errorf("write runtime state temp file: %w", writeErr)
	}

	return nil
}
