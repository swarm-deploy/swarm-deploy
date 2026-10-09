package history

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/codec"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// Repository provides context-aware history reads.
type Repository interface {
	// List returns retained history in publication order.
	Read(context.Context) ([]Entry, error)
	// ReadRecent filters by SQL and returns the latest matching publication sequence oldest first.
	ReadRecent(context.Context, QueryOptions) ([]Entry, error)
	// Query filters and pages history using a stable value cursor.
	QueryPage(context.Context, QueryOptions) (Page, error)
}

// SQLStore persists the user-visible projection, independently of the outbox.
type SQLStore struct {
	db       *storage.Database
	capacity int
}

// NewSQLStore creates a history repository sharing the application's transactor.
func NewSQLStore(db *storage.Database, capacity int) (*SQLStore, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("event history capacity must be positive")
	}
	return &SQLStore{db: db, capacity: capacity}, nil
}

// Name identifies the history projection.
func (s *SQLStore) Name() string { return "event-history" }

// Slow reports that projection performs storage I/O.
func (s *SQLStore) Slow() bool { return true }

// Handle materializes selected user facts; failure signals remain alert inputs.
func (s *SQLStore) Handle(ctx context.Context, e events.Envelope) error {
	if e.Event.Type() == events.TypeDeployFailed || e.Event.Type() == events.TypeNodeDisconnected {
		return nil
	}
	if e.ID == "" || e.OccurredAt.IsZero() {
		return fmt.Errorf("history requires a persisted event ID and publication time")
	}
	payload, err := codec.Encode(e.Event)
	if err != nil {
		return err
	}
	e.Event, err = codec.Decode(e.Event.Type().Name(), codec.Version, payload)
	if err != nil {
		return err
	}
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		entry := toEntry(e.OccurredAt, e)
		if insertErr := s.insert(ctx, entry, e.ID); insertErr != nil {
			return insertErr
		}
		_, pruneErr := s.db.Get(ctx).ExecContext(ctx, `DELETE FROM event_history WHERE sequence NOT IN (
			SELECT sequence FROM event_history ORDER BY sequence DESC LIMIT ?)`, s.capacity)
		return pruneErr
	})
}

// Import preserves historical values and ordering without synthesizing deployments.
// It must be called within the startup import transaction.
func (s *SQLStore) Import(ctx context.Context, entries []Entry) error {
	for _, entry := range entries {
		if err := s.insert(ctx, entry, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLStore) insert(ctx context.Context, entry Entry, source any) error {
	payload, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = s.db.Get(ctx).ExecContext(ctx, `INSERT INTO event_history
		(id,source_event_id,event_type,severity,severity_rank,category,created_at_ns,payload)
		VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(source_event_id) DO NOTHING`,
		entry.ID, source, entry.Type.Name(), entry.Severity, severityRank(entry.Severity),
		entry.Category, entry.CreatedAt.UnixNano(), string(payload))
	return err
}

// List returns the retained publication sequence, including original import order.
func (s *SQLStore) Read(ctx context.Context) ([]Entry, error) {
	return storage.QueryJSON[Entry](ctx, s.db.Get, "SELECT payload FROM event_history ORDER BY sequence")
}

// Query applies filtering, ordering and cursor boundaries in SQLite.
func (s *SQLStore) QueryPage(ctx context.Context, options QueryOptions) (Page, error) {
	if options.Limit < 1 || options.Limit > 100 {
		return Page{}, fmt.Errorf("event history limit must be between 1 and 100")
	}
	if options.Sort == "" {
		options.Sort = SortTimeDesc
	}
	order, ok := historyOrders[options.Sort]
	if !ok {
		return Page{}, ErrInvalidSort
	}
	cursor, err := decodePageCursor(options.Cursor, options.Sort)
	if err != nil {
		return Page{}, err
	}
	where, args := historyFilters(options)
	if options.Cursor != "" {
		condition, values := historyCursorSQL(cursor)
		where = append(where, condition)
		args = append(args, values...)
	}
	args = append(args, options.Limit+1)
	entries, err := storage.QueryJSON[Entry](ctx, s.db.Get,
		"SELECT payload FROM event_history WHERE "+strings.Join(where, " AND ")+" ORDER BY "+order+" LIMIT ?", args...)
	if err != nil {
		return Page{}, err
	}
	page := Page{Entries: entries}
	if len(entries) > options.Limit {
		page.Entries = entries[:options.Limit]
		page.NextCursor = encodePageCursor(page.Entries[len(page.Entries)-1], options.Sort)
	}
	return page, nil
}

// ReadRecent preserves the legacy sequence order while performing filters in SQLite.
func (s *SQLStore) ReadRecent(ctx context.Context, options QueryOptions) ([]Entry, error) {
	where, args := historyFilters(options)
	query := "SELECT sequence,payload FROM event_history WHERE " + strings.Join(where, " AND ")
	if options.Limit > 0 {
		query += " ORDER BY sequence DESC LIMIT ?"
		args = append(args, options.Limit)
	}
	return storage.QueryJSON[Entry](ctx, s.db.Get,
		"SELECT payload FROM ("+query+") ORDER BY sequence", args...)
}

func historyFilters(options QueryOptions) ([]string, []any) {
	where := []string{"1=1"}
	args := []any{}
	addHistoryFilter(&where, &args, "severity", options.Severities)
	addHistoryFilter(&where, &args, "category", options.Categories)
	addHistoryFilter(&where, &args, "event_type", options.Types)
	if options.Since != nil {
		where = append(where, "created_at_ns>=?")
		args = append(args, options.Since.UnixNano())
	}
	return where, args
}

var historyOrders = map[SortOrder]string{
	SortTimeDesc: "created_at_ns DESC,id ASC", SortTimeAsc: "created_at_ns ASC,id ASC",
	SortSeverityDesc: "severity_rank DESC,created_at_ns DESC,id ASC",
	SortSeverityAsc:  "severity_rank ASC,created_at_ns DESC,id ASC",
}

func addHistoryFilter[T ~string](where *[]string, args *[]any, column string, values []T) {
	if len(values) == 0 {
		return
	}
	marks := make([]string, len(values))
	for i, value := range values {
		marks[i] = "?"
		*args = append(*args, value)
	}
	*where = append(*where, column+" IN ("+strings.Join(marks, ",")+")")
}

func historyCursorSQL(c pageCursor) (string, []any) {
	timeOp := "<"
	if c.Sort == SortTimeAsc {
		timeOp = ">"
	}
	condition := "(created_at_ns " + timeOp + " ? OR (created_at_ns=? AND id>?))"
	args := []any{c.CreatedAt.UnixNano(), c.CreatedAt.UnixNano(), c.ID}
	if c.Sort == SortSeverityAsc || c.Sort == SortSeverityDesc {
		op := "<"
		if c.Sort == SortSeverityAsc {
			op = ">"
		}
		condition = "(severity_rank " + op + " ? OR (severity_rank=? AND " + condition + "))"
		args = append([]any{severityRank(c.Severity), severityRank(c.Severity)}, args...)
	}
	return condition, args
}
