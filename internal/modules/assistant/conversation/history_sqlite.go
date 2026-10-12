package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// SQLHistoryStorage stores chat metadata and ordered turns in a shared database.
type SQLHistoryStorage struct{ db *storage.Database }

// NewSQLHistoryStorage creates a conversation repository.
func NewSQLHistoryStorage(db *storage.Database) *SQLHistoryStorage { return &SQLHistoryStorage{db: db} }

// List returns metadata without loading conversation turns.
func (s *SQLHistoryStorage) ReadChats(ctx context.Context) ([]ChatSummary, error) {
	return s.queryChatSummaries(ctx,
		"SELECT payload FROM assistant_chats ORDER BY updated_at_ns DESC,id")
}

// Get reads metadata and turns from a consistent transaction snapshot.
func (s *SQLHistoryStorage) ReadChat(ctx context.Context, id string) (Chat, bool, error) {
	var chat Chat
	found := false
	err := s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		var payload []byte
		err := s.db.QueryRow(ctx, "SELECT payload FROM assistant_chats WHERE id=?", id).Scan(&payload)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var meta ChatSummary
		if err = json.Unmarshal(payload, &meta); err != nil {
			return err
		}
		turns, err := s.queryTurns(ctx,
			"SELECT payload FROM assistant_turns WHERE chat_id=? ORDER BY sequence", id)
		if err != nil {
			return err
		}
		chat = Chat{ID: meta.ID, Title: meta.Title, CreatedAt: meta.CreatedAt,
			UpdatedAt: meta.UpdatedAt, Usage: meta.Usage, Turns: turns}
		found = true
		return nil
	})
	return chat, found, err
}

// AppendWithUsage appends turns and usage atomically, creating metadata lazily.
func (s *SQLHistoryStorage) SaveTurns(ctx context.Context, id string, usage TokenUsage, turns ...Turn) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("assistant conversation id is required")
	}
	if len(turns) == 0 && usage.IsZero() {
		return nil
	}
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		chat, exists, err := s.ReadChat(ctx, id)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if !exists {
			chat = Chat{ID: id, Title: buildChatTitle(turns), CreatedAt: now}
		}
		chat.UpdatedAt = now
		chat.Turns = append(chat.Turns, turns...)
		if !usage.IsZero() {
			if chat.Usage == nil {
				chat.Usage = &TokenUsage{}
			}
			chat.Usage.Add(usage)
		}
		return s.Save(ctx, chat)
	})
}

// Append appends turns without a usage sample.
func (s *SQLHistoryStorage) Append(ctx context.Context, id string, turns ...Turn) error {
	return s.SaveTurns(ctx, id, TokenUsage{}, turns...)
}

// Save atomically persists an entire chat, preserving timestamps during import.
func (s *SQLHistoryStorage) Save(ctx context.Context, chat Chat) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		meta := ChatSummary{ID: chat.ID, Title: chat.Title, CreatedAt: chat.CreatedAt,
			UpdatedAt: chat.UpdatedAt, Usage: chat.Usage}
		payload, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		if _, err = s.db.Exec(ctx, `INSERT INTO assistant_chats VALUES (?,?,?)
			ON CONFLICT(id) DO UPDATE SET updated_at_ns=excluded.updated_at_ns,payload=excluded.payload`,
			chat.ID, chat.UpdatedAt.UnixNano(), string(payload)); err != nil {
			return err
		}
		if _, err = s.db.Exec(ctx, "DELETE FROM assistant_turns WHERE chat_id=?", chat.ID); err != nil {
			return err
		}
		for i, turn := range chat.Turns {
			payload, err = json.Marshal(turn)
			if err != nil {
				return err
			}
			if _, err = s.db.Exec(ctx,
				"INSERT INTO assistant_turns VALUES (?,?,?)", chat.ID, i, string(payload)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *SQLHistoryStorage) queryChatSummaries(
	ctx context.Context, query string, args ...any,
) ([]ChatSummary, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ChatSummary{}
	for rows.Next() {
		var payload []byte
		if err = rows.Scan(&payload); err != nil {
			return nil, err
		}
		var value ChatSummary
		if err = json.Unmarshal(payload, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *SQLHistoryStorage) queryTurns(ctx context.Context, query string, args ...any) ([]Turn, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Turn{}
	for rows.Next() {
		var payload []byte
		if err = rows.Scan(&payload); err != nil {
			return nil, err
		}
		var value Turn
		if err = json.Unmarshal(payload, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}
