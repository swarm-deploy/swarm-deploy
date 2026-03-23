package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	entrypoint "github.com/artarts36/go-entrypoint"
	"github.com/artarts36/swarm-deploy/internal/config"
	"github.com/artarts36/swarm-deploy/internal/controller"
	"github.com/artarts36/swarm-deploy/internal/entrypoints/healthserver"
	"github.com/artarts36/swarm-deploy/internal/entrypoints/webhookserver"
	"github.com/artarts36/swarm-deploy/internal/entrypoints/webserver"
	"github.com/artarts36/swarm-deploy/internal/event/dispatcher"
	"github.com/artarts36/swarm-deploy/internal/event/events"
	"github.com/artarts36/swarm-deploy/internal/event/history"
	"github.com/artarts36/swarm-deploy/internal/event/notifiers"
	notify2 "github.com/artarts36/swarm-deploy/internal/event/notify"
	gitx "github.com/artarts36/swarm-deploy/internal/git"
	"github.com/artarts36/swarm-deploy/internal/gitops"
	"github.com/artarts36/swarm-deploy/internal/metrics"
	"github.com/artarts36/swarm-deploy/internal/service"
	"github.com/artarts36/swarm-deploy/internal/swarm"
	"github.com/cappuccinotm/slogx"
	"github.com/cappuccinotm/slogx/slogm"
	"github.com/prometheus/client_golang/prometheus"
)

const shutdownTimeout = 30 * time.Second

//nolint:funlen//not need
func main() {
	ctx := context.Background()

	slogx.RequestIDKey = "x-request-id"

	configPath := flag.String("config", "swarm-deploy.yaml", "Path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.ErrorContext(ctx, "failed to load config", slog.Any("err", err))
		os.Exit(1)
	}

	slog.SetDefault(slog.New(slogx.NewChain(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.Spec.Log.Level.Level()}),
		slogm.RequestID(),
	)))

	err = os.MkdirAll(cfg.Spec.DataDir, 0o755)
	if err != nil {
		slog.ErrorContext(
			ctx,
			"failed to create data dir",
			slog.String("dir", cfg.Spec.DataDir),
			slog.Any("err", err),
		)
		os.Exit(1)
	}

	gitSyncer, err := gitops.NewSyncer(gitx.NewAuthResolver(), cfg.Spec.Git, cfg.Spec.DataDir)
	if err != nil {
		slog.ErrorContext(ctx, "failed to build git syncer", slog.Any("err", err))
		os.Exit(1)
	}

	metricRecorder, err := metrics.New(prometheus.DefaultRegisterer)
	if err != nil {
		slog.ErrorContext(ctx, "failed to init metrics", slog.Any("err", err))
		os.Exit(1)
	}

	deployer, err := swarm.NewDeployer(
		cfg.Spec.Swarm.Command,
		cfg.Spec.Swarm.StackDeployArgs,
		cfg.Spec.Swarm.InitJobPollEvery.Value,
		cfg.Spec.Swarm.InitJobMaxDuration.Value,
		swarm.ExecRunner{},
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to init deployer", slog.Any("err", err))
		os.Exit(1)
	}

	inspector, err := swarm.NewInspector()
	if err != nil {
		slog.ErrorContext(ctx, "failed to init service inspector", slog.Any("err", err))
		os.Exit(1)
	}

	eventDispatcher, eventHistory, serviceStore, err := buildEventDispatcher(cfg, inspector)
	if err != nil {
		slog.ErrorContext(ctx, "failed to build event dispatcher", slog.Any("err", err))
		os.Exit(1)
	}

	control := controller.New(
		cfg,
		gitSyncer,
		deployer,
		metricRecorder,
		eventDispatcher,
	)

	webApplication, err := webserver.NewApplication(
		cfg.Spec.Web.Address,
		control,
		inspector,
		eventHistory,
		serviceStore,
		eventDispatcher,
		cfg.Spec.Web.Security.Authentication,
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to init web server", slog.Any("err", err))
		os.Exit(1)
	}
	webhookApplication := webhookserver.NewApplication(cfg.Spec.Sync.Webhook.Address, cfg, control)

	healthServer := healthserver.NewApplication(cfg.Spec.HealthServer)

	entrypoints := []entrypoint.Entrypoint{
		webApplication.Entrypoint(),
		healthServer.Entrypoint(),
		{
			Name: "sync-controller",
			Run: func(ctx context.Context) error {
				return control.Run(ctx)
			},
		},
	}

	if webhookApplication.Enabled() {
		entrypoints = append(entrypoints, webhookApplication.Entrypoint())
	}

	runner := entrypoint.NewRunner(
		entrypoints,
		entrypoint.WithShutdownTimeout(shutdownTimeout),
	)

	slog.InfoContext(ctx, "starting swarm deploy",
		slog.String("web.address", cfg.Spec.Web.Address),
		slog.String("webhook.address", cfg.Spec.Sync.Webhook.Address),
		slog.Bool("webhook.enabled", webhookApplication.Enabled()),
		slog.String("healthServer.address", cfg.Spec.HealthServer.Address),
		slog.String("healthz.path", cfg.Spec.HealthServer.Healthz.Path),
		slog.String("metrics.path", cfg.Spec.HealthServer.Metrics.Path),
		slog.String("mode", cfg.Spec.Sync.Mode),
		slog.String("repo", cfg.Spec.Git.Repository),
		slog.String("log.level", cfg.Spec.Log.Level.String()),
	)
	err = runner.Run()
	if err != nil {
		slog.ErrorContext(ctx, "failed to run", slog.Any("err", err))
		os.Exit(1)
	}
}

func buildEventDispatcher(
	cfg *config.Config,
	inspector *swarm.Inspector,
) (dispatcher.Dispatcher, *history.Store, *service.Store, error) {
	subs := map[events.Type][]dispatcher.Subscriber{}

	historyStore, err := history.NewStore(
		filepath.Join(cfg.Spec.DataDir, "event-history.json"),
		cfg.Spec.EventHistory.Capacity,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("build history store: %w", err)
	}
	addEventHistorySubscriber(subs, historyStore)

	serviceStore, err := service.NewStore(filepath.Join(cfg.Spec.DataDir, "services.json"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("build service store: %w", err)
	}
	subs[events.TypeDeploySuccess] = append(
		subs[events.TypeDeploySuccess],
		service.NewSubscriber(serviceStore, inspector, service.NewMetadataExtractor()),
	)

	dispatcherLink := &dispatcherProxy{}

	for eventType, channels := range cfg.Spec.Notifications.On {
		for _, tg := range channels.Telegram {
			token, resolveErr := tg.ResolveToken()
			if resolveErr != nil {
				return nil, nil, nil, fmt.Errorf("resolve telegram token for %q: %w", tg.Name, resolveErr)
			}

			tgNotifier, notifierErr := notifiers.NewTelegramNotifier(
				tg.Name,
				token,
				tg.ChatID,
				notifiers.TelegramOptions{
					ChatThreadID: tg.ResolveChatThreadID(),
					Message:      tg.Message,
				},
			)
			if notifierErr != nil {
				return nil, nil, nil, fmt.Errorf("build telegram notifier %q: %w", tg.Name, notifierErr)
			}

			sub := notify2.NewSubscriber(tgNotifier, dispatcherLink)
			subs[eventType] = append(subs[eventType], sub)
		}

		for _, custom := range channels.Custom {
			notifier := notifiers.NewCustomWebhookNotifier(custom.Name, custom.ResolveURL(), custom.Method, custom.Header)

			sub := notify2.NewSubscriber(notifier, dispatcherLink)
			subs[eventType] = append(subs[eventType], sub)
		}
	}

	if len(cfg.Spec.Notifications.On) == 0 {
		slog.Info("notification subscribers not found")
	}

	slog.Info("found event subscribers", slog.Int("subscribers", len(subs)))

	eventDispatcher := dispatcher.NewQueueDispatcher(subs)
	dispatcherLink.Dispatcher = eventDispatcher

	return eventDispatcher, historyStore, serviceStore, nil
}

func addEventHistorySubscriber(subs map[events.Type][]dispatcher.Subscriber, store *history.Store) {
	subs[events.TypeDeploySuccess] = append(subs[events.TypeDeploySuccess], store)
	subs[events.TypeDeployFailed] = append(subs[events.TypeDeployFailed], store)
	subs[events.TypeSendNotificationFailed] = append(subs[events.TypeSendNotificationFailed], store)
	subs[events.TypeSyncManualStarted] = append(subs[events.TypeSyncManualStarted], store)
	subs[events.TypeUserAuthenticated] = append(subs[events.TypeUserAuthenticated], store)
}

type dispatcherProxy struct {
	dispatcher.Dispatcher
}
