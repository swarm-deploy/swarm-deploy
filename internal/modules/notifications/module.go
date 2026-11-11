package notifications

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/alerts"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/eventnotify"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/notifiers"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// InitModule registers notification subscribers before the Event worker starts.
func InitModule(
	ctx context.Context, spec config.NotificationSpec, bus dispatcher.Dispatcher, db storage.Database,
) error {
	slog.InfoContext(ctx, "[notifications] init notification subscribers")
	if err := registerEventSubscribers(ctx, bus, spec); err != nil {
		return fmt.Errorf("init notification subscribers: %w", err)
	}
	if err := alerts.Register(ctx, bus, db, spec); err != nil {
		return fmt.Errorf("init alert notifications: %w", err)
	}
	return nil
}

func registerEventSubscribers(ctx context.Context, bus dispatcher.Dispatcher, spec config.NotificationSpec) error {
	subscribersCount := 0
	for eventType, routes := range spec.Events {
		if !eventType.Valid() {
			return fmt.Errorf("unknown notifications.events event type %q", eventType)
		}

		defaultTemplate, ok := eventnotify.StandardTemplate(eventType)
		if !ok {
			return fmt.Errorf("notification template is not defined for event type %q", eventType)
		}
		count, err := registerTelegramNotifications(
			bus, eventType, spec.Messengers.Telegram, routes.Telegram, defaultTemplate,
		)
		if err != nil {
			return err
		}
		subscribersCount += count
		if err = registerCustomNotifications(bus, eventType, routes.Custom); err != nil {
			return err
		}
		subscribersCount += len(routes.Custom)
	}

	if len(spec.Events) == 0 {
		slog.InfoContext(ctx, "[notifications] notification subscribers not found")
	} else {
		slog.InfoContext(ctx, "[notifications] notification subscribers registered",
			slog.Int("subscribers", subscribersCount))
	}

	return nil
}

func registerTelegramNotifications(
	bus dispatcher.Dispatcher,
	eventType events.TypeName,
	telegram config.NotificationTelegramSpec,
	channels []config.TelegramChannel,
	defaultTemplate string,
) (int, error) {
	count := 0
	botID, _, _ := strings.Cut(string(telegram.BotToken.Content), ":")
	botIdentity := sha256.Sum256([]byte(botID))
	for ruleIndex, channel := range channels {
		for _, chatName := range channel.ChatNames() {
			chat := telegram.Chats[chatName]
			notifier, err := notifiers.NewTelegramNotifier(
				chatName, string(telegram.BotToken.Content), chat.ID,
				notifiers.TelegramOptions{
					ChatThreadID: chat.ThreadID, Message: channel.Message, DefaultMessage: defaultTemplate,
					Retries: telegram.Retries, SOCKS5Address: telegram.Proxy.SOCKS5.Address.Value,
				},
			)
			if err != nil {
				return 0, fmt.Errorf("build telegram notifier %q: %w", chatName, err)
			}
			// Each rule has its own delivery, keyed by its physical destination across alias changes.
			id := fmt.Sprintf("notification:telegram:%x:%s:%d:%d", botIdentity, chat.ID, chat.ThreadID, ruleIndex)
			if err = bus.Subscribe(eventType, id, dispatcher.External(eventnotify.NewSubscriber(notifier))); err != nil {
				return 0, err
			}
			count++
		}
	}
	return count, nil
}

func registerCustomNotifications(
	bus dispatcher.Dispatcher, eventType events.TypeName, channels []config.CustomChannel,
) error {
	for _, channel := range channels {
		notifier := notifiers.NewCustomWebhookNotifier(
			channel.Name, channel.URL.Value.String(), channel.Method, channel.Header,
		)
		identity := sha256.Sum256([]byte(channel.Method + " " + channel.URL.Value.String()))
		id := fmt.Sprintf("notification:custom:%s:%x", channel.Name, identity)
		if err := bus.Subscribe(eventType, id, dispatcher.External(eventnotify.NewSubscriber(notifier))); err != nil {
			return err
		}
	}
	return nil
}
