package stackloop

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
	"go.uber.org/mock/gomock"
)

func TestDeployStackIgnoresInitJobPersistenceFailure(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		originalErr error
		lastStatus  deploymentmodel.InitJobStatus
	}{
		{name: "successful deployment", lastStatus: deploymentmodel.InitJobSucceeded},
		{name: "failed deployment", originalErr: assert.AnError, lastStatus: deploymentmodel.InitJobFailed},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db := testutil.OpenSQLite(t)
			publisher := &dispatcher.NopDispatcher{}
			service := deployment.NewService(db, publisher)
			attempt, err := deployments.NewService(db, publisher).StartIfChanged(t.Context(), "app", "commit", compose.File{
				Compose: compose.Compose{Services: compose.Services{{Name: "api", Image: "api:1"}}},
			}, true, service.ProjectInterruption)
			require.NoError(t, err)
			require.NotNil(t, attempt)
			_, err = db.Exec(t.Context(), `CREATE TRIGGER reject_first_init_job BEFORE INSERT ON deployment_init_jobs
				WHEN NEW.ordinal = 1 BEGIN SELECT RAISE(ABORT,'injected persistence failure'); END`)
			require.NoError(t, err)

			ctrl := gomock.NewController(t)
			stackDeployer := deployer.NewMockStackDeployer(ctrl)
			stackDeployer.EXPECT().DeployStack(
				gomock.Any(), "app", gomock.Any(), gomock.Any(), gomock.Any(),
			).DoAndReturn(func(
				_ context.Context,
				_, _, _ string,
				_ compose.Compose,
			) (deployer.Result, error) {
				now := time.Now().UTC()
				return deployer.Result{InitJobs: []deployer.InitJobResult{
					{ServiceName: "api", JobName: "migrate", Status: deployer.InitJobSucceeded,
						StartedAt: now, FinishedAt: now, Logs: []byte("first"), LogsAvailable: true},
					{ServiceName: "api", JobName: "seed", Status: deployer.InitJobStatus(testCase.lastStatus),
						StartedAt: now, FinishedAt: now, Logs: []byte("second"), LogsAvailable: true},
				}}, testCase.originalErr
			})
			reconciler := &Reconciler{deployer: stackDeployer, deployments: service}
			payload := &pipelinePayload{
				Attempt: attempt, Stack: config.StackSpec{Name: "app"},
				Desired:           &compose.File{Path: "compose.yaml", Compose: compose.Compose{}},
				DeployComposePath: "compose.yaml",
			}

			err = reconciler.deployStack(t.Context(), payload)
			if testCase.originalErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, testCase.originalErr)
			}
			store := deploymentstore.NewStore(db)
			stored, err := store.Get(t.Context(), attempt.DeploymentID)
			require.NoError(t, err)
			require.Len(t, stored.Attempts, 1)
			assert.Len(t, stored.Attempts[0].InitJobs, 1,
				"a failed INSERT must not prevent later results from being saved")
			logs, err := store.InitJobLogs(t.Context(), attempt.DeploymentID, attempt.ID, 2)
			require.NoError(t, err)
			assert.Equal(t, []byte("second"), logs)
		})
	}
}

func TestPersistInitJobsUsesDetachedBoundedContextAndAssignsOrdinals(t *testing.T) {
	db := testutil.OpenSQLite(t)
	publisher := &dispatcher.NopDispatcher{}
	service := deployment.NewService(db, publisher)
	attempt, err := deployments.NewService(db, publisher).StartIfChanged(t.Context(), "app", "commit", compose.File{
		Compose: compose.Compose{Services: compose.Services{{Name: "api", Image: "api:1"}}},
	}, true, service.ProjectInterruption)
	require.NoError(t, err)
	require.NotNil(t, attempt)
	reconciler := &Reconciler{deployments: service}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	now := time.Now().UTC()

	reconciler.persistInitJobs(ctx, attempt.ID, []deployer.InitJobResult{
		{ServiceName: "api", JobName: "migrate", Status: deployer.InitJobSucceeded,
			StartedAt: now, FinishedAt: now, Logs: []byte("first output"), LogsAvailable: true},
		{ServiceName: "worker", JobName: "warm-cache", Status: deployer.InitJobInterrupted,
			StartedAt: now, FinishedAt: now, Logs: []byte("final output"), LogsAvailable: true},
	})

	store := deploymentstore.NewStore(db)
	logs, err := store.InitJobLogs(t.Context(), attempt.DeploymentID, attempt.ID, 2)
	require.NoError(t, err)
	assert.Equal(t, []byte("final output"), logs)
	stored, err := store.Get(t.Context(), attempt.DeploymentID)
	require.NoError(t, err)
	require.Len(t, stored.Attempts, 1)
	require.Len(t, stored.Attempts[0].InitJobs, 2)
	assert.Equal(t, attempt.ID, stored.Attempts[0].InitJobs[0].AttemptID)
	assert.Equal(t, []int{1, 2}, []int{
		stored.Attempts[0].InitJobs[0].Ordinal,
		stored.Attempts[0].InitJobs[1].Ordinal,
	})
	assert.Equal(t, deploymentmodel.InitJobInterrupted, stored.Attempts[0].InitJobs[1].Status)
}
