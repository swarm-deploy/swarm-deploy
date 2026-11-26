package conversation

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// SQLHistoryStorage stores chat metadata and ordered turns in a shared database.
type SQLHistoryStorage struct{ db storage.Database }

// NewSQLHistoryStorage creates a conversation repository.
func NewSQLHistoryStorage(db storage.Database) *SQLHistoryStorage { return &SQLHistoryStorage{db: db} }

// List returns metadata without loading conversation turns.
func (s *SQLHistoryStorage) ReadChats(ctx context.Context) ([]ChatSummary, error) {
	query, _, err := s.db.Dialect().From("assistant_chats").Prepared(true).
		Select(chatColumns...).Order(goqu.I("updated_at").Desc(), goqu.I("id").Asc()).ToSQL()
	if err != nil {
		return nil, err
	}
	var rows []chatRow
	if err = s.db.Select(ctx, &rows, query); err != nil {
		return nil, err
	}
	result := make([]ChatSummary, 0, len(rows))
	for _, row := range rows {
		summary, summaryErr := row.summary()
		if summaryErr != nil {
			return nil, summaryErr
		}
		result = append(result, summary)
	}
	return result, nil
}

// Get reads metadata and turns from a consistent transaction snapshot.
func (s *SQLHistoryStorage) ReadChat(ctx context.Context, id string) (Chat, bool, error) {
	var chat Chat
	found := false
	err := s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		var err error
		chat, found, err = s.readChatMetadata(ctx, id)
		if err != nil || !found {
			return err
		}
		turnsQuery, turnsArgs, buildErr := s.db.Dialect().From("assistant_turns").Prepared(true).
			Select("sequence", "payload").Where(goqu.Ex{"chat_id": id}).Order(goqu.I("sequence").Asc()).ToSQL()
		if buildErr != nil {
			return buildErr
		}
		chat.Turns, err = s.queryTurns(ctx, turnsQuery, turnsArgs...)
		return err
	})
	if err != nil {
		return Chat{}, false, err
	}
	return chat, found, nil
}

func (s *SQLHistoryStorage) readChatMetadata(ctx context.Context, id string) (Chat, bool, error) {
	query, args, err := s.db.Dialect().From("assistant_chats").Prepared(true).
		Select(chatColumns...).SelectAppend("created_by").Where(goqu.Ex{"id": id}).ToSQL()
	if err != nil {
		return Chat{}, false, err
	}
	var row chatRow
	err = s.db.Get(ctx, &row, query, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return Chat{}, false, nil
	}
	if err != nil {
		return Chat{}, false, err
	}
	meta, err := row.summary()
	if err != nil {
		return Chat{}, false, err
	}
	return Chat{ID: meta.ID, Title: meta.Title, CreatedAt: meta.CreatedAt, CreatedBy: row.CreatedBy.String,
		UpdatedAt: meta.UpdatedAt, Unread: meta.Unread, Usage: meta.Usage,
		PrimaryArtifact: meta.PrimaryArtifact}, true, nil
}

// MarkRead clears unread only when sequence still identifies the latest assistant reply.
func (s *SQLHistoryStorage) MarkRead(ctx context.Context, id string, sequence int) error {
	dialect := s.db.Dialect()
	latestReply := dialect.From("assistant_turns").Select(goqu.MAX("sequence")).
		Where(goqu.Ex{"chat_id": id}, goqu.L("json_extract(payload, '$.role')").Eq("assistant"))
	query, args, err := dialect.Update("assistant_chats").Prepared(true).
		Set(goqu.Record{"unread": false}).
		Where(goqu.Ex{"id": id, "unread": true}, goqu.L("(?)", latestReply).Eq(sequence)).ToSQL()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}

// SaveTurns appends turns and usage atomically, creating metadata lazily.
// The creator is recorded only on the first insert.
func (s *SQLHistoryStorage) SaveTurns(
	ctx context.Context,
	id, createdBy string,
	usage TokenUsage,
	turns ...Turn,
) (int, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return -1, fmt.Errorf("assistant conversation id is required")
	}
	if len(turns) == 0 && usage.IsZero() {
		return -1, nil
	}
	sequence := -1
	err := s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		chat, exists, err := s.readChatMetadata(ctx, id)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if !exists {
			chat = Chat{ID: id, Title: defaultChatTitle, CreatedAt: now, CreatedBy: createdBy, UpdatedAt: now}
		}
		chat.addUsage(usage)
		if len(turns) == 0 {
			return s.saveChatSummary(ctx, chat)
		}
		next := 0
		if exists {
			next, err = s.nextTurnSequence(ctx, id)
			if err != nil {
				return err
			}
		}
		if next == 0 && chat.Title == defaultChatTitle {
			chat.Title = buildChatTitle(turns)
		}
		chat.UpdatedAt = now
		chat.Unread = chat.Unread || slices.ContainsFunc(turns, func(turn Turn) bool {
			return turn.Role == "assistant"
		})
		chat.PrimaryArtifact = selectPrimaryArtifact(chat.PrimaryArtifact, turns)
		if err = s.saveChatSummary(ctx, chat); err != nil {
			return err
		}
		sequence = next + len(turns) - 1
		return s.insertTurns(ctx, id, next, turns)
	})
	return sequence, err
}

// Append appends turns without a usage sample.
func (s *SQLHistoryStorage) Append(ctx context.Context, id string, turns ...Turn) error {
	_, err := s.SaveTurns(ctx, id, "", TokenUsage{}, turns...)
	return err
}

// Save atomically persists an entire chat, preserving timestamps during import.
func (s *SQLHistoryStorage) Save(ctx context.Context, chat Chat) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		chat.PrimaryArtifact = selectPrimaryArtifact(nil, chat.Turns)
		if err := s.saveChatSummary(ctx, chat); err != nil {
			return err
		}
		deleteQuery, deleteArgs, err := s.db.Dialect().Delete("assistant_turns").Prepared(true).
			Where(goqu.Ex{"chat_id": chat.ID}).ToSQL()
		if err != nil {
			return err
		}
		if _, err = s.db.Exec(ctx, deleteQuery, deleteArgs...); err != nil {
			return err
		}
		return s.insertTurns(ctx, chat.ID, 0, chat.Turns)
	})
}

func (s *SQLHistoryStorage) saveChatSummary(ctx context.Context, chat Chat) error {
	if chat.CreatedAt.IsZero() || chat.UpdatedAt.IsZero() {
		return fmt.Errorf("assistant chat timestamps are required")
	}
	input, output, total := usageColumns(chat.Usage)
	var createdBy any
	if chat.CreatedBy != "" {
		createdBy = chat.CreatedBy
	}
	var primaryArtifact any
	if chat.PrimaryArtifact != nil {
		encoded, err := json.Marshal(chat.PrimaryArtifact)
		if err != nil {
			return err
		}
		primaryArtifact = string(encoded)
	}
	query, args, err := s.db.Dialect().Insert("assistant_chats").Prepared(true).
		Rows(goqu.Record{
			"id": chat.ID, "title": chat.Title, "created_at": chat.CreatedAt.UTC(), "created_by": createdBy,
			"updated_at": chat.UpdatedAt.UTC(), "input_tokens": input,
			"output_tokens": output, "total_tokens": total, "unread": chat.Unread,
			"primary_artifact": primaryArtifact,
		}).
		OnConflict(goqu.DoUpdate("id", goqu.Record{
			"title": goqu.I("excluded.title"), "created_at": goqu.I("excluded.created_at"),
			"updated_at": goqu.I("excluded.updated_at"), "input_tokens": goqu.I("excluded.input_tokens"),
			"output_tokens": goqu.I("excluded.output_tokens"), "total_tokens": goqu.I("excluded.total_tokens"),
			"unread":           goqu.I("excluded.unread"),
			"primary_artifact": goqu.I("excluded.primary_artifact"),
		})).ToSQL()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}

func (s *SQLHistoryStorage) nextTurnSequence(ctx context.Context, chatID string) (int, error) {
	query, args, err := s.db.Dialect().From("assistant_turns").Prepared(true).
		Select(goqu.COALESCE(goqu.MAX("sequence"), -1)).Where(goqu.Ex{"chat_id": chatID}).ToSQL()
	if err != nil {
		return 0, err
	}
	var last int
	if err = s.db.Get(ctx, &last, query, args...); err != nil {
		return 0, err
	}
	return last + 1, nil
}

func (s *SQLHistoryStorage) insertTurns(ctx context.Context, chatID string, next int, turns []Turn) error {
	for i, turn := range turns {
		if err := s.saveTurn(ctx, chatID, next+i, turn); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLHistoryStorage) saveTurn(ctx context.Context, chatID string, sequence int, turn Turn) error {
	payload, err := json.Marshal(turn)
	if err != nil {
		return err
	}
	query, args, err := s.db.Dialect().Insert("assistant_turns").Prepared(true).
		Rows(goqu.Record{"chat_id": chatID, "sequence": sequence, "payload": string(payload)}).ToSQL()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}

func (s *SQLHistoryStorage) queryTurns(ctx context.Context, query string, args ...any) ([]Turn, error) {
	var rows []struct {
		Sequence int    `db:"sequence"`
		Payload  []byte `db:"payload"`
	}
	if err := s.db.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	result := make([]Turn, 0, len(rows))
	for _, row := range rows {
		var value Turn
		if err := json.Unmarshal(row.Payload, &value); err != nil {
			return nil, err
		}
		value.Sequence = row.Sequence
		result = append(result, value)
	}
	return result, nil
}

var chatColumns = []any{
	"id", "title", "created_at", "updated_at", "unread", "input_tokens", "output_tokens", "total_tokens",
	"primary_artifact",
}

type chatRow struct {
	ID              string         `db:"id"`
	Title           string         `db:"title"`
	CreatedAt       time.Time      `db:"created_at"`
	CreatedBy       sql.NullString `db:"created_by"`
	UpdatedAt       time.Time      `db:"updated_at"`
	Unread          bool           `db:"unread"`
	InputTokens     sql.NullInt64  `db:"input_tokens"`
	OutputTokens    sql.NullInt64  `db:"output_tokens"`
	TotalTokens     sql.NullInt64  `db:"total_tokens"`
	PrimaryArtifact sql.NullString `db:"primary_artifact"`
}

func (r chatRow) summary() (ChatSummary, error) {
	result := ChatSummary{
		ID: r.ID, Title: r.Title, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), Unread: r.Unread,
	}
	if r.InputTokens.Valid || r.OutputTokens.Valid || r.TotalTokens.Valid {
		result.Usage = &TokenUsage{
			InputTokens: r.InputTokens.Int64, OutputTokens: r.OutputTokens.Int64, TotalTokens: r.TotalTokens.Int64,
		}
	}
	if r.PrimaryArtifact.Valid {
		var artifact Artifact
		if err := json.Unmarshal([]byte(r.PrimaryArtifact.String), &artifact); err != nil {
			return ChatSummary{}, fmt.Errorf("decode assistant primary artifact: %w", err)
		}
		result.PrimaryArtifact = &artifact
	}
	return result, nil
}

func selectPrimaryArtifact(current *Artifact, turns []Turn) *Artifact {
	var selected *Artifact
	hasPullRequest := false
	if current != nil {
		selectedArtifact := *current
		selected = &selectedArtifact
		hasPullRequest = current.Type == ArtifactTypePullRequest
	}
	for _, turn := range turns {
		for _, artifact := range turn.Artifacts {
			if artifact.Type == ArtifactTypePullRequest {
				selectedArtifact := artifact
				selected = &selectedArtifact
				hasPullRequest = true
				continue
			}
			if !hasPullRequest {
				selectedArtifact := artifact
				selected = &selectedArtifact
			}
		}
	}
	return selected
}

func usageColumns(usage *TokenUsage) (any, any, any) {
	if usage == nil {
		return nil, nil, nil
	}
	return usage.InputTokens, usage.OutputTokens, usage.TotalTokens
}
