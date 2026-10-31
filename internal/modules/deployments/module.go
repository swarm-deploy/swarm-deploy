// Package deployments owns deployment persistence and execution infrastructure.
package deployments

import (
	"context"

	"github.com/docker/docker/client"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Module contains deployment lifecycle and execution services exposed to consumers.
type Module struct {
	// Controller owns synchronous deployment execution, completion, and recovery.
	Controller *Controller
}

// Container supplies dependencies required by the Deployments module.
type Container interface {
	// GetStorage returns the shared database and transactor.
	GetStorage() storage.Database
	// GetEventModule returns transactional event publication infrastructure.
	GetEventModule() *event.Module
	// GetDockerClient returns the shared Docker API client.
	GetDockerClient() *client.Client
	// GetFileSystem returns the shared filesystem.
	GetFileSystem() fs.FileSystem
	// GetMetrics returns the shared application metrics.
	GetMetrics() *metrics.Group
	// GetSwarm returns the shared Docker Swarm services.
	GetSwarm() *swarm.Swarm
}

// InitModule initializes deployment execution infrastructure.
func InitModule(_ context.Context, cfg *config.Config, cnt Container) (*Module, error) {
	stackDeployer := deployer.NewDeployer(
		cfg.Spec.Swarm.InitJobPollEvery.Value,
		cfg.Spec.Swarm.InitJobMaxDuration.Value,
		cnt.GetDockerClient(),
		cnt.GetSwarm(),
		cnt.GetMetrics().Deploys,
		deployer.NewSecretResolver(
			cnt.GetFileSystem(),
			deployer.NewAgeDecoder(cfg.Spec.SecretRotation.Age.IdentityFile.Content),
			deployer.PlaintextDecoder{},
		),
	)

	lifecycle := NewService(cnt.GetStorage(), cnt.GetEventModule().Dispatcher)
	controller := NewController(
		lifecycle,
		stackDeployer,
		cnt.GetSwarm().Services,
		pruner.NewServicePruner(
			cnt.GetSwarm().Services, cnt.GetEventModule().Dispatcher, cfg.Spec.Sync.Policy,
		),
		cnt.GetMetrics().Deploys,
	)
	return &Module{Controller: controller}, nil
}
