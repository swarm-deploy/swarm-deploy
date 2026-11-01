package deployments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	pipe "github.com/artarts36/gopipe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

type failedJobWithLogs struct{ logs []string }

func (e *failedJobWithLogs) Error() string  { return "job exited with status 1" }
func (e *failedJobWithLogs) Logs() []string { return e.logs }

func TestInitJobDiagnosticsAreBoundedAndSanitized(t *testing.T) {
	failure := &failedJobWithLogs{logs: []string{
		"migration failed: missing table",
		"token=do-not-persist",
		"database_url=postgres://do-not-persist",
		"password do-not-persist",
		strings.Repeat("x", 3000),
	}}
	wrapped := fmt.Errorf("deploy: %w", failure)
	diagnostic, found := withInitJobLogs(wrapped)
	require.True(t, found)
	require.ErrorIs(t, diagnostic, failure)
	assert.Contains(t, diagnostic.Error(), "migration failed: missing table")
	assert.Contains(t, diagnostic.Error(), "init job logs:")
	assert.NotContains(t, diagnostic.Error(), "do-not-persist")
	assert.LessOrEqual(t, len(diagnostic.Error()),
		maxInitJobDiagnosticBytes+len(wrapped.Error())+len("\ninit job logs:\n"))

	for _, testCase := range []struct {
		name string
		logs []string
		want []string
	}{
		{name: "empty"},
		{name: "filtered", logs: []string{"password=hidden", "  "}},
		{name: "multiline", logs: []string{" first\n\n second "}, want: []string{"first", "second"}},
		{
			name: "byte limit includes separators",
			logs: []string{"first", strings.Repeat("x", maxInitJobDiagnosticBytes)},
			want: []string{"first", strings.Repeat("x", maxInitJobDiagnosticBytes-len("first\n"))},
		},
		{
			name: "line limit", logs: []string{strings.Repeat("line\n", maxInitJobDiagnosticLines+1)},
			want: strings.Split(strings.TrimSuffix(strings.Repeat("line\n", maxInitJobDiagnosticLines), "\n"), "\n"),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			lines := initJobDiagnosticLines(testCase.logs)
			assert.Equal(t, testCase.want, lines)
			assert.LessOrEqual(t, len(strings.Join(lines, "\n")), maxInitJobDiagnosticBytes)
		})
	}
}

func TestControllerExecuteCompletesSuccessAndAvoidsDuplicateExecution(t *testing.T) {
	db, lifecycle, bus := openService(t)
	mocks := gomock.NewController(t)
	stackDeployer := deployer.NewMockStackDeployer(mocks)
	services := swarm.NewMockServiceManager(mocks)
	controller := NewController(
		lifecycle, stackDeployer, services,
		pruner.NewServicePruner(services, bus, config.SyncPolicySpec{}), &metrics.NopDeploys{},
	)
	req := executionRequest("api:1")
	stackDeployer.EXPECT().DeployStack(gomock.Any(), "app", "app.yaml", "rendered.yaml", gomock.Any()).
		Return(deployer.Result{}, nil)
	services.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, nil)

	result, err := controller.Execute(t.Context(), req)
	require.NoError(t, err)
	require.NotNil(t, result.Attempt)
	assert.Equal(t, deploymentmodel.Succeeded, result.Status)
	stored, err := deploymentstore.NewStore(db).Get(t.Context(), result.Attempt.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Succeeded, stored.Status)

	unchanged, err := controller.Execute(t.Context(), req)
	require.NoError(t, err)
	assert.Nil(t, unchanged.Attempt)
}

func TestControllerExecuteClassifiesAndPersistsFailures(t *testing.T) {
	tests := []struct {
		name        string
		deployErr   error
		result      deployer.Result
		setupLive   func(*swarm.MockServiceManager)
		wantStatus  deploymentmodel.Status
		wantPhase   deploymentmodel.Phase
		wantCode    string
		wantInitJob bool
	}{
		{
			name: "known paused update",
			deployErr: &pipe.StepError{StepName: "wait for service convergence",
				Err: fmt.Errorf("service api paused; failed-task exit_code=1: %w", waiter.ErrConvergenceFailed)},
			wantStatus: deploymentmodel.Failed, wantPhase: deploymentmodel.PhaseVerification, wantCode: "convergence_failed",
		},
		{
			name: "apply", deployErr: errors.New("apply failed"),
			wantStatus: deploymentmodel.Failed, wantPhase: deploymentmodel.PhaseApply, wantCode: "apply_failed",
		},
		{
			name:      "init job",
			deployErr: &pipe.StepError{StepName: "run init jobs", Err: errors.New("job failed")},
			result: deployer.Result{InitJobs: []deployer.InitJobResult{{
				ServiceName: "api", JobName: "migrate", Status: deployer.InitJobFailed,
				StartedAt: time.Now().Add(-time.Second), FinishedAt: time.Now(), Logs: []byte("migration failed"),
				LogsAvailable: true,
			}}},
			wantStatus: deploymentmodel.Failed, wantPhase: deploymentmodel.PhaseApply,
			wantCode: "init_failed", wantInitJob: true,
		},
		{
			name: "service waiter",
			deployErr: &pipe.StepError{
				StepName: "wait for service convergence", Err: errors.New("service did not converge"),
			},
			wantStatus: deploymentmodel.Interrupted, wantPhase: deploymentmodel.PhaseVerification,
			wantCode: "verification_unknown",
		},
		{
			name: "live state observation",
			setupLive: func(services *swarm.MockServiceManager) {
				services.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return(nil, errors.New("inspect failed"))
			},
			wantStatus: deploymentmodel.Interrupted, wantPhase: deploymentmodel.PhaseVerification,
			wantCode: "verification_unknown",
		},
		{
			name: "cleanup",
			setupLive: func(services *swarm.MockServiceManager) {
				services.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{{
					ID: "old", Name: "old", Labels: map[string]string{
						labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue,
					},
				}}, nil)
				services.EXPECT().Remove(gomock.Any(), "old").Return(errors.New("remove failed"))
			},
			wantStatus: deploymentmodel.Failed, wantPhase: deploymentmodel.PhaseCleanup, wantCode: "prune_failed",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			db, lifecycle, bus := openService(t)
			mocks := gomock.NewController(t)
			stackDeployer := deployer.NewMockStackDeployer(mocks)
			services := swarm.NewMockServiceManager(mocks)
			controller := NewController(
				lifecycle, stackDeployer, services,
				pruner.NewServicePruner(services, bus, config.SyncPolicySpec{Prune: true}), &metrics.NopDeploys{},
			)
			stackDeployer.EXPECT().DeployStack(gomock.Any(), "app", "app.yaml", "rendered.yaml", gomock.Any()).
				Return(testCase.result, testCase.deployErr)
			if testCase.deployErr == nil {
				testCase.setupLive(services)
			}

			result, err := controller.Execute(t.Context(), executionRequest("api:1"))
			require.Error(t, err)
			require.NotNil(t, result.Attempt)
			assert.Equal(t, testCase.wantCode, result.ErrorCode)
			stored, readErr := deploymentstore.NewStore(db).Get(t.Context(), result.Attempt.DeploymentID)
			require.NoError(t, readErr)
			assert.Equal(t, testCase.wantStatus, stored.Status)
			assert.Equal(t, testCase.wantPhase, stored.Phase)
			if testCase.wantInitJob {
				require.Len(t, stored.Attempts, 1)
				require.Len(t, stored.Attempts[0].InitJobs, 1)
				assert.Equal(t, "migrate", stored.Attempts[0].InitJobs[0].JobName)
			}
		})
	}
}

func TestControllerExecuteRetriesAndSupersedesThroughCanonicalLifecycle(t *testing.T) {
	db, lifecycle, bus := openService(t)
	mocks := gomock.NewController(t)
	stackDeployer := deployer.NewMockStackDeployer(mocks)
	services := swarm.NewMockServiceManager(mocks)
	controller := NewController(
		lifecycle, stackDeployer, services,
		pruner.NewServicePruner(services, bus, config.SyncPolicySpec{}), &metrics.NopDeploys{},
	)

	stackDeployer.EXPECT().DeployStack(gomock.Any(), "app", "app.yaml", "rendered.yaml", gomock.Any()).
		Return(deployer.Result{}, errors.New("failed"))
	failed, err := controller.Execute(t.Context(), executionRequest("api:1"))
	require.Error(t, err)
	stackDeployer.EXPECT().DeployStack(gomock.Any(), "app", "app.yaml", "rendered.yaml", gomock.Any(), deployer.StackOptions{RetryFailedUpdates: true, UnresolvedServices: []string{"api"}}).
		Return(deployer.Result{}, nil)
	services.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return(nil, nil)
	reqRetry := executionRequest("api:1")
	reqRetry.ManualRetry = true
	retry, err := controller.Execute(t.Context(), reqRetry)
	require.NoError(t, err)
	assert.Equal(t, failed.Attempt.DeploymentID, retry.Attempt.DeploymentID)
	assert.Equal(t, 2, retry.Attempt.Number)

	running, err := lifecycle.StartIfChanged(t.Context(), "other", "one", desiredServices("api"), true)
	require.NoError(t, err)
	require.NotNil(t, running)
	req := executionRequest("worker:1")
	req.Stack.Name = "other"
	req.Commit = "two"
	stackDeployer.EXPECT().DeployStack(gomock.Any(), "other", "app.yaml", "rendered.yaml", gomock.Any(), deployer.StackOptions{UnresolvedServices: []string{"api"}}).
		Return(deployer.Result{}, nil)
	services.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return(nil, nil)
	newer, err := controller.Execute(t.Context(), req)
	require.NoError(t, err)
	assert.NotEqual(t, running.DeploymentID, newer.Attempt.DeploymentID)
	previous, err := deploymentstore.NewStore(db).Get(t.Context(), running.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Superseded, previous.Status)
}

func TestControllerRecoveryInterruptsRunningAttemptsIdempotently(t *testing.T) {
	db, lifecycle, _ := openService(t)
	attempt, err := lifecycle.StartIfChanged(t.Context(), "app", "commit", desiredServices("api"), true)
	require.NoError(t, err)
	controller := NewController(lifecycle, nil, nil, nil, &metrics.NopDeploys{})

	require.NoError(t, controller.Recover(t.Context()))
	require.NoError(t, controller.Recover(t.Context()))
	stored, err := deploymentstore.NewStore(db).Get(t.Context(), attempt.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Interrupted, stored.Status)
	require.Len(t, stored.Attempts, 1)
	assert.Equal(t, deploymentmodel.Interrupted, stored.Attempts[0].Status)
}

func TestControllerPersistsInitJobsWithDetachedBoundedContexts(t *testing.T) {
	db, lifecycle, _ := openService(t)
	attempt, err := lifecycle.StartIfChanged(t.Context(), "app", "commit", desiredServices("api"), true)
	require.NoError(t, err)
	controller := NewController(lifecycle, nil, nil, nil, &metrics.NopDeploys{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	now := time.Date(2026, time.October, 31, 12, 0, 0, 0, time.UTC)

	controller.persistInitJobs(ctx, attempt.ID, []deployer.InitJobResult{
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
	assert.Equal(t, []int{1, 2}, []int{
		stored.Attempts[0].InitJobs[0].Ordinal, stored.Attempts[0].InitJobs[1].Ordinal,
	})
	assert.Equal(t, deploymentmodel.InitJobInterrupted, stored.Attempts[0].InitJobs[1].Status)
}

func executionRequest(image string) ExecuteRequest {
	return ExecuteRequest{
		Stack: config.StackSpec{Name: "app"}, Commit: "commit",
		Desired: compose.File{Path: "app.yaml", Digest: image, Compose: compose.Compose{
			Services: compose.Services{{Name: "api", Image: image}},
		}},
		DeployComposePath: "rendered.yaml", SourceChanged: true,
	}
}
