package event

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	eventmetrics "github.com/swarm-deploy/swarm-deploy/internal/modules/event/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/notifiers"
	notify2 "github.com/swarm-deploy/swarm-deploy/internal/modules/event/notify"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

type Module struct {
	Dispatcher dispatcher.Dispatcher
	History    *history.Store

	cfg             *config.Config
	queueDispatcher *dispatcher.QueueDispatcher
}

type Container interface {
	GetFileSystem() fs.FileSystem
	GetMetrics() *metrics.Group
}

func InitModule(cfg *config.Config, cnt Container) (*Module, error) {
	srv := &Module{
		cfg: cfg,
	}

	historyStore, err := history.NewStore(
		filepath.Join(cfg.Spec.DataDir, "event-history.json"),
		cfg.Spec.EventHistory.Capacity,
		cnt.GetFileSystem(),
	)
	if err != nil {
		return nil, fmt.Errorf("init history store: %w", err)
	}

	srv.History = historyStore
	queueDispatcher := dispatcher.NewQueueDispatcher()
	srv.Dispatcher = queueDispatcher
	srv.queueDispatcher = queueDispatcher

	if cfg.Spec.Web.Security.Authentication.Strategy() != config.AuthenticationStrategyNone {
		srv.Dispatcher = dispatcher.NewEnrichableDispatcher(
			dispatcher.WrapEnrichers(security.EnrichEvent()),
			srv.Dispatcher,
		)
	}

	srv.subscribeOnAllEvents(historyStore)
	srv.subscribeOnAllEvents(eventmetrics.NewSubscriber(cnt.GetMetrics().Events))

	slog.Info("[event-dispatcher] init notification subscribers")
	if err = srv.initNotificationSubscribers(); err != nil {
		return nil, fmt.Errorf("init notification subscribers: %w", err)
	}

	return srv, nil
}

// Shutdown stops the event dispatcher after all queued events have been handled.
func (s *Module) Shutdown(ctx context.Context) error {
	return s.queueDispatcher.Shutdown(ctx)
}

func (s *Module) initNotificationSubscribers() error {
	subscribersCount := 0

	for eventTypeName, channels := range s.cfg.Spec.Notifications.On {
		eventType, ok := events.ParseType(string(eventTypeName))
		if !ok {
			return fmt.Errorf("unknown notifications.on event type %q", eventTypeName)
		}

		for _, tg := range channels.Telegram {
			tgNotifier, notifierErr := notifiers.NewTelegramNotifier(
				tg.Name,
				string(tg.BotToken.Content),
				tg.ChatID,
				notifiers.TelegramOptions{
					ChatThreadID:  tg.ChatThreadID,
					Message:       tg.Message,
					Retries:       s.cfg.Spec.Notifications.Messengers.Telegram.Retries,
					SOCKS5Address: s.cfg.Spec.Notifications.Messengers.Telegram.Proxy.SOCKS5.Address.Value,
				},
			)
			if notifierErr != nil {
				return fmt.Errorf("build telegram notifier %q: %w", tg.Name, notifierErr)
			}

			s.Dispatcher.Subscribe(eventType, notify2.NewSubscriber(tgNotifier, s.Dispatcher))
			subscribersCount++
		}

		for _, custom := range channels.Custom {
			notifier := notifiers.NewCustomWebhookNotifier(custom.Name, custom.URL.Value.String(), custom.Method, custom.Header)

			s.Dispatcher.Subscribe(eventType, notify2.NewSubscriber(notifier, s.Dispatcher))
			subscribersCount++
		}
	}

	if len(s.cfg.Spec.Notifications.On) == 0 {
		slog.Info("[event-dispatcher] notification subscribers not found")
	} else {
		slog.Info(
			"[event-dispatcher] notification subscribers registered",
			slog.Int("subscribers", subscribersCount),
		)
	}

	return nil
}

func (s *Module) subscribeOnAllEvents(subscriber dispatcher.Subscriber) {
	for _, typ := range events.Types {
		s.Dispatcher.Subscribe(typ, subscriber)
	}
}
