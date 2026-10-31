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

type StackReconciler interface {
	Reconcile(ctx context.Context, req ReconciliationRequest) error
	Cleanup(ctx context.Context, stack config.StackSpec) error
}

type ReconciliationRequest struct {
	// Stack is the desired stack specification to reconcile.
	Stack config.StackSpec
	// Commit is the git revision associated with this reconciliation attempt.
	Commit string
	// IsManual reports whether reconciliation was triggered manually.
	IsManual bool
	// SourceDirectory selects an immutable source snapshot for this operation.
	SourceDirectory string
	// OriginalSourceDirectory identifies the checkout path before snapshotting.
	OriginalSourceDirectory string
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
