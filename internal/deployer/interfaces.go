//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=$GOFILE -destination=mock.go -package=deployer

package deployer

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

// StackDeployer reconciles one stack via deploy command execution.
type StackDeployer interface {
	// DeployStack reconciles init-job resources from sourceComposePath and applies deployComposePath.
	DeployStack(ctx context.Context, stackName, sourceComposePath, deployComposePath string, desired compose.Compose) error
}
