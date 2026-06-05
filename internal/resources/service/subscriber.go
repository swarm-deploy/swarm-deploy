package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Subscriber persists service metadata on deploySuccess events.
type Subscriber struct {
	store            *Store
	inspector        swarm.ServiceManager
	images           swarm.ImageManager
	metadata         *MetadataExtractor
	webRouteResolver *WebRouteResolver
}

// NewSubscriber creates a service metadata event subscriber.
func NewSubscriber(
	store *Store,
	inspector swarm.ServiceManager,
	images swarm.ImageManager,
	metadata *MetadataExtractor,
) *Subscriber {
	return &Subscriber{
		store:            store,
		inspector:        inspector,
		images:           images,
		metadata:         metadata,
		webRouteResolver: NewWebRouteResolver(),
	}
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
		labels := Labels{}
		containerEnv := []string(nil)
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

		resolved := s.metadata.Resolve(deployedService.Image, labels)
		repositoryURL := ResolveRepositoryURL(labels)
		serviceInfo := Info{
			Name:          deployedService.Name,
			Stack:         deploySuccess.StackName,
			Description:   resolved.Description,
			Type:          resolved.Type,
			Image:         deployedService.Image,
			Environment:   environment,
			Spec:          spec,
			RepositoryURL: repositoryURL,
			WebRoutes:     s.webRouteResolver.Resolve(environment),
		}

		services = append(services, serviceInfo)
	}

	if err := s.store.ReplaceStack(deploySuccess.StackName, services); err != nil {
		return fmt.Errorf("persist services for stack %s: %w", deploySuccess.StackName, err)
	}

	return nil
}
