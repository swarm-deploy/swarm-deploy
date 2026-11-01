package deployer

import (
	"context"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type applyPayload struct {
	deadline time.Time
	baseline []swarm.StackService
	changed  []string
}

func (p *applyPayload) stageContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithDeadline(ctx, p.deadline)
}

type deployStackPayload struct {
	applyPayload
	stackName          string
	sourceComposePath  string
	deployComposePath  string
	desired            compose.Compose
	converted          *converter.StackSpec
	resolved           resolvedResources
	result             Result
	retryFailedUpdates bool
	unresolvedServices []string
}

type deployServicePayload struct {
	applyPayload
	stackName         string
	sourceComposePath string
	serviceName       string
	desired           compose.Compose
	deploy            compose.Compose
	prepared          compose.Compose
	converted         *converter.StackSpec
	resolved          resolvedResources
	result            Result
}
