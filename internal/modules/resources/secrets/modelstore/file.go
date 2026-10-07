package modelstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	sharedfs "github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

const (
	secretFileMode = 0o600
	secretDirMode  = 0o755
)

type persistedSecrets struct {
	Secrets []model.Secret `json:"secrets"`
}

// FileStore persists secret metadata in an atomically replaced JSON file.
type FileStore struct {
	mu     sync.RWMutex
	path   string
	fs     sharedfs.FileSystem
	rows   []model.Secret
	byName map[string]int
}

// NewFileStore loads a file-backed secret metadata store.
func NewFileStore(ctx context.Context, path string, filesystem sharedfs.FileSystem) (*FileStore, error) {
	store := &FileStore{path: path, fs: filesystem}
	if err := store.load(ctx); err != nil {
		return nil, err
	}

	return store, nil
}

// List returns a defensive copy of the current snapshot.
func (s *FileStore) List(_ context.Context) ([]model.Secret, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return cloneSecrets(s.rows), nil
}

// GetByName returns a defensive copy of a secret found by name.
func (s *FileStore) GetByName(_ context.Context, name string) (model.Secret, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	index, exists := s.byName[name]
	if !exists {
		return model.Secret{}, ErrSecretNotFound
	}

	return cloneSecret(s.rows[index]), nil
}

// Replace atomically replaces the in-memory and persisted snapshots.
func (s *FileStore) Replace(ctx context.Context, secrets []model.Secret) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows := cloneSecrets(secrets)
	sortSecrets(rows)
	payload, err := json.Marshal(persistedSecrets{Secrets: rows})
	if err != nil {
		return fmt.Errorf("encode secret store: %w", err)
	}

	tmpPath := s.path + ".tmp"
	if err = s.fs.WriteFile(ctx, tmpPath, payload, secretFileMode); err != nil {
		return fmt.Errorf("write secret store temporary file: %w", err)
	}
	if err = s.fs.Rename(ctx, tmpPath, s.path); err != nil {
		return fmt.Errorf("replace secret store file: %w", err)
	}

	s.setRows(rows)
	return nil
}

func (s *FileStore) load(ctx context.Context) error {
	if err := s.fs.CreateDirectory(ctx, filepath.Dir(s.path), secretDirMode); err != nil {
		return fmt.Errorf("create secret store directory: %w", err)
	}

	s.setRows(nil)
	payload, err := s.fs.ReadFile(ctx, s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("read secret store: %w", err)
	}
	if len(payload) == 0 {
		return nil
	}

	var persisted persistedSecrets
	if err = json.Unmarshal(payload, &persisted); err != nil {
		return fmt.Errorf("decode secret store: %w", err)
	}

	rows := cloneSecrets(persisted.Secrets)
	sortSecrets(rows)
	s.setRows(rows)
	return nil
}

func (s *FileStore) setRows(rows []model.Secret) {
	s.rows = rows
	s.byName = make(map[string]int, len(rows))
	for index, secret := range rows {
		s.byName[secret.Name] = index
	}
}

func sortSecrets(secrets []model.Secret) {
	sort.Slice(secrets, func(i, j int) bool {
		if secrets[i].Name != secrets[j].Name {
			return secrets[i].Name < secrets[j].Name
		}

		return secrets[i].ID < secrets[j].ID
	})
}

func cloneSecrets(secrets []model.Secret) []model.Secret {
	if len(secrets) == 0 {
		return []model.Secret{}
	}

	cloned := make([]model.Secret, len(secrets))
	for index, secret := range secrets {
		cloned[index] = cloneSecret(secret)
	}

	return cloned
}

func cloneSecret(secret model.Secret) model.Secret {
	if len(secret.Labels) == 0 {
		return secret
	}

	labels := secret.Labels
	secret.Labels = make(map[string]string, len(labels))
	for key, value := range labels {
		secret.Labels[key] = value
	}

	return secret
}
