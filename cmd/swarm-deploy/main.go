package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	entrypoint "github.com/artarts36/go-entrypoint"
	"github.com/artarts36/swarm-deploy/internal/config"
	"github.com/artarts36/swarm-deploy/internal/controller"
	"github.com/artarts36/swarm-deploy/internal/entrypoints/healthserver"
	"github.com/artarts36/swarm-deploy/internal/entrypoints/webhookserver"
	"github.com/artarts36/swarm-deploy/internal/entrypoints/webserver"
	"github.com/artarts36/swarm-deploy/internal/event/dispatcher"
	"github.com/artarts36/swarm-deploy/internal/event/events"
	"github.com/artarts36/swarm-deploy/internal/event/notifiers"
	notify2 "github.com/artarts36/swarm-deploy/internal/event/notify"
	gitx "github.com/artarts36/swarm-deploy/internal/git"
	"github.com/artarts36/swarm-deploy/internal/gitops"
	"github.com/artarts36/swarm-deploy/internal/metrics"
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
	logger := slog.New(slogx.NewChain(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}),
		slogm.RequestID(),
	))
	slog.SetDefault(logger)

	configPath := flag.String("config", "swarm-deploy.yaml", "Path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.ErrorContext(ctx, "failed to load config", slog.Any("err", err))
		os.Exit(1)
	}

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

	eventDispatcher, err := buildEventDispatcher(cfg)
	if err != nil {
		slog.ErrorContext(ctx, "failed to build event dispatcher", slog.Any("err", err))
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
	)
	err = runner.Run()
	if err != nil {
		slog.ErrorContext(ctx, "failed to run", slog.Any("err", err))
		os.Exit(1)
	}
}

func buildEventDispatcher(cfg *config.Config) (dispatcher.Dispatcher, error) {
	subs := map[events.Type][]dispatcher.Subscriber{}

	for eventType, channels := range cfg.Spec.Notifications.On {
		for _, tg := range channels.Telegram {
			token, err := tg.ResolveToken()
			if err != nil {
				return nil, fmt.Errorf("resolve telegram token for %q: %w", tg.Name, err)
			}

			tgNotifier, err := notifiers.NewTelegramNotifier(
				tg.Name,
				token,
				tg.ChatID,
				notifiers.TelegramOptions{
					ChatThreadID: tg.ResolveChatThreadID(),
					Message:      tg.Message,
				},
			)
			if err != nil {
				return nil, fmt.Errorf("build telegram notifier %q: %w", tg.Name, err)
			}

			subs[eventType] = append(subs[eventType], notify2.NewSubscriber(tgNotifier))
		}

		for _, custom := range channels.Custom {
			notifier := notifiers.NewCustomWebhookNotifier(custom.Name, custom.ResolveURL(), custom.Method, custom.Header)

			subs[eventType] = append(subs[eventType], notify2.NewSubscriber(notifier))
		}
	}

	if len(subs) == 0 {
		slog.Info("event subscribers not found")

		return &dispatcher.NopDispatcher{}, nil
	}

	slog.Info("found event subscribers", slog.Int("subscribers", len(subs)))

	return dispatcher.NewQueueDispatcher(subs), nil
}
