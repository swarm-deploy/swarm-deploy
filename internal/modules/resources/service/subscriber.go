package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/enrichment"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/enrichment/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	webroute "github.com/swarm-deploy/webroute/api"
)

// Subscriber persists service metadata on deploySuccess events.
type Subscriber struct {
	store            modelstore.Store
	inspector        swarm.ServiceManager
	images           swarm.ImageManager
	configs          swarm.ConfigManager
	fileSystem       fs.FileSystem
	extractor        *metadata.Extractor
	webRouteResolver *enrichment.WebRouteResolver
}

// NewSubscriber creates a service metadata event subscriber.
func NewSubscriber(
	store modelstore.Store,
	swarmService *swarm.Swarm,
	fileSystem fs.FileSystem,
	extractor *metadata.Extractor,
) *Subscriber {
	return &Subscriber{
		store:            store,
		inspector:        swarmService.Services,
		images:           swarmService.Images,
		configs:          swarmService.Configs,
		fileSystem:       fileSystem,
		extractor:        extractor,
		webRouteResolver: enrichment.NewWebRouteResolver(),
	}
}

func (s *Subscriber) Name() string {
	return "save-service-metadata"
}

func (s *Subscriber) Slow() bool {
	return true
}

// Handle processes deploySuccess events and persists resolved services snapshot.
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

	if err := s.store.ReplaceStack(ctx, deploySuccess.StackName, services); err != nil {
		return fmt.Errorf("persist services for stack %s: %w", deploySuccess.StackName, err)
	}

	return nil
}

func (s *Subscriber) loadWebRouteConfigs(
	ctx context.Context,
	stackName string,
	service compose.Service,
	configs []swarm.ServiceConfig,
) []webroute.ServiceConfig {
	if len(configs) == 0 {
		return nil
	}

	out := make([]webroute.ServiceConfig, 0, len(configs))
	for i, cfg := range configs {
		desiredRef := matchDesiredConfigRef(service.Configs, cfg, i)
		routeConfig, ok := s.loadWebRouteConfig(ctx, stackName, service.Name, cfg, desiredRef)
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
	desiredRef *compose.ObjectRef,
) (webroute.ServiceConfig, bool) {
	if ref.Target == "" {
		return nil, false
	}

	if data, ok := s.loadConfigFromFileSystem(ctx, stackName, serviceName, desiredRef); ok {
		return enrichment.NewWebRouteConfig(ref.Target, data), true
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

func (s *Subscriber) loadConfigFromFileSystem(
	ctx context.Context,
	stackName string,
	serviceName string,
	ref *compose.ObjectRef,
) ([]byte, bool) {
	if ref == nil || ref.File == "" {
		return nil, false
	}

	data, err := s.fileSystem.ReadFile(ctx, ref.File)
	if err != nil {
		slog.InfoContext(ctx, "[service] failed to load repository config for web route resolution",
			slog.String("stack", stackName),
			slog.String("service", serviceName),
			slog.String("config", ref.Source),
			slog.String("path", ref.File),
			slog.Any("err", err),
		)
		return nil, false
	}

	return data, true
}
