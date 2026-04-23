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
	"github.com/swarm-deploy/swarm-deploy/internal/controller"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/differ"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/healthserver"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webhookserver"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver"
	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/event/logx"
	eventmetrics "github.com/swarm-deploy/swarm-deploy/internal/event/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/event/notifiers"
	notify2 "github.com/swarm-deploy/swarm-deploy/internal/event/notify"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/git"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	swarmnode "github.com/swarm-deploy/swarm-deploy/internal/node"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
	"github.com/swarm-deploy/swarm-deploy/internal/service"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
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
		logx.EventType(),
		security.LogUser(),
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

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		slog.ErrorContext(ctx, "failed to init docker client", slog.Any("err", err))
		os.Exit(1)
	}

	swarmService := swarm.NewSwarm(dockerClient, cfg.Spec.Swarm.Command)

	deployerSvc := deployer.NewDeployer(
		cfg.Spec.Swarm.StackDeployArgs,
		cfg.Spec.Swarm.InitJobPollEvery.Value,
		cfg.Spec.Swarm.InitJobMaxDuration.Value,
		swarmService.BinaryRunner,
		dockerClient,
		swarmService,
		metricsGroup.Deploys,
	)

	nodeStore, err := swarmnode.NewNodeStore(filepath.Join(cfg.Spec.DataDir, "nodes.json"))
	if err != nil {
		slog.ErrorContext(ctx, "failed to init node store", slog.Any("err", err))
		os.Exit(1)
	}
	nodeCollector := swarmnode.NewNodeCollector(swarmService.Nodes, nodeStore)

	eventDispatcher, eventHistory, serviceStore, err := buildEventDispatcher(
		cfg,
		swarmService.Services,
		metricsGroup.Events,
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to build event dispatcher", slog.Any("err", err))
		os.Exit(1)
	}

	control := controller.New(
		cfg,
		gitRepository,
		deployerSvc,
		metricsGroup,
		eventDispatcher,
	)

	assistantService, err := buildAssistantService(
		cfg,
		serviceStore,
		eventHistory,
		nodeStore,
		swarmService,
		gitRepository,
		control,
		eventDispatcher,
		metricsGroup,
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to build assistant service", slog.Any("err", err))
		os.Exit(1)
	}

	webApplication, err := webserver.NewApplication(
		cfg.Spec.Web.Address,
		control,
		swarmService.Services,
		swarmService.Secrets,
		eventHistory,
		serviceStore,
		nodeStore,
		assistantService,
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
			Name: "nodes-collector",
			Run: func(ctx context.Context) error {
				return nodeCollector.Run(ctx)
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
		os.Exit(1)
	}
}

func buildAssistantService(
	cfg *config.Config,
	serviceStore *service.Store,
	eventHistory *history.Store,
	nodeStore *swarmnode.Store,
	swarmService *swarm.Swarm,
	gitRepository gitx.Repository,
	control *controller.Controller,
	eventDispatcher dispatcher.Dispatcher,
	metrics *metrics.Group,
) (assistant.Assistant, error) {
	if !cfg.Spec.Assistant.Enabled {
		return &assistant.DisabledAssistant{}, nil
	}

	temperature, err := cfg.Spec.Assistant.Model.OpenAI.ResolveTemperature()
	if err != nil {
		return nil, fmt.Errorf("resolve assistant temperature: %w", err)
	}

	maxTokens, err := cfg.Spec.Assistant.Model.OpenAI.ResolveMaxTokens()
	if err != nil {
		return nil, fmt.Errorf("resolve assistant maxTokens: %w", err)
	}

	imageVersionResolver, err := registry.NewImageVersionResolver()
	if err != nil {
		return nil, fmt.Errorf("build image version resolver: %w", err)
	}

	commitDiffer := differ.New()

	toolExecutor := mcpserver.NewExecutor(
		eventHistory,
		nodeStore,
		swarmService,
		serviceStore,
		imageVersionResolver,
		gitRepository,
		cfg.Spec.Stacks,
		commitDiffer,
		control,
		eventDispatcher,
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
		MaxTokens:               maxTokens,
		SystemPrompt:            cfg.Spec.Assistant.SystemPrompt,
		AllowedTools:            cfg.Spec.Assistant.Tools,
		ConversationInMemoryTTL: cfg.Spec.Assistant.Conversation.Storage.InMemory.TTL.Value,
	}, serviceStore, toolExecutor, eventDispatcher, metrics.Assistant)
}

func buildEventDispatcher(
	cfg *config.Config,
	serviceLabelsInspector service.LabelsInspector,
	eventMetrics metrics.Events,
) (dispatcher.Dispatcher, *history.Store, *service.Store, error) {
	historyStore, err := history.NewStore(
		filepath.Join(cfg.Spec.DataDir, "event-history.json"),
		cfg.Spec.EventHistory.Capacity,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("build history store: %w", err)
	}

	serviceStore, err := service.NewStore(filepath.Join(cfg.Spec.DataDir, "services.json"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("build service store: %w", err)
	}

	var eventDispatcher dispatcher.Dispatcher = dispatcher.NewQueueDispatcher()

	if cfg.Spec.Web.Security.Authentication.Strategy() != config.AuthenticationStrategyNone {
		eventDispatcher = dispatcher.NewPropagatableDispatcher(
			dispatcher.WrapPropagators(security.PropagateEvent()),
			eventDispatcher,
		)
	}

	subscribeOnAllEvents(eventDispatcher, historyStore)
	subscribeOnAllEvents(eventDispatcher, eventmetrics.NewSubscriber(eventMetrics))

	dispatcherLink := &dispatcherProxy{Dispatcher: eventDispatcher}
	subscribersCount := 0

	eventDispatcher.Subscribe(
		events.TypeDeploySuccess,
		service.NewSubscriber(serviceStore, serviceLabelsInspector, service.NewMetadataExtractor()),
	)
	subscribersCount++

	for eventType, channels := range cfg.Spec.Notifications.On {
		for _, tg := range channels.Telegram {
			tgNotifier, notifierErr := notifiers.NewTelegramNotifier(
				tg.Name,
				string(tg.BotToken.Content),
				tg.ChatID,
				notifiers.TelegramOptions{
					ChatThreadID:  tg.ChatThreadID,
					Message:       tg.Message,
					Retries:       cfg.Spec.Notifications.Messengers.Telegram.Retries,
					SOCKS5Address: cfg.Spec.Notifications.Messengers.Telegram.Proxy.SOCKS5.Address.Value,
				},
			)
			if notifierErr != nil {
				return nil, nil, nil, fmt.Errorf("build telegram notifier %q: %w", tg.Name, notifierErr)
			}

			eventDispatcher.Subscribe(eventType, notify2.NewSubscriber(tgNotifier, dispatcherLink))
			subscribersCount++
		}

		for _, custom := range channels.Custom {
			notifier := notifiers.NewCustomWebhookNotifier(custom.Name, custom.URL.Value.String(), custom.Method, custom.Header)

			eventDispatcher.Subscribe(eventType, notify2.NewSubscriber(notifier, dispatcherLink))
			subscribersCount++
		}
	}

	if len(cfg.Spec.Notifications.On) == 0 {
		slog.Info("notification subscribers not found")
	}

	slog.Info(
		"found event subscribers",
		slog.Int("subscribers", subscribersCount),
	)

	return eventDispatcher, historyStore, serviceStore, nil
}

func subscribeOnAllEvents(dispatcher dispatcher.Dispatcher, subscriber dispatcher.Subscriber) {
	for _, typ := range events.Types {
		dispatcher.Subscribe(typ, subscriber)
	}
}

type dispatcherProxy struct {
	dispatcher.Dispatcher
}
