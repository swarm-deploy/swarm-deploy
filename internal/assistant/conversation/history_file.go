package conversation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	chatFileMode      = 0o600
	chatTitleMaxRunes = 60
)

// Chat is a persisted assistant conversation.
type Chat struct {
	// ID identifies the conversation.
	ID string `json:"id"`
	// Title is derived from the first user message.
	Title string `json:"title"`
	// Turns contains the full user-visible conversation history.
	Turns []Turn `json:"messages"`
	// CreatedAt is the timestamp when the chat was first persisted.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the timestamp of the latest persisted message.
	UpdatedAt time.Time `json:"updated_at"`
}

// HistoryStorage persists complete assistant chats independently from the in-memory context cache.
type HistoryStorage interface {
	// List returns persisted chats ordered by latest update first.
	List() ([]Chat, error)
	// Get returns a persisted chat by conversation id.
	Get(id string) (Chat, bool, error)
	// Append appends turns to a persisted chat, creating it lazily when needed.
	Append(id string, turns ...Turn) error
}

// FileHistoryStorage stores each assistant chat in a separate JSON file.
type FileHistoryStorage struct {
	mu  sync.Mutex
	dir string
	now func() time.Time
}

// NewFileHistoryStorage creates a file-backed assistant chat history store.
func NewFileHistoryStorage(dir string) (*FileHistoryStorage, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("assistant chat history directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create assistant chat history directory: %w", err)
	}
	return &FileHistoryStorage{dir: dir, now: time.Now}, nil
}

// List returns persisted chats ordered by latest update first.
func (s *FileHistoryStorage) List() ([]Chat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read assistant chat history directory: %w", err)
	}

	chats := make([]Chat, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		payload, readErr := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if readErr != nil {
			return nil, fmt.Errorf("read assistant chat history file %s: %w", entry.Name(), readErr)
		}
		var chat Chat
		if decodeErr := json.Unmarshal(payload, &chat); decodeErr != nil {
			return nil, fmt.Errorf("decode assistant chat history file %s: %w", entry.Name(), decodeErr)
		}
		chats = append(chats, chat)
	}

	sort.SliceStable(chats, func(i, j int) bool {
		return chats[i].UpdatedAt.After(chats[j].UpdatedAt)
	})
	return chats, nil
}

// Get returns a persisted chat by conversation id.
func (s *FileHistoryStorage) Get(id string) (Chat, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(id)
}

// Append appends turns to a persisted chat, creating it lazily when needed.
func (s *FileHistoryStorage) Append(id string, turns ...Turn) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("assistant conversation id is required")
	}
	if len(turns) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	chat, exists, err := s.getLocked(id)
	if err != nil {
		return err
	}

	now := s.now().UTC()
	if !exists {
		chat = Chat{ID: id, Title: buildChatTitle(turns), Turns: []Turn{}, CreatedAt: now}
	}
	chat.Turns = append(chat.Turns, turns...)
	chat.UpdatedAt = now
	return s.writeLocked(chat)
}

func (s *FileHistoryStorage) getLocked(id string) (Chat, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Chat{}, false, nil
	}

	payload, err := os.ReadFile(s.chatPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Chat{}, false, nil
		}
		return Chat{}, false, fmt.Errorf("read assistant chat history: %w", err)
	}

	var chat Chat
	if err = json.Unmarshal(payload, &chat); err != nil {
		return Chat{}, false, fmt.Errorf("decode assistant chat history: %w", err)
	}
	return chat, true, nil
}

func (s *FileHistoryStorage) writeLocked(chat Chat) error {
	payload, err := json.Marshal(chat)
	if err != nil {
		return fmt.Errorf("encode assistant chat history: %w", err)
	}
	path := s.chatPath(chat.ID)
	tmpPath := path + ".tmp"
	if err = os.WriteFile(tmpPath, payload, chatFileMode); err != nil {
		return fmt.Errorf("write assistant chat history temp file: %w", err)
	}
	if err = os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace assistant chat history file: %w", err)
	}
	return nil
}

func (s *FileHistoryStorage) chatPath(id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".json")
}

func buildChatTitle(turns []Turn) string {
	for _, turn := range turns {
		if turn.Role != "user" {
			continue
		}
		title := strings.Join(strings.Fields(turn.Content), " ")
		if title == "" {
			continue
		}
		if utf8.RuneCountInString(title) <= chatTitleMaxRunes {
			return title
		}
		runes := []rune(title)
		return strings.TrimSpace(string(runes[:chatTitleMaxRunes])) + "…"
	}
	return "New chat"
}
