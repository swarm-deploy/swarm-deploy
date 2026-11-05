package initjob

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestInitJobRunnerUsesServiceManagerWithCompleteSpecAndOptions(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		t.Run(fmt.Sprint(authenticated), func(t *testing.T) {
			t.Setenv("DOCKER_CONFIG", t.TempDir())
			t.Setenv("DOCKER_AUTH_CONFIG", "")
			if authenticated {
				t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"registry.example":{"username":"robot","password":"test-value"}}}`)
			}
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			// Only log collection uses the SDK, and must use the ID returned by the manager.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/v1.48/services/created-id/logs", r.URL.Path)
				assert.Equal(t, "all", r.URL.Query().Get("tail"))
				_, _ = w.Write(dockerLogFrame([]byte("done\n")))
			}))
			t.Cleanup(server.Close)
			dockerClient, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.48"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, dockerClient.Close()) })
			runner := &InitJobRunner{
				dockerClient: dockerClient, swarmService: &swarm.Swarm{Services: manager},
				authManager: registry.NewAuthManager(), metrics: &metrics.NopDeploys{},
				pollInterval: time.Millisecond, timeout: 2 * time.Minute,
				deleteTasks: make(chan initJobDeleteTask, 1),
			}
			spec := Spec{
				StackName: "app", ServiceName: "api", DefaultNetwork: []string{"default"},
				NetworkTargets:  map[string]string{"default": "network-id"},
				ServiceSecrets:  []compose.ObjectRef{{Source: "password"}},
				ServiceConfigs:  []compose.ObjectRef{{Source: "settings", Target: "/etc/app"}},
				ResolvedSecrets: map[string]resources.ResolvedResource{"password": {ID: "secret-id", Name: "app_password"}},
				ResolvedConfigs: map[string]resources.ResolvedResource{"settings": {ID: "config-id", Name: "app_settings"}},
				Job: compose.InitJob{Name: "migrate", Image: "registry.example/app:1",
					Entrypoint: []string{"sh", "-c"}, Command: []string{"migrate up"},
					Environment: compose.Environment{Map: map[string]string{"MODE": "migration"}}},
			}
			spec.Job.Timeout.Value = time.Minute
			wantOptions, err := runner.buildInitServiceCreateOptions(spec.Job.Image)
			require.NoError(t, err)
			assert.Equal(t, authenticated, wantOptions.QueryRegistry)
			assert.Equal(t, authenticated, wantOptions.EncodedRegistryAuth != "")
			contextKey := struct{}{}
			ctx := context.WithValue(t.Context(), contextKey, "propagated")
			manager.EXPECT().Create(gomock.Any(), gomock.Any(), wantOptions).DoAndReturn(
				func(createCtx context.Context, got dockerswarm.ServiceSpec, _ dockerswarm.ServiceCreateOptions) (string, error) {
					assert.Equal(t, "propagated", createCtx.Value(contextKey))
					deadline, ok := createCtx.Deadline()
					require.True(t, ok)
					assert.InDelta(t, time.Minute.Seconds(), time.Until(deadline).Seconds(), 2)
					assert.Regexp(t, `^app_migrate-[0-9]+$`, got.Name)
					want, buildErr := runner.buildInitServiceSpec(createCtx, spec, got.Name)
					require.NoError(t, buildErr)
					assert.Equal(t, want, got)
					return "created-id", nil
				})
			manager.EXPECT().ListTasks(gomock.Any(), "created-id").Return([]swarm.ServiceTask{{
				CurrentState: swarm.TaskStateComplete,
			}}, nil)
			manager.EXPECT().Remove(gomock.Any(), "created-id").Return(nil)
			result, err := runner.Run(ctx, spec)
			require.NoError(t, err)
			assert.Equal(t, Succeeded, result.Status)
			assert.Equal(t, []byte("done\n"), result.Logs)
			select {
			case task := <-runner.deleteTasks:
				assert.Equal(t, "created-id", task.serviceID)
				runner.removeServiceWithRetry(t.Context(), task)
			default:
				t.Fatal("cleanup task not enqueued")
			}
		})
	}
}

func TestInitJobRunnerCleanupRetriesAndAcceptsNotFound(t *testing.T) {
	for _, retryFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(retryFirst), func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			calls := 0
			if retryFirst {
				manager.EXPECT().Remove(gomock.Any(), "id").DoAndReturn(func(context.Context, string) error {
					calls++
					return fmt.Errorf("remove: connection interrupted")
				})
			}
			manager.EXPECT().Remove(gomock.Any(), "id").DoAndReturn(func(ctx context.Context, _ string) error {
				calls++
				_, ok := ctx.Deadline()
				assert.True(t, ok)
				return fmt.Errorf("remove: %w", swarm.ErrServiceNotFound)
			})
			runner := &InitJobRunner{swarmService: &swarm.Swarm{Services: manager}}
			runner.removeServiceWithRetry(t.Context(), initJobDeleteTask{serviceID: "id", serviceName: "job"})
			wantCalls := 1
			if retryFirst {
				wantCalls++
			}
			assert.Equal(t, wantCalls, calls)
		})
	}
}

func TestInitJobRunnerMissingNetworkUsesStackCandidateThenOriginalName(t *testing.T) {
	for _, stackNetworkExists := range []bool{false, true} {
		t.Run(fmt.Sprint(stackNetworkExists), func(t *testing.T) {
			manager := swarm.NewMockNetworkManager(gomock.NewController(t))
			missing := fmt.Errorf("inspect network: %w", swarm.ErrNetworkNotFound)
			manager.EXPECT().Get(gomock.Any(), "default").Return(swarm.Network{}, missing)
			want := "default"
			if stackNetworkExists {
				manager.EXPECT().Get(gomock.Any(), "app_default").Return(swarm.Network{ID: "network-id"}, nil)
				want = "network-id"
			} else {
				manager.EXPECT().Get(gomock.Any(), "app_default").Return(swarm.Network{}, missing)
			}
			runner := &InitJobRunner{swarmService: &swarm.Swarm{Networks: manager}}
			assert.Equal(t, want, runner.resolveNetworkTarget(t.Context(), "app", "default"))
		})
	}
}

func TestInitJobRunnerPreservesTaskOrderingAndFailureDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, taskError, message, want string }{
		{"error", " exit 1 ", "other detail", "exit 1"},
		{"message", "", " worker rejected task ", "worker rejected task"},
		{"state", "", "", "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			manager.EXPECT().ListTasks(gomock.Any(), "id").Return([]swarm.ServiceTask{
				{ID: "old", CurrentState: swarm.TaskStateComplete, UpdatedAt: time.Now().Add(-time.Minute)},
				{ID: "latest", CurrentState: swarm.TaskStateFailed, UpdatedAt: time.Now(), Error: tc.taskError, Message: tc.message},
			}, nil)
			runner := &InitJobRunner{swarmService: &swarm.Swarm{Services: manager}, pollInterval: time.Millisecond}
			err := runner.waitJob(t.Context(), "id", "job")
			var failed *JobFailedError
			require.ErrorAs(t, err, &failed)
			assert.Equal(t, "latest", failed.ID)
			assert.Equal(t, tc.want, failed.Reason)
		})
	}
}

func TestInitJobCreationPreservesCancellationAndTimeout(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprint(canceled), func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			manager.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, _ dockerswarm.ServiceSpec, _ dockerswarm.ServiceCreateOptions) (string, error) {
					<-ctx.Done()
					return "", ctx.Err()
				})
			runner := &InitJobRunner{
				swarmService: &swarm.Swarm{Services: manager}, authManager: &registry.NopAuthManager{},
				metrics: &metrics.NopDeploys{}, timeout: 10 * time.Millisecond,
				deleteTasks: make(chan initJobDeleteTask, 1),
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := context.DeadlineExceeded
			if canceled {
				cancel()
				want = context.Canceled
			}
			_, err := runner.Run(ctx, Spec{StackName: "app", Job: compose.InitJob{Name: "migrate", Image: "busybox:1.36"}})
			require.ErrorIs(t, err, want)
			assert.Empty(t, runner.deleteTasks)
		})
	}
}
