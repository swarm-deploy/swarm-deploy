package stackloop

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/preparation"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func newTestReconciler(
	cfg *config.Config,
	gitSync gitx.Repository,
	lifecycle *deployments.Service,
	stackDeployer deployer.StackDeployer,
	swarmService *swarm.Swarm,
	eventDispatcher dispatcher.Dispatcher,
	deployMetrics metrics.Deploys,
	stateStore modelstore.Store,
	db storage.Database,
) *Reconciler {
	executor := deployments.NewController(
		lifecycle, stackDeployer, swarmService.Services,
		pruner.NewServicePruner(swarmService.Services, eventDispatcher, cfg.Spec.GitOps.Sync.Policy),
		deployMetrics,
	)
	rotator := NewRotator()
	preparer := preparation.New(cfg, gitSync, rotator)
	seedAppliedHashesForLegacyFixture(stateStore, preparer, db)
	return New(
		cfg, executor, swarmService, eventDispatcher, deployMetrics, stateStore, preparer, rotator, db,
	)
}

// Older stackloop fixtures represented an already-applied stack solely with
// GitOps SourceDigest. Seed equivalent hash evidence only when the fixture's
// current source still matches and no deployment hash state exists. Tests that
// exercise pending or failed deployments already have state and are untouched.
func seedAppliedHashesForLegacyFixture(
	stateStore modelstore.Store,
	preparer *preparation.Preparer,
	db storage.Database,
) {
	ctx := context.Background()
	runtime, err := stateStore.Read(ctx)
	if err != nil {
		return
	}
	store := deploymentstore.NewStore(db)
	for name, state := range runtime.Stacks {
		if state.SourceDigest == "" {
			continue
		}
		if _, stateErr := store.HashState(ctx, name); stateErr == nil ||
			!errors.Is(stateErr, deploymentstore.ErrNotFound) {
			continue
		}
		stack := config.StackSpec{Name: name, ComposeFile: name + ".yaml"}
		desired, loadErr := preparer.Load(ctx, stack, state.LastCommit)
		if loadErr != nil || desired.Digest != state.SourceDigest {
			continue
		}
		effective, prepareErr := preparer.Prepare(ctx, preparation.Request{Stack: stack, Desired: desired})
		if prepareErr != nil {
			continue
		}
		prepared, digestErr := deploymentmodel.Prepare(*effective)
		if digestErr != nil {
			continue
		}
		if seedErr := db.WithinTransaction(ctx, func(ctx context.Context) error {
			hashState, saveErr := store.SaveDesiredState(ctx, name, state.LastCommit, prepared)
			if saveErr != nil {
				return saveErr
			}
			pending := hashState.Pending()
			_, confirmErr := store.ConfirmAppliedState(
				ctx, name, state.LastCommit, prepared,
				pending.Services, pending.Removals, pending.StackResources,
			)
			return confirmErr
		}); seedErr != nil {
			panic(seedErr)
		}
	}
}

func mockRepositoryFiles(repository *gitx.MockRepository, dir string) {
	repository.EXPECT().WorkingDir().Return(dir).AnyTimes()
	repository.EXPECT().ReadFile(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, path string) ([]byte, error) {
			return os.ReadFile(filepath.Join(dir, path))
		}).AnyTimes()
}
