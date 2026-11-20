package conversation

import (
	"context"
	"fmt"
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

func (s *SQLHistoryStorage) addDailyUsage(ctx context.Context, at time.Time, usage TokenUsage) error {
	query, args, err := s.db.Dialect().Insert("assistant_daily_usage").Prepared(true).
		Rows(goqu.Record{
			"day": at.UTC().Format(time.DateOnly), "input_tokens": usage.InputTokens,
			"output_tokens": usage.OutputTokens, "total_tokens": usage.TotalTokens,
		}).
		OnConflict(goqu.DoUpdate("day", goqu.Record{
			"input_tokens":  goqu.L("assistant_daily_usage.input_tokens + excluded.input_tokens"),
			"output_tokens": goqu.L("assistant_daily_usage.output_tokens + excluded.output_tokens"),
			"total_tokens":  goqu.L("assistant_daily_usage.total_tokens + excluded.total_tokens"),
		})).ToSQL()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}

// ReadUsage groups recorded consumption from inclusive from to exclusive to.
func (s *SQLHistoryStorage) ReadUsage(
	ctx context.Context,
	from, to time.Time,
	interval UsageInterval,
) ([]UsageBucket, error) {
	query, args, err := s.db.Dialect().From("assistant_daily_usage").Prepared(true).
		Select("day", "input_tokens", "output_tokens", "total_tokens").
		Where(goqu.C("day").Gte(from.UTC().Format(time.DateOnly)), goqu.C("day").Lt(to.UTC().Format(time.DateOnly))).
		Order(goqu.C("day").Asc()).ToSQL()
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Day          string `db:"day"`
		InputTokens  int64  `db:"input_tokens"`
		OutputTokens int64  `db:"output_tokens"`
		TotalTokens  int64  `db:"total_tokens"`
	}
	if err = s.db.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}

	buckets := make([]UsageBucket, 0, len(rows))
	for _, row := range rows {
		day, parseErr := time.Parse(time.DateOnly, row.Day)
		if parseErr != nil {
			return nil, fmt.Errorf("parse assistant usage day: %w", parseErr)
		}
		start := interval.start(day)
		if len(buckets) == 0 {
			buckets = append(buckets, UsageBucket{Start: start})
		}
		// Keep gaps between recorded buckets visible on the time axis.
		for cursor := interval.next(buckets[len(buckets)-1].Start); !cursor.After(start); cursor = interval.next(cursor) {
			buckets = append(buckets, UsageBucket{Start: cursor})
		}
		buckets[len(buckets)-1].Add(TokenUsage{
			InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, TotalTokens: row.TotalTokens,
		})
	}
	return buckets, nil
}

func (i UsageInterval) start(day time.Time) time.Time {
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
