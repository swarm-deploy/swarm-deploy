package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

var dialect = goqu.Dialect("sqlite3")

// SQLHistoryStorage stores chat metadata and ordered turns in a shared database.
type SQLHistoryStorage struct{ db *storage.Database }

// NewSQLHistoryStorage creates a conversation repository.
func NewSQLHistoryStorage(db *storage.Database) *SQLHistoryStorage { return &SQLHistoryStorage{db: db} }

// List returns metadata without loading conversation turns.
func (s *SQLHistoryStorage) ReadChats(ctx context.Context) ([]ChatSummary, error) {
	query, _, err := dialect.From("assistant_chats").Prepared(true).
		Select("payload").Order(goqu.I("updated_at_ns").Desc(), goqu.I("id").Asc()).ToSQL()
	if err != nil {
		return nil, err
	}
	return s.queryChatSummaries(ctx, query)
}

// Get reads metadata and turns from a consistent transaction snapshot.
func (s *SQLHistoryStorage) ReadChat(ctx context.Context, id string) (Chat, bool, error) {
	var chat Chat
	found := false
	err := s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		query, args, buildErr := dialect.From("assistant_chats").Prepared(true).
			Select("payload").Where(goqu.Ex{"id": id}).ToSQL()
		if buildErr != nil {
			return buildErr
		}
		var payload []byte
		err := s.db.Get(ctx, &payload, query, args...)
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
		turnsQuery, turnsArgs, buildErr := dialect.From("assistant_turns").Prepared(true).
			Select("payload").Where(goqu.Ex{"chat_id": id}).Order(goqu.I("sequence").Asc()).ToSQL()
		if buildErr != nil {
			return buildErr
		}
		turns, err := s.queryTurns(ctx, turnsQuery, turnsArgs...)
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
		if err := s.saveChatSummary(ctx, chat); err != nil {
			return err
		}
		deleteQuery, deleteArgs, err := dialect.Delete("assistant_turns").Prepared(true).
			Where(goqu.Ex{"chat_id": chat.ID}).ToSQL()
		if err != nil {
			return err
		}
		if _, err = s.db.Exec(ctx, deleteQuery, deleteArgs...); err != nil {
			return err
		}
		for i, turn := range chat.Turns {
			if err = s.saveTurn(ctx, chat.ID, i, turn); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *SQLHistoryStorage) saveChatSummary(ctx context.Context, chat Chat) error {
	meta := ChatSummary{ID: chat.ID, Title: chat.Title, CreatedAt: chat.CreatedAt,
		UpdatedAt: chat.UpdatedAt, Usage: chat.Usage}
	payload, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	query, args, err := dialect.Insert("assistant_chats").Prepared(true).
		Rows(goqu.Record{"id": chat.ID, "updated_at_ns": chat.UpdatedAt.UnixNano(), "payload": string(payload)}).
		OnConflict(goqu.DoUpdate("id", goqu.Record{
			"updated_at_ns": goqu.L("excluded.updated_at_ns"),
			"payload":       goqu.L("excluded.payload"),
		})).ToSQL()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}

func (s *SQLHistoryStorage) saveTurn(ctx context.Context, chatID string, sequence int, turn Turn) error {
	payload, err := json.Marshal(turn)
	if err != nil {
		return err
	}
	query, args, err := dialect.Insert("assistant_turns").Prepared(true).
		Rows(goqu.Record{"chat_id": chatID, "sequence": sequence, "payload": string(payload)}).ToSQL()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}

func (s *SQLHistoryStorage) queryChatSummaries(
	ctx context.Context, query string, args ...any,
) ([]ChatSummary, error) {
	var payloads [][]byte
	if err := s.db.Select(ctx, &payloads, query, args...); err != nil {
		return nil, err
	}
	result := make([]ChatSummary, 0, len(payloads))
	for _, payload := range payloads {
		var value ChatSummary
		if err := json.Unmarshal(payload, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func (s *SQLHistoryStorage) queryTurns(ctx context.Context, query string, args ...any) ([]Turn, error) {
	var payloads [][]byte
	if err := s.db.Select(ctx, &payloads, query, args...); err != nil {
		return nil, err
	}
	result := make([]Turn, 0, len(payloads))
	for _, payload := range payloads {
		var value Turn
		if err := json.Unmarshal(payload, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}
