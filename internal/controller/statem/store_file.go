package statem

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

const fileModePrivate = 0o600

// FileStore persists runtime state in a JSON file.
type FileStore struct {
	mu    sync.RWMutex
	path  string
	state Runtime
}

// NewFileStore creates a file-backed runtime state store and loads current state from disk.
func NewFileStore(path string) (*FileStore, error) {
	s := &FileStore{
		path: path,
		state: Runtime{
			Stacks:   map[string]Stack{},
			Networks: map[string]Network{},
		},
	}

	if err := s.load(); err != nil {
		return nil, err
	}

	return s, nil
}

// Get returns a snapshot copy of current runtime state.
func (s *FileStore) Get() Runtime {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return cloneRuntime(s.state)
}

// Update applies state mutation and persists updated runtime state to disk.
func (s *FileStore) Update(fn func(*Runtime)) {
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

func (s *FileStore) load() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create runtime state dir: %w", err)
	}

	payload, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("read runtime state file: %w", err)
	}
	if len(payload) == 0 {
		return nil
	}

	var decoded Runtime
	if unmarshalErr := json.Unmarshal(payload, &decoded); unmarshalErr != nil {
		return fmt.Errorf("decode runtime state file: %w", unmarshalErr)
	}
	if decoded.Stacks == nil {
		decoded.Stacks = map[string]Stack{}
	}
	if decoded.Networks == nil {
		decoded.Networks = map[string]Network{}
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

	if writeErr := os.WriteFile(s.path, payload, fileModePrivate); writeErr != nil {
		return fmt.Errorf("write runtime state temp file: %w", writeErr)
	}

	return nil
}
