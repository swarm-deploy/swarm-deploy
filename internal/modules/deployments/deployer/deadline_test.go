package deployer

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/initjob"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestSequentialInitJobsShareExecutionDeadline(t *testing.T) {
	for _, earlyFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "shared deadline", true: "earlier failure"}[earlyFailure], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				jobs := NewMockInitJobExecutor(ctrl)
				// No waiter or application calls are allowed after Init Jobs fail.
				d := &Deployer{initJobRunner: jobs, serviceWaiter: NewMockServiceWaitExecutor(ctrl)}
				manager := swarm.NewMockServiceManager(ctrl)
				wireNativeTestDeployerWithServiceManager(t, d, manager, false)
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
				defer cancel()
				started := time.Now()
				firstFailure := errors.New("migration failed")
				jobs.EXPECT().Run(gomock.Any(), gomock.Cond(func(spec initjob.Spec) bool {
					return spec.Job.Name == "first"
				})).DoAndReturn(func(ctx context.Context, _ initjob.Spec) (initjob.Result, error) {
					deadline, _ := ctx.Deadline()
					assert.Equal(t, started.Add(15*time.Minute), deadline)
					time.Sleep(8 * time.Minute)
					result := initjob.Result{StartedAt: started, FinishedAt: time.Now()}
					if earlyFailure {
						return result, firstFailure
					}
					return result, nil
				})
				if !earlyFailure {
					jobs.EXPECT().Run(gomock.Any(), gomock.Cond(func(spec initjob.Spec) bool {
						return spec.Job.Name == "second"
					})).DoAndReturn(func(ctx context.Context, _ initjob.Spec) (initjob.Result, error) {
						deadline, _ := ctx.Deadline()
						assert.Equal(t, started.Add(15*time.Minute), deadline, "the second job cannot reset the budget")
						<-ctx.Done()
						return initjob.Result{StartedAt: time.Now()}, ctx.Err()
					})
				}
				desired := minimalCompose("api")
				desired.Services[0].InitJobs = []compose.InitJob{
					composeInitJob("first"), composeInitJob("second"), composeInitJob("never-started"),
				}
				result, err := d.DeployStack(ctx, StackRequest{StackName: "demo", Desired: desired})
				assert.True(t, InitJobsFailed(err))
				if earlyFailure {
					require.ErrorIs(t, err, firstFailure)
					assert.Equal(t, 8*time.Minute, time.Since(started))
					assert.Len(t, result.InitJobs, 1)
				} else {
					require.ErrorIs(t, err, context.DeadlineExceeded)
					assert.Equal(t, 15*time.Minute, time.Since(started))
					assert.Len(t, result.InitJobs, 2)
				}
			})
		})
	}
}

func TestServiceWaiterUsesRemainingExecutionBudget(t *testing.T) {
	for _, preparation := range []time.Duration{0, 14 * time.Minute} {
		t.Run(preparation.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				jobs := NewMockInitJobExecutor(ctrl)
				d := &Deployer{initJobRunner: jobs}
				manager := wireNativeTestDeployer(t, d)
				manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
				d.serviceWaiter = waiter.NewServiceWaiter(manager, nil, time.Second, 10*time.Minute)
				desired := minimalCompose("api")
				desired.Services[0].InitJobs = []compose.InitJob{composeInitJob("prepare")}
				jobs.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(
					func(context.Context, initjob.Spec) (initjob.Result, error) {
						time.Sleep(preparation)
						return initjob.Result{}, nil
					})
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
				defer cancel()
				started := time.Now()
				_, err := d.DeployStack(ctx, StackRequest{StackName: "demo", Desired: desired})
				require.ErrorIs(t, err, context.DeadlineExceeded)
				assert.True(t, ConvergenceFailed(err))
				assert.NotErrorIs(t, err, waiter.ErrConvergenceFailed)
				assert.Equal(t, min(preparation+10*time.Minute, 15*time.Minute), time.Since(started))
			})
		})
	}
}
