package stackloop

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/drift"
)

type pauseSelfHealExecutor struct{ services []string }

func (e *pauseSelfHealExecutor) Execute(context.Context, deployments.ExecuteRequest) (deployments.ExecuteResult, error) {
	return deployments.ExecuteResult{}, nil
}

func (e *pauseSelfHealExecutor) ExecuteService(
	_ context.Context, request deployments.ExecuteServiceRequest,
) (deployer.Result, error) {
	e.services = append(e.services, request.ServiceName)
	return deployer.Result{}, nil
}

func (e *pauseSelfHealExecutor) Recover(context.Context) error { return nil }

func TestSelfHealSkipsPreservedServiceAndRepairsPeer(t *testing.T) {
	executor := &pauseSelfHealExecutor{}
	reconciler := &Reconciler{executor: executor, cfg: &config.Config{Spec: config.Spec{
		GitOps: config.GitOpsSpec{Sync: config.SyncSpec{Policy: config.SyncPolicySpec{SelfHeal: true}}},
	}}}
	payload := &pipelinePayload{
		Stack: config.StackSpec{Name: "app"},
		Desired: &compose.File{Compose: compose.Compose{Services: compose.Services{
			{Name: "api", Image: "api:1"}, {Name: "worker", Image: "worker:1"},
		}}},
		PreservedServices: []string{"api"},
		Drift: map[string]drift.ServiceDrift{
			"api": {ServiceName: "api"}, "worker": {ServiceName: "worker"},
		},
		PendingSelfHeal: map[string]bool{},
	}

	require.NoError(t, reconciler.selfHealDrift(t.Context(), payload))
	assert.Equal(t, []string{"worker"}, executor.services)
	assert.NotContains(t, payload.Remediations, "api")
}
