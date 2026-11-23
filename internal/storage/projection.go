package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
)

// AdvanceProjection accepts a strictly newer source in the caller's projection transaction.
// Source IDs use UUIDv7 to preserve publication order within a millisecond.
func AdvanceProjection(
	ctx context.Context, db Database, projection, resource string, at time.Time, id string,
) (bool, error) {
	if at.IsZero() {
		return false, fmt.Errorf("projection occurrence time is required")
	}
	at = at.UTC()
	accepted := false
	err := db.WithinTransaction(ctx, func(ctx context.Context) error {
		record := goqu.Record{
			"projection":      projection,
			"resource":        resource,
			"occurred_at":     at,
			"source_event_id": id,
		}
		query, args, buildErr := db.Dialect().Insert("projection_versions").Prepared(true).
			Rows(record).
			OnConflict(goqu.DoNothing()).
			ToSQL()
		if buildErr != nil {
			return buildErr
		}
		result, execErr := db.Exec(ctx, query, args...)
		if execErr != nil {
			return execErr
		}
		count, countErr := result.RowsAffected()
		if countErr != nil {
			return countErr
		}
		if count == 1 {
			accepted = true
			return nil
		}
		query, args, buildErr = db.Dialect().Update("projection_versions").Prepared(true).
			Set(goqu.Record{"occurred_at": at, "source_event_id": id}).
			Where(
				goqu.Ex{"projection": projection, "resource": resource},
				goqu.L("(?,?) > (occurred_at,source_event_id)", at, id),
			).
			ToSQL()
		if buildErr != nil {
			return buildErr
		}
		result, execErr = db.Exec(ctx, query, args...)
		if execErr != nil {
			return execErr
		}
		count, countErr = result.RowsAffected()
		accepted = count == 1
		return countErr
	})
	return accepted, err
}
