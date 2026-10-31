package handlers

import (
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	alertstore "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	recommendationstore "github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/modelstore"
	swarmnode "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/node"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secretmanager"
	secretstore "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secretservice"
	servicestore "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/logparser"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type handler struct {
	deployments *deploymentstore.Store
	generated.UnimplementedHandler
	stackProvider    config.StackProvider
	stateStore       modelstore.ReadStore
	control          *controller.Controller
	serviceInspector swarm.ServiceManager
	eventDispatcher  dispatcher.Dispatcher
	taskLogs         *logparser.Service
	secrets          secretstore.Store
	secretRelations  *secretservice.Resolver
	secretManagers   *secretmanager.Domain
	networks         swarm.NetworkManager
	dockerSecrets    swarm.SecretManager
	dockerConfigs    swarm.ConfigManager
	nodeManager      swarm.NodeManager
	history          history.Repository
	services         servicestore.Store
	nodes            swarmnode.Repository
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
	history history.Repository,
	services servicestore.Store,
	nodes swarmnode.Repository,
	secrets secretstore.Store,
	secretManagers *secretmanager.Domain,
	recommendations recommendationstore.Store,
	alerts alertstore.Store,
	assistantService assistant.Assistant,
	deployments *deploymentstore.Store,
	taskLogs *logparser.Service,
	eventDispatcher dispatcher.Dispatcher,
) *handler {
	return &handler{deployments: deployments,
		stackProvider:    stackProvider,
		stateStore:       stateStore,
		control:          control,
		serviceInspector: swarmService.Services,
		taskLogs:         taskLogs,
		eventDispatcher:  eventDispatcher,
		secrets:          secrets,
		secretRelations:  secretservice.NewResolver(services, secrets),
		secretManagers:   secretManagers,
		networks:         swarmService.Networks,
		dockerSecrets:    swarmService.Secrets,
		dockerConfigs:    swarmService.Configs,
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
