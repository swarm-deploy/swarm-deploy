//go:generate mockgen -source=$GOFILE -destination=mock.go -package=deployer

package deployer

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

// StackDeployer reconciles one stack via deploy command execution.
type StackDeployer interface {
	// DeployStack applies a rendered compose file for the given stack.
	DeployStack(ctx context.Context, stackName, composePath string, services []compose.Service) error
}
