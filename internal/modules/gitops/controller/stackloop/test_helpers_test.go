package stackloop

import (
	"context"
	"os"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/preparation"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
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
	fileSystem fs.FileSystem,
	db storage.Database,
) *Reconciler {
	executor := deployments.NewController(
		lifecycle, stackDeployer, swarmService.Services,
		pruner.NewServicePruner(swarmService.Services, eventDispatcher, cfg.Spec.Sync.Policy),
		deployMetrics,
	)
	rotator := NewRotator()
	preparer := preparation.New(cfg, gitSync, fileSystem, rotator)
	return New(
		cfg, executor, swarmService, eventDispatcher, deployMetrics, stateStore, preparer, rotator, db,
	)
}

// expectRevisionReads serves repository files at any revision from the test working tree.
func expectRevisionReads(repository *gitx.MockRepository, dir string) {
	repository.EXPECT().ReadFileAt(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, path string) ([]byte, error) {
			return os.ReadFile(filepath.Join(dir, path))
		}).AnyTimes()
}
