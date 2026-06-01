package handlers

import (
	"github.com/swarm-deploy/swarm-deploy/internal/assistant"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/controller"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/modelstore"
	swarmnode "github.com/swarm-deploy/swarm-deploy/internal/resources/node"
	"github.com/swarm-deploy/swarm-deploy/internal/resources/service"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type handler struct {
	generated.UnimplementedHandler
	stackProvider    config.StackProvider
	stateStore       modelstore.ReadStore
	control          *controller.Controller
	serviceInspector swarm.ServiceManager
	secrets          swarm.SecretManager
	networks         swarm.NetworkManager
	history          *history.Store
	services         *service.Store
	nodes            *swarmnode.Store
	assistant        assistant.Assistant
	git              gitx.Repository
}

var _ generated.Handler = (*handler)(nil)

func New(
	stackProvider config.StackProvider,
	stateStore modelstore.ReadStore,
	control *controller.Controller,
	gitRepository gitx.Repository,
	swarmService *swarm.Swarm,
	history *history.Store,
	services *service.Store,
	nodes *swarmnode.Store,
	assistantService assistant.Assistant,
) generated.Handler {
	return &handler{
		stackProvider:    stackProvider,
		stateStore:       stateStore,
		control:          control,
		serviceInspector: swarmService.Services,
		secrets:          swarmService.Secrets,
		networks:         swarmService.Networks,
		history:          history,
		services:         services,
		nodes:            nodes,
		assistant:        assistantService,
		git:              gitRepository,
	}
}
