package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
)

// AdvanceProjection accepts a strictly newer source in the caller's projection transaction.
// Source IDs use UUIDv7 to preserve publication order within a millisecond.
func AdvanceProjection(
	ctx context.Context, db *Database, projection, resource string, at time.Time, id string,
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
		query, args, buildErr := goqu.Dialect("sqlite3").Insert("projection_versions").Prepared(true).
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
		query, args, buildErr = goqu.Dialect("sqlite3").Update("projection_versions").Prepared(true).
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

// AdvanceProjectionSequence accepts a strictly newer durable publication sequence.
func AdvanceProjectionSequence(
	ctx context.Context, db *Database, projection, resource string, sequence int64, id string,
) (bool, error) {
	if sequence <= 0 || id == "" {
		return false, fmt.Errorf("projection publication sequence and source event ID are required")
	}
	accepted := false
	err := db.WithinTransaction(ctx, func(ctx context.Context) error {
		record := goqu.Record{
			"projection": projection, "resource": resource,
			"occurred_at": time.Unix(0, 0).UTC(), "source_event_id": id,
			"publication_sequence": sequence,
		}
		query, args, buildErr := goqu.Dialect("sqlite3").Insert("projection_versions").Prepared(true).
			Rows(record).OnConflict(goqu.DoNothing()).ToSQL()
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
		query, args, buildErr = goqu.Dialect("sqlite3").Update("projection_versions").Prepared(true).
			Set(goqu.Record{"publication_sequence": sequence, "source_event_id": id}).
			Where(goqu.Ex{"projection": projection, "resource": resource},
				goqu.C("publication_sequence").Lt(sequence)).ToSQL()
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

// ProjectionSequence returns a sequence high-water mark, or zero when absent.
func ProjectionSequence(ctx context.Context, db *Database, projection, resource string) (int64, error) {
	var sequence int64
	query, args, err := goqu.Dialect("sqlite3").From("projection_versions").Prepared(true).
		Select("publication_sequence").Where(goqu.Ex{"projection": projection, "resource": resource}).ToSQL()
	if err != nil {
		return 0, err
	}
	err = db.Get(ctx, &sequence, query, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return sequence, err
}
