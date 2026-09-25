package conversation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	chatFileMode      = 0o600
	chatTitleMaxRunes = 60
	historyIndexFile  = "index.json"
)

// Chat is a persisted assistant conversation.
type Chat struct {
	// ID identifies the conversation.
	ID string `json:"id"`
	// Title is derived from the first user message.
	Title string `json:"title"`
	// CreatedAt is the timestamp when the chat was first persisted.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the timestamp of the latest persisted message.
	UpdatedAt time.Time `json:"updated_at"`
	// Turns contains the full user-visible conversation history.
	Turns []Turn `json:"messages"`
}

// ChatSummary contains metadata required to render the chat list.
type ChatSummary struct {
	// ID identifies the conversation.
	ID string `json:"id"`
	// Title is derived from the first user message.
	Title string `json:"title"`
	// CreatedAt is the timestamp when the chat was first persisted.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the timestamp of the latest persisted message.
	UpdatedAt time.Time `json:"updated_at"`
}

type historyIndex struct {
	Chats []ChatSummary `json:"chats"`
}

// HistoryStorage persists complete assistant chats independently from the in-memory context cache.
type HistoryStorage interface {
	// List returns persisted chat metadata ordered by latest update first.
	List() []ChatSummary
	// Get returns a persisted chat by conversation id.
	Get(id string) (Chat, bool, error)
	// Append appends turns to a persisted chat, creating it lazily when needed.
	Append(id string, turns ...Turn) error
}

// FileHistoryStorage stores each assistant chat in a separate JSON file and keeps list metadata in memory.
type FileHistoryStorage struct {
	mu        sync.RWMutex
	dir       string
	summaries []ChatSummary
	now       func() time.Time
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

	store := &FileHistoryStorage{
		dir:       dir,
		summaries: []ChatSummary{},
		now:       time.Now,
	}
	if err := store.loadIndex(); err != nil {
		return nil, err
	}

	return store, nil
}

// List returns persisted chat metadata ordered by latest update first.
func (s *FileHistoryStorage) List() []ChatSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return append([]ChatSummary(nil), s.summaries...)
}

// Get returns a persisted chat by conversation id.
func (s *FileHistoryStorage) Get(id string) (Chat, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

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
		chat = Chat{
			ID:        id,
			Title:     buildChatTitle(turns),
			CreatedAt: now,
			Turns:     []Turn{},
		}
	}
	chat.Turns = append(chat.Turns, turns...)
	chat.UpdatedAt = now

	if err = s.writeChatLocked(chat); err != nil {
		return err
	}

	s.upsertSummaryLocked(ChatSummary{
		ID:        chat.ID,
		Title:     chat.Title,
		CreatedAt: chat.CreatedAt,
		UpdatedAt: chat.UpdatedAt,
	})
	if err = s.writeIndexLocked(); err != nil {
		return err
	}

	return nil
}

func (s *FileHistoryStorage) loadIndex() error {
	payload, err := os.ReadFile(s.indexPath())
	if err == nil {
		var index historyIndex
		if decodeErr := json.Unmarshal(payload, &index); decodeErr != nil {
			return fmt.Errorf("decode assistant chat history index: %w", decodeErr)
		}
		s.summaries = append([]ChatSummary(nil), index.Chats...)
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	return fmt.Errorf("read assistant chat history index: %w", err)
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

func (s *FileHistoryStorage) upsertSummaryLocked(summary ChatSummary) {
	updated := make([]ChatSummary, 0, len(s.summaries)+1)
	updated = append(updated, summary)
	for _, existing := range s.summaries {
		if existing.ID == summary.ID {
			continue
		}
		updated = append(updated, existing)
	}
	s.summaries = updated
}

func (s *FileHistoryStorage) writeChatLocked(chat Chat) error {
	payload, err := json.Marshal(chat)
	if err != nil {
		return fmt.Errorf("encode assistant chat history: %w", err)
	}
	if err = writeJSONFileAtomic(s.chatPath(chat.ID), payload); err != nil {
		return fmt.Errorf("write assistant chat history: %w", err)
	}

	return nil
}

func (s *FileHistoryStorage) writeIndexLocked() error {
	payload, err := json.Marshal(historyIndex{Chats: s.summaries})
	if err != nil {
		return fmt.Errorf("encode assistant chat history index: %w", err)
	}
	if err = writeJSONFileAtomic(s.indexPath(), payload); err != nil {
		return fmt.Errorf("write assistant chat history index: %w", err)
	}

	return nil
}

func writeJSONFileAtomic(path string, payload []byte) error {
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, payload, chatFileMode); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}

	return nil
}

func (s *FileHistoryStorage) chatPath(id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".json")
}

func (s *FileHistoryStorage) indexPath() string {
	return filepath.Join(s.dir, historyIndexFile)
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
