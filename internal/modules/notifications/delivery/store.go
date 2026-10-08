//go:generate mockgen -source=$GOFILE -destination=mocks_store.go -package=delivery

package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	sharedfs "github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

const (
	storeFileMode = 0o600
)

// Correlation links a sent message to a caller-defined key.
type Correlation struct {
	// CorrelationKey is an opaque caller-defined key.
	CorrelationKey string `json:"correlationKey"`
	// ChannelID identifies the transport channel.
	ChannelID string `json:"channelId"`
	// Receipt contains data required by the transport to edit the message.
	Receipt Receipt `json:"receipt"`
	// CreatedAt is when the message was sent.
	CreatedAt time.Time `json:"createdAt"`
	// ExpiresAt is when the correlation is no longer valid.
	ExpiresAt time.Time `json:"expiresAt"`
}

// Store persists correlations keyed by (CorrelationKey, ChannelID).
type Store interface {
	// Get returns a live correlation; expired correlations are reported as not found.
	Get(ctx context.Context, key, channelID string) (Correlation, bool, error)
	// Put saves a correlation and drops expired ones.
	Put(ctx context.Context, correlation Correlation) error
	// Delete removes a correlation; a missing one is not an error.
	Delete(ctx context.Context, key, channelID string) error
}

type storeKey struct{ key, channelID string }

type persistedCorrelations struct {
	Correlations []Correlation `json:"correlations"`
}

// FileStore keeps correlations in an atomically replaced JSON file so they survive restarts.
type FileStore struct {
	mu    sync.Mutex
	path  string
	fs    sharedfs.FileSystem
	now   func() time.Time
	items map[storeKey]Correlation
}

// NewFileStore loads correlations from path, dropping expired ones.
func NewFileStore(ctx context.Context, path string, filesystem sharedfs.FileSystem) (*FileStore, error) {
	store := &FileStore{path: path, fs: filesystem, now: time.Now, items: make(map[storeKey]Correlation)}

	payload, err := filesystem.ReadFile(ctx, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return store, nil
		}
		return nil, fmt.Errorf("read delivery store: %w", err)
	}
	if len(payload) == 0 {
		return store, nil
	}

	var persisted persistedCorrelations
	if err = json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode delivery store: %w", err)
	}
	for _, c := range persisted.Correlations {
		store.items[storeKey{c.CorrelationKey, c.ChannelID}] = c
	}
	if store.pruneLocked() {
		if err = store.flushLocked(ctx); err != nil {
			return nil, err
		}
	}

	return store, nil
}

// Get returns a live correlation.
func (s *FileStore) Get(_ context.Context, key, channelID string) (Correlation, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.items[storeKey{key, channelID}]
	if !ok || !c.ExpiresAt.After(s.now()) {
		return Correlation{}, false, nil
	}
	return c, true, nil
}

// Put saves a correlation.
func (s *FileStore) Put(ctx context.Context, correlation Correlation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.items[storeKey{correlation.CorrelationKey, correlation.ChannelID}] = correlation
	s.pruneLocked()
	return s.flushLocked(ctx)
}

// Delete removes a correlation.
func (s *FileStore) Delete(ctx context.Context, key, channelID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.items[storeKey{key, channelID}]; !ok {
		return nil
	}
	delete(s.items, storeKey{key, channelID})
	s.pruneLocked()
	return s.flushLocked(ctx)
}

func (s *FileStore) pruneLocked() bool {
	now := s.now()
	pruned := false
	for k, c := range s.items {
		if !c.ExpiresAt.After(now) {
			delete(s.items, k)
			pruned = true
		}
	}
	return pruned
}

func (s *FileStore) flushLocked(ctx context.Context) error {
	persisted := persistedCorrelations{Correlations: make([]Correlation, 0, len(s.items))}
	for _, c := range s.items {
		persisted.Correlations = append(persisted.Correlations, c)
	}
	payload, err := json.Marshal(persisted)
	if err != nil {
		return fmt.Errorf("encode delivery store: %w", err)
	}
	tmpPath := s.path + ".tmp"
	if err = s.fs.WriteFile(ctx, tmpPath, payload, storeFileMode); err != nil {
		return fmt.Errorf("write delivery store temporary file: %w", err)
	}
	if err = s.fs.Rename(ctx, tmpPath, s.path); err != nil {
		return fmt.Errorf("replace delivery store file: %w", err)
	}
	return nil
}
