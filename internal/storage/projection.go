package storage

import (
	"context"
	"time"

	"github.com/Thiht/transactor/stdlib"
)

// AdvanceProjection accepts a strictly newer source in the caller's projection transaction.
// Source IDs use UUIDv7 to preserve publication order within a millisecond.
func AdvanceProjection(
	ctx context.Context, get stdlib.DBGetter, projection, resource string, at time.Time, id string,
) (bool, error) {
	result, err := get(ctx).ExecContext(ctx, `INSERT INTO projection_versions VALUES(?,?,?,?)
 ON CONFLICT(projection,resource) DO UPDATE SET
 occurred_at_ms=excluded.occurred_at_ms,source_event_id=excluded.source_event_id
 WHERE (excluded.occurred_at_ms,excluded.source_event_id)>
 (projection_versions.occurred_at_ms,projection_versions.source_event_id)`,
		projection, resource, at.UnixMilli(), id)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
