package initjob

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	swarmclient "github.com/swarm-deploy/swarm-deploy/internal/swarm"
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

func TestInitJobRunnerCollectsLogsAfterTimeoutWithDetachedBoundedContext(t *testing.T) {
	deleted := make(chan struct{}, 1)
	server := newInitJobDockerServer(t, swarm.TaskStateRunning, "", []byte("still running\n"), false, deleted)
	defer server.Close()
	dockerClient, err := client.NewClientWithOpts(
		client.WithHost(server.URL), client.WithHTTPClient(server.Client()), client.WithVersion("1.48"),
	)
	require.NoError(t, err)
	runner := newTestInitJobRunner(50*time.Millisecond, 5*time.Millisecond, dockerClient)

	execution, runErr := runner.Run(t.Context(), InitJobSpec{
		StackName: "app", ServiceName: "api",
		Job: compose.InitJob{Name: "migrate", Image: "example/migrate:1"},
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
}

func TestTailBufferKeepsExactLastBytes(t *testing.T) {
	buffer := newTailBuffer(4)
	_, err := buffer.Write([]byte{0, 1, 2})
	require.NoError(t, err)
	_, err = buffer.Write([]byte{3, 4, 5})
	require.NoError(t, err)
	assert.Equal(t, []byte{2, 3, 4, 5}, buffer.Bytes())
	assert.True(t, buffer.Truncated())
}

func newTestInitJobRunner(pollInterval, timeout time.Duration, dockerClient *client.Client) *InitJobRunner {
	runner := &InitJobRunner{
		dockerClient: dockerClient, swarmService: swarmclient.NewSwarm(dockerClient, ""),
		authManager: &registry.NopAuthManager{}, metrics: &metrics.NopDeploys{},
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
			err := json.NewEncoder(w).Encode([]swarm.Task{{
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
