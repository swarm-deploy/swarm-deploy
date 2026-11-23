package initjob

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/artarts36/specw"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	swarmclient "github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestBuildInitServiceSpecMapsEntrypointAndCommand(t *testing.T) {
	runner := &InitJobRunner{}

	serviceSpec, err := runner.buildInitServiceSpec(context.Background(), InitJobSpec{
		StackName:   "test-stack",
		ServiceName: "api",
		Job: compose.InitJob{
			Name:       "migrate",
			Image:      "ghcr.io/acme/api:1.0.0",
			Entrypoint: []string{"/bin/sh", "-c"},
			Command:    []string{"./bin/migrate up"},
		},
	}, "migrate-test")
	require.NoError(t, err)
	require.NotNil(t, serviceSpec.TaskTemplate.ContainerSpec)

	assert.Equal(t, []string{"/bin/sh", "-c"}, serviceSpec.TaskTemplate.ContainerSpec.Command)
	assert.Equal(t, []string{"./bin/migrate up"}, serviceSpec.TaskTemplate.ContainerSpec.Args)
}

func TestBuildInitServiceSpecMapsCommandToArgsWithoutEntrypoint(t *testing.T) {
	runner := &InitJobRunner{}

	serviceSpec, err := runner.buildInitServiceSpec(context.Background(), InitJobSpec{
		StackName:   "test-stack",
		ServiceName: "api",
		Job: compose.InitJob{
			Name:    "migrate",
			Image:   "ghcr.io/acme/api:1.0.0",
			Command: []string{"./bin/migrate", "up"},
		},
	}, "migrate-test")
	require.NoError(t, err)
	require.NotNil(t, serviceSpec.TaskTemplate.ContainerSpec)

	assert.Empty(t, serviceSpec.TaskTemplate.ContainerSpec.Command)
	assert.Equal(t, []string{"./bin/migrate", "up"}, serviceSpec.TaskTemplate.ContainerSpec.Args)
}

func TestBuildInitServiceSpecUsesReconciledComposeNetworkTargets(t *testing.T) {
	runner := &InitJobRunner{}
	serviceSpec, err := runner.buildInitServiceSpec(context.Background(), InitJobSpec{
		StackName:      "demo",
		ServiceName:    "api",
		DefaultNetwork: []string{"default"},
		NetworkTargets: map[string]string{"default": "network-id"},
		Job:            compose.InitJob{Name: "migrate", Image: "alpine:3"},
	}, "migrate-test")
	require.NoError(t, err)
	assert.Equal(t, []swarm.NetworkAttachmentConfig{{Target: "network-id"}}, serviceSpec.TaskTemplate.Networks)
}

func TestBuildInitServiceSpecUsesResolvedResourceIDs(t *testing.T) {
	runner := &InitJobRunner{swarmService: &swarmclient.Swarm{}}

	spec, err := runner.buildInitServiceSpec(context.Background(), InitJobSpec{
		StackName:   "demo",
		ServiceName: "api",
		ServiceConfigs: []compose.ObjectRef{{
			Source: "app-config",
			Target: "/etc/app/config.yaml",
		}},
		ServiceSecrets: []compose.ObjectRef{{Source: "db-password"}},
		ResolvedConfigs: map[string]resources.ResolvedResource{
			"app-config": {ID: "config-id", Name: "demo_app-config"},
		},
		ResolvedSecrets: map[string]resources.ResolvedResource{
			"db-password": {ID: "secret-id", Name: "demo_db-password"},
		},
		Job: compose.InitJob{Image: "example/migrate:latest"},
	}, "migrate")
	require.NoError(t, err, "build init job spec")

	container := spec.TaskTemplate.ContainerSpec
	require.Len(t, container.Configs, 1, "expected config reference")
	assert.Equal(t, "config-id", container.Configs[0].ConfigID)
	assert.Equal(t, "demo_app-config", container.Configs[0].ConfigName)
	require.Len(t, container.Secrets, 1, "expected secret reference")
	assert.Equal(t, "secret-id", container.Secrets[0].SecretID)
	assert.Equal(t, "demo_db-password", container.Secrets[0].SecretName)
	assert.Equal(t, "/run/secrets/db-password", container.Secrets[0].File.Name)
	assert.Equal(t, "0", container.Secrets[0].File.UID)
	assert.Equal(t, "0", container.Secrets[0].File.GID)
}

func TestBuildInitServiceSpecMountsConfigsLikeService(t *testing.T) {
	parsed, err := compose.Parse([]byte(`services:
  api:
    image: alpine
    secrets: [token, {source: token, target: /etc/token}, {source: cert, target: tls.crt}]
    configs: [app, {source: plain, uid: "1000"}, {source: base, target: /etc/base.conf, mode: 0440}]
    x-init-deploy-jobs:
      - image: migrator
        secrets:
          - {source: token, target: token, uid: "1000", gid: "1001", mode: 0400}
          - {source: cert, target: /run/secrets/tls.crt}
          - key
        configs:
          - {source: app, target: app, uid: "1000", mode: 0400}
          - local
          - {source: owned, uid: "1000", mode: 0400}
          - {source: job, target: /run/secrets/job}
`))
	require.NoError(t, err)
	service := parsed.Services[0]
	resolvedResources := func(names ...string) map[string]resources.ResolvedResource {
		result := map[string]resources.ResolvedResource{}
		for _, name := range names {
			result[name] = resources.ResolvedResource{ID: name + "-id", Name: "demo_" + name}
		}
		return result
	}
	runner := &InitJobRunner{swarmService: &swarmclient.Swarm{}}

	spec, err := runner.buildInitServiceSpec(context.Background(), InitJobSpec{
		StackName: "demo", ServiceName: "api", Job: service.InitJobs[0],
		ServiceSecrets: service.Secrets, ServiceConfigs: service.Configs,
		ResolvedSecrets: resolvedResources("token", "cert", "key"),
		ResolvedConfigs: resolvedResources("app", "plain", "base", "local", "owned", "job"),
	}, "migrate")
	require.NoError(t, err)

	secret := func(name, target string) *swarm.SecretReference {
		return &swarm.SecretReference{SecretID: name + "-id", SecretName: "demo_" + name,
			File: &swarm.SecretReferenceFileTarget{Name: target, UID: "0", GID: "0", Mode: compose.DefaultObjectFileMode}}
	}
	config := func(name, target, uid string, mode os.FileMode) *swarm.ConfigReference {
		return &swarm.ConfigReference{ConfigID: name + "-id", ConfigName: "demo_" + name,
			File: &swarm.ConfigReferenceFileTarget{Name: target, UID: uid, GID: "0", Mode: mode}}
	}
	token := secret("token", "/run/secrets/token")
	token.File.UID, token.File.GID, token.File.Mode = "1000", "1001", 0o400
	assert.Equal(t, []*swarm.SecretReference{
		secret("token", "/etc/token"),
		token,
		secret("cert", "/run/secrets/tls.crt"),
		secret("key", "/run/secrets/key"),
	}, spec.TaskTemplate.ContainerSpec.Secrets, "equivalent targets are mounted once with the job's file settings")
	assert.Equal(t, []*swarm.ConfigReference{
		config("plain", "/plain", "1000", compose.DefaultObjectFileMode),
		config("base", "/etc/base.conf", "0", 0o440),
		config("app", "app", "1000", 0o400),
		config("local", "/local", "0", compose.DefaultObjectFileMode),
		config("owned", "/owned", "1000", 0o400),
		config("job", "/run/secrets/job", "0", compose.DefaultObjectFileMode),
	}, spec.TaskTemplate.ContainerSpec.Configs)
}

func TestInitJobRunnerCollectsLogsWithoutChangingOutcomeAndCleansUp(t *testing.T) {
	largeOutput := append(bytes.Repeat([]byte("x"), initJobLogLimit), []byte("last")...)
	for _, testCase := range []struct {
		name          string
		taskState     swarm.TaskState
		taskError     string
		logs          []byte
		logsFailure   bool
		wantStatus    InitJobStatus
		wantError     bool
		wantAvailable bool
		wantTruncated bool
		wantLogs      []byte
	}{
		{name: "successful execution", taskState: swarm.TaskStateComplete, logs: []byte("migration complete\n"),
			wantStatus: InitJobSucceeded, wantAvailable: true, wantLogs: []byte("migration complete\n")},
		{name: "successful empty output", taskState: swarm.TaskStateComplete, logs: []byte{},
			wantStatus: InitJobSucceeded, wantAvailable: true, wantLogs: []byte{}},
		{name: "failed execution", taskState: swarm.TaskStateFailed, taskError: "exit code 1", logs: []byte("failed\n"),
			wantStatus: InitJobFailed, wantError: true, wantAvailable: true, wantLogs: []byte("failed\n")},
		{name: "logs unavailable after success", taskState: swarm.TaskStateComplete, logsFailure: true,
			wantStatus: InitJobSucceeded, wantAvailable: false},
		{name: "logs unavailable after failure", taskState: swarm.TaskStateFailed, taskError: "exit code 2", logsFailure: true,
			wantStatus: InitJobFailed, wantError: true, wantAvailable: false},
		{name: "last 64 KiB retained", taskState: swarm.TaskStateComplete, logs: largeOutput,
			wantStatus: InitJobSucceeded, wantAvailable: true, wantTruncated: true,
			wantLogs: largeOutput[len(largeOutput)-initJobLogLimit:]},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			deleted := make(chan struct{}, 1)
			server := newInitJobDockerServer(t, testCase.taskState, testCase.taskError, testCase.logs,
				testCase.logsFailure, deleted)
			defer server.Close()
			dockerClient, err := client.NewClientWithOpts(
				client.WithHost(server.URL), client.WithHTTPClient(server.Client()), client.WithVersion("1.48"),
			)
			require.NoError(t, err)
			runner := newTestInitJobRunner(time.Millisecond, time.Second, dockerClient)

			execution, runErr := runner.Run(t.Context(), InitJobSpec{
				StackName: "app", ServiceName: "api",
				Job: compose.InitJob{Name: "migrate", Image: "example/migrate:1"},
			})
			if testCase.wantError {
				require.Error(t, runErr)
			} else {
				require.NoError(t, runErr)
			}
			assert.Equal(t, testCase.wantStatus, execution.Status)
			assert.Equal(t, testCase.wantAvailable, execution.LogsAvailable)
			assert.Equal(t, testCase.wantTruncated, execution.LogsTruncated)
			assert.Equal(t, testCase.wantLogs, execution.Logs)
			select {
			case <-deleted:
			case <-time.After(2 * time.Second):
				t.Fatal("Init Job service cleanup was not called")
			}
		})
	}
}

func TestInitJobRunnerCollectsLogsAfterStageTimeoutWithinParentDeadline(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(fmt.Sprint(override), func(t *testing.T) {
			deleted := make(chan struct{}, 1)
			server := newInitJobDockerServer(t, swarm.TaskStateRunning, "", []byte("still running\n"), false, deleted)
			defer server.Close()
			dockerClient, err := client.NewClientWithOpts(
				client.WithHost(server.URL), client.WithHTTPClient(server.Client()), client.WithVersion("1.48"),
			)
			require.NoError(t, err)
			// Leave enough time for service creation even on a loaded test runner; the
			// always-running task still guarantees that the job reaches its timeout.
			runner := newTestInitJobRunner(10*time.Millisecond, 100*time.Millisecond, dockerClient)
			job := compose.InitJob{Name: "migrate", Image: "example/migrate:1"}
			if override {
				runner.timeout = time.Hour
				job.Timeout = specw.Duration{Value: 100 * time.Millisecond}
			}

			execution, runErr := runner.Run(t.Context(), InitJobSpec{
				StackName: "app", ServiceName: "api",
				Job: job,
			})
			require.ErrorIs(t, runErr, context.DeadlineExceeded)
			assert.Equal(t, InitJobInterrupted, execution.Status)
			assert.True(t, execution.LogsAvailable)
			assert.Equal(t, []byte("still running\n"), execution.Logs)
			select {
			case <-deleted:
			case <-time.After(2 * time.Second):
				t.Fatal("Init Job service cleanup was not called")
			}
		})
	}
}

func TestInitJobLogStreamCancellationPreventsSuccess(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(started)
		<-req.Context().Done()
	}))
	defer server.Close()
	defer server.CloseClientConnections()
	dockerClient, err := client.NewClientWithOpts(
		client.WithHost(server.URL), client.WithHTTPClient(server.Client()), client.WithVersion("1.48"),
	)
	require.NoError(t, err)
	defer dockerClient.Close()
	manager := swarmclient.NewMockServiceManager(gomock.NewController(t))
	manager.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return("job-id", nil)
	manager.EXPECT().ListTasks(gomock.Any(), "job-id").Return([]swarmclient.ServiceTask{{
		CurrentState: swarmclient.TaskStateComplete,
	}}, nil)
	manager.EXPECT().LogTail(gomock.Any(), "job-id", initJobLogLimit).
		DoAndReturn(swarmclient.NewSwarm(dockerClient).Services.LogTail)
	runner := &InitJobRunner{
		swarmService: &swarmclient.Swarm{Services: manager}, authManager: &registry.NopAuthManager{},
		metrics: &metrics.NopDeploys{}, pollInterval: time.Millisecond, timeout: time.Minute,
		deleteTasks: make(chan initJobDeleteTask, 1),
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	var result Result
	go func() {
		var runErr error
		result, runErr = runner.Run(ctx, Spec{
			StackName: "app", ServiceName: "api", Job: compose.InitJob{Name: "completed", Image: "alpine:3"},
		})
		done <- runErr
	}()
	<-started
	cancel()
	select {
	case err = <-done:
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, Interrupted, result.Status)
	case <-time.After(time.Second):
		t.Fatal("Init Job logs ignored parent cancellation")
	}
}

func newTestInitJobRunner(pollInterval, timeout time.Duration, dockerClient *client.Client) *InitJobRunner {
	runner := &InitJobRunner{
		swarmService: swarmclient.NewSwarm(dockerClient),
		authManager:  &registry.NopAuthManager{}, metrics: &metrics.NopDeploys{},
		pollInterval: pollInterval, timeout: timeout,
		deleteTasks: make(chan initJobDeleteTask, initJobDeleteTasksBuffer),
	}
	go runner.runDeleteWorker()
	return runner
}

func newInitJobDockerServer(
	t *testing.T,
	state swarm.TaskState,
	taskError string,
	logs []byte,
	logsFailure bool,
	deleted chan<- struct{},
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/services/create"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ID":"service-id"}`))
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/tasks"):
			w.Header().Set("Content-Type", "application/json")
			err := json.MarshalWrite(w, []swarm.Task{{
				ID: "task-id", Meta: swarm.Meta{UpdatedAt: time.Now()}, Status: swarm.TaskStatus{State: state, Err: taskError},
			}})
			require.NoError(t, err)
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/services/service-id/logs"):
			if logsFailure {
				http.Error(w, "logs unavailable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
			_, _ = w.Write(dockerLogFrame(logs))
		case req.Method == http.MethodDelete && strings.HasSuffix(req.URL.Path, "/services/service-id"):
			select {
			case deleted <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, fmt.Sprintf("unexpected Docker request: %s %s", req.Method, req.URL.Path), http.StatusNotFound)
		}
	}))
}

func dockerLogFrame(payload []byte) []byte {
	frame := make([]byte, 8+len(payload))
	frame[0] = 1
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[8:], payload)
	return frame
}
