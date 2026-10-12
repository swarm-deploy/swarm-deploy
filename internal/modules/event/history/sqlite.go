package history

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	"github.com/doug-martin/goqu/v9/exp"
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

// Handle materializes selected user facts; failure signals remain alert inputs.
func (s *SQLStore) Handle(ctx context.Context, e events.Envelope) error {
	switch e.Event.Type() {
	case events.TypeServiceCatalogUpdated, events.TypeDeployFailed, events.TypeNodeDisconnected:
		return nil
	}
	if e.ID == "" || e.OccurredAt.IsZero() || e.PublicationSequence <= 0 {
		return fmt.Errorf("history requires a persisted event ID, publication time and sequence")
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
		if insertErr := s.insert(ctx, entry, e.ID, e.PublicationSequence); insertErr != nil {
			return insertErr
		}
		limit := uint(s.capacity) // #nosec G115 -- NewSQLStore accepts only positive int capacities.
		retained := goqu.Dialect("sqlite3").From("event_history").Prepared(true).
			Select("publication_sequence").
			Order(goqu.I("publication_sequence").Desc()).
			Limit(limit)
		query, args, buildErr := goqu.Dialect("sqlite3").Delete("event_history").Prepared(true).
			Where(goqu.C("publication_sequence").NotIn(retained)).
			ToSQL()
		if buildErr != nil {
			return buildErr
		}
		_, pruneErr := s.db.Exec(ctx, query, args...)
		return pruneErr
	})
}

func (s *SQLStore) insert(ctx context.Context, entry Entry, source any, publicationSequence int64) error {
	payload, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	// goqu's SQLite DoNothing cannot target only source_event_id; event_history
	// has other unique columns whose conflicts must still fail.
	_, err = s.db.Exec(ctx, `INSERT INTO event_history
		(publication_sequence,id,source_event_id,event_type,severity,severity_rank,category,created_at_ns,payload)
		VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(source_event_id) DO NOTHING`,
		publicationSequence, entry.ID, source, entry.Type.Name(), entry.Severity, severityRank(entry.Severity),
		entry.Category, entry.CreatedAt.UnixNano(), string(payload))
	return err
}

// List returns the retained publication sequence, in publication order.
func (s *SQLStore) Read(ctx context.Context) ([]Entry, error) {
	dataset := goqu.Dialect("sqlite3").From("event_history").Prepared(true).Select("payload").
		Order(goqu.I("publication_sequence").Asc())
	return s.queryEntriesDataset(ctx, dataset)
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
	dataset := goqu.Dialect("sqlite3").From("event_history").Prepared(true).Select("payload").
		Where(historyFilters(options)...)
	if options.Cursor != "" {
		dataset = dataset.Where(historyCursorExpression(cursor))
	}
	dataset = dataset.Order(order...).Limit(uint(options.Limit + 1))
	entries, err := s.queryEntriesDataset(ctx, dataset)
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

// ReadRecent preserves publication order while performing filters in SQLite.
func (s *SQLStore) ReadRecent(ctx context.Context, options QueryOptions) ([]Entry, error) {
	dialect := goqu.Dialect("sqlite3")
	recent := dialect.From("event_history").Prepared(true).Select("publication_sequence", "payload").
		Where(historyFilters(options)...)
	if options.Limit > 0 {
		recent = recent.Order(goqu.I("publication_sequence").Desc()).Limit(uint(options.Limit))
	}
	dataset := dialect.From(recent.As("recent")).Prepared(true).Select("payload").
		Order(goqu.I("publication_sequence").Asc())
	return s.queryEntriesDataset(ctx, dataset)
}

func (s *SQLStore) queryEntriesDataset(ctx context.Context, dataset *goqu.SelectDataset) ([]Entry, error) {
	query, args, err := dataset.ToSQL()
	if err != nil {
		return nil, err
	}
	var payloads [][]byte
	if err = s.db.Select(ctx, &payloads, query, args...); err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(payloads))
	for _, payload := range payloads {
		var entry Entry
		if err = json.Unmarshal(payload, &entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func historyFilters(options QueryOptions) []exp.Expression {
	where := []exp.Expression{}
	addHistoryFilter(&where, "severity", options.Severities)
	addHistoryFilter(&where, "category", options.Categories)
	addHistoryFilter(&where, "event_type", options.Types)
	if options.Since != nil {
		where = append(where, goqu.C("created_at_ns").Gte(options.Since.UnixNano()))
	}
	return where
}

var historyOrders = map[SortOrder][]exp.OrderedExpression{
	SortTimeDesc: {goqu.I("created_at_ns").Desc(), goqu.I("id").Asc()},
	SortTimeAsc:  {goqu.I("created_at_ns").Asc(), goqu.I("id").Asc()},
	SortSeverityDesc: {goqu.I("severity_rank").Desc(), goqu.I("created_at_ns").Desc(),
		goqu.I("id").Asc()},
	SortSeverityAsc: {goqu.I("severity_rank").Asc(), goqu.I("created_at_ns").Desc(),
		goqu.I("id").Asc()},
}

func addHistoryFilter[T ~string](where *[]exp.Expression, column string, values []T) {
	if len(values) == 0 {
		return
	}
	items := make([]any, len(values))
	for i := range values {
		items[i] = values[i]
	}
	*where = append(*where, goqu.C(column).In(items...))
}

func historyCursorExpression(c pageCursor) exp.Expression {
	timeComparison := goqu.C("created_at_ns").Lt(c.CreatedAt.UnixNano())
	if c.Sort == SortTimeAsc {
		timeComparison = goqu.C("created_at_ns").Gt(c.CreatedAt.UnixNano())
	}
	condition := goqu.Or(timeComparison, goqu.And(
		goqu.C("created_at_ns").Eq(c.CreatedAt.UnixNano()), goqu.C("id").Gt(c.ID),
	))
	if c.Sort == SortSeverityAsc || c.Sort == SortSeverityDesc {
		severityComparison := goqu.C("severity_rank").Lt(severityRank(c.Severity))
		if c.Sort == SortSeverityAsc {
			severityComparison = goqu.C("severity_rank").Gt(severityRank(c.Severity))
		}
		condition = goqu.Or(severityComparison, goqu.And(
			goqu.C("severity_rank").Eq(severityRank(c.Severity)), condition,
		))
	}
	return condition
}
