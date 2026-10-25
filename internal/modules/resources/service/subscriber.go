package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/enrichment"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/enrichment/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	webroute "github.com/swarm-deploy/webroute/api"
)

// Subscriber persists service metadata on DeploySuccess events.
type Subscriber struct {
	db               *storage.Database
	events           dispatcher.Dispatcher
	store            modelstore.Store
	inspector        swarm.ServiceManager
	images           swarm.ImageManager
	configs          swarm.ConfigManager
	extractor        *metadata.Extractor
	webRouteResolver *enrichment.WebRouteResolver
}

// NewSubscriber creates a service metadata event subscriber.
func NewSubscriber(
	store modelstore.Store,
	swarmService *swarm.Swarm,
	extractor *metadata.Extractor,
	db *storage.Database,
	publisher dispatcher.Dispatcher,
) *Subscriber {
	return &Subscriber{db: db, events: publisher,
		store:            store,
		inspector:        swarmService.Services,
		images:           swarmService.Images,
		configs:          swarmService.Configs,
		extractor:        extractor,
		webRouteResolver: enrichment.NewWebRouteResolver(),
	}
}

func (s *Subscriber) Name() string {
	return "save-service-metadata"
}

// Handle processes DeploySuccess events and persists resolved services snapshot.
func (s *Subscriber) Handle(ctx context.Context, event events.Envelope) error {
	deploySuccess, ok := event.Event.(*events.DeploySuccess)
	if !ok {
		return nil
	}

	services := make([]model.Info, 0, len(deploySuccess.StackDefinition.Compose.Services))
	for _, deployedService := range deploySuccess.StackDefinition.Compose.Services {
		serviceRef := swarm.NewServiceReference(deploySuccess.StackName, deployedService.Name)

		slog.DebugContext(ctx, "[service-store] inspecting service labels",
			slog.String("stack_name", deploySuccess.StackName),
			slog.String("service_name", deployedService.Name),
		)

		spec := swarm.ServiceSpec{Image: deployedService.Image}
		labels := metadata.Labels{}
		containerEnv := []string(nil)
		containerConfigs := []webroute.ServiceConfig(nil)
		status, statusErr := s.inspector.GetStatus(ctx, serviceRef)
		if statusErr != nil {
			slog.WarnContext(ctx, "[service] failed to inspect service status",
				slog.String("stack", deploySuccess.StackName),
				slog.String("service", deployedService.Name),
				slog.Any("err", statusErr),
			)
		} else {
			spec = status.Spec
			labels.Service = status.Spec.Labels
			labels.Container = status.ContainerLabels
			containerEnv = status.ContainerEnv
			containerConfigs = s.loadWebRouteConfigs(
				ctx,
				deploySuccess.StackName,
				deployedService,
				deploySuccess.StackDefinition.Compose.Configs,
				status.ContainerConfigs,
			)
		}

		imageMeta, imageErr := s.images.Get(ctx, spec.Image)
		if imageErr != nil {
			if !errors.Is(imageErr, swarm.ErrImageNotFound) {
				slog.WarnContext(ctx, "[service] failed to inspect image",
					slog.String("stack", deploySuccess.StackName),
					slog.String("service", deployedService.Name),
					slog.String("image", spec.Image),
					slog.Any("err", imageErr),
				)
			}
		} else {
			labels.Image = imageMeta.Labels
		}

		var environment map[string]string
		if len(containerEnv) > 0 {
			parsedEnvironment, environmentErr := compose.NewEnvironment(containerEnv)
			if environmentErr != nil {
				slog.WarnContext(ctx, "[service] failed to parse service environment",
					slog.String("stack", deploySuccess.StackName),
					slog.String("service", deployedService.Name),
					slog.Any("err", environmentErr),
				)
			} else {
				environment = parsedEnvironment.Map
			}
		}

		serviceInfo := model.Info{
			Metadata:    s.extractor.Extract(deployedService.Image, labels),
			Name:        deployedService.Name,
			Stack:       deploySuccess.StackName,
			Image:       deployedService.Image,
			Environment: environment,
			Spec:        spec,
			WebRoutes:   s.webRouteResolver.Resolve(ctx, environment, containerConfigs),
		}

		services = append(services, serviceInfo)
	}

	return s.persistProjection(ctx, event.ID, deploySuccess, services)
}

func (s *Subscriber) persistProjection(
	ctx context.Context, sourceID string, deploySuccess *events.DeploySuccess, services []model.Info,
) error {
	if sourceID == "" {
		return fmt.Errorf("service catalog projection requires source event ID")
	}
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		current, err := s.currentDeployment(ctx, deploySuccess)
		if err != nil {
			return err
		}
		if !current {
			return nil
		}
		query, args, err := goqu.Dialect("sqlite3").Insert("service_catalog_receipts").Prepared(true).
			Rows(goqu.Record{"source_event_id": sourceID}).
			OnConflict(goqu.DoNothing()).
			ToSQL()
		if err != nil {
			return err
		}
		result, err := s.db.Exec(ctx, query, args...)
		if err != nil {
			return err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if inserted == 0 {
			return nil
		}
		if err = s.store.ReplaceStack(ctx, deploySuccess.StackName, services); err != nil {
			return fmt.Errorf("persist services for stack %s: %w", deploySuccess.StackName, err)
		}
		return s.events.Publish(ctx, &events.ServiceCatalogUpdated{StackName: deploySuccess.StackName})
	})
}

func (s *Subscriber) currentDeployment(ctx context.Context, deploySuccess *events.DeploySuccess) (bool, error) {
	if deploySuccess.DeploymentID == "" {
		return true, nil
	}
	var current bool
	exists := goqu.Dialect("sqlite3").From("desired_snapshots").Prepared(true).Select(goqu.L("1")).
		Where(goqu.Ex{"stack": deploySuccess.StackName, "deployment_id": deploySuccess.DeploymentID})
	query, args, err := goqu.Dialect("sqlite3").From().Prepared(true).
		Select(goqu.L("EXISTS ?", exists)).ToSQL()
	if err != nil {
		return false, err
	}
	err = s.db.Get(ctx, &current, query, args...)
	return current, err
}

func (s *Subscriber) loadWebRouteConfigs(
	ctx context.Context,
	stackName string,
	service compose.Service,
	desiredConfigs compose.Configs,
	configs []swarm.ServiceConfig,
) []webroute.ServiceConfig {
	if len(configs) == 0 {
		return nil
	}

	out := make([]webroute.ServiceConfig, 0, len(configs))
	for i, cfg := range configs {
		desiredRef := matchDesiredConfigRef(service.Configs, cfg, i)
		var desiredConfig *compose.Config
		if desiredRef != nil {
			desiredConfig = desiredConfigs[desiredRef.Source]
		}

		routeConfig, ok := s.loadWebRouteConfig(ctx, stackName, service.Name, cfg, desiredConfig)
		if !ok {
			continue
		}

		out = append(out, routeConfig)
	}

	return out
}

func matchDesiredConfigRef(
	refs []compose.ObjectRef,
	live swarm.ServiceConfig,
	index int,
) *compose.ObjectRef {
	if live.Target != "" {
		for i := range refs {
			if refs[i].Target == live.Target {
				return &refs[i]
			}
		}
	}

	if index >= 0 && index < len(refs) {
		return &refs[index]
	}

	return nil
}

func (s *Subscriber) loadWebRouteConfig(
	ctx context.Context,
	stackName string,
	serviceName string,
	ref swarm.ServiceConfig,
	desiredConfig *compose.Config,
) (webroute.ServiceConfig, bool) {
	if ref.Target == "" {
		return nil, false
	}

	if desiredConfig != nil && desiredConfig.Data != nil {
		return enrichment.NewWebRouteConfig(ref.Target, desiredConfig.Data), true
	}

	if len(ref.Data) > 0 {
		return enrichment.NewWebRouteConfig(ref.Target, ref.Data), true
	}

	configName := ref.ConfigName
	if configName == "" {
		configName = ref.ConfigID
	}
	if configName == "" {
		return nil, false
	}

	cfg, err := s.configs.Get(ctx, configName)
	if err != nil {
		slog.InfoContext(ctx, "[service] failed to load service config for web route resolution",
			slog.String("stack", stackName),
			slog.String("service", serviceName),
			slog.String("config", configName),
			slog.Any("err", err),
		)

		return nil, false
	}
	if len(cfg.Data) == 0 {
		return nil, false
	}

	return enrichment.NewWebRouteConfig(ref.Target, cfg.Data), true
}
