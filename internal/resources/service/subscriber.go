package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/resources/service/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	webroute "github.com/swarm-deploy/webroute/api"
)

// Subscriber persists service metadata on deploySuccess events.
type Subscriber struct {
	store            *Store
	inspector        swarm.ServiceManager
	images           swarm.ImageManager
	configs          configReader
	cfg              *config.Config
	fileSystem       fs.FileSystem
	composeLoader    compose.FileLoader
	metadata         *metadata.Extractor
	webRouteResolver *WebRouteResolver
}

type configReader interface {
	// Get returns Docker config payload by name or ID.
	Get(ctx context.Context, configName string) (swarm.Config, error)
}

// NewSubscriber creates a service metadata event subscriber.
func NewSubscriber(
	store *Store,
	inspector swarm.ServiceManager,
	images swarm.ImageManager,
	configs configReader,
	metadata *metadata.Extractor,
) *Subscriber {
	return &Subscriber{
		store:            store,
		inspector:        inspector,
		images:           images,
		configs:          configs,
		metadata:         metadata,
		webRouteResolver: NewWebRouteResolver(),
	}
}

// NewSubscriberWithRepository creates a service metadata subscriber with repository-backed config resolution.
func NewSubscriberWithRepository(
	store *Store,
	inspector swarm.ServiceManager,
	images swarm.ImageManager,
	configs configReader,
	cfg *config.Config,
	fileSystem fs.FileSystem,
	metadata *metadata.Extractor,
) *Subscriber {
	subscriber := NewSubscriber(store, inspector, images, configs, metadata)
	subscriber.cfg = cfg
	subscriber.fileSystem = fileSystem
	if fileSystem != nil {
		subscriber.composeLoader = compose.NewFileLoaderWithReader(fileSystem.ReadFile)
	}

	return subscriber
}

func (s *Subscriber) Name() string {
	return "save-service-metadata"
}

func (s *Subscriber) Slow() bool {
	return true
}

// Handle processes deploySuccess events and persists resolved services snapshot.
func (s *Subscriber) Handle(ctx context.Context, event events.Event) error {
	deploySuccess, ok := event.(*events.DeploySuccess)
	if !ok {
		return nil
	}

	desired := s.loadDesiredCompose(ctx, deploySuccess.StackName)
	services := make([]Info, 0, len(deploySuccess.Services))
	for _, deployedService := range deploySuccess.Services {
		serviceRef := swarm.NewServiceReference(deploySuccess.StackName, deployedService.Name)

		slog.DebugContext(ctx, "[service-store] inspecting service labels",
			slog.String("stack_name", deploySuccess.StackName),
			slog.String("service_name", deployedService.Name),
		)

		spec := swarm.ServiceSpec{
			Image: deployedService.Image,
		}
		labels := metadata.Labels{}
		containerEnv := []string(nil)
		containerConfigs := []webroute.ServiceConfig(nil)
		status, statusErr := s.inspector.GetStatus(ctx, serviceRef)
		if statusErr != nil {
			slog.WarnContext(
				ctx,
				"[service] failed to inspect service status",
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
				desired,
			)
		}

		imageMeta, imageErr := s.images.Get(ctx, spec.Image)
		if imageErr != nil {
			if !errors.Is(imageErr, swarm.ErrImageNotFound) {
				slog.WarnContext(
					ctx,
					"[service] failed to inspect image",
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
				slog.WarnContext(
					ctx,
					"[service] failed to parse service environment",
					slog.String("stack", deploySuccess.StackName),
					slog.String("service", deployedService.Name),
					slog.Any("err", environmentErr),
				)
			} else {
				environment = parsedEnvironment.Map
			}
		}

		serviceInfo := Info{
			Metadata:    s.metadata.Extract(deployedService.Image, labels),
			Name:        deployedService.Name,
			Stack:       deploySuccess.StackName,
			Image:       deployedService.Image,
			Environment: environment,
			Spec:        spec,
			WebRoutes:   s.webRouteResolver.Resolve(ctx, environment, containerConfigs),
		}

		services = append(services, serviceInfo)
	}

	if err := s.store.ReplaceStack(deploySuccess.StackName, services); err != nil {
		return fmt.Errorf("persist services for stack %s: %w", deploySuccess.StackName, err)
	}

	return nil
}

func (s *Subscriber) loadDesiredCompose(ctx context.Context, stackName string) *compose.File {
	if s.cfg == nil || s.fileSystem == nil || s.composeLoader == nil {
		return nil
	}

	for _, stack := range s.cfg.Spec.Stacks {
		if stack.Name != stackName {
			continue
		}

		composePath := stack.ComposeFile
		if !filepath.IsAbs(composePath) {
			composePath = filepath.Join(s.cfg.Spec.DataDir, "repo", composePath)
		}

		file, err := s.composeLoader.Load(ctx, composePath)
		if err != nil {
			slog.InfoContext(ctx, "[service] failed to load desired compose for web route resolution",
				slog.String("stack", stackName),
				slog.String("path", composePath),
				slog.Any("err", err),
			)
			return nil
		}

		return file
	}

	return nil
}

func (s *Subscriber) loadWebRouteConfigs(
	ctx context.Context,
	stackName string,
	service compose.Service,
	configs []swarm.ServiceConfig,
	desired *compose.File,
) []webroute.ServiceConfig {
	if len(configs) == 0 {
		return nil
	}

	out := make([]webroute.ServiceConfig, 0, len(configs))
	for i, cfg := range configs {
		desiredRef := matchDesiredConfigRef(service.Configs, cfg, i)
		routeConfig, ok := s.loadWebRouteConfig(ctx, stackName, service.Name, cfg, desiredRef, desired)
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
	desired *compose.File,
) (webroute.ServiceConfig, bool) {
	if ref.Target == "" {
		return nil, false
	}

	if data, ok := s.loadRepositoryConfig(ctx, stackName, serviceName, desiredRef, desired); ok {
		return newWebRouteConfig(ref.Target, data), true
	}

	if len(ref.Data) > 0 {
		return newWebRouteConfig(ref.Target, ref.Data), true
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

	return newWebRouteConfig(ref.Target, cfg.Data), true
}

func (s *Subscriber) loadRepositoryConfig(
	ctx context.Context,
	stackName string,
	serviceName string,
	ref *compose.ObjectRef,
	desired *compose.File,
) ([]byte, bool) {
	if ref == nil || desired == nil || s.fileSystem == nil {
		return nil, false
	}

	shared, ok := desired.Compose.Configs[ref.Source]
	if !ok || shared == nil || shared.External || shared.File == "" {
		return nil, false
	}

	path := shared.File
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(desired.Path), path)
	}

	data, err := s.fileSystem.ReadFile(ctx, path)
	if err != nil {
		slog.InfoContext(ctx, "[service] failed to load repository config for web route resolution",
			slog.String("stack", stackName),
			slog.String("service", serviceName),
			slog.String("config", ref.Source),
			slog.String("path", path),
			slog.Any("err", err),
		)
		return nil, false
	}

	return data, true
}
