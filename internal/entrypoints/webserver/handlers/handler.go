package handlers

import (
	"github.com/swarm-deploy/swarm-deploy/internal/assistant"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	alertstore "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	recommendationstore "github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/modelstore"
	swarmnode "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/node"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service"
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
	nodeManager      swarm.NodeManager
	history          *history.Store
	services         *service.Store
	nodes            *swarmnode.Store
	recommendations  recommendationstore.Store
	alerts           alertstore.Store
	assistant        assistant.Assistant
	git              gitx.Repository
	composeLoader    compose.FileLoader
}

var _ generated.Handler = (*handler)(nil)
var _ generated.RawHandler = (*handler)(nil)

func New(
	stackProvider config.StackProvider,
	stateStore modelstore.ReadStore,
	control *controller.Controller,
	gitRepository gitx.Repository,
	swarmService *swarm.Swarm,
	history *history.Store,
	services *service.Store,
	nodes *swarmnode.Store,
	recommendations recommendationstore.Store,
	alerts alertstore.Store,
	assistantService assistant.Assistant,
) *handler {
	return &handler{
		stackProvider:    stackProvider,
		stateStore:       stateStore,
		control:          control,
		serviceInspector: swarmService.Services,
		secrets:          swarmService.Secrets,
		networks:         swarmService.Networks,
		nodeManager:      swarmService.Nodes,
		history:          history,
		services:         services,
		nodes:            nodes,
		recommendations:  recommendations,
		alerts:           alerts,
		assistant:        assistantService,
		git:              gitRepository,
		composeLoader:    compose.NewFileLoaderWithReader(gitRepository.ReadFile),
	}
}
