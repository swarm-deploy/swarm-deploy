// Package deployments owns deployment persistence and execution infrastructure.
package deployments

import (
	"context"

	"github.com/docker/docker/client"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Module contains deployment execution infrastructure exposed to consumers.
type Module struct {
	// Deployer executes stack and service deployments.
	Deployer deployer.StackDeployer
}

// Container supplies dependencies required by the Deployments module.
type Container interface {
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

	return &Module{Deployer: stackDeployer}, nil
}
