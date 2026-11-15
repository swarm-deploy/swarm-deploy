package stackloop

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/preparation"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
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
	return New(
		cfg, executor, swarmService, eventDispatcher, deployMetrics, stateStore, preparer, rotator, db,
	)
}

func mockRepositoryFiles(repository *gitx.MockRepository, dir string) {
	repository.EXPECT().WorkingDir().Return(dir).AnyTimes()
	repository.EXPECT().ReadFile(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, path string) ([]byte, error) {
			return os.ReadFile(filepath.Join(dir, path))
		}).AnyTimes()
}

// seedAppliedDesired gives drift-focused tests an already applied effective revision.
func seedAppliedDesired(t *testing.T, r *Reconciler, stack config.StackSpec) {
	t.Helper()
	desired, err := r.preparer.Load(t.Context(), stack, "previous")
	require.NoError(t, err)
	prepared, err := r.preparer.Prepare(t.Context(), preparation.Request{Stack: stack, Desired: desired})
	require.NoError(t, err)
	require.NoError(t, r.stateStore.Update(t.Context(), func(runtime *model.Runtime) {
		state := runtime.Stacks[stack.Name]
		state.SourceDigest = desired.Digest
		if state.Services == nil {
			state.Services = map[string]model.Service{}
		}
		for _, service := range prepared.Compose.Services {
			current := state.Services[service.Name]
			current.AppliedDigest, current.AppliedCommit = prepared.ServiceDigests[service.Name], "previous"
			current.Image = service.Image
			state.Services[service.Name] = current
		}
		runtime.Stacks[stack.Name] = state
	}))
}
