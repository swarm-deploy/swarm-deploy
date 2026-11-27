// Package alerts delivers alert lifecycle notifications through the shared Outbox.
package alerts

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/notifiers"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// MessageLink persists the transport state for one alert and physical recipient.
type MessageLink struct {
	// AlertID identifies the alert lifecycle.
	AlertID string `db:"alert_id"`
	// Messenger identifies the transport.
	Messenger string `db:"messenger"`
	// BotIdentity identifies the Telegram bot without its token.
	BotIdentity string `db:"bot_identity"`
	// ChatID is the actual Telegram chat.
	ChatID string `db:"chat_id"`
	// ThreadID is the actual Telegram topic.
	ThreadID int64 `db:"thread_id"`
	// OpenedMessageID identifies the initial Telegram message.
	OpenedMessageID sql.NullInt64 `db:"opened_message_id"`
	// OpenedAt is the time the initial message was accepted.
	OpenedAt sql.NullTime `db:"opened_at"`
	// ResolvedAt is the time the resolution action was accepted.
	ResolvedAt sql.NullTime `db:"resolved_at"`
	// ResolvedAction records the action actually accepted by Telegram.
	ResolvedAction sql.NullString `db:"resolved_action"`
	// ReplyMessageID identifies a sent reply when applicable.
	ReplyMessageID sql.NullInt64 `db:"reply_message_id"`
}

// Store persists Telegram message identities in the application SQLite database.
type Store struct{ db storage.Database }

// NewStore creates a message-link repository.
func NewStore(db storage.Database) *Store { return &Store{db: db} }

// Ensure records a physical destination before an external send is attempted.
func (s *Store) Ensure(ctx context.Context, alertID string, recipient events.AlertRecipient) error {
	query, args, err := s.db.Dialect().Insert("notification_messages").Prepared(true).Rows(goqu.Record{
		"alert_id": alertID, "messenger": "telegram", "bot_identity": recipient.BotIdentity,
		"chat_id": recipient.ChatID, "thread_id": recipient.ThreadID,
	}).OnConflict(goqu.DoNothing()).ToSQL()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}

// Get returns the persisted transport state for one physical destination.
func (s *Store) Get(ctx context.Context, alertID string, recipient events.AlertRecipient) (MessageLink, error) {
	var row MessageLink
	query, args, err := s.db.Dialect().From("notification_messages").Prepared(true).Select(
		"alert_id", "messenger", "bot_identity", "chat_id", "thread_id", "opened_message_id", "opened_at",
		"resolved_at", "resolved_action", "reply_message_id",
	).Where(goqu.Ex{"alert_id": alertID, "messenger": "telegram", "bot_identity": recipient.BotIdentity,
		"chat_id": recipient.ChatID, "thread_id": recipient.ThreadID}).ToSQL()
	if err != nil {
		return row, err
	}
	err = s.db.Get(ctx, &row, query, args...)
	return row, err
}

// HasAny reports whether any physical opening route was already persisted.
func (s *Store) HasAny(ctx context.Context, alertID string) (bool, error) {
	var count int
	query, args, err := s.db.Dialect().From("notification_messages").Prepared(true).Select(goqu.COUNT("*")).
		Where(goqu.Ex{"alert_id": alertID}).ToSQL()
	if err != nil {
		return false, err
	}
	if err = s.db.Get(ctx, &count, query, args...); err != nil {
		return false, err
	}
	return count > 0, nil
}

// MarkOpened records an accepted initial message before Outbox acknowledgement.
func (s *Store) MarkOpened(
	ctx context.Context, alertID string, recipient events.AlertRecipient, receipt notifiers.Receipt, at time.Time,
) error {
	if receipt.MessageID <= 0 {
		return fmt.Errorf("telegram returned no opening message ID")
	}
	query, args, err := s.db.Dialect().Update("notification_messages").Prepared(true).Set(goqu.Record{
		"opened_message_id": receipt.MessageID, "opened_at": at.UTC(),
	}).Where(goqu.Ex{"alert_id": alertID, "messenger": "telegram", "bot_identity": recipient.BotIdentity,
		"chat_id": recipient.ChatID, "thread_id": recipient.ThreadID,
		"opened_message_id": nil, "resolved_at": nil}).ToSQL()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}

// MarkResolved records the accepted operation and any sent reply identifier.
func (s *Store) MarkResolved(
	ctx context.Context, alertID string, recipient events.AlertRecipient, receipt notifiers.Receipt, at time.Time,
) error {
	action := receipt.Action
	if action == "" {
		action = notifiers.ActionSend
	}
	var reply sql.NullInt64
	if action == notifiers.ActionReply {
		if receipt.MessageID <= 0 {
			return fmt.Errorf("telegram returned no reply message ID")
		}
		reply = sql.NullInt64{Int64: receipt.MessageID, Valid: true}
	}
	query, args, err := s.db.Dialect().Update("notification_messages").Prepared(true).Set(goqu.Record{
		"resolved_at": at.UTC(), "resolved_action": string(action), "reply_message_id": reply,
	}).Where(goqu.Ex{"alert_id": alertID, "messenger": "telegram", "bot_identity": recipient.BotIdentity,
		"chat_id": recipient.ChatID, "thread_id": recipient.ThreadID, "resolved_at": nil}).ToSQL()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}

// KnownRecipients returns physical destinations retained for old incidents.
func (s *Store) KnownRecipients(ctx context.Context) ([]events.AlertRecipient, error) {
	var rows []struct {
		BotIdentity string `db:"bot_identity"`
		ChatID      string `db:"chat_id"`
		ThreadID    int64  `db:"thread_id"`
	}
	query, args, err := s.db.Dialect().From("notification_messages").Prepared(true).
		Select("bot_identity", "chat_id", "thread_id").Distinct().ToSQL()
	if err != nil {
		return nil, err
	}
	if err = s.db.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	result := make([]events.AlertRecipient, 0, len(rows))
	for _, row := range rows {
		result = append(result, events.AlertRecipient{
			BotIdentity: row.BotIdentity, ChatID: row.ChatID, ThreadID: row.ThreadID,
		})
	}
	return result, nil
}

// CompletedAlertIDs returns alert lifecycles with at least one completed message link.
func (s *Store) CompletedAlertIDs(ctx context.Context) ([]string, error) {
	alertIDs := []string{}
	query, args, err := s.db.Dialect().From("notification_messages").Prepared(true).
		Select("alert_id").Distinct().Where(goqu.I("resolved_at").IsNotNull()).ToSQL()
	if err != nil {
		return nil, err
	}
	if err = s.db.Select(ctx, &alertIDs, query, args...); err != nil {
		return nil, err
	}
	return alertIDs, nil
}

// DeleteCompleted removes completed message links for explicitly allowed alert lifecycles.
func (s *Store) DeleteCompleted(ctx context.Context, alertIDs []string) error {
	if len(alertIDs) == 0 {
		return nil
	}
	query, args, err := s.db.Dialect().Delete("notification_messages").Prepared(true).
		Where(goqu.I("alert_id").In(alertIDs), goqu.I("resolved_at").IsNotNull()).ToSQL()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}
