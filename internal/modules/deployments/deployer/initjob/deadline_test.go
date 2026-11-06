package initjob

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestStalledInitJobStopsAtParentDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := swarm.NewMockServiceManager(gomock.NewController(t))
		runner := &InitJobRunner{
			swarmService: &swarm.Swarm{Services: manager}, authManager: &registry.NopAuthManager{},
			metrics: &metrics.NopDeploys{}, pollInterval: time.Second, timeout: time.Hour,
			deleteTasks: make(chan initJobDeleteTask, 1),
		}
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
		defer cancel()
		manager.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return("job-id", nil)
		manager.EXPECT().ListTasks(gomock.Any(), "job-id").DoAndReturn(
			func(ctx context.Context, _ string) ([]swarm.ServiceTask, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			})
		started := time.Now()
		result, err := runner.Run(ctx, Spec{
			StackName: "app", ServiceName: "api", Job: compose.InitJob{Name: "stalled", Image: "alpine:3"},
		})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, Interrupted, result.Status)
		assert.False(t, result.LogsAvailable, "log collection must not detach from the expired attempt")
		assert.Equal(t, 15*time.Minute, time.Since(started))
		assert.Equal(t, "job-id", (<-runner.deleteTasks).serviceID)
	})
}

func TestInitJobCleanupFallbackHonorsExecutionDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := swarm.NewMockServiceManager(gomock.NewController(t))
		runner := &InitJobRunner{
			swarmService: &swarm.Swarm{Services: manager},
			deleteTasks:  make(chan initJobDeleteTask), // Full queue forces synchronous cleanup.
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		manager.EXPECT().Remove(gomock.Any(), "job-id").DoAndReturn(func(ctx context.Context, _ string) error {
			<-ctx.Done()
			return ctx.Err()
		})
		started := time.Now()
		runner.enqueueDeleteTask(ctx, initJobDeleteTask{serviceID: "job-id"})
		assert.Equal(t, time.Second, time.Since(started), "retry delays cannot extend the parent deadline")
	})
}
