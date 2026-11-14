// Package deployments owns deployment persistence and execution infrastructure.
package deployments

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Module contains deployment lifecycle and execution services exposed to consumers.
type Module struct {
	// Controller owns synchronous deployment execution, completion, and recovery.
	Controller *Controller
	// Store persists and queries deployment lifecycle and sanitized desired state.
	Store *deploymentstore.Store
}

// Container supplies dependencies required by the Deployments module.
type Container interface {
	// GetStorage returns the shared database and transactor.
	GetStorage() storage.Database
	// GetEventModule returns transactional event publication infrastructure.
	GetEventModule() *event.Module
	// GetMetrics returns the shared application metrics.
	GetMetrics() *metrics.Group
	// GetSwarm returns the shared Docker Swarm services.
	GetSwarm() *swarm.Swarm
}

// InitModule initializes deployment execution infrastructure.
func InitModule(_ context.Context, cfg *config.Config, cnt Container) (*Module, error) {
	stackDeployer := deployer.NewDeployer(
		cfg.Spec.Deployments.PollInterval.Value,
		cfg.Spec.Deployments.InitJobs.MaxDuration.Value,
		cnt.GetSwarm(),
		cnt.GetMetrics().Deploys,
		deployer.NewSecretResolver(
			deployer.NewAgeDecoder(cfg.Spec.GitOps.Secrets.Age.IdentityFile.Content),
			deployer.PlaintextDecoder{},
		),
	)

	lifecycle := NewService(cnt.GetStorage(), cnt.GetEventModule().Dispatcher)
	controller := NewController(
		lifecycle,
		stackDeployer,
		cnt.GetSwarm().Services,
		pruner.NewServicePruner(
			cnt.GetSwarm().Services, cnt.GetEventModule().Dispatcher, cfg.Spec.GitOps.Sync.Policy,
		),
		cnt.GetMetrics().Deploys,
	)
	return &Module{Controller: controller, Store: lifecycle.store}, nil
}
