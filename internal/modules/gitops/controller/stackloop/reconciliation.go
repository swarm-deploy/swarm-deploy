package stackloop

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
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

// StackReconciler loads stack sources and applies them to Docker Swarm.
//
// Load is the only operation that reads the repository working tree. Reconcile and Cleanup work
// exclusively with the definition returned by Load, so they may run concurrently with Git updates.
// Reconcile and Cleanup for the same stack must not run concurrently; different stacks may.
type StackReconciler interface {
	// Load reads and validates the source Compose definition of a stack from the working tree.
	Load(ctx context.Context, stack config.StackSpec) (*compose.File, error)
	// Reconcile applies the stack source captured in the request.
	Reconcile(ctx context.Context, req ReconciliationRequest) error
	// Cleanup removes stale rotated configs and secrets of a stack using its loaded source.
	Cleanup(ctx context.Context, stack config.StackSpec, source *compose.File) error
}

type ReconciliationRequest struct {
	// Stack is the desired stack specification to reconcile.
	Stack config.StackSpec
	// Commit is the git revision associated with this reconciliation attempt.
	Commit string
	// IsManual reports whether reconciliation was triggered manually.
	IsManual bool
	// Source is the source Compose definition loaded at Commit. It is nil when SourceErr is set.
	Source *compose.File
	// SourceErr is the failure to load Source. Reconcile records it as a preparation failure.
	SourceErr error
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
