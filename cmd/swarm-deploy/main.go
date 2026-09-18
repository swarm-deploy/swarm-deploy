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
	"github.com/cappuccinotm/slogx"
	"github.com/cappuccinotm/slogx/slogm"
	"github.com/docker/docker/client"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/swarm-deploy/swarm-deploy/internal/assistant"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/healthserver"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver"
	mcpTools "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/tools"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/sd"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webhookserver"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver"
	"github.com/swarm-deploy/swarm-deploy/internal/event"
	"github.com/swarm-deploy/swarm-deploy/internal/event/logx"
	"github.com/swarm-deploy/swarm-deploy/internal/githosting"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/controller"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/differ"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/resources"
	swarmnode "github.com/swarm-deploy/swarm-deploy/internal/resources/node"
	"github.com/swarm-deploy/swarm-deploy/internal/resources/service"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const shutdownTimeout = 30 * time.Second

var (
	Version   = "0.1.0"
	BuildDate = "2026-05-26 23:51:00"
)

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
		logx.EventType(),
		security.LogUser(),
	)))

	tracerProvider, err := sd.InitTracerProvider(ctx, cfg.Spec.Tracing)
	if err != nil {
		slog.ErrorContext(ctx, "failed to init tracing", slog.Any("err", err))
		os.Exit(1)
	}

	stopTracing := func() {
		if tracerProvider != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()

			if shutdownErr := tracerProvider.Shutdown(shutdownCtx); shutdownErr != nil {
				slog.ErrorContext(shutdownCtx, "failed to shutdown tracing", slog.Any("err", shutdownErr))
			}
		}
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

	gitRepository := gitx.NewRepository(cfg.Spec.Git, filepath.Join(cfg.Spec.DataDir, "repo"))

	metricsGroup := metrics.NewGroup(metrics.CreateGroupParams{
		Namespace: "swarm_deploy",
		Assistant: cfg.Spec.Assistant.Enabled,
		MCP:       cfg.Spec.Assistant.Enabled,
	})
	if err = prometheus.Register(metricsGroup); err != nil {
		slog.ErrorContext(ctx, "failed to init metrics", slog.Any("err", err))
		os.Exit(1)
	}

	metricsGroup.BuildInfo.Set(Version, BuildDate)

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		slog.ErrorContext(ctx, "failed to init docker client", slog.Any("err", err))
		os.Exit(1)
	}

	swarmSvc := swarm.NewSwarm(dockerClient, cfg.Spec.Swarm.Command)

	deployerSvc := deployer.NewDeployer(
		cfg.Spec.Swarm.StackDeployArgs,
		cfg.Spec.Swarm.InitJobPollEvery.Value,
		cfg.Spec.Swarm.InitJobMaxDuration.Value,
		swarmSvc.BinaryRunner,
		dockerClient,
		swarmSvc,
		metricsGroup.Deploys,
	)

	filesystem := fs.TraceOS()

	eventService, err := event.InitService(cfg, metricsGroup.Events, filesystem)
	if err != nil {
		slog.ErrorContext(ctx, "failed to init event service", slog.Any("err", err))
		os.Exit(1)
	}

	resourcesService, err := resources.InitService(ctx, cfg, swarmSvc, eventService.Dispatcher)
	if err != nil {
		slog.ErrorContext(ctx, "failed to init resources service", slog.Any("err", err))
		os.Exit(1)
	}

	resourcesService.RegisterEventSubscribers(eventService.Dispatcher)

	recommendationsService, err := recommendations.InitService(ctx,
		cfg,
		filesystem,
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to init recommendations service", slog.Any("err", err))
		os.Exit(1)
	}

	gitopsService, err := gitops.InitService(ctx, cfg, filesystem)
	if err != nil {
		slog.ErrorContext(ctx, "failed to init gitops service", slog.Any("err", err))
		os.Exit(1)
	}

	control := controller.New(
		cfg,
		gitRepository,
		swarmSvc,
		deployerSvc,
		metricsGroup,
		eventService.Dispatcher,
		gitopsService.Store,
		filesystem,
	)

	assistantService, err := buildAssistantService(
		cfg,
		resourcesService.ServiceStore,
		resourcesService.NodeStore,
		swarmSvc,
		gitRepository,
		recommendationsService.Store,
		control,
		metricsGroup,
		eventService,
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to build assistant service", slog.Any("err", err))
		os.Exit(1)
	}

	recommendationsService.RegisterEventSubscribers(eventService.Dispatcher)

	webApplication, err := webserver.NewApplication(
		cfg.Spec.Web.Address,
		cfg,
		gitopsService.Store,
		control,
		gitRepository,
		swarmSvc,
		eventService.History,
		resourcesService.ServiceStore,
		resourcesService.NodeStore,
		recommendationsService.Store,
		assistantService,
		eventService.Dispatcher,
		cfg.Spec.Web.Security.Authentication,
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to init web server", slog.Any("err", err))
		os.Exit(1)
	}
	webhookApplication := webhookserver.NewApplication(cfg.Spec.Sync.Webhook.Address, cfg, control)

	healthServer := healthserver.NewApplication(cfg.Spec.HealthServer)

	entrypoints := []entrypoint.Entrypoint{
		{
			Name: "state-store",
			Run: func(ctx context.Context) error {
				gitopsService.Store.(*modelstore.WarmupStore).Sync(ctx)
				return nil
			},
			Stop: func(ctx context.Context) error {
				gitopsService.Store.Stop()
				return nil
			},
		},
		webApplication.Entrypoint(),
		healthServer.Entrypoint(),
		{
			Name: "nodes-collector",
			Run: func(ctx context.Context) error {
				return resourcesService.NodeCollector.Run(ctx)
			},
		},
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
		slog.String("web.security", cfg.Spec.Web.Security.Authentication.Strategy()),
		slog.String("webhook.address", cfg.Spec.Sync.Webhook.Address),
		slog.Bool("webhook.enabled", webhookApplication.Enabled()),
		slog.String("healthServer.address", cfg.Spec.HealthServer.Address),
		slog.String("healthz.path", cfg.Spec.HealthServer.Healthz.Path),
		slog.String("metrics.path", cfg.Spec.HealthServer.Metrics.Path),
		slog.String("mode", cfg.Spec.Sync.Mode),
		slog.String("repo", cfg.Spec.Git.Repository),
		slog.String("log.level", cfg.Spec.Log.Level.String()),
		slog.Bool("assistant.enabled", cfg.Spec.Assistant.Enabled),
	)
	err = runner.Run()
	if err != nil {
		slog.ErrorContext(ctx, "failed to run", slog.Any("err", err))
		stopTracing()
		os.Exit(1)
	}

	stopTracing()
}

func buildAssistantService(
	cfg *config.Config,
	serviceStore *service.Store,
	nodeStore *swarmnode.Store,
	swarmService *swarm.Swarm,
	gitRepository gitx.Repository,
	recommendations mcpTools.RecommendationsReader,
	control *controller.Controller,
	metrics *metrics.Group,
	eventService *event.Service,
) (assistant.Assistant, error) {
	if !cfg.Spec.Assistant.Enabled {
		return &assistant.DisabledAssistant{}, nil
	}

	temperature, err := cfg.Spec.Assistant.Model.OpenAI.ResolveTemperature()
	if err != nil {
		return nil, fmt.Errorf("resolve assistant temperature: %w", err)
	}

	imageVersionResolver, err := registry.NewImageVersionResolver()
	if err != nil {
		return nil, fmt.Errorf("build image version resolver: %w", err)
	}

	commitDiffer := differ.New()
	hostingProviders, err := githosting.NewProviderManager(cfg.Spec.Hostings)
	if err != nil {
		return nil, fmt.Errorf("build hosting providers: %w", err)
	}

	toolExecutor := mcpserver.NewExecutor(
		eventService.History,
		nodeStore,
		swarmService,
		serviceStore,
		recommendations,
		imageVersionResolver,
		gitRepository,
		hostingProviders,
		cfg.Spec.Stacks,
		commitDiffer,
		control,
		eventService.Dispatcher,
		metrics.MCP,
	)

	return assistant.NewService(assistant.Config{
		Enabled:                 cfg.Spec.Assistant.Enabled,
		ModelName:               cfg.Spec.Assistant.Model.Name,
		EmbeddingModelName:      cfg.Spec.Assistant.Model.EmbeddingName,
		BaseURL:                 cfg.Spec.Assistant.Model.OpenAI.BaseURL,
		APIToken:                string(cfg.Spec.Assistant.Model.OpenAI.APIToken.Content),
		OrganizationID:          cfg.Spec.Assistant.Model.OpenAI.OrganizationID,
		Temperature:             temperature,
		MaxTokens:               cfg.Spec.Assistant.Model.OpenAI.MaxTokens,
		SystemPrompt:            cfg.Spec.Assistant.SystemPrompt,
		AllowedTools:            cfg.Spec.Assistant.Tools,
		ConversationInMemoryTTL: cfg.Spec.Assistant.Conversation.Storage.InMemory.TTL.Value,
	}, serviceStore, toolExecutor, eventService.Dispatcher, metrics.Assistant)
}
