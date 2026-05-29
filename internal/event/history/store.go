package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
)

const fileModePrivate = 0o600

// Entry is a persisted event view returned by API.
type Entry struct {
	// Type is a unique event type.
	Type events.Type `json:"type"`
	// Severity is an event priority level.
	Severity events.Severity `json:"severity"`
	// Category is an event functional group.
	Category events.Category `json:"category"`
	// CreatedAt is event creation timestamp.
	CreatedAt time.Time `json:"created_at"`
	// Message is a short human-readable event description.
	Message string `json:"message"`
	// Details contains optional event-specific details like stack, commit or error.
	Details map[string]string `json:"details,omitempty"`
}

// Store persists a bounded event list in a json file.
type Store struct {
	mu       sync.RWMutex
	path     string
	capacity int
	now      func() time.Time
	entries  []Entry
}

// NewStore creates history store and loads current state from disk.
func NewStore(path string, capacity int) (*Store, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("event history capacity must be > 0, got %d", capacity)
	}

	s := &Store{
		path:     path,
		capacity: capacity,
		now:      time.Now,
	}

	if err := s.load(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *Store) Name() string {
	return "save-event-history"
}

func (s *Store) Slow() bool {
	return false
}

// Handle appends event to history and persists updated file.
func (s *Store) Handle(_ context.Context, event events.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = append(s.entries, toEntry(s.now(), event))
	if len(s.entries) > s.capacity {
		s.entries = s.entries[len(s.entries)-s.capacity:]
	}

	return s.flushLocked()
}

// List returns a copy of current event history.
func (s *Store) List() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Entry, len(s.entries))
	for i, entry := range s.entries {
		out[i] = entry
		out[i].Details = cloneDetails(entry.Details)
	}

	return out
}

func (s *Store) load() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create event history dir: %w", err)
	}

	payload, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read event history file: %w", err)
	}
	if len(payload) == 0 {
		return nil
	}

	unmarshalErr := json.Unmarshal(payload, &s.entries)
	if unmarshalErr != nil {
		return fmt.Errorf("decode event history: %w", unmarshalErr)
	}

	if len(s.entries) > s.capacity {
		s.entries = s.entries[len(s.entries)-s.capacity:]
		flushErr := s.flushLocked()
		if flushErr != nil {
			return flushErr
		}
	}

	return nil
}

func (s *Store) flushLocked() error {
	payload, err := json.Marshal(s.entries)
	if err != nil {
		return fmt.Errorf("encode event history: %w", err)
	}

	tmpPath := fmt.Sprintf("%s.tmp", s.path)
	writeErr := os.WriteFile(tmpPath, payload, fileModePrivate)
	if writeErr != nil {
		return fmt.Errorf("write event history temp file: %w", writeErr)
	}
	renameErr := os.Rename(tmpPath, s.path)
	if renameErr != nil {
		return fmt.Errorf("replace event history file: %w", renameErr)
	}

	return nil
}

func toEntry(now time.Time, event events.Event) Entry {
	eventType := event.Type()

	return Entry{
		Type:      eventType,
		Severity:  eventType.Severity(),
		Category:  eventType.Category(),
		CreatedAt: now,
		Message:   event.Message(),
		Details:   cloneDetails(event.Details()),
	}
}

func cloneDetails(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}
