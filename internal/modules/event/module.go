package event

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	eventmetrics "github.com/swarm-deploy/swarm-deploy/internal/modules/event/metrics"
	notify2 "github.com/swarm-deploy/swarm-deploy/internal/modules/event/notify"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	alertnotifications "github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/alerts"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/notifiers"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

type Module struct {
	Dispatcher dispatcher.Dispatcher
	History    *history.SQLStore

	// Bus is the only durable delivery worker.
	Bus *dispatcher.OutboxDispatcher
	cfg *config.Config
	db  *storage.Database
}

type Container interface {
	// GetStorage returns the shared database.
	GetStorage() *storage.Database
	GetFileSystem() fs.FileSystem
	GetMetrics() *metrics.Group
}

func InitModule(ctx context.Context, cfg *config.Config, cnt Container) (*Module, error) {
	srv := &Module{
		cfg: cfg,
		db:  cnt.GetStorage(),
	}

	historyStore, err := history.NewSQLStore(cnt.GetStorage(), cfg.Spec.EventHistory.Capacity)
	if err != nil {
		return nil, fmt.Errorf("init history store: %w", err)
	}

	srv.History = historyStore
	srv.Bus = dispatcher.NewOutboxDispatcher(outbox.New(cnt.GetStorage()))
	srv.Dispatcher = srv.Bus

	if cfg.Spec.Web.Security.Authentication.Strategy() != config.AuthenticationStrategyNone {
		srv.Dispatcher = dispatcher.NewEnrichableDispatcher(
			dispatcher.WrapEnrichers(security.EnrichEvent()),
			srv.Dispatcher,
		)
	}

	if err = dispatcher.SubscribeMany(srv.Dispatcher, projectionEventTypes(), "event-history", historyStore); err != nil {
		return nil, err
	}
	if err = dispatcher.SubscribeMany(srv.Dispatcher, projectionEventTypes(),
		"event-metrics", eventmetrics.NewSubscriber(cnt.GetMetrics().Events)); err != nil {
		return nil, err
	}

	slog.InfoContext(ctx, "[event-bus] init notification subscribers")
	if err = srv.initNotificationSubscribers(ctx); err != nil {
		return nil, fmt.Errorf("init notification subscribers: %w", err)
	}
	if err = alertnotifications.Register(ctx, srv.Dispatcher, srv.db, cfg.Spec.Notifications); err != nil {
		return nil, fmt.Errorf("init alert notifications: %w", err)
	}

	return srv, nil
}

// Run processes durable deliveries until cancellation.
func (s *Module) Run(ctx context.Context) error { return s.Bus.Run(ctx) }

func (s *Module) initNotificationSubscribers(ctx context.Context) error {
	subscribersCount := 0

	for eventTypeName, channels := range s.cfg.Spec.Notifications.Events {
		if !eventTypeName.Valid() {
			return fmt.Errorf("unknown notifications.events event type %q", eventTypeName)
		}

		defaultTemplate, ok := notify2.StandardTemplate(eventTypeName)
		if !ok {
			return fmt.Errorf("notification template is not defined for event type %q", eventTypeName)
		}
		count, err := s.registerTelegramNotifications([]events.TypeName{eventTypeName}, channels.Telegram, defaultTemplate)
		if err != nil {
			return err
		}
		subscribersCount += count
		count, err = s.registerCustomNotifications([]events.TypeName{eventTypeName}, channels.Custom)
		if err != nil {
			return err
		}
		subscribersCount += count
	}

	if len(s.cfg.Spec.Notifications.Events) == 0 {
		slog.InfoContext(ctx, "[event-bus] notification subscribers not found")
	} else {
		slog.InfoContext(ctx,
			"[event-bus] notification subscribers registered",
			slog.Int("subscribers", subscribersCount),
		)
	}

	return nil
}

func (s *Module) registerTelegramNotifications(
	types []events.TypeName,
	channels []config.TelegramChannel,
	defaultTemplate string,
) (int, error) {
	count := 0
	telegram := s.cfg.Spec.Notifications.Messengers.Telegram
	botID, _, _ := strings.Cut(string(telegram.BotToken.Content), ":")
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
			// Route identity must remain unique for independently delivered rules.
			// The destination identity is stable across chat alias changes.
			id := fmt.Sprintf("notification:telegram:%x:%s:%d:%d",
				sha256.Sum256([]byte(botID)), chat.ID, chat.ThreadID, ruleIndex)
			for _, typ := range types {
				if err = s.Dispatcher.Subscribe(typ, id, dispatcher.External(notify2.NewSubscriber(notifier))); err != nil {
					return 0, err
				}
				count++
			}
		}
	}
	return count, nil
}

func (s *Module) registerCustomNotifications(types []events.TypeName, channels []config.CustomChannel) (int, error) {
	count := 0
	for _, channel := range channels {
		notifier := notifiers.NewCustomWebhookNotifier(
			channel.Name, channel.URL.Value.String(), channel.Method, channel.Header,
		)
		identity := sha256.Sum256([]byte(channel.Method + " " + channel.URL.Value.String()))
		id := fmt.Sprintf("notification:custom:%s:%x", channel.Name, identity)
		for _, typ := range types {
			if err := s.Dispatcher.Subscribe(typ, id, dispatcher.External(notify2.NewSubscriber(notifier))); err != nil {
				return 0, err
			}
			count++
		}
	}
	return count, nil
}

// Keep the original projection subscription set independent of IsPublic.
// History.Handle retains its own persistence filters.
func projectionEventTypes() []events.TypeName {
	return []events.TypeName{
		events.TypeNameServiceCatalogUpdated, events.TypeNameDeploySuccess, events.TypeNameSendNotificationFailed,
		events.TypeNameSyncManualStarted, events.TypeNameWebhookReceived, events.TypeNameNodeJoined,
		events.TypeNameNodeConnected, events.TypeNameNodeDisconnected, events.TypeNameServiceMissed,
		events.TypeNameServiceDrifted, events.TypeNameServiceDriftFixed,
		events.TypeNameServiceReplicasIncreased, events.TypeNameServiceReplicasDecreased,
		events.TypeNameServiceRestarted, events.TypeNameServicePruned, events.TypeNameNetworkCreated,
		events.TypeNameUserAuthenticated, events.TypeNameAssistantPromptInjectionDetected,
	}
}
