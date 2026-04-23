package handlers

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/assistant"
	"github.com/swarm-deploy/swarm-deploy/internal/controller"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/event/history"
	swarmnode "github.com/swarm-deploy/swarm-deploy/internal/node"
	"github.com/swarm-deploy/swarm-deploy/internal/service"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// ServiceStatusInspector reads compact status snapshot for a stack service.
type ServiceStatusInspector interface {
	// InspectServiceStatus returns compact status snapshot for a stack service.
	GetStatus(ctx context.Context, serviceRef swarm.ServiceReference) (swarm.ServiceStatus, error)
}

// SecretsReader reads current Docker secrets snapshot.
type SecretsReader interface {
	// List returns current Docker secrets snapshot.
	List(ctx context.Context) ([]swarm.Secret, error)
}

type handler struct {
	generated.UnimplementedHandler
	control          *controller.Controller
	serviceInspector ServiceStatusInspector
	secrets          SecretsReader
	history          *history.Store
	services         *service.Store
	nodes            *swarmnode.Store
	assistant        assistant.Assistant
}

var _ generated.Handler = (*handler)(nil)

func New(
	control *controller.Controller,
	serviceInspector ServiceStatusInspector,
	secrets SecretsReader,
	history *history.Store,
	services *service.Store,
	nodes *swarmnode.Store,
	assistantService assistant.Assistant,
) generated.Handler {
	return &handler{
		control:          control,
		serviceInspector: serviceInspector,
		secrets:          secrets,
		history:          history,
		services:         services,
		nodes:            nodes,
		assistant:        assistantService,
	}
}
