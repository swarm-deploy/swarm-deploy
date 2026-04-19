package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/artarts36/swarm-deploy/internal/event/events"
	"github.com/artarts36/swarm-deploy/internal/service/webroute"
	"github.com/artarts36/swarm-deploy/internal/swarm"
)

// LabelsInspector provides labels from service, container and image inspect.
type LabelsInspector interface {
	// InspectServiceLabels returns labels for a service and its image.
	Labels(ctx context.Context, stackName, serviceName string) (swarm.ServiceLabels, error)
}

// Subscriber persists service metadata on deploySuccess events.
type Subscriber struct {
	store            *Store
	inspector        LabelsInspector
	metadata         *MetadataExtractor
	webRouteResolver *webroute.Resolver
}

// NewSubscriber creates a service metadata event subscriber.
func NewSubscriber(store *Store, inspector LabelsInspector, metadata *MetadataExtractor) *Subscriber {
	return &Subscriber{
		store:            store,
		inspector:        inspector,
		metadata:         metadata,
		webRouteResolver: webroute.NewResolver(),
	}
}

func (s *Subscriber) Name() string {
	return "save-service-metadata"
}

// Handle processes deploySuccess events and persists resolved services snapshot.
func (s *Subscriber) Handle(ctx context.Context, event events.Event) error {
	deploySuccess, ok := event.(*events.DeploySuccess)
	if !ok {
		return nil
	}

	services := make([]Info, 0, len(deploySuccess.Services))
	for _, deployedService := range deploySuccess.Services {
		slog.DebugContext(ctx, "[service-store] inspecting service labels",
			slog.String("stack_name", deploySuccess.StackName),
			slog.String("service_name", deployedService.Name),
		)

		inspectedLabels, inspectErr := s.inspector.Labels(
			ctx,
			deploySuccess.StackName,
			deployedService.Name,
		)
		if inspectErr != nil {
			slog.WarnContext(
				ctx,
				"[service] failed to inspect service labels",
				slog.String("stack", deploySuccess.StackName),
				slog.String("service", deployedService.Name),
				slog.Any("err", inspectErr),
			)
		}
		labels := Labels{
			Service:   inspectedLabels.Service,
			Container: inspectedLabels.Container,
			Image:     inspectedLabels.Image,
		}

		resolved := s.metadata.Resolve(deployedService.Image, labels)
		repositoryURL := ResolveRepositoryURL(labels)
		services = append(services, Info{
			Name:          deployedService.Name,
			Stack:         deploySuccess.StackName,
			Description:   resolved.Description,
			Type:          resolved.Type,
			Image:         deployedService.Image,
			RepositoryURL: repositoryURL,
			WebRoutes:     s.webRouteResolver.Resolve(inspectedLabels.ContainerEnv),
		})
	}

	if err := s.store.ReplaceStack(deploySuccess.StackName, services); err != nil {
		return fmt.Errorf("persist services for stack %s: %w", deploySuccess.StackName, err)
	}

	return nil
}
