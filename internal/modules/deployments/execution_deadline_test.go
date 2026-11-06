package deployments

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	pipe "github.com/artarts36/gopipe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestExecutionDeadlinePersistsTerminalLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stage  string
		cancel bool
		nilErr bool
		status deploymentmodel.Status
		code   string
	}{
		{name: "apply timeout", stage: "apply stack", status: deploymentmodel.Failed, code: "apply_failed"},
		{name: "init timeout", stage: "run init jobs", status: deploymentmodel.Failed, code: "init_failed"},
		{name: "verification timeout", stage: "wait for service convergence", status: deploymentmodel.Interrupted, code: "verification_unknown"},
		{name: "cancelled execution", stage: "apply stack", cancel: true, status: deploymentmodel.Failed, code: "apply_failed"},
		{name: "late apply success", stage: "apply stack", nilErr: true, status: deploymentmodel.Failed, code: "apply_failed"},
		{name: "late observation success", stage: "observation", nilErr: true, status: deploymentmodel.Interrupted, code: "verification_unknown"},
		{name: "late cleanup success", stage: "cleanup", nilErr: true, status: deploymentmodel.Failed, code: "prune_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				db, lifecycle, bus := openService(t)
				subscribe(t, db, bus, events.TypeNameDeployFailed, events.TypeNameDeployInterrupted, events.TypeNameDeploySuccess)
				ctrl := gomock.NewController(t)
				stackDeployer := deployer.NewMockStackDeployer(ctrl)
				manager := swarm.NewMockServiceManager(ctrl)
				controller := NewController(lifecycle, stackDeployer, manager,
					pruner.NewServicePruner(manager, bus, config.SyncPolicySpec{Prune: true}), &metrics.NopDeploys{})
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				started := time.Now()
				stall := func(ctx context.Context) error {
					deadline, ok := ctx.Deadline()
					require.True(t, ok)
					assert.Equal(t, started.Add(15*time.Minute), deadline)
					if tc.cancel {
						cancel()
					}
					<-ctx.Done()
					if tc.nilErr {
						return nil
					}
					return &pipe.StepError{StepName: tc.stage, Err: ctx.Err()}
				}
				stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Any()).DoAndReturn(
					func(ctx context.Context, _ deployer.StackRequest) (deployer.Result, error) {
						if tc.stage == "observation" || tc.stage == "cleanup" {
							// Time spent preparing/applying must count against final operations too.
							time.Sleep(14 * time.Minute)
							return deployer.Result{}, nil
						}
						return deployer.Result{}, stall(ctx)
					})
				if tc.stage == "observation" {
					manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).DoAndReturn(
						func(ctx context.Context, _ swarm.ListServicesFilter) ([]swarm.StackService, error) {
							return nil, stall(ctx)
						})
				}
				if tc.stage == "cleanup" {
					manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{{
						ID: "old", Name: "old", Labels: map[string]string{
							labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue,
						},
					}}, nil)
					manager.EXPECT().Remove(gomock.Any(), "old").DoAndReturn(
						func(ctx context.Context, _ string) error { return stall(ctx) })
				}

				result, err := controller.Execute(ctx, executionRequest("api:1"))
				wantErr := context.DeadlineExceeded
				if tc.cancel {
					wantErr = context.Canceled
				}
				require.ErrorIs(t, err, wantErr)
				require.NotNil(t, result.Attempt)
				assert.Equal(t, tc.status, result.Status)
				stored, err := deploymentstore.NewStore(db).Get(t.Context(), result.Attempt.DeploymentID)
				require.NoError(t, err)
				assert.Equal(t, tc.status, stored.Status)
				assert.Equal(t, tc.code, stored.ErrorCode)
				require.Len(t, stored.Attempts, 1)
				assert.NotNil(t, stored.Attempts[0].FinishedAt)
				assert.Empty(t, controller.pending)
				assert.Empty(t, publications(t, db, events.TypeNameDeploySuccess))
				terminal := events.TypeNameDeployFailed
				if tc.status == deploymentmodel.Interrupted {
					terminal = events.TypeNameDeployInterrupted
				}
				assert.Len(t, publications(t, db, terminal), 1)
				if !tc.cancel {
					assert.Equal(t, 15*time.Minute, time.Since(started))
				}
			})
		})
	}
}

func TestCancelledCompletionPersistenceRemainsRecoverable(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending completion", true: "restart recovery"}[restart], func(t *testing.T) {
			db, _, bus := openService(t)
			subscribe(t, db, bus, events.TypeNameDeployFailed, events.TypeNameDeployInterrupted)
			ctrl := gomock.NewController(t)
			publisher := dispatcher.NewMockDispatcher(ctrl)
			unavailable := errors.New("completion storage unavailable")
			fail := true
			publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, event events.Event) error {
					if _, terminal := event.(*events.DeployFailed); terminal && fail {
						require.NoError(t, ctx.Err())
						deadline, ok := ctx.Deadline()
						require.True(t, ok)
						assert.LessOrEqual(t, time.Until(deadline), 5*time.Second)
						return unavailable
					}
					return bus.Publish(ctx, event)
				}).AnyTimes()
			lifecycle := NewService(db, publisher)
			stackDeployer := deployer.NewMockStackDeployer(ctrl)
			controller := NewController(lifecycle, stackDeployer, nil, nil, &metrics.NopDeploys{})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, _ deployer.StackRequest) (deployer.Result, error) {
					cancel()
					return deployer.Result{}, ctx.Err()
				})
			result, err := controller.Execute(ctx, executionRequest("api:1"))
			require.ErrorIs(t, err, context.Canceled)
			require.ErrorIs(t, err, unavailable)
			store := deploymentstore.NewStore(db)
			stored, err := store.Get(t.Context(), result.Attempt.DeploymentID)
			require.NoError(t, err)
			assert.Equal(t, deploymentmodel.Running, stored.Status, "failed publication rolls back the terminal transaction")
			assert.Empty(t, publications(t, db, events.TypeNameDeployFailed))
			fail = false
			wantStatus := deploymentmodel.Failed
			if restart {
				controller = NewController(NewService(db, publisher), nil, nil, nil, &metrics.NopDeploys{})
				require.NoError(t, controller.Recover(t.Context()))
				require.NoError(t, controller.Recover(t.Context()))
				wantStatus = deploymentmodel.Interrupted
			} else {
				result, err = controller.Execute(t.Context(), executionRequest("api:1"))
				require.NoError(t, err)
				assert.Nil(t, result.Attempt, "cancelled apply is not automatically retryable")
			}
			stored, err = store.Get(t.Context(), stored.ID)
			require.NoError(t, err)
			assert.Equal(t, wantStatus, stored.Status)
			assert.Len(t, stored.Attempts, 1)
		})
	}
}

func TestTimedOutAttemptsRetryOnlyAfterBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db, lifecycle, _ := openService(t)
		ctrl := gomock.NewController(t)
		stackDeployer := deployer.NewMockStackDeployer(ctrl)
		controller := NewController(lifecycle, stackDeployer, nil, nil, &metrics.NopDeploys{})
		stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, _ deployer.StackRequest) (deployer.Result, error) {
				<-ctx.Done()
				return deployer.Result{}, &pipe.StepError{StepName: "wait for service convergence", Err: ctx.Err()}
			}).Times(3)
		var previousID string
		for index, delay := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute} {
			result, err := controller.Execute(t.Context(), executionRequest("api:1"))
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.NotNil(t, result.Attempt)
			assert.Equal(t, index+1, result.Attempt.Number)
			assert.NotEqual(t, previousID, result.Attempt.ID)
			previousID = result.Attempt.ID
			for _, wait := range []time.Duration{0, delay - time.Nanosecond} {
				time.Sleep(wait)
				deferred, err := controller.Execute(t.Context(), executionRequest("api:1"))
				require.NoError(t, err)
				assert.Nil(t, deferred.Attempt)
				assert.True(t, deferred.RetryDeferred)
			}
			time.Sleep(time.Nanosecond)
		}
		latest, _, err := deploymentstore.NewStore(db).Latest(t.Context(), "app")
		require.NoError(t, err)
		assert.Equal(t, 3, latest.AttemptCount)
		assert.True(t, latest.Retryable)
	})
}

func TestTerminalPersistenceHasItsOwnBoundedBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db, _, bus := openService(t)
		ctrl := gomock.NewController(t)
		publisher := dispatcher.NewMockDispatcher(ctrl)
		publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, event events.Event) error {
				if _, terminal := event.(*events.DeployFailed); terminal {
					require.NoError(t, ctx.Err(), "execution cancellation must not cancel completion")
					<-ctx.Done()
					return ctx.Err()
				}
				return bus.Publish(ctx, event)
			}).AnyTimes()
		stackDeployer := deployer.NewMockStackDeployer(ctrl)
		stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, _ deployer.StackRequest) (deployer.Result, error) {
				<-ctx.Done()
				return deployer.Result{}, ctx.Err()
			})
		controller := NewController(NewService(db, publisher), stackDeployer, nil, nil, &metrics.NopDeploys{})
		started := time.Now()
		result, err := controller.Execute(t.Context(), executionRequest("api:1"))
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, 15*time.Minute+5*time.Second, time.Since(started))
		stored, err := deploymentstore.NewStore(db).Get(t.Context(), result.Attempt.DeploymentID)
		require.NoError(t, err)
		assert.Equal(t, deploymentmodel.Running, stored.Status)
		require.Len(t, controller.pending, 1, "a failed completion remains available for recovery")
	})
}
