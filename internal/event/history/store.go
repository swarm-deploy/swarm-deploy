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

	"github.com/artarts36/swarm-deploy/internal/event/dispatcher"
	"github.com/artarts36/swarm-deploy/internal/event/events"
)

const fileModePrivate = 0o600

// Entry is a persisted event view returned by API.
type Entry struct {
	// Type is a unique event type.
	Type events.Type `json:"type"`
	// CreatedAt is event creation timestamp.
	CreatedAt time.Time `json:"created_at"`
	// Message is a short human-readable event description.
	Message string `json:"message"`
	// Stack is a related stack name if event belongs to a stack.
	Stack string `json:"stack,omitempty"`
	// Commit is a related git commit if available.
	Commit string `json:"commit,omitempty"`
	// Error contains a failure reason for failed events.
	Error string `json:"error,omitempty"`
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

// Handle appends event to history and persists updated file.
func (s *Store) Handle(_ context.Context, event dispatcher.Event) error {
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
	copy(out, s.entries)
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

func toEntry(now time.Time, event dispatcher.Event) Entry {
	e := Entry{
		Type:      event.Type(),
		CreatedAt: now,
		Message:   "Event captured",
	}

	switch typed := event.(type) {
	case *events.DeploySuccess:
		e.Stack = typed.StackName
		e.Commit = typed.Commit
		e.Message = fmt.Sprintf("Deploy succeeded for stack %s", typed.StackName)
	case *events.DeployFailed:
		e.Stack = typed.StackName
		e.Commit = typed.Commit
		e.Message = fmt.Sprintf("Deploy failed for stack %s", typed.StackName)
		if typed.Error != nil {
			e.Error = typed.Error.Error()
		}
	case *events.SyncManualStarted:
		e.Message = "Manual sync started"
	}

	return e
}
