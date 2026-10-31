package deployer

import (
	"context"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	stackpkg "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/stack"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type applyPayload struct {
	deadline time.Time
	baseline []swarm.StackService
}

func (p *applyPayload) stageContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithDeadline(ctx, p.deadline)
}

type deployStackPayload struct {
	applyPayload
	stackName         string
	sourceComposePath string
	deployComposePath string
	desired           compose.Compose
	resolved          resolvedResources
	result            Result
}

type deployServicePayload struct {
	applyPayload
	stackName         string
	sourceComposePath string
	serviceName       string
	desired           compose.Compose
	deploy            compose.Compose
	prepared          *stackpkg.PreparedService
	resolved          resolvedResources
	result            Result
}

func (p *deployServicePayload) cleanup() {
	if p.prepared != nil {
		p.prepared.Cleanup()
	}
}
