package history

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

type SortOrder string

const maxEventCursorLength = 2048
const severityScoreAlert = 3
const severityScoreError = 2
const severityScoreWarn = 1
const severityScoreInfo = 0

const (
	SortTimeDesc     SortOrder = "time_desc"
	SortTimeAsc      SortOrder = "time_asc"
	SortSeverityDesc SortOrder = "severity_desc"
	SortSeverityAsc  SortOrder = "severity_asc"
)

var (
	ErrInvalidCursor = errors.New("invalid event history cursor")
	ErrInvalidSort   = errors.New("invalid event history sort")
)

type QueryOptions struct {
	Severities []events.Severity
	Categories []events.Category
	Types      []events.TypeName
	Since      *time.Time
	Limit      int
	Cursor     string
	Sort       SortOrder
}

type Page struct {
	Entries    []Entry
	NextCursor string
}

type pageCursor struct {
	Sort      SortOrder       `json:"sort"`
	CreatedAt time.Time       `json:"created_at"`
	Severity  events.Severity `json:"severity"`
	ID        string          `json:"id"`
}

// Query returns a page from a stable ordering of the in-memory history.
// The cursor is based on event values rather than slice offsets so new events
// or eviction of older entries do not shift the pagination boundary.
func (s *Store) Query(options QueryOptions) (Page, error) {
	if options.Limit < 1 || options.Limit > 100 {
		return Page{}, fmt.Errorf("event history limit must be between 1 and 100")
	}
	if options.Sort == "" {
		options.Sort = SortTimeDesc
	}
	switch options.Sort {
	case SortTimeDesc, SortTimeAsc, SortSeverityDesc, SortSeverityAsc:
	default:
		return Page{}, fmt.Errorf("%w: %q", ErrInvalidSort, options.Sort)
	}

	cursor, err := decodePageCursor(options.Cursor, options.Sort)
	if err != nil {
		return Page{}, err
	}

	severitySet := toSeveritySet(options.Severities)
	categorySet := toCategorySet(options.Categories)
	typeSet := toTypeSet(options.Types)

	s.mu.RLock()
	defer s.mu.RUnlock()

	matched := make([]*Entry, 0, len(s.entries))
	for i := range s.entries {
		entry := &s.entries[i]
		if matchesEntryFilters(*entry, severitySet, categorySet, typeSet, options.Since) {
			matched = append(matched, entry)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		return entryLess(*matched[i], *matched[j], options.Sort)
	})

	page := Page{Entries: make([]Entry, 0, min(options.Limit, len(matched)))}
	for _, entry := range matched {
		if options.Cursor != "" && !entryLess(cursor.entry(), *entry, options.Sort) {
			continue
		}
		if len(page.Entries) == options.Limit {
			last := page.Entries[len(page.Entries)-1]
			page.NextCursor = encodePageCursor(last, options.Sort)
			break
		}
		copied := *entry
		copied.Details = cloneDetails(entry.Details)
		page.Entries = append(page.Entries, copied)
	}

	return page, nil
}

func entryLess(left, right Entry, order SortOrder) bool {
	switch order {
	case SortSeverityDesc, SortSeverityAsc:
		if left.Severity != right.Severity {
			return (severityRank(left.Severity) > severityRank(right.Severity)) == (order == SortSeverityDesc)
		}
		return timeLess(left, right, SortTimeDesc)
	case SortTimeDesc, SortTimeAsc:
		return timeLess(left, right, order)
	default:
		return timeLess(left, right, order)
	}
}

func timeLess(left, right Entry, order SortOrder) bool {
	if left.CreatedAt.Equal(right.CreatedAt) {
		return left.ID < right.ID
	}
	if order == SortTimeAsc {
		return left.CreatedAt.Before(right.CreatedAt)
	}
	return left.CreatedAt.After(right.CreatedAt)
}

func severityRank(severity events.Severity) int {
	switch severity {
	case events.SeverityAlert:
		return severityScoreAlert
	case events.SeverityError:
		return severityScoreError
	case events.SeverityWarn:
		return severityScoreWarn
	case events.SeverityInfo:
		return severityScoreInfo
	default:
		return severityScoreInfo
	}
}

func encodePageCursor(entry Entry, order SortOrder) string {
	data, _ := json.Marshal(pageCursor{
		Sort: order, CreatedAt: entry.CreatedAt, Severity: entry.Severity, ID: entry.ID,
	})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodePageCursor(raw string, order SortOrder) (pageCursor, error) {
	if raw == "" {
		return pageCursor{}, nil
	}
	if len(raw) > maxEventCursorLength {
		return pageCursor{}, ErrInvalidCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return pageCursor{}, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	var cursor pageCursor
	if unmarshalErr := json.Unmarshal(data, &cursor); unmarshalErr != nil {
		return pageCursor{}, fmt.Errorf("%w: %v", ErrInvalidCursor, unmarshalErr)
	}
	if cursor.ID == "" || cursor.CreatedAt.IsZero() || cursor.Sort != order {
		return pageCursor{}, ErrInvalidCursor
	}
	return cursor, nil
}

func (c pageCursor) entry() Entry {
	return Entry{ID: c.ID, CreatedAt: c.CreatedAt, Severity: c.Severity}
}
