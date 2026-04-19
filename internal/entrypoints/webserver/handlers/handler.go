package handlers

import (
	"context"

	"github.com/artarts36/swarm-deploy/internal/assistant"
	"github.com/artarts36/swarm-deploy/internal/controller"
	generated "github.com/artarts36/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/artarts36/swarm-deploy/internal/event/history"
	"github.com/artarts36/swarm-deploy/internal/service"
	"github.com/artarts36/swarm-deploy/internal/swarm"
	swarminspector "github.com/artarts36/swarm-deploy/internal/swarm/inspector"
)

// ServiceStatusInspector reads compact status snapshot for a stack service.
type ServiceStatusInspector interface {
	// InspectServiceStatus returns compact status snapshot for a stack service.
	GetStatus(ctx context.Context, stackName, serviceName string) (swarm.ServiceStatus, error)
}

type handler struct {
	generated.UnimplementedHandler
	control          *controller.Controller
	serviceInspector ServiceStatusInspector
	history          *history.Store
	services         *service.Store
	nodes            *swarminspector.NodeStore
	assistant        assistant.Assistant
}

var _ generated.Handler = (*handler)(nil)

func New(
	control *controller.Controller,
	serviceInspector ServiceStatusInspector,
	history *history.Store,
	services *service.Store,
	nodes *swarminspector.NodeStore,
	assistantService assistant.Assistant,
) generated.Handler {
	return &handler{
		control:          control,
		serviceInspector: serviceInspector,
		history:          history,
		services:         services,
		nodes:            nodes,
		assistant:        assistantService,
	}
}
