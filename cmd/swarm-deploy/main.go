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
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/healthserver"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/sd"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webhookserver"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver"
	"github.com/swarm-deploy/swarm-deploy/internal/legacyimport"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/logx"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/buildinfo"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
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

	slog.SetDefault(slog.New(slogx.NewChain(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.Spec.Log.Level.Level()}),
		slogm.RequestID(),
		logx.EventType(),
		security.LogUser(),
	)))

	tracerProvider, err := sd.InitTracerProvider(ctx, cfg.Spec.Tracing, buildinfo.Info{
		Version: Version,
		Date:    BuildDate,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to init tracing", slog.Any("err", err))
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

	db, err := storage.Open(ctx, cfg.Spec.DataDir)
	if err != nil {
		slog.ErrorContext(ctx, "failed to open database", slog.Any("err", err))
		os.Exit(1)
	}
	if err = legacyimport.Run(ctx, db, cfg.Spec.DataDir); err != nil {
		slog.ErrorContext(ctx, "legacy import failed; source files are unchanged", slog.Any("err", err))
		_ = db.Close()
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

	cnt.Swarm = swarm.NewSwarm(dockerClient, cfg.Spec.Swarm.Command)
	cnt.Deployer = deployer.NewDeployer(
		cfg.Spec.Swarm.InitJobPollEvery.Value,
		cfg.Spec.Swarm.InitJobMaxDuration.Value,
		dockerClient,
		cnt.GetSwarm(),
		metricsGroup.Deploys,
	)

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
		cfg,
		cnt.GitOps,
		cnt.Swarm,
		cnt.Event,
		cnt.Resources,
		cnt.Recommendations.Store,
		cnt.AlertManagement.Store,
		cnt.Assistant.Service,
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
		slog.String("web.security", cfg.Spec.Web.Security.Authentication.Strategy()),
		slog.String("webhook.address", cfg.Spec.Sync.Webhook.Address),
		slog.Bool("webhook.enabled", cfg.Spec.Sync.Webhook.Enabled),
		slog.String("healthServer.address", cfg.Spec.HealthServer.Address),
		slog.String("healthz.path", cfg.Spec.HealthServer.Healthz.Path),
		slog.String("metrics.path", cfg.Spec.HealthServer.Metrics.Path),
		slog.String("mode", cfg.Spec.Sync.Mode),
		slog.String("repo", cfg.Spec.Git.Repository),
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
	Storage         *storage.Database
	FileSystem      fs.FileSystem
	Metrics         *metrics.Group
	Swarm           *swarm.Swarm
	EventDispatcher dispatcher.Dispatcher
	Deployer        deployer.StackDeployer

	Event           *event.Module
	Resources       *resources.Module
	GitOps          *gitops.Module
	Recommendations *recommendations.Module
	AlertManagement *alertmanagement.Module
	Assistant       *assistant.Module
}

// GetStorage returns the shared application database.
func (c *container) GetStorage() *storage.Database { return c.Storage }

func (c *container) GetFileSystem() fs.FileSystem {
	return c.FileSystem
}

func (c *container) GetMetrics() *metrics.Group {
	return c.Metrics
}

func (c *container) GetSwarm() *swarm.Swarm {
	return c.Swarm
}

func (c *container) GetEventModule() *event.Module {
	return c.Event
}

func (c *container) GetResourcesModule() *resources.Module {
	return c.Resources
}

func (c *container) GetGitOpsModule() *gitops.Module {
	return c.GitOps
}

func (c *container) GetRecommendationsModule() *recommendations.Module {
	return c.Recommendations
}

func (c *container) GetDeployer() deployer.StackDeployer {
	return c.Deployer
}
