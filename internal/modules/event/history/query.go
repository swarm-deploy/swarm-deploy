package history

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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

