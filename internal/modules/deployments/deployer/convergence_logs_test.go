package deployer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestWaitEnrichesOnlyConfirmedConvergenceFailures(t *testing.T) {
	failure := fmt.Errorf("%w: service api rollback_started; id=new-task state=failed exit_code=23",
		waiter.ErrConvergenceFailed)
	apiErr := errors.New("access denied: token=private-value")
	for _, tc := range []struct {
		name      string
		cause     error
		messages  []string
		apiErr    error
		streamErr error
		canceled  bool
		noTask    bool
		request   bool
		want      []string
	}{
		{name: "success"},
		{name: "verification interrupted", cause: context.DeadlineExceeded},
		{name: "unknown observation", cause: errors.New("Docker unavailable")},
		{name: "logs available", cause: failure, request: true,
			messages: []string{"application starting", "migration failed: missing table"},
			want:     []string{"application starting", "migration failed: missing table", "service api task new-task logs:"}},
		{name: "no logs", cause: failure, request: true, want: []string{"Task logs unavailable: no logs found"}},
		{name: "API failure", cause: failure, request: true, apiErr: apiErr,
			want: []string{"Task logs unavailable: Docker API error: access denied", "****"}},
		{name: "stream failure", cause: failure, request: true, streamErr: errors.New("stream disconnected"),
			want: []string{"Task logs unavailable: Docker API error: stream disconnected"}},
		{name: "already canceled", cause: failure, canceled: true,
			want: []string{"Task logs unavailable: parent context canceled"}},
		{name: "no identified task", cause: failure, noTask: true,
			want: []string{"Task logs unavailable: failed task could not be identified"}},
		{name: "credentials filtered", cause: failure, request: true,
			messages: []string{"password private-value", "Authorization: Bearer private-value", "api_key=private-value",
				"postgres://private-value@db/app", "database_url=private-value", "-----BEGIN PRIVATE KEY-----",
				"startup failed: missing table"}, want: []string{"startup failed: missing table"}},
		{name: "only sensitive logs", cause: failure, request: true, messages: []string{"token=private-value"},
			want: []string{"Task logs unavailable: no logs found"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			manager := swarm.NewMockServiceManager(ctrl)
			observer := NewMockServiceWaitExecutor(ctrl)
			result := waiter.ServiceWaitResult{FailedTasks: []waiter.ServiceProgress{
				{Name: "api", FailedTaskID: "new-task", State: waiter.ServiceFailed},
			}}
			if tc.noTask {
				result.FailedTasks = nil
			}
			observer.EXPECT().Wait(gomock.Any(), gomock.Any()).Return(result, tc.cause)
			if tc.request {
				manager.EXPECT().TaskLogs(gomock.Any(), "new-task", swarm.TaskLogsOptions{Follow: false, Limit: 20}).
					DoAndReturn(func(ctx context.Context, _ string, _ swarm.TaskLogsOptions) (<-chan swarm.LogEntry, <-chan error, error) {
						deadline, ok := ctx.Deadline()
						require.True(t, ok)
						assert.InDelta(t, 5, time.Until(deadline).Seconds(), 0.1)
						entries, errs := diagnosticLogStream(tc.messages, tc.streamErr)
						return entries, errs, tc.apiErr
					})
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			d := &Deployer{serviceWaiter: observer, services: manager}
			actual, err := d.wait(ctx, waiter.ServiceWaitRequest{StackName: "app"})
			assert.Equal(t, result, actual)
			if tc.cause == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.cause)
			assert.Equal(t, errors.Is(tc.cause, waiter.ErrConvergenceFailed), errors.Is(err, waiter.ErrConvergenceFailed))
			assert.ErrorContains(t, err, tc.cause.Error())
			for _, want := range tc.want {
				assert.ErrorContains(t, err, want)
			}
			assert.NotContains(t, err.Error(), "private-value")
			if tc.apiErr != nil {
				assert.NotErrorIs(t, err, tc.apiErr, "log errors must not add another failure classification")
			}
		})
	}
}

func TestTaskLogCollectionDeadlineAndParentCancellation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		request   bool
		cancel    bool
		deadline  bool
		wantDelay time.Duration
	}{
		{name: "request timeout", request: true, wantDelay: 5 * time.Second},
		{name: "stream timeout", wantDelay: 5 * time.Second},
		{name: "cancel request", request: true, cancel: true, wantDelay: 10 * time.Millisecond},
		{name: "cancel stream", cancel: true, wantDelay: 10 * time.Millisecond},
		{name: "parent deadline", deadline: true, wantDelay: time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				manager := swarm.NewMockServiceManager(ctrl)
				observer := NewMockServiceWaitExecutor(ctrl)
				failure := fmt.Errorf("%w: original failure", waiter.ErrConvergenceFailed)
				observer.EXPECT().Wait(gomock.Any(), gomock.Any()).Return(waiter.ServiceWaitResult{
					FailedTasks: []waiter.ServiceProgress{{Name: "api", FailedTaskID: "failed-task"}},
				}, failure)
				manager.EXPECT().TaskLogs(gomock.Any(), "failed-task", gomock.Any()).DoAndReturn(
					func(ctx context.Context, _ string, _ swarm.TaskLogsOptions) (<-chan swarm.LogEntry, <-chan error, error) {
						if tc.request {
							<-ctx.Done()
							return nil, nil, ctx.Err()
						}
						return make(chan swarm.LogEntry), make(chan error), nil
					})
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if tc.cancel {
					time.AfterFunc(tc.wantDelay, cancel)
				}
				if tc.deadline {
					var stop context.CancelFunc
					ctx, stop = context.WithTimeout(ctx, tc.wantDelay)
					defer stop()
				}
				d := &Deployer{serviceWaiter: observer, services: manager}
				started := time.Now()
				_, err := d.wait(ctx, waiter.ServiceWaitRequest{StackName: "app"})
				assert.Equal(t, tc.wantDelay, time.Since(started))
				require.ErrorIs(t, err, failure)
				require.ErrorIs(t, err, waiter.ErrConvergenceFailed)
				assert.NotErrorIs(t, err, context.DeadlineExceeded)
				assert.NotErrorIs(t, err, context.Canceled)
				if tc.cancel {
					assert.ErrorContains(t, err, "Task logs unavailable: parent context canceled")
				} else if tc.deadline {
					assert.ErrorContains(t, err, "Task logs unavailable: parent context deadline exceeded")
				} else {
					assert.ErrorContains(t, err, "Task logs unavailable: retrieval timed out after 5s")
				}
			})
		})
	}
}

func TestMultipleFailedServiceLogsAreBounded(t *testing.T) {
	for _, tc := range []struct {
		name     string
		messages []string
		requests int
	}{
		{name: "task count", messages: []string{"startup failed"}, requests: 3},
		{name: "line count", messages: []string{strings.Repeat("failure\n", 25)}, requests: 1},
		{name: "byte count", messages: []string{strings.Repeat("x", 3000)}, requests: 1},
		{name: "newline byte budget", messages: []string{"first", strings.Repeat("x", 3000)}, requests: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			manager := swarm.NewMockServiceManager(ctrl)
			observer := NewMockServiceWaitExecutor(ctrl)
			result := waiter.ServiceWaitResult{}
			for i := range 5 {
				result.FailedTasks = append(result.FailedTasks, waiter.ServiceProgress{
					Name: fmt.Sprintf("service-%d", i), FailedTaskID: fmt.Sprintf("task-%d", i),
				})
				if i < tc.requests {
					manager.EXPECT().TaskLogs(gomock.Any(), fmt.Sprintf("task-%d", i), gomock.Any()).DoAndReturn(
						func(_ context.Context, _ string, _ swarm.TaskLogsOptions) (<-chan swarm.LogEntry, <-chan error, error) {
							entries, errs := diagnosticLogStream(tc.messages, nil)
							return entries, errs, nil
						})
				}
			}
			observer.EXPECT().Wait(gomock.Any(), gomock.Any()).Return(result, waiter.ErrConvergenceFailed)
			d := &Deployer{serviceWaiter: observer, services: manager}
			_, err := d.wait(t.Context(), waiter.ServiceWaitRequest{StackName: "app"})
			require.ErrorIs(t, err, waiter.ErrConvergenceFailed)
			assert.ErrorContains(t, err, "collection limit reached")
			var content []string
			inLogs := false
			for _, line := range strings.Split(err.Error(), "\n") {
				if strings.HasPrefix(line, "service ") {
					inLogs = true
					continue
				}
				if inLogs && !strings.HasPrefix(line, "Task logs unavailable") {
					content = append(content, line)
				}
			}
			assert.LessOrEqual(t, len(content), faults.MaxDiagnosticLogLines)
			assert.LessOrEqual(t, len(strings.Join(content, "\n")), faults.MaxDiagnosticLogBytes)
		})
	}
}

func TestFailedServicesShareOneLogCollectionDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctrl := gomock.NewController(t)
		manager := swarm.NewMockServiceManager(ctrl)
		observer := NewMockServiceWaitExecutor(ctrl)
		result := waiter.ServiceWaitResult{FailedTasks: []waiter.ServiceProgress{
			{Name: "api", FailedTaskID: "api-task"},
			{Name: "db", FailedTaskID: "db-task"},
			{Name: "worker", FailedTaskID: "worker-task"},
		}}
		observer.EXPECT().Wait(gomock.Any(), gomock.Any()).Return(result, waiter.ErrConvergenceFailed)
		manager.EXPECT().TaskLogs(gomock.Any(), "api-task", gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, _ swarm.TaskLogsOptions) (<-chan swarm.LogEntry, <-chan error, error) {
				time.Sleep(4 * time.Second)
				entries, errs := diagnosticLogStream([]string{"startup failed"}, nil)
				return entries, errs, nil
			})
		manager.EXPECT().TaskLogs(gomock.Any(), "db-task", gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, _ swarm.TaskLogsOptions) (<-chan swarm.LogEntry, <-chan error, error) {
				return make(chan swarm.LogEntry), make(chan error), nil
			})
		d := &Deployer{serviceWaiter: observer, services: manager}
		started := time.Now()
		_, err := d.wait(t.Context(), waiter.ServiceWaitRequest{StackName: "app"})
		require.ErrorIs(t, err, waiter.ErrConvergenceFailed)
		assert.Equal(t, 5*time.Second, time.Since(started))
		assert.ErrorContains(t, err, "startup failed")
		assert.ErrorContains(t, err, "service db task db-task logs:\nTask logs unavailable: retrieval timed out after 5s")
		assert.ErrorContains(t, err, "service worker task worker-task logs:\nTask logs unavailable: retrieval timed out after 5s")
	})
}

func TestDeployPipelinesIncludeFailedTaskLogs(t *testing.T) {
	for _, single := range []bool{false, true} {
		t.Run(fmt.Sprintf("single service %t", single), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			manager := swarm.NewMockServiceManager(ctrl)
			observer := NewMockServiceWaitExecutor(ctrl)
			observer.EXPECT().Snapshot(gomock.Any(), "demo").Return(nil, nil)
			observer.EXPECT().Wait(gomock.Any(), gomock.Any()).Return(waiter.ServiceWaitResult{
				FailedTasks: []waiter.ServiceProgress{{Name: "api", FailedTaskID: "new-task"}},
			}, waiter.ErrConvergenceFailed)
			manager.EXPECT().TaskLogs(gomock.Any(), "new-task", gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, _ swarm.TaskLogsOptions) (<-chan swarm.LogEntry, <-chan error, error) {
					entries, errs := diagnosticLogStream([]string{"startup failed", "token=private-value"}, nil)
					return entries, errs, nil
				})
			d := &Deployer{serviceWaiter: observer, secretResolver: newTestSecretResolver()}
			wireNativeTestDeployerWithServiceManager(t, d, manager, true)
			var err error
			if single {
				_, err = d.DeployService(t.Context(), ServiceRequest{StackName: "demo", ServiceName: "api", Desired: minimalCompose("api")})
			} else {
				_, err = d.DeployStack(t.Context(), StackRequest{StackName: "demo", Desired: minimalCompose("api")})
			}
			require.ErrorIs(t, err, waiter.ErrConvergenceFailed)
			assert.True(t, ConvergenceFailed(err))
			assert.ErrorContains(t, err, "startup failed")
			assert.NotContains(t, err.Error(), "private-value")
		})
	}
}

func TestStartFirstRollbackCollectsFailedNewTaskLogs(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	now := time.Now()
	baseline := swarm.StackService{ID: "api-id", Name: "api", Mode: "replicated", CreatedAt: now.Add(-time.Hour), UpdatedAt: now}
	baseline.ServiceSpec = nativeMinimalServiceSpec("api")
	baseline.ServiceSpec.UpdateConfig = &dockerswarm.UpdateConfig{Order: "start-first"}
	rolledBack := baseline
	rolledBack.UpdatedAt = now.Add(2 * time.Minute)
	rolledBack.UpdateStatus = &swarm.ServiceUpdateStatus{State: swarm.ServiceUpdateStateRollbackStarted,
		StartedAt: now.Add(time.Minute), Message: "update rolled back due to failure of task failed-new-task"}
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{rolledBack}, nil)
	exit := 23
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return([]swarm.ServiceTask{
		{ID: "healthy-old-task", CurrentState: swarm.TaskStateRunning, CreatedAt: now.Add(-time.Hour)},
		{ID: "failed-new-task", CurrentState: swarm.TaskStateFailed, CreatedAt: now.Add(time.Second), ExitCode: &exit},
		{ID: "unrelated-history", CurrentState: swarm.TaskStateFailed, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(3 * time.Minute)},
	}, nil)
	manager.EXPECT().TaskLogs(gomock.Any(), "failed-new-task", swarm.TaskLogsOptions{Follow: false, Limit: 20}).DoAndReturn(
		func(_ context.Context, _ string, _ swarm.TaskLogsOptions) (<-chan swarm.LogEntry, <-chan error, error) {
			entries, errs := diagnosticLogStream([]string{"new version failed to start"}, nil)
			return entries, errs, nil
		})
	d := &Deployer{serviceWaiter: waiter.NewServiceWaiter(manager, nil, time.Millisecond, time.Second), services: manager}
	result, err := d.wait(t.Context(), waiter.ServiceWaitRequest{
		StackName: "demo", ServiceNames: []string{"api"}, AppliedServices: []string{"api"}, Baseline: []swarm.StackService{baseline},
	})
	require.ErrorIs(t, err, waiter.ErrConvergenceFailed)
	require.Len(t, result.FailedTasks, 1)
	assert.Equal(t, "failed-new-task", result.FailedTasks[0].FailedTaskID)
	for _, want := range []string{"rollback_started", "service api", "id=failed-new-task state=failed exit_code=23", "new version failed to start"} {
		assert.ErrorContains(t, err, want)
	}
	assert.NotContains(t, err.Error(), "healthy-old-task")
	assert.NotContains(t, err.Error(), "unrelated-history")
}

func diagnosticLogStream(messages []string, streamErr error) (<-chan swarm.LogEntry, <-chan error) {
	entries := make(chan swarm.LogEntry, len(messages))
	errs := make(chan error, 1)
	for i, message := range messages {
		stream := "stdout"
		if i%2 != 0 {
			stream = "stderr"
		}
		entries <- swarm.LogEntry{Message: message, Stream: stream}
	}
	if streamErr != nil {
		errs <- streamErr
	}
	close(entries)
	close(errs)
	return entries, errs
}
