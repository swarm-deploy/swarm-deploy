package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	entrypoint "github.com/artarts36/go-entrypoint"
	"github.com/cappuccinotm/slogx"
	"github.com/cappuccinotm/slogx/slogm"
	"github.com/docker/docker/client"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/configstore"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/healthserver"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/sd"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webhookserver"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/logx"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/buildinfo"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/logparser"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const shutdownTimeout = 30 * time.Second

var (
	Version   = "0.1.0"
	BuildDate = "2026-05-26 23:51:00"
)

var modules = []module{
	{
		Name: "event",
		Initialize: func(ctx context.Context, cfg *config.Config, cnt *container) error {
			mod, err := event.InitModule(ctx, cfg, cnt)
			cnt.Event = mod
			if err != nil {
				return err
			}
			cnt.ConfigStore = configstore.New(cfg, mod.Dispatcher)
			return prometheus.Register(mod.Bus)
		},
	},
	{
		Name: "alert-management",
		Initialize: func(ctx context.Context, cfg *config.Config, cnt *container) error {
			mod, err := alertmanagement.InitModule(ctx, cfg, cnt)
			cnt.AlertManagement = mod
			return err
		},
	},
	{
		Name: "resources",
		Initialize: func(ctx context.Context, cfg *config.Config, cnt *container) error {
			mod, err := resources.InitModule(ctx, cfg, cnt)
			cnt.Resources = mod
			return err
		},
	},
	{
		Name: "recommendations",
		Initialize: func(ctx context.Context, cfg *config.Config, cnt *container) error {
			mod, err := recommendations.InitModule(ctx, cfg, cnt)
			cnt.Recommendations = mod
			return err
		},
	},
	{
		Name: "deployments",
		Initialize: func(ctx context.Context, cfg *config.Config, cnt *container) error {
			mod, err := deployments.InitModule(ctx, cfg, cnt)
			cnt.Deployments = mod
			return err
		},
	},
	{
		Name: "gitops",
		Initialize: func(ctx context.Context, cfg *config.Config, cnt *container) error {
			mod, err := gitops.InitModule(ctx, cfg, cnt)
			cnt.GitOps = mod
			return err
		},
	},
	{
		Name: "assistant",
		Initialize: func(ctx context.Context, cfg *config.Config, cnt *container) error {
			mod, err := assistant.InitModule(ctx, cfg, cnt)
			cnt.Assistant = mod
			return err
		},
	},
}

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

	logHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.Spec.Log.Level.Level()})
	slog.SetDefault(slog.New(slogx.NewChain(logHandler, loggingMiddlewares()...)))

	tracerProvider, err := sd.InitTracerProvider(ctx, cfg.Spec.Tracing, buildinfo.Info{
		Version: Version,
		Date:    BuildDate,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to init tracing", slog.Any("err", err))
		os.Exit(1)
	}
	slog.SetDefault(slog.New(slogx.NewChain(logHandler, loggingMiddlewares()...)))

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

	db, err := storage.OpenSQLite(ctx, cfg.Spec.DataDir)
	if err != nil {
		slog.ErrorContext(ctx, "failed to open database", slog.Any("err", err))
		os.Exit(1)
	}
	cnt := &container{
		Storage:    db,
		FileSystem: fs.TraceOS(),
		Metrics:    metricsGroup,
	}

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		slog.ErrorContext(ctx, "failed to init docker client", slog.Any("err", err))
		os.Exit(1)
	}

	cnt.Swarm = swarm.NewSwarm(dockerClient)
	cnt.DockerClient = dockerClient
	cnt.TaskLogs = logparser.NewService(cnt.Swarm.Services)

	for _, mod := range modules {
		slog.InfoContext(ctx, "[main] initializing module", slog.Any("module", mod.Name))

		err = mod.Initialize(ctx, cfg, cnt)
		if err != nil {
			slog.ErrorContext(ctx, "failed to init module", slog.Any("err", err),
				slog.String("module", mod.Name),
			)
			os.Exit(1)
		}

		slog.InfoContext(ctx, "[main] module initialized",
			slog.Any("module", mod.Name),
		)
	}

	webApplication, err := webserver.NewApplication(
		cfg.Spec.Web.Address,
		cfg.Spec.Web.BasePath,
		cnt.ConfigStore,
		cnt.GitOps,
		cnt.Swarm,
		cnt.Event,
		cnt.Resources,
		cnt.Recommendations.Store,
		cnt.AlertManagement.Store,
		cnt.Assistant.Service,
		cnt.TaskLogs,
		cfg.Spec.Web.Security.Authentication,
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to init web server", slog.Any("err", err))
		os.Exit(1)
	}

	healthServer := healthserver.NewApplication(cfg.Spec.HealthServer)
	syncControllerDone := make(chan struct{})

	entrypoints := []entrypoint.Entrypoint{
		{
			Name: "sync-controller",
			Run: func(ctx context.Context) error {
				defer close(syncControllerDone)

				return cnt.GitOps.Controller.Run(ctx)
			},
		},
		{
			Name: "event-dispatcher",
			Run: func(ctx context.Context) error {
				workerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
				defer cancel()
				go func() { <-ctx.Done(); <-syncControllerDone; cancel() }()
				return cnt.Event.Run(workerCtx)
			},
		},
		webApplication.Entrypoint(),
		healthServer.Entrypoint(),
		{
			Name: "swarm-events",
			Run: func(ctx context.Context) error {
				return cnt.Swarm.Events.Run(ctx)
			},
		},
		{
			Name: "nodes-collector",
			Run: func(ctx context.Context) error {
				return cnt.Resources.NodeCollector.Run(ctx)
			},
		},
		{
			Name: "secrets-collector",
			Run: func(ctx context.Context) error {
				return cnt.Resources.Secrets.Collector.Run(ctx)
			},
		},
	}

	if cfg.Spec.Sync.Webhook.Enabled {
		webhookApplication, werr := webhookserver.NewApplication(cfg.Spec.Sync.Webhook.Address, cfg, cnt.GitOps.Controller)
		if werr != nil {
			slog.ErrorContext(ctx, "failed to create webhook application", slog.Any("err", werr))
			os.Exit(1)
		}

		entrypoints = append(entrypoints, webhookApplication.Entrypoint())
	}

	runner := entrypoint.NewRunner(entrypoints)

	slog.InfoContext(ctx, "starting swarm deploy",
		slog.String("web.address", cfg.Spec.Web.Address),
		slog.String("web.basePath", cfg.Spec.Web.BasePath),
		slog.String("web.security", cfg.Spec.Web.Security.Authentication.Strategy()),
		slog.String("webhook.address", cfg.Spec.Sync.Webhook.Address),
		slog.Bool("webhook.enabled", cfg.Spec.Sync.Webhook.Enabled),
		slog.String("healthServer.address", cfg.Spec.HealthServer.Address),
		slog.String("healthz.path", cfg.Spec.HealthServer.Healthz.Path),
		slog.String("metrics.path", cfg.Spec.HealthServer.Metrics.Path),
		slog.String("mode", cfg.Spec.Sync.Mode),
		slog.String("repo", repositoryLogValue(cfg.Spec.Git.Repository)),
		slog.String("log.level", cfg.Spec.Log.Level.String()),
		slog.Bool("assistant.enabled", cfg.Spec.Assistant.Enabled),
	)
	err = runner.Run()
	if closeErr := db.Close(); closeErr != nil {
		slog.ErrorContext(ctx, "failed to close database", slog.Any("err", closeErr))
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to run", slog.Any("err", err))
		shutdownTracing(tracerProvider)
		os.Exit(1)
	}

	shutdownTracing(tracerProvider)
}

func loggingMiddlewares() []slogx.Middleware {
	middlewares := []slogx.Middleware{slogm.RequestID()}
	if tracing.Enabled() {
		middlewares = append(middlewares, tracing.LogTraceID())
	}

	return append(middlewares, logx.EventType(), security.LogUser())
}

func repositoryLogValue(repository string) string {
	return gitx.RepositoryIdentifier(repository)
}

func shutdownTracing(tracerProvider *sdktrace.TracerProvider) {
	if tracerProvider == nil {
		return
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := tracerProvider.Shutdown(shutdownCtx); err != nil {
		slog.ErrorContext(shutdownCtx, "failed to shutdown tracing", slog.Any("err", err))
	}
}

type module struct {
	Name       string
	Initialize func(ctx context.Context, cfg *config.Config, cnt *container) error
}

type container struct {
	// Storage owns the shared database and transactor.
	Storage storage.Database
	// DockerClient is the shared Docker API client.
	DockerClient *client.Client

	FileSystem      fs.FileSystem
	Metrics         *metrics.Group
	Swarm           *swarm.Swarm
	TaskLogs        *logparser.Service
	EventDispatcher dispatcher.Dispatcher
	ConfigStore     *configstore.ConfigStore

	Event *event.Module
	// Deployments owns deployment execution infrastructure.
	Deployments     *deployments.Module
	Resources       *resources.Module
	GitOps          *gitops.Module
	Recommendations *recommendations.Module
	AlertManagement *alertmanagement.Module
	Assistant       *assistant.Module
}

// GetStorage returns the shared application database.
func (c *container) GetStorage() storage.Database { return c.Storage }

// GetDockerClient returns the shared Docker API client.
func (c *container) GetDockerClient() *client.Client { return c.DockerClient }

func (c *container) GetFileSystem() fs.FileSystem {
	return c.FileSystem
}

func (c *container) GetMetrics() *metrics.Group {
	return c.Metrics
}

func (c *container) GetSwarm() *swarm.Swarm {
	return c.Swarm
}

// GetTaskLogs returns the shared task log parsing and filtering service.
func (c *container) GetTaskLogs() *logparser.Service {
	return c.TaskLogs
}

func (c *container) GetEventModule() *event.Module {
	return c.Event
}

// GetConfigStore returns the shared runtime configuration store.
func (c *container) GetConfigStore() *configstore.ConfigStore { return c.ConfigStore }

func (c *container) GetResourcesModule() *resources.Module {
	return c.Resources
}

// GetDeploymentsModule returns the initialized Deployments module.
func (c *container) GetDeploymentsModule() *deployments.Module {
	return c.Deployments
}

func (c *container) GetGitOpsModule() *gitops.Module {
	return c.GitOps
}

func (c *container) GetRecommendationsModule() *recommendations.Module {
	return c.Recommendations
}

// GetAlertManagementModule returns the initialized Alert Management module.
func (c *container) GetAlertManagementModule() *alertmanagement.Module {
	return c.AlertManagement
}
