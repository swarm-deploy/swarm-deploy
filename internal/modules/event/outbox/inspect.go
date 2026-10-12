package outbox

import (
	"context"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
)

// FailedDelivery exposes operational metadata, never serialized event contents.
type FailedDelivery struct {
	// EventID identifies the retained publication.
	EventID string `json:"event_id" db:"event_id"`
	// SubscriptionID identifies the failed destination.
	SubscriptionID string `json:"subscription_id" db:"subscription_id"`
	// EventType is the safe domain type.
	EventType string `json:"event_type" db:"event_type"`
	// Attempts counts processing claims.
	Attempts int `json:"attempts" db:"attempts"`
	// LastError contains a stable error category.
	LastError string `json:"last_error" db:"last_error"`
}

// ListFailed returns retained terminal failures for explicit operator action.
func (b *Bus) ListFailed(ctx context.Context) ([]FailedDelivery, error) {
	query, args, err := goqu.Dialect("sqlite3").
		From(goqu.T("outbox_deliveries").As("d")).Prepared(true).
		Join(goqu.T("outbox_events").As("e"), goqu.On(goqu.I("e.id").Eq(goqu.I("d.event_id")))).
		Select(
			goqu.I("d.event_id"), goqu.I("d.subscription_id"), goqu.I("e.event_type"),
			goqu.I("d.attempts"), goqu.COALESCE(goqu.I("d.last_error"), "").As("last_error"),
		).
		Where(goqu.I("d.status").Eq("failed")).
		Order(goqu.I("d.updated_at_ms").Asc(), goqu.I("d.event_id").Asc(), goqu.I("d.subscription_id").Asc()).
		ToSQL()
	if err != nil {
		return nil, err
	}
	result := []FailedDelivery{}
	err = b.db.Select(ctx, &result, query, args...)
	return result, err
}
