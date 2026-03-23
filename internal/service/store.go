package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const fileModePrivate = 0o600

// Store persists service metadata in a json file.
type Store struct {
	mu   sync.RWMutex
	path string
	rows []Info
}

// NewStore creates service store and loads saved rows from disk.
func NewStore(path string) (*Store, error) {
	s := &Store{
		path: path,
	}

	if err := s.load(); err != nil {
		return nil, err
	}

	return s, nil
}

// List returns a copy of all saved services.
func (s *Store) List() []Info {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Info, len(s.rows))
	copy(out, s.rows)
	return out
}

// ReplaceStack replaces stack services with a new snapshot and saves it to disk.
func (s *Store) ReplaceStack(stackName string, services []Info) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	updated := make([]Info, 0, len(s.rows)+len(services))
	for _, current := range s.rows {
		if current.Stack == stackName {
			continue
		}
		updated = append(updated, current)
	}
	for _, service := range services {
		normalized := normalizeInfo(service)
		if normalized.Name == "" {
			continue
		}
		normalized.Stack = stackName
		updated = append(updated, normalized)
	}

	sortInfos(updated)
	s.rows = updated

	return s.flushLocked()
}

func (s *Store) load() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create services dir: %w", err)
	}

	payload, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("read services file: %w", err)
	}
	if len(payload) == 0 {
		return nil
	}

	var rows []Info
	if unmarshalErr := json.Unmarshal(payload, &rows); unmarshalErr != nil {
		return fmt.Errorf("decode services file: %w", unmarshalErr)
	}

	s.rows = make([]Info, 0, len(rows))
	for _, row := range rows {
		normalized := normalizeInfo(row)
		if normalized.Name == "" || normalized.Stack == "" {
			continue
		}
		s.rows = append(s.rows, normalized)
	}

	sortInfos(s.rows)
	return nil
}

func (s *Store) flushLocked() error {
	payload, err := json.Marshal(s.rows)
	if err != nil {
		return fmt.Errorf("encode services file: %w", err)
	}

	tmpPath := fmt.Sprintf("%s.tmp", s.path)
	if writeErr := os.WriteFile(tmpPath, payload, fileModePrivate); writeErr != nil {
		return fmt.Errorf("write services temp file: %w", writeErr)
	}
	if renameErr := os.Rename(tmpPath, s.path); renameErr != nil {
		return fmt.Errorf("replace services file: %w", renameErr)
	}

	return nil
}

func normalizeInfo(info Info) Info {
	info.Name = strings.TrimSpace(info.Name)
	info.Stack = strings.TrimSpace(info.Stack)
	info.Description = strings.TrimSpace(info.Description)
	info.Image = strings.TrimSpace(info.Image)

	return info
}

func sortInfos(rows []Info) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Stack != rows[j].Stack {
			return rows[i].Stack < rows[j].Stack
		}

		return rows[i].Name < rows[j].Name
	})
}
