package deployments

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	pipe "github.com/artarts36/gopipe"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

type failedJobWithLogs struct{ logs []string }

func (e *failedJobWithLogs) Error() string  { return "job exited with status 1" }
func (e *failedJobWithLogs) Logs() []string { return e.logs }

func TestInitJobDiagnosticsAreBoundedAndSanitized(t *testing.T) {
	failure := &failedJobWithLogs{logs: []string{
		"migration failed: missing table",
		`ERROR failed to load config err="unknown configuration field" password="do-not-persist"`,
		"token=do-not-persist",
		"database_url=postgres://do-not-persist",
		"password do-not-persist",
		strings.Repeat(".", 3000),
	}}
	wrapped := fmt.Errorf("deploy: %w", failure)
	diagnostic, found := withInitJobLogs(wrapped)
	require.True(t, found)
	require.ErrorIs(t, diagnostic, failure)
	assert.Contains(t, diagnostic.Error(), "migration failed: missing table")
	assert.Contains(t, diagnostic.Error(), `err="unknown configuration field" password=****`)
	assert.Contains(t, diagnostic.Error(), "init job logs:")
	assert.NotContains(t, diagnostic.Error(), "do-not-persist")
	assert.LessOrEqual(t, len(diagnostic.Error()),
		faults.MaxDiagnosticLogBytes+len(wrapped.Error())+len("\ninit job logs:\n"))

	for _, testCase := range []struct {
		name string
		logs []string
		want []string
	}{
		{name: "empty"},
		{name: "masked", logs: []string{"password=hidden", "  "}, want: []string{"password=****"}},
		{name: "unsupported credentials", logs: []string{"startup password hidden", "  "}},
		{name: "private key", logs: []string{"-----BEGIN PRIVATE KEY-----", "hidden", "-----END PRIVATE KEY-----"}},
		{name: "multiline", logs: []string{" first\n\n second "}, want: []string{"first", "second"}},
		{
			name: "byte limit includes separators",
			logs: []string{"first", strings.Repeat(".", faults.MaxDiagnosticLogBytes)},
			want: []string{"first", strings.Repeat(".", faults.MaxDiagnosticLogBytes-len("first\n"))},
		},
		{
			name: "line limit", logs: []string{strings.Repeat("log line\n", faults.MaxDiagnosticLogLines+1)},
			want: strings.Split(strings.TrimSuffix(strings.Repeat("log line\n", faults.MaxDiagnosticLogLines), "\n"), "\n"),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			lines := faults.DiagnosticLogLines(testCase.logs, faults.MaxDiagnosticLogLines, faults.MaxDiagnosticLogBytes)
			assert.Equal(t, testCase.want, lines)
			assert.LessOrEqual(t, len(strings.Join(lines, "\n")), faults.MaxDiagnosticLogBytes)
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
	stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Cond(func(req deployer.StackRequest) bool {
		return req.StackName == "app" &&
			req.SourceComposePath == "app.yaml"
	})).
		Return(deployer.Result{}, nil)
	services.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, nil)

	result, err := controller.Execute(t.Context(), req)
	require.NoError(t, err)
	require.NotNil(t, result.Attempt)
	assert.Equal(t, deploymentmodel.Succeeded, result.Status)
	stored, err := deploymentstore.NewStore(db).Get(t.Context(), result.Attempt.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Succeeded, stored.Status)

	stackDeployer.EXPECT().RefreshRegistryAuth(gomock.Any(), deployer.StackRequest{
		StackName: req.Stack.Name, SourceComposePath: req.Desired.Path, Desired: req.Desired.Compose,
	}).Return(nil)
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
			name: "blocked external job", deployErr: &pipe.StepError{StepName: "apply stack",
				Err: fmt.Errorf("%w: service job requires zero replicas", services.ErrUpdateBlocked)},
			wantStatus: deploymentmodel.Failed, wantPhase: deploymentmodel.PhaseApply, wantCode: "apply_failed",
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
			stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Cond(func(req deployer.StackRequest) bool {
				return req.StackName == "app" &&
					req.SourceComposePath == "app.yaml"
			})).
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

	stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Cond(func(req deployer.StackRequest) bool {
		return req.StackName == "app" &&
			req.SourceComposePath == "app.yaml"
	})).
		Return(deployer.Result{}, errors.New("failed"))
	failed, err := controller.Execute(t.Context(), executionRequest("api:1"))
	require.Error(t, err)
	stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Cond(func(req deployer.StackRequest) bool {
		assert.Equal(t, deployer.StackOptions{
			TargetServices: []string{"api"}, PendingRemovals: []string{}, StackResourcesPending: true,
			RetryFailedUpdates: true, UnresolvedServices: []string{"api"},
		}, req.Options)
		return req.StackName == "app" && req.SourceComposePath == "app.yaml"
	})).
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
	stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Cond(func(req deployer.StackRequest) bool {
		assert.Equal(t, deployer.StackOptions{
			TargetServices: []string{"api"}, PendingRemovals: []string{}, StackResourcesPending: true,
			UnresolvedServices: []string{"api"},
		}, req.Options)
		return req.StackName == "other" && req.SourceComposePath == "app.yaml"
	})).
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
		SourceChanged: true,
	}
}

func TestSuccessfulUnchangedDeploymentMaintenanceDoesNotChangeLifecycle(t *testing.T) {
	db, lifecycle, bus := openService(t)
	ctrl := gomock.NewController(t)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	manager := swarm.NewMockServiceManager(ctrl)
	controller := NewController(lifecycle, stackDeployer, manager,
		pruner.NewServicePruner(manager, bus, config.SyncPolicySpec{}), &metrics.NopDeploys{})
	req := executionRequest("api:1")
	attempt, err := lifecycle.StartIfChanged(t.Context(), req.Stack.Name, req.Commit, req.Desired, true)
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), attempt.ID, Completion{Outcome: CompletionSucceeded}))
	store := deploymentstore.NewStore(db)
	before, err := store.Get(t.Context(), attempt.DeploymentID)
	require.NoError(t, err)
	req.SourceChanged = false
	request := deployer.StackRequest{StackName: req.Stack.Name, SourceComposePath: req.Desired.Path, Desired: req.Desired.Compose}
	failure := errors.New("synthetic maintenance failure")
	gomock.InOrder(
		stackDeployer.EXPECT().RefreshRegistryAuth(gomock.Any(), request).Return(failure),
		stackDeployer.EXPECT().RefreshRegistryAuth(gomock.Any(), request).Return(nil),
	)
	result, err := controller.Execute(t.Context(), req)
	require.ErrorIs(t, err, ErrRegistryAuthRefresh)
	require.ErrorIs(t, err, failure)
	assert.Nil(t, result.Attempt)
	assert.Equal(t, deploymentmodel.Succeeded, result.Status)
	assert.Empty(t, result.ErrorCode)
	assert.False(t, result.RetryDeferred)
	result, err = controller.Execute(t.Context(), req)
	require.NoError(t, err)
	assert.Nil(t, result.Attempt)
	after, err := store.Get(t.Context(), attempt.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, before, after, "credential maintenance must not create or alter an Attempt")
}

func TestControllerExecuteCompletesStackOptionsFailure(t *testing.T) {
	for _, tc := range []struct {
		name           string
		failCompletion bool
	}{
		{name: "completion persisted"},
		{name: "completion recovered on next execution", failCompletion: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _, bus := openService(t)
			mocks := gomock.NewController(t)
			mockDB := storage.NewMockDatabase(mocks)
			optionsErr := errors.New("read stack options failed")
			completionErr := errors.New("persist completion failed")
			transactions := 0
			failNextRead := false
			mockDB.EXPECT().Dialect().Return(db.Dialect()).AnyTimes()
			mockDB.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, fn func(context.Context) error) error {
					transactions++
					if transactions == 2 && tc.failCompletion {
						return completionErr
					}
					err := db.WithinTransaction(ctx, fn)
					if transactions == 1 && err == nil {
						// Fail the options read only after StartIfChanged has committed the Attempt.
						failNextRead = true
					}
					return err
				}).AnyTimes()
			mockDB.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, dest any, query string, args ...any) error {
					if failNextRead {
						failNextRead = false
						return optionsErr
					}
					return db.Get(ctx, dest, query, args...)
				}).AnyTimes()
			mockDB.EXPECT().Select(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(db.Select).AnyTimes()
			delegateDatabaseExec(t, mockDB, db)
			stackDeployer := deployer.NewMockStackDeployer(mocks)
			manager := swarm.NewMockServiceManager(mocks)
			metricGroup := metrics.NewGroup(metrics.CreateGroupParams{Namespace: "test"})
			registry := prometheus.NewRegistry()
			registry.MustRegister(metricGroup)
			now := time.Date(2026, time.November, 6, 12, 0, 0, 0, time.UTC)
			lifecycle := NewService(mockDB, bus, WithClock(func() time.Time { return now }))
			controller := NewController(lifecycle, stackDeployer, manager,
				pruner.NewServicePruner(manager, bus, config.SyncPolicySpec{}), metricGroup.Deploys)
			req := executionRequest("api:1")

			// No DeployStack expectation: calling it before the retry must fail the test.
			result, err := controller.Execute(t.Context(), req)
			require.ErrorIs(t, err, optionsErr)
			require.NotNil(t, result.Attempt)
			assert.Equal(t, deploymentmodel.Failed, result.Status)
			assert.Equal(t, "apply_failed", result.ErrorCode)
			families, gatherErr := registry.Gather()
			require.NoError(t, gatherErr)
			foundDeploys := false
			for _, family := range families {
				if family.GetName() != "test_deploys_total" {
					continue
				}
				foundDeploys = true
				require.Len(t, family.Metric, 1)
				assert.Equal(t, float64(1), family.Metric[0].GetCounter().GetValue())
				labels := map[string]string{}
				for _, label := range family.Metric[0].Label {
					labels[label.GetName()] = label.GetValue()
				}
				assert.Equal(t, map[string]string{"stack": "app", "service": "api", "status": "failed"}, labels)
			}
			assert.True(t, foundDeploys)

			store := deploymentstore.NewStore(db)
			stored, readErr := store.Get(t.Context(), result.Attempt.DeploymentID)
			require.NoError(t, readErr)
			require.Len(t, stored.Attempts, 1)
			if tc.failCompletion {
				require.ErrorIs(t, err, completionErr)
				assert.Equal(t, deploymentmodel.Running, stored.Status)
				assert.Equal(t, deploymentmodel.Running, stored.Attempts[0].Status)
				pending, ok := controller.pending[req.Stack.Name]
				require.True(t, ok)
				assert.Equal(t, result.Attempt.ID, pending.attemptID)
				assert.Equal(t, CompletionApplyFailed, pending.completion.Outcome)
				assert.True(t, pending.completion.Retryable)
				assert.ErrorIs(t, pending.completion.Diagnostic, optionsErr)
			} else {
				assert.Equal(t, deploymentmodel.Failed, stored.Status)
				assert.Equal(t, deploymentmodel.Failed, stored.Attempts[0].Status)
				assert.Equal(t, "apply_failed", stored.ErrorCode)
				assert.True(t, stored.Retryable)
				assert.True(t, stored.Attempts[0].Retryable)
				assert.Empty(t, controller.pending)
			}

			// Restore database access while retaining the controller's pending completion.
			lifecycle.db, lifecycle.store = db, store
			req.SourceChanged = false
			deferred, err := controller.Execute(t.Context(), req)
			require.NoError(t, err)
			assert.Nil(t, deferred.Attempt)
			assert.True(t, deferred.RetryDeferred)
			assert.Empty(t, controller.pending)
			failed, err := store.GetAttempt(t.Context(), result.Attempt.ID)
			require.NoError(t, err)
			assert.Equal(t, deploymentmodel.Failed, failed.Status)
			assert.Equal(t, "apply_failed", failed.ErrorCode)
			assert.Equal(t, optionsErr.Error(), failed.ErrorDiagnostic)
			assert.True(t, failed.Retryable)
			require.NotNil(t, failed.FinishedAt)
			stored, err = store.Get(t.Context(), result.Attempt.DeploymentID)
			require.NoError(t, err)
			assert.Equal(t, deploymentmodel.Failed, stored.Status)
			assert.True(t, stored.Retryable)

			// Recreate the lifecycle to verify the backoff uses only persisted retry metadata.
			controller.lifecycle = NewService(db, bus, WithClock(func() time.Time { return now }))
			now = failed.FinishedAt.Add(automaticRetryDelay - time.Nanosecond)
			deferred, err = controller.Execute(t.Context(), req)
			require.NoError(t, err)
			assert.Nil(t, deferred.Attempt)
			assert.True(t, deferred.RetryDeferred)

			now = now.Add(time.Nanosecond)
			stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Any()).Return(deployer.Result{}, nil)
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return(nil, nil)
			retry, err := controller.Execute(t.Context(), req)
			require.NoError(t, err)
			require.NotNil(t, retry.Attempt)
			assert.Equal(t, result.Attempt.DeploymentID, retry.Attempt.DeploymentID)
			assert.NotEqual(t, result.Attempt.ID, retry.Attempt.ID)
			assert.Equal(t, 2, retry.Attempt.Number)
			assert.Equal(t, deploymentmodel.Succeeded, retry.Status)
			assert.Empty(t, controller.pending)
			stored, err = store.Get(t.Context(), result.Attempt.DeploymentID)
			require.NoError(t, err)
			assert.Equal(t, deploymentmodel.Succeeded, stored.Status)
			failed, err = store.GetAttempt(t.Context(), result.Attempt.ID)
			require.NoError(t, err)
			assert.Equal(t, deploymentmodel.Failed, failed.Status)
			assert.Equal(t, "apply_failed", failed.ErrorCode)
			assert.Equal(t, optionsErr.Error(), failed.ErrorDiagnostic)
			assert.True(t, failed.Retryable)
			assert.NotNil(t, failed.FinishedAt)
		})
	}
}

type recordingMatcher struct {
	record func(any)
}

func (m recordingMatcher) Matches(value any) bool {
	m.record(value)
	return true
}

func (recordingMatcher) String() string { return "records any value" }

func delegateDatabaseExec(t *testing.T, mockDB *storage.MockDatabase, db storage.Database) {
	t.Helper()
	const maximumSQLArguments = 16
	for argumentCount := maximumSQLArguments; argumentCount > 0; argumentCount-- {
		var execCtx context.Context
		var execQuery string
		execArgs := make([]any, argumentCount)
		matchers := make([]any, argumentCount)
		for index := range argumentCount {
			index := index
			matchers[index] = recordingMatcher{record: func(value any) {
				execArgs[index] = value
				if index == argumentCount-1 {
					_, execErr := db.Exec(execCtx, execQuery, execArgs...)
					require.NoError(t, execErr)
				}
			}}
		}
		mockDB.EXPECT().Exec(
			recordingMatcher{record: func(value any) { execCtx = value.(context.Context) }},
			recordingMatcher{record: func(value any) { execQuery = value.(string) }},
			matchers...,
		).Return(driver.RowsAffected(1), nil).AnyTimes()
	}
}
