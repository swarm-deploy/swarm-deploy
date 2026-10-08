// Package alerts adapts alert lifecycle changes to notifications.
package alerts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/delivery"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/notifiers"
)

const correlationKeyPrefix = "alert:"

// DefaultMessageTemplate is the Telegram template used when a channel has no custom one.
const DefaultMessageTemplate = `{{if eq .status "resolved"}}✅ Alert resolved{{else}}🔻 Alert opened{{end}}: {{.alert.Title}}
{{.alert.ResourceType}}: {{.alert.ResourceID}}
{{.alert.Message}}{{if .resolution}}
{{.resolution}}{{end}}`

// Channel is a notifier with a stable identifier of its real destination.
type Channel struct {
	// ID uniquely identifies the destination (chat, thread, bot) and is used in delivery correlation.
	ID string
	// Notifier delivers messages to the destination.
	Notifier notifiers.Notifier
}

// Notifier converts alert lifecycle changes to notifications. It implements alertmanagement.Observer.
type Notifier struct {
	mode     config.AlertNotificationMode
	channels []Channel
	store    delivery.Store
	ttl      time.Duration
	now      func() time.Time
}

// NewNotifier creates an alert notifier; a non-positive ttl selects delivery.DefaultTTL.
func NewNotifier(
	mode config.AlertNotificationMode,
	channels []Channel,
	store delivery.Store,
	ttl time.Duration,
) *Notifier {
	if ttl <= 0 {
		ttl = delivery.DefaultTTL
	}
	return &Notifier{mode: mode, channels: channels, store: store, ttl: ttl, now: time.Now}
}

// AlertOpened sends a message about the opened alert and remembers it for edit and reply modes.
func (n *Notifier) AlertOpened(ctx context.Context, alert model.Alert) error {
	return n.forEachChannel(func(ch Channel) error {
		if n.mode == config.AlertNotificationModeSend {
			_, err := ch.Notifier.Notify(ctx, message(alert, "open"))
			return err
		}

		key := correlationKeyPrefix + alert.ID
		_, found, err := n.store.Get(ctx, key, ch.ID)
		if err != nil {
			return fmt.Errorf("get correlation: %w", err)
		}
		if found {
			// Repeated processing: the opening message was already sent.
			return nil
		}

		receipt, err := ch.Notifier.Notify(ctx, message(alert, "open"))
		if err != nil {
			return err
		}
		if receipt.MessageID == "" {
			return nil
		}

		now := n.now()
		err = n.store.Put(ctx, delivery.Correlation{
			CorrelationKey: key, ChannelID: ch.ID, Receipt: receipt, CreatedAt: now, ExpiresAt: now.Add(n.ttl),
		})
		if err != nil {
			// The message is delivered; losing the correlation only turns the closing into a plain message.
			slog.ErrorContext(ctx, "[alerts-notifier] save correlation",
				slog.String("alert.id", alert.ID), slog.String("channel", ch.ID), slog.Any("err", err))
		}
		return nil
	})
}

// AlertResolved notifies about the resolved alert according to the configured mode.
func (n *Notifier) AlertResolved(ctx context.Context, alert model.Alert) error {
	return n.forEachChannel(func(ch Channel) error {
		msg := message(alert, "resolved")
		if n.mode == config.AlertNotificationModeSend {
			_, err := ch.Notifier.Notify(ctx, msg)
			return err
		}

		key := correlationKeyPrefix + alert.ID
		correlation, found, err := n.store.Get(ctx, key, ch.ID)
		if err != nil {
			return fmt.Errorf("get correlation: %w", err)
		}

		if found {
			if n.mode == config.AlertNotificationModeEdit {
				msg.EditMessageID = correlation.Receipt.MessageID
			} else {
				msg.ReplyToID = correlation.Receipt.MessageID
			}
		}

		_, err = ch.Notifier.Notify(ctx, msg)
		if errors.Is(err, notifiers.ErrMessageUnavailable) && msg.EditMessageID != "" {
			msg.EditMessageID = ""
			_, err = ch.Notifier.Notify(ctx, msg)
		}
		if err != nil {
			// The correlation is kept so that a retry can still reach the original message.
			return err
		}

		if found {
			if delErr := n.store.Delete(ctx, key, ch.ID); delErr != nil {
				slog.ErrorContext(ctx, "[alerts-notifier] delete correlation",
					slog.String("alert.id", alert.ID), slog.String("channel", ch.ID), slog.Any("err", delErr))
			}
		}
		return nil
	})
}

// forEachChannel runs fn for every channel and joins errors so one broken channel does not block others.
func (n *Notifier) forEachChannel(fn func(Channel) error) error {
	var errs []error
	for _, ch := range n.channels {
		if err := fn(ch); err != nil {
			errs = append(errs, fmt.Errorf("channel %s: %w", ch.Notifier.Name(), err))
		}
	}
	return errors.Join(errs...)
}

func message(alert model.Alert, status string) notifiers.Message {
	resolution := ""
	if alert.Resolution != nil {
		resolution = alert.Resolution.Message
	}
	return notifiers.Message{Payload: notifiers.Fields{"alert": alert, "resolution": resolution, "status": status}}
}
