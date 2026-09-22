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

	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	sharedfs "github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

const (
	// MaxStoredAlerts is the maximum persisted alert count when resolved alerts can be pruned.
	MaxStoredAlerts = 100
	alertFileMode   = 0o600
	alertDirMode    = 0o755
)

type persistedAlerts struct {
	Alerts []model.Alert `json:"alerts"`
}

// FileStore persists alert state in an atomically replaced JSON file.
type FileStore struct {
	mu                sync.RWMutex
	path              string
	fs                sharedfs.FileSystem
	alerts            []model.Alert
	byID              map[string]int
	openByFingerprint map[string]string
}

// NewFileStore loads a file-backed alert repository and reconciles retention.
func NewFileStore(ctx context.Context, path string, filesystem sharedfs.FileSystem) (*FileStore, error) {
	store := &FileStore{path: path, fs: filesystem}
	if err := store.load(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

// Create persists a new incident and rejects a second open alert for its fingerprint.
func (s *FileStore) Create(ctx context.Context, alert model.Alert) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byID[alert.ID]; exists {
		return fmt.Errorf("create alert %q: duplicate id", alert.ID)
	}
	if alert.Status == model.AlertStatusOpen {
		if _, exists := s.openByFingerprint[alert.Fingerprint]; exists {
			return ErrOpenAlertExists
		}
	}
	index := len(s.alerts)
	s.alerts = append(s.alerts, cloneAlert(alert))
	s.byID[alert.ID] = index
	if alert.Status == model.AlertStatusOpen {
		s.openByFingerprint[alert.Fingerprint] = alert.ID
	}
	s.pruneLocked()
	return s.flushLocked(ctx)
}

// Update replaces an existing incident and preserves open fingerprint uniqueness.
func (s *FileStore) Update(ctx context.Context, alert model.Alert) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, exists := s.byID[alert.ID]
	if !exists {
		return ErrAlertNotFound
	}
	if alert.Status == model.AlertStatusOpen {
		if id, found := s.openByFingerprint[alert.Fingerprint]; found && id != alert.ID {
			return ErrOpenAlertExists
		}
	}
	previous := s.alerts[index]
	s.alerts[index] = cloneAlert(alert)
	if previous.Status == model.AlertStatusOpen {
		delete(s.openByFingerprint, previous.Fingerprint)
	}
	if alert.Status == model.AlertStatusOpen {
		s.openByFingerprint[alert.Fingerprint] = alert.ID
	}
	s.pruneLocked()
	return s.flushLocked(ctx)
}

// Get returns an incident by ID.
func (s *FileStore) Get(_ context.Context, id string) (model.Alert, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	index, exists := s.byID[id]
	if !exists {
		return model.Alert{}, ErrAlertNotFound
	}
	return cloneAlert(s.alerts[index]), nil
}

// FindOpenByFingerprint returns the current open incident for a correlation key.
func (s *FileStore) FindOpenByFingerprint(_ context.Context, fingerprint string) (model.Alert, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, exists := s.openByFingerprint[fingerprint]
	if !exists {
		return model.Alert{}, ErrAlertNotFound
	}
	return cloneAlert(s.alerts[s.byID[id]]), nil
}

// List returns newest-updated alerts first.
func (s *FileStore) List(_ context.Context, filter ListFilter) ([]model.Alert, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]model.Alert, 0, len(s.alerts))
	for _, alert := range s.alerts {
		if filter.Status != "" && alert.Status != filter.Status {
			continue
		}
		result = append(result, cloneAlert(alert))
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].UpdatedAt.After(result[j].UpdatedAt) })
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}
	return result, nil
}

func (s *FileStore) load(ctx context.Context) error {
	if err := s.fs.CreateDirectory(ctx, filepath.Dir(s.path), alertDirMode); err != nil {
		return fmt.Errorf("create alert store directory: %w", err)
	}
	s.alerts = []model.Alert{}
	payload, err := s.fs.ReadFile(ctx, s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read alert store: %w", err)
	}
	if err == nil && len(payload) > 0 {
		var persisted persistedAlerts
		if decodeErr := json.Unmarshal(payload, &persisted); decodeErr != nil {
			return fmt.Errorf("decode alert store: %w", decodeErr)
		}
		s.alerts = persisted.Alerts
	}
	s.rebuildIndexesLocked()
	if len(s.openByFingerprint) != countOpenAlerts(s.alerts) {
		return errors.New("alert store contains duplicate open fingerprints")
	}
	if s.pruneLocked() {
		return s.flushLocked(ctx)
	}
	return nil
}

func (s *FileStore) rebuildIndexesLocked() {
	s.byID = make(map[string]int, len(s.alerts))
	s.openByFingerprint = make(map[string]string)
	for index, alert := range s.alerts {
		s.byID[alert.ID] = index
		if alert.Status == model.AlertStatusOpen {
			s.openByFingerprint[alert.Fingerprint] = alert.ID
		}
	}
}

func (s *FileStore) pruneLocked() bool {
	excess := len(s.alerts) - MaxStoredAlerts
	if excess <= 0 {
		return false
	}
	resolved := make([]int, 0, len(s.alerts))
	for index := range s.alerts {
		if s.alerts[index].Status == model.AlertStatusResolved {
			resolved = append(resolved, index)
		}
	}
	sort.SliceStable(resolved, func(i, j int) bool {
		left, right := s.alerts[resolved[i]].ResolvedAt, s.alerts[resolved[j]].ResolvedAt
		if left == nil {
			return true
		}
		if right == nil {
			return false
		}
		return left.Before(*right)
	})
	if excess > len(resolved) {
		excess = len(resolved)
	}
	remove := make(map[int]struct{}, excess)
	for _, index := range resolved[:excess] {
		remove[index] = struct{}{}
	}
	kept := make([]model.Alert, 0, len(s.alerts)-excess)
	for index, alert := range s.alerts {
		if _, shouldRemove := remove[index]; !shouldRemove {
			kept = append(kept, alert)
		}
	}
	s.alerts = kept
	s.rebuildIndexesLocked()
	return excess > 0
}

func (s *FileStore) flushLocked(ctx context.Context) error {
	payload, err := json.Marshal(persistedAlerts{Alerts: s.alerts})
	if err != nil {
		return fmt.Errorf("encode alert store: %w", err)
	}
	tmpPath := s.path + ".tmp"
	if err = s.fs.WriteFile(ctx, tmpPath, payload, alertFileMode); err != nil {
		return fmt.Errorf("write alert store temporary file: %w", err)
	}
	if err = s.fs.Rename(ctx, tmpPath, s.path); err != nil {
		return fmt.Errorf("replace alert store file: %w", err)
	}
	return nil
}

func countOpenAlerts(alerts []model.Alert) int {
	count := 0
	for _, alert := range alerts {
		if alert.Status == model.AlertStatusOpen {
			count++
		}
	}
	return count
}

func cloneAlert(alert model.Alert) model.Alert {
	if alert.ResolvedAt != nil {
		value := *alert.ResolvedAt
		alert.ResolvedAt = &value
	}
	if alert.Resolution != nil {
		value := *alert.Resolution
		alert.Resolution = &value
	}
	return alert
}
