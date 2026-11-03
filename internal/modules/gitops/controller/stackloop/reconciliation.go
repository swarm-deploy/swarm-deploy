package stackloop

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/preparation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// StackReconciler applies stack definitions to Docker Swarm.
//
// Stack sources are read at the requested Git revision, never from the working tree, so operations
// may run while Git is updated. Operations for the same stack must not run concurrently; different
// stacks may.
type StackReconciler interface {
	// Reconcile applies the stack definition at the requested commit.
	Reconcile(ctx context.Context, req ReconciliationRequest) error
	// Cleanup removes stale rotated configs and secrets of a stack using its definition at commit.
	Cleanup(ctx context.Context, stack config.StackSpec, commit string) error
}

type ReconciliationRequest struct {
	// Stack is the desired stack specification to reconcile.
	Stack config.StackSpec
	// Commit is the git revision whose stack definition is applied.
	Commit string
	// IsManual reports whether reconciliation was triggered manually.
	IsManual bool
}

func NewStackReconciler(
	cfg *config.Config,
	executor deployments.Executor,
	swarmService *swarm.Swarm,
	eventDispatcher dispatcher.Dispatcher,
	deployMetrics metrics.Deploys,
	stateStore modelstore.Store,
	preparer *preparation.Preparer,
	rotator *Rotator,
	db storage.Database,
) StackReconciler {
	reconciler := New(
		cfg,
		executor,
		swarmService,
		eventDispatcher,
		deployMetrics,
		stateStore,
		preparer, rotator, db,
	)

	tp, tracingEnabled := tracing.GetTracerProvider()
	if !tracingEnabled {
		return reconciler
	}

	return NewTraceableReconciler(tp, reconciler)
}
