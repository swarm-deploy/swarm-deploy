package deployer

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	stackpkg "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/stack"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type applyPayload struct {
	ctx      context.Context
	cancel   context.CancelFunc
	baseline []swarm.StackService
}

func (p *applyPayload) cancelApply() {
	if p.cancel != nil {
		p.cancel()
	}
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
	p.cancelApply()
	if p.prepared != nil {
		p.prepared.Cleanup()
	}
}
