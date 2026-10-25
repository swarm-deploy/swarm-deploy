package stackloop

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
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
			service := deployment.NewService(db, &dispatcher.NopDispatcher{})
			attempt, err := service.StartIfChanged(t.Context(), "app", "commit", compose.File{
				Compose: compose.Compose{Services: compose.Services{{Name: "api", Image: "api:1"}}},
			}, true)
			require.NoError(t, err)
			require.NotNil(t, attempt)
			_, err = db.Exec(t.Context(), `CREATE TRIGGER reject_init_job BEFORE INSERT ON deployment_init_jobs
				BEGIN SELECT RAISE(ABORT,'injected persistence failure'); END`)
			require.NoError(t, err)

			ctrl := gomock.NewController(t)
			stackDeployer := deployer.NewMockStackDeployer(ctrl)
			stackDeployer.EXPECT().DeployStack(
				gomock.Any(), "app", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
			).DoAndReturn(func(
				ctx context.Context,
				_, _, _ string,
				_ compose.Compose,
				options deployer.DeployOptions,
			) error {
				now := time.Now().UTC()
				options.InitJobFinished(ctx, deploymentmodel.InitJobExecution{
					Ordinal: 1, ServiceName: "api", JobName: "migrate", Status: deploymentmodel.InitJobSucceeded,
					StartedAt: now, FinishedAt: now, Logs: []byte("first"), LogsAvailable: true,
				})
				options.InitJobFinished(ctx, deploymentmodel.InitJobExecution{
					Ordinal: 2, ServiceName: "api", JobName: "seed", Status: testCase.lastStatus,
					StartedAt: now, FinishedAt: now, Logs: []byte("second"), LogsAvailable: true,
				})
				return testCase.originalErr
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
			var count int
			require.NoError(t, db.Get(t.Context(), &count, "SELECT count(*) FROM deployment_init_jobs"))
			assert.Zero(t, count)
		})
	}
}

func TestPersistInitJobExecutionUsesDetachedBoundedContext(t *testing.T) {
	db := testutil.OpenSQLite(t)
	service := deployment.NewService(db, &dispatcher.NopDispatcher{})
	attempt, err := service.StartIfChanged(t.Context(), "app", "commit", compose.File{
		Compose: compose.Compose{Services: compose.Services{{Name: "api", Image: "api:1"}}},
	}, true)
	require.NoError(t, err)
	require.NotNil(t, attempt)
	reconciler := &Reconciler{deployments: service}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	now := time.Now().UTC()

	reconciler.persistInitJobExecution(ctx, deploymentmodel.InitJobExecution{
		AttemptID: attempt.ID, Ordinal: 1, ServiceName: "api", JobName: "migrate",
		Status: deploymentmodel.InitJobInterrupted, StartedAt: now, FinishedAt: now,
		Logs: []byte("final output"), LogsAvailable: true,
	})

	logs, err := deploymentstore.NewStore(db).InitJobLogs(t.Context(), attempt.DeploymentID, attempt.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, []byte("final output"), logs)
}
