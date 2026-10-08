// Package notifications wires notification subscribers that are independent from the event dispatcher.
package notifications

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/alerts"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/delivery"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/notifiers"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

// Container supplies dependencies required by Notifications.
type Container interface {
	// GetFileSystem returns the application filesystem abstraction.
	GetFileSystem() fs.FileSystem
	// GetAlertManagementModule returns the alert management module.
	GetAlertManagementModule() *alertmanagement.Module
}

// InitModule subscribes alert notifications to alert lifecycle changes.
// Event notifications are still registered by the event module.
func InitModule(ctx context.Context, cfg *config.Config, container Container) error {
	spec := cfg.Spec.Notifications
	if len(spec.Alerts.Telegram) == 0 {
		return nil
	}

	channels := make([]alerts.Channel, 0, len(spec.Alerts.Telegram))
	for _, tg := range spec.Alerts.Telegram {
		message := tg.Message
		if message == "" {
			message = alerts.DefaultMessageTemplate
		}
		notifier, err := notifiers.NewTelegramNotifier(
			tg.Name,
			string(tg.BotToken.Content),
			tg.ChatID,
			notifiers.TelegramOptions{
				ChatThreadID:  tg.ChatThreadID,
				Message:       message,
				Retries:       spec.Messengers.Telegram.Retries,
				SOCKS5Address: spec.Messengers.Telegram.Proxy.SOCKS5.Address.Value,
			},
		)
		if err != nil {
			return fmt.Errorf("build alert telegram channel %q: %w", tg.Name, err)
		}
		channels = append(channels, alerts.Channel{ID: telegramChannelID(tg), Notifier: notifier})
	}

	store, err := delivery.NewFileStore(
		ctx,
		filepath.Join(cfg.Spec.DataDir, "notification-deliveries.state.json"),
		container.GetFileSystem(),
	)
	if err != nil {
		return fmt.Errorf("init delivery store: %w", err)
	}

	container.GetAlertManagementModule().Subscriber.Observe(
		alerts.NewNotifier(spec.Alerts.Mode, channels, store, spec.Alerts.CorrelationTTL),
	)

	return nil
}

// telegramChannelID identifies a Telegram destination by bot, chat and thread; the token is only hashed.
func telegramChannelID(tg config.TelegramChannel) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", tg.BotToken.Content, tg.ChatID, tg.ChatThreadID)))
	return "telegram:" + hex.EncodeToString(sum[:8])
}
