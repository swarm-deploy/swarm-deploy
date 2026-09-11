package stackloop

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type StackReconciler interface {
	Reconcile(ctx context.Context, req ReconciliationRequest) error
}

type ReconciliationRequest struct {
	// Stack is the desired stack specification to reconcile.
	Stack config.StackSpec
	// Commit is the git revision associated with this reconciliation attempt.
	Commit string
	// IsManual reports whether reconciliation was triggered manually.
	IsManual bool
}

func NewStackReconciler(
	cfg *config.Config,
	gitSync gitx.Repository,
	stackDeployer deployer.StackDeployer,
	swarmService *swarm.Swarm,
	eventDispatcher dispatcher.Dispatcher,
	deployMetrics metrics.Deploys,
	stateStore modelstore.Store,
	filesystem fs.FileSystem,
) StackReconciler {
	reconciler := New(
		cfg,
		gitSync,
		stackDeployer,
		swarmService,
		eventDispatcher,
		deployMetrics,
		stateStore,
		filesystem,
	)

	tp, tracingEnabled := tracing.GetTracerProvider()
	if !tracingEnabled {
		return reconciler
	}

	return NewTraceableReconciler(tp, reconciler)
}
