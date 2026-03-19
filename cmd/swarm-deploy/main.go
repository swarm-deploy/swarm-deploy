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
	"github.com/artarts36/swarm-deploy/internal/entrypoints/apiserver"
	"github.com/artarts36/swarm-deploy/internal/entrypoints/frontendserver"
	"github.com/artarts36/swarm-deploy/internal/entrypoints/healthserver"
	"github.com/artarts36/swarm-deploy/internal/entrypoints/webhookserver"
	"github.com/artarts36/swarm-deploy/internal/gitops"
	"github.com/artarts36/swarm-deploy/internal/metrics"
	"github.com/artarts36/swarm-deploy/internal/notify"
	"github.com/artarts36/swarm-deploy/internal/swarm"
	"github.com/prometheus/client_golang/prometheus"
)

const shutdownTimeout = 30 * time.Second

func main() { //nolint:funlen // not need
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	configPath := flag.String("config", "swarm-deploy.yaml", "Path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", slog.Any("err", err))
		os.Exit(1)
	}

	err = os.MkdirAll(cfg.Spec.DataDir, 0o755)
	if err != nil {
		slog.Error("failed to create data dir", slog.String("dir", cfg.Spec.DataDir), slog.Any("err", err))
		os.Exit(1)
	}

	gitSyncer, err := gitops.NewSyncer(cfg.Spec.Git, cfg.Spec.DataDir)
	if err != nil {
		slog.Error("failed to build git syncer", slog.Any("err", err))
		os.Exit(1)
	}

	metricRecorder, err := metrics.New(prometheus.DefaultRegisterer)
	if err != nil {
		slog.Error("failed to init metrics", slog.Any("err", err))
		os.Exit(1)
	}

	notifier, err := buildNotifiers(cfg)
	if err != nil {
		slog.Error("failed to build notifiers", slog.Any("err", err))
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
		slog.Error("failed to init deployer", slog.Any("err", err))
		os.Exit(1)
	}

	control := controller.New(
		cfg,
		gitSyncer,
		deployer,
		metricRecorder,
		notifier,
	)

	apiApplication := apiserver.NewApplication(cfg.Spec.Web.APIAddress, control)
	webhookApplication := webhookserver.NewApplication(cfg.Spec.Sync.Webhook.Address, cfg, control)
	frontendApplication, err := frontendserver.NewApplication(cfg.Spec.Web.FrontendAddress)
	if err != nil {
		slog.Error("failed to init frontend server", slog.Any("err", err))
		os.Exit(1)
	}

	healthServer := healthserver.NewApplication(cfg.Spec.HealthServer)

	entrypoints := []entrypoint.Entrypoint{
		apiApplication.Entrypoint(),
		frontendApplication.Entrypoint(),
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

	slog.Info("starting swarm deploy",
		slog.String("api.address", cfg.Spec.Web.APIAddress),
		slog.String("frontend.address", cfg.Spec.Web.FrontendAddress),
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
		slog.Error("failed to run", slog.Any("err", err))
		os.Exit(1)
	}
}

func buildNotifiers(cfg *config.Config) (*notify.Manager, error) {
	notifiers := make([]notify.Notifier, 0, len(cfg.Spec.Notifications.Telegram)+len(cfg.Spec.Notifications.Custom))

	for _, tg := range cfg.Spec.Notifications.Telegram {
		token, err := tg.ResolveToken()
		if err != nil {
			return nil, fmt.Errorf("resolve telegram token for %q: %w", tg.Name, err)
		}

		tgNotifier, err := notify.NewTelegramNotifier(
			tg.Name,
			token,
			tg.ChatID,
			notify.TelegramOptions{
				ChatThreadID: tg.ResolveChatThreadID(),
				Message:      tg.Message,
			},
		)
		if err != nil {
			return nil, fmt.Errorf("build telegram notifier %q: %w", tg.Name, err)
		}
		notifiers = append(notifiers, tgNotifier)
	}

	for _, custom := range cfg.Spec.Notifications.Custom {
		notifiers = append(
			notifiers,
			notify.NewCustomWebhookNotifier(custom.Name, custom.ResolveURL(), custom.Method, custom.Header),
		)
	}

	return notify.NewManager(notifiers...), nil
}
