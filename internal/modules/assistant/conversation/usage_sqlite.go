package conversation

import (
	"context"
	"time"

	"github.com/doug-martin/goqu/v9"
)

// UsageInterval is a UTC calendar grouping for recorded token consumption.
type UsageInterval string

const (
	UsageDaily   UsageInterval = "daily"
	UsageWeekly  UsageInterval = "weekly"
	UsageMonthly UsageInterval = "monthly"

	daysInWeek = 7
)

// UsageBucket contains recorded token consumption for one calendar interval.
type UsageBucket struct {
	// Start is the beginning of the calendar interval in UTC.
	Start time.Time
	// TokenUsage holds the recorded totals inside the requested range.
	TokenUsage
}

// ReadUsage groups stored token totals by chat creation time in [from, to).
func (s *SQLHistoryStorage) ReadUsage(
	ctx context.Context,
	from, to time.Time,
	interval UsageInterval,
) ([]UsageBucket, error) {
	createdAt := goqu.C("created_at")
	query, args, err := s.db.Dialect().From("assistant_chats").Prepared(true).
		Select("created_at", "input_tokens", "output_tokens", "total_tokens").
		Where(createdAt.Gte(from), createdAt.Lt(to), goqu.C("input_tokens").IsNotNull()).
		Order(createdAt.Asc()).ToSQL()
	if err != nil {
		return nil, err
	}
	var rows []chatRow
	if err = s.db.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}

	buckets := []UsageBucket{}
	for _, row := range rows {
		start := interval.start(row.CreatedAt)
		if len(buckets) == 0 {
			buckets = append(buckets, UsageBucket{Start: start})
		}
		// Keep gaps between recorded buckets visible on the time axis.
		for cursor := interval.next(buckets[len(buckets)-1].Start); !cursor.After(start); cursor = interval.next(cursor) {
			buckets = append(buckets, UsageBucket{Start: cursor})
		}
		buckets[len(buckets)-1].Add(TokenUsage{
			InputTokens: row.InputTokens.Int64, OutputTokens: row.OutputTokens.Int64, TotalTokens: row.TotalTokens.Int64,
		})
	}
	return buckets, nil
}

func (i UsageInterval) start(createdAt time.Time) time.Time {
	year, month, date := createdAt.UTC().Date()
	day := time.Date(year, month, date, 0, 0, 0, 0, time.UTC)
	switch i {
	case UsageWeekly:
		return day.AddDate(0, 0, -((int(day.Weekday()) + daysInWeek - 1) % daysInWeek))
	case UsageMonthly:
		return day.AddDate(0, 0, 1-day.Day())
	case UsageDaily:
		return day
	}
	return day
}

func (i UsageInterval) next(start time.Time) time.Time {
	switch i {
	case UsageWeekly:
		return start.AddDate(0, 0, daysInWeek)
	case UsageMonthly:
		return start.AddDate(0, 1, 0)
	case UsageDaily:
		return start.AddDate(0, 0, 1)
	}
	return start.AddDate(0, 0, 1)
}
