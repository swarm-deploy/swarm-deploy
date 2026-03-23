package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/artarts36/swarm-deploy/internal/event/events"
	"github.com/artarts36/swarm-deploy/internal/swarm"
)

// LabelsInspector provides labels from service, container and image inspect.
type LabelsInspector interface {
	// InspectServiceLabels returns labels for a service and its image.
	InspectServiceLabels(ctx context.Context, stackName, serviceName, imageRef string) (swarm.ServiceLabels, error)
}

// Subscriber persists service metadata on deploySuccess events.
type Subscriber struct {
	store     *Store
	inspector LabelsInspector
	resolver  *MetadataExtractor
}

// NewSubscriber creates a service metadata event subscriber.
func NewSubscriber(store *Store, inspector LabelsInspector, resolver *MetadataExtractor) *Subscriber {
	return &Subscriber{
		store:     store,
		inspector: inspector,
		resolver:  resolver,
	}
}

// Handle processes deploySuccess events and persists resolved services snapshot.
func (s *Subscriber) Handle(ctx context.Context, event events.Event) error {
	if s.store == nil {
		return errors.New("service store is nil")
	}

	deploySuccess, ok := event.(*events.DeploySuccess)
	if !ok {
		return nil
	}

	services := make([]Info, 0, len(deploySuccess.Services))
	for _, deployedService := range deploySuccess.Services {
		inspectedLabels, inspectErr := s.inspector.InspectServiceLabels(
			ctx,
			deploySuccess.StackName,
			deployedService.Name,
			deployedService.Image,
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

		resolved := s.resolver.Resolve(deployedService.Image, labels)
		services = append(services, Info{
			Name:        deployedService.Name,
			Stack:       deploySuccess.StackName,
			Description: resolved.Description,
			Type:        resolved.Type,
			Image:       deployedService.Image,
		})
	}

	if err := s.store.ReplaceStack(deploySuccess.StackName, services); err != nil {
		return fmt.Errorf("persist services for stack %s: %w", deploySuccess.StackName, err)
	}

	return nil
}
