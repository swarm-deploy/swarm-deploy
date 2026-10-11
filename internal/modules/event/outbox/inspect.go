package outbox

import "context"

// FailedDelivery exposes operational metadata, never serialized event contents.
type FailedDelivery struct {
	// EventID identifies the retained publication.
	EventID string `json:"event_id"`
	// SubscriptionID identifies the failed destination.
	SubscriptionID string `json:"subscription_id"`
	// EventType is the safe domain type.
	EventType string `json:"event_type"`
	// Attempts counts processing claims.
	Attempts int `json:"attempts"`
	// LastError contains a stable error category.
	LastError string `json:"last_error"`
}

// ListFailed returns retained terminal failures for explicit operator action.
func (b *Bus) ListFailed(ctx context.Context) ([]FailedDelivery, error) {
	rows, err := b.db.Get(ctx).QueryContext(ctx, `SELECT d.event_id,d.subscription_id,e.event_type,
 d.attempts,coalesce(d.last_error,'')
 FROM outbox_deliveries d JOIN outbox_events e ON e.id=d.event_id WHERE d.status='failed'
 ORDER BY d.updated_at_ms,d.event_id,d.subscription_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []FailedDelivery{}
	for rows.Next() {
		var d FailedDelivery
		if err = rows.Scan(&d.EventID, &d.SubscriptionID, &d.EventType, &d.Attempts, &d.LastError); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
