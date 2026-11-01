package swarm

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceManagerListServices(t *testing.T) {
	t.Parallel()

	one := uint64(1)
	zero := uint64(0)
	updateStartedAt := time.Date(2026, time.October, 28, 10, 30, 0, 0, time.UTC)
	updateCompletedAt := time.Date(2026, time.October, 28, 10, 45, 0, 0, time.UTC)
	services := []dockerswarm.Service{
		{
			ID: "stacked",
			Meta: dockerswarm.Meta{
				CreatedAt: time.Date(2026, time.October, 28, 10, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2026, time.October, 28, 11, 0, 0, 0, time.UTC),
			},
			Spec: dockerswarm.ServiceSpec{
				Annotations: dockerswarm.Annotations{
					Name:   "infra_postgres",
					Labels: map[string]string{stackNamespaceLabelKey: "infra"},
				},
				TaskTemplate: dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{Image: "postgres:18"}},
				Mode:         dockerswarm.ServiceMode{Replicated: &dockerswarm.ReplicatedService{Replicas: &one}},
			},
			ServiceStatus: &dockerswarm.ServiceStatus{RunningTasks: 1, DesiredTasks: 1},
			UpdateStatus: &dockerswarm.UpdateStatus{
				State:       dockerswarm.UpdateStateCompleted,
				StartedAt:   &updateStartedAt,
				CompletedAt: &updateCompletedAt,
			},
		},
		{
			ID: "standalone",
			Spec: dockerswarm.ServiceSpec{
				Annotations:  dockerswarm.Annotations{Name: "node-exporter"},
				TaskTemplate: dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{Image: "exporter:latest"}},
				Mode:         dockerswarm.ServiceMode{Global: &dockerswarm.GlobalService{}},
			},
			ServiceStatus: &dockerswarm.ServiceStatus{RunningTasks: 2, DesiredTasks: 3},
		},
		{
			ID: "zero",
			Spec: dockerswarm.ServiceSpec{
				Annotations: dockerswarm.Annotations{
					Name:   "infra_paused",
					Labels: map[string]string{stackNamespaceLabelKey: "infra"},
				},
				Mode: dockerswarm.ServiceMode{Replicated: &dockerswarm.ReplicatedService{Replicas: &zero}},
			},
			ServiceStatus: &dockerswarm.ServiceStatus{},
		},
		{
			ID: "job",
			Spec: dockerswarm.ServiceSpec{
				Annotations: dockerswarm.Annotations{Name: "cleanup"},
				Mode: dockerswarm.ServiceMode{ReplicatedJob: &dockerswarm.ReplicatedJob{
					MaxConcurrent: &one,
				}},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		assert.Equal(t, "/v1.48/services", req.URL.Path)
		assert.Equal(t, "true", req.URL.Query().Get("status"))
		_, hasStackFilter := req.URL.Query()["filters"]
		response := services
		if hasStackFilter && req.URL.Query().Get("filters") != "{}" {
			assert.JSONEq(t, `{"label":{"com.docker.stack.namespace=infra":true}}`, req.URL.Query().Get("filters"))
			response = []dockerswarm.Service{services[0], services[2]}
		}

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(response))
	}))
	t.Cleanup(server.Close)

	manager := newServiceManager(newTestDockerClient(t, server))

	all, err := manager.ListServices(t.Context(), ListServicesFilter{})
	require.NoError(t, err)
	require.Len(t, all, 4)
	assert.Equal(t, "cleanup", all[0].Name)
	assert.Equal(t, "replicated-job", all[0].Mode)
	assert.Equal(t, uint64(0), all[0].RunningTasks, "missing status must map safely")
	assert.Equal(t, uint64(0), *all[1].Replicas)
	assert.Equal(t, uint64(0), all[1].RunningTasks)
	assert.Equal(t, uint64(0), all[1].DesiredTasks, "0/0 replicas are valid")
	assert.Equal(t, "postgres", all[2].Name)
	assert.Equal(t, "infra", all[2].Stack)
	assert.Equal(t, uint64(1), all[2].RunningTasks)
	assert.Equal(t, uint64(1), all[2].DesiredTasks)
	assert.Equal(t, time.Date(2026, time.October, 28, 11, 0, 0, 0, time.UTC), all[2].UpdatedAt)
	require.NotNil(t, all[2].UpdateStatus)
	assert.Equal(t, "completed", all[2].UpdateStatus.State)
	assert.Equal(t, "node-exporter", all[3].Name)
	assert.Empty(t, all[3].Stack)
	assert.Equal(t, "global", all[3].Mode)

	stacked, err := manager.ListServices(t.Context(), ListServicesFilter{Stack: "infra"})
	require.NoError(t, err)
	require.Len(t, stacked, 2)
	assert.Equal(t, []string{"infra_paused", "infra_postgres"}, []string{stacked[0].FullName, stacked[1].FullName})
}

func TestServiceManagerNativeApplyOperations(t *testing.T) {
	t.Parallel()
	requests := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, req.Method+" "+req.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method + " " + req.URL.Path {
		case http.MethodGet + " /v1.48/services/demo_api":
			require.NoError(t, json.NewEncoder(w).Encode(dockerswarm.Service{
				ID: "service-id", Meta: dockerswarm.Meta{Version: dockerswarm.Version{Index: 7}},
				Spec: dockerswarm.ServiceSpec{Annotations: dockerswarm.Annotations{Name: "demo_api"}},
			}))
		case http.MethodPost + " /v1.48/services/create":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]string{"ID": "created-id"}))
		case http.MethodPost + " /v1.48/services/service-id/update":
			assert.Equal(t, "7", req.URL.Query().Get("version"))
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{}))
		default:
			http.Error(w, `{"message":"unexpected request"}`, http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)

	manager := newServiceManager(newTestDockerClient(t, server))
	service, err := manager.Inspect(t.Context(), "demo_api")
	require.NoError(t, err)
	assert.Equal(t, uint64(7), service.Version.Index)
	createdID, err := manager.Create(t.Context(), dockerswarm.ServiceSpec{
		Annotations: dockerswarm.Annotations{Name: "demo_api"},
	}, dockerswarm.ServiceCreateOptions{QueryRegistry: true})
	require.NoError(t, err)
	assert.Equal(t, "created-id", createdID)
	require.NoError(t, manager.Update(t.Context(), "service-id", dockerswarm.Version{Index: 7},
		dockerswarm.ServiceSpec{Annotations: dockerswarm.Annotations{Name: "demo_api"}},
		dockerswarm.ServiceUpdateOptions{RegistryAuthFrom: dockerswarm.RegistryAuthFromSpec, QueryRegistry: true}))
	assert.Equal(t, []string{
		"GET /v1.48/services/demo_api", "POST /v1.48/services/create", "POST /v1.48/services/service-id/update",
	}, requests)
}

func TestServiceManagerMapsApplyConflicts(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"update out of sequence"}`, http.StatusConflict)
	}))
	t.Cleanup(server.Close)
	manager := newServiceManager(newTestDockerClient(t, server))

	_, err := manager.Create(t.Context(), dockerswarm.ServiceSpec{
		Annotations: dockerswarm.Annotations{Name: "demo_api"},
	}, dockerswarm.ServiceCreateOptions{})
	assert.ErrorIs(t, err, ErrServiceConflict)
	err = manager.Update(t.Context(), "service-id", dockerswarm.Version{Index: 7}, dockerswarm.ServiceSpec{
		Annotations: dockerswarm.Annotations{Name: "demo_api"},
	}, dockerswarm.ServiceUpdateOptions{})
	assert.ErrorIs(t, err, ErrServiceConflict)
}

func TestServiceManagerListTasksByIDOrFullName(t *testing.T) {
	t.Parallel()

	for _, serviceIDOrName := range []string{"service-id", "infra_api"} {
		t.Run(serviceIDOrName, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				assert.Equal(t, "/v1.48/tasks", req.URL.Path)
				assert.JSONEq(t, `{"service":{"`+serviceIDOrName+`":true}}`, req.URL.Query().Get("filters"))
				w.Header().Set("Content-Type", "application/json")
				require.NoError(t, json.NewEncoder(w).Encode([]dockerswarm.Task{
					{
						ID:           "task-running",
						Slot:         3,
						NodeID:       "node-1",
						DesiredState: dockerswarm.TaskStateRunning,
						Status: dockerswarm.TaskStatus{
							State: dockerswarm.TaskStateRunning,
							ContainerStatus: &dockerswarm.ContainerStatus{
								ContainerID: "container-1",
								ExitCode:    17,
							},
						},
					},
					{
						ID:           "task-pending",
						DesiredState: dockerswarm.TaskStateRunning,
						Status:       dockerswarm.TaskStatus{State: dockerswarm.TaskStatePending},
					},
				}))
			}))
			t.Cleanup(server.Close)

			manager := newServiceManager(newTestDockerClient(t, server))
			tasks, err := manager.ListTasks(t.Context(), serviceIDOrName)
			require.NoError(t, err)
			require.Len(t, tasks, 2)
			assert.Equal(t, 3, tasks[0].Slot)
			assert.Equal(t, TaskStateRunning, tasks[0].DesiredState)
			assert.Equal(t, "container-1", tasks[0].ContainerID)
			require.NotNil(t, tasks[0].ExitCode)
			assert.Equal(t, 17, *tasks[0].ExitCode)
			assert.Empty(t, tasks[1].ContainerID)
			assert.Nil(t, tasks[1].ExitCode)
		})
	}
}

func TestServiceManagerGetTask(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		assert.Equal(t, "/v1.48/tasks/task-1", req.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(dockerswarm.Task{
			ID:        "task-1",
			ServiceID: "service-1",
			NodeID:    "node-1",
			Slot:      4,
			Meta: dockerswarm.Meta{
				CreatedAt: time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2026, time.October, 28, 12, 1, 0, 0, time.UTC),
			},
			DesiredState: dockerswarm.TaskStateRunning,
			Status: dockerswarm.TaskStatus{
				State: dockerswarm.TaskStateRunning,
				ContainerStatus: &dockerswarm.ContainerStatus{
					ContainerID: "container-1",
					ExitCode:    7,
				},
			},
			Spec: dockerswarm.TaskSpec{
				ContainerSpec: &dockerswarm.ContainerSpec{
					Image: "api:v2",
					Env:   []string{"APP_ENV=production", "VALUE=left=right"},
				},
				Resources: &dockerswarm.ResourceRequirements{
					Reservations: &dockerswarm.Resources{NanoCPUs: 100, MemoryBytes: 200},
					Limits:       &dockerswarm.Limit{NanoCPUs: 300, MemoryBytes: 400},
				},
			},
		}))
	}))
	t.Cleanup(server.Close)

	manager := newServiceManager(newTestDockerClient(t, server))
	details, err := manager.GetTask(t.Context(), "task-1")
	require.NoError(t, err)
	assert.Equal(t, "service-1", details.ServiceID)
	assert.Equal(t, "api:v2", details.Image)
	assert.Equal(t, []string{"APP_ENV=production", "VALUE=left=right"}, details.Environment)
	require.NotNil(t, details.ExitCode)
	assert.Equal(t, 7, *details.ExitCode)
	assert.Equal(t, &TaskResources{NanoCPUs: 100, MemoryBytes: 200}, details.Reservations)
	assert.Equal(t, &TaskResources{NanoCPUs: 300, MemoryBytes: 400}, details.Limits)
}

func TestServiceManagerGetTaskWithoutContainerStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(dockerswarm.Task{ID: "pending"}))
	}))
	t.Cleanup(server.Close)

	manager := newServiceManager(newTestDockerClient(t, server))
	details, err := manager.GetTask(t.Context(), "pending")
	require.NoError(t, err)
	assert.Empty(t, details.ContainerID)
	assert.Nil(t, details.ExitCode)
	assert.Empty(t, details.Environment)
}

func TestServiceManagerGetTaskNotFound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"task not found"}`, http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	manager := newServiceManager(newTestDockerClient(t, server))
	_, err := manager.GetTask(t.Context(), "missing")
	assert.ErrorIs(t, err, ErrTaskNotFound)
}

func newTestDockerClient(t *testing.T, server *httptest.Server) *client.Client {
	t.Helper()

	dockerClient, err := client.NewClientWithOpts(
		client.WithHost(server.URL),
		client.WithHTTPClient(server.Client()),
		client.WithVersion("1.48"),
	)
	require.NoError(t, err)

	return dockerClient
}

func TestDemultiplexDockerLogStreamReturnsRawOnPlainText(t *testing.T) {
	raw := []byte("2026-04-18T12:00:00Z hello\n2026-04-18T12:00:01Z warning\n")

	decoded := demultiplexDockerLogStream(raw)

	assert.Equal(t, raw, decoded, "plain-text stream must stay unchanged")
}

func TestDemultiplexDockerLogStreamDemultiplexesFrames(t *testing.T) {
	frame1 := []byte("2026-04-18T12:00:00Z stdout hello\n")
	frame2 := []byte("2026-04-18T12:00:01Z stderr warning\n")

	raw := append(encodeDockerLogFrame(1, frame1), encodeDockerLogFrame(2, frame2)...)
	expected := append([]byte{}, frame1...)
	expected = append(expected, frame2...)

	decoded := demultiplexDockerLogStream(raw)

	assert.Equal(t, expected, decoded, "multiplexed stream must be demultiplexed")
}

func TestBuildDockerServiceLogsOptionsDefaults(t *testing.T) {
	options := buildDockerServiceLogsOptions(ServiceLogsOptions{})

	assert.Equal(t, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Tail:       "200",
	}, options, "unexpected default logs options")
}

func TestBuildDockerServiceLogsOptionsWithBounds(t *testing.T) {
	since := time.Date(2026, time.April, 18, 9, 0, 0, 123000000, time.FixedZone("UTC+3", 3*60*60))
	until := since.Add(5 * time.Minute)

	options := buildDockerServiceLogsOptions(ServiceLogsOptions{
		Limit: 123,
		Since: &since,
		Until: &until,
	})

	require.Equal(t, "123", options.Tail, "unexpected tail")
	assert.Equal(t, "2026-04-18T06:00:00.123Z", options.Since, "unexpected since")
	assert.Equal(t, "2026-04-18T06:05:00.123Z", options.Until, "unexpected until")
	assert.True(t, options.ShowStdout, "stdout must be enabled")
	assert.True(t, options.ShowStderr, "stderr must be enabled")
	assert.True(t, options.Timestamps, "timestamps must be enabled")
}

func TestBuildDockerTaskLogsOptionsDefaults(t *testing.T) {
	options := buildDockerTaskLogsOptions(TaskLogsOptions{})

	assert.Equal(t, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Follow:     false,
		Tail:       "200",
	}, options, "unexpected default task logs options")
}

func TestBuildDockerTaskLogsOptionsWithFollowAndTail(t *testing.T) {
	since := time.Date(2026, time.April, 18, 9, 0, 0, 123000000, time.FixedZone("MSK", 3*60*60))
	until := since.Add(5 * time.Minute)
	options := buildDockerTaskLogsOptions(TaskLogsOptions{
		Follow: true,
		Limit:  25,
		Since:  &since,
		Until:  &until,
	})

	assert.Equal(t, "25", options.Tail, "unexpected tail")
	assert.True(t, options.Follow, "follow must be enabled")
	assert.True(t, options.ShowStdout, "stdout must be enabled")
	assert.True(t, options.ShowStderr, "stderr must be enabled")
	assert.True(t, options.Timestamps, "timestamps must be enabled")
	assert.Equal(t, "2026-04-18T06:00:00.123Z", options.Since, "unexpected since")
	assert.Equal(t, "2026-04-18T06:05:00.123Z", options.Until, "unexpected until")
}

func TestReadDockerLogEntriesDemultiplexesStreamAndTimestamp(t *testing.T) {
	raw := append(
		encodeDockerLogFrame(1, []byte("2026-09-15T18:20:11.123Z server started\n")),
		encodeDockerLogFrame(2, []byte("2026-09-15T18:20:12.456Z failed request\n"))...,
	)
	entries := make(chan LogEntry, 2)

	err := readDockerLogEntries(context.Background(), bytes.NewReader(raw), entries)
	close(entries)
	require.NoError(t, err, "read docker log entries")

	got := make([]LogEntry, 0, 2)
	for entry := range entries {
		got = append(got, entry)
	}

	require.Len(t, got, 2)
	assert.Equal(t, defaultLogStream, got[0].Stream)
	assert.Equal(t, "server started", got[0].Message)
	assert.Equal(t, time.Date(2026, time.September, 15, 18, 20, 11, 123000000, time.UTC), got[0].Timestamp)
	assert.Equal(t, stderrLogStream, got[1].Stream)
	assert.Equal(t, "failed request", got[1].Message)
}

func TestReadDockerLogEntriesKeepsShortPlainText(t *testing.T) {
	entries := make(chan LogEntry, 1)

	err := readDockerLogEntries(context.Background(), bytes.NewReader([]byte("ready\n")), entries)
	close(entries)
	require.NoError(t, err, "read docker log entries")

	got := make([]LogEntry, 0, 1)
	for entry := range entries {
		got = append(got, entry)
	}

	require.Len(t, got, 1)
	assert.Equal(t, defaultLogStream, got[0].Stream)
	assert.Equal(t, "ready", got[0].Message)
}

func TestToServiceConfigRefsMapsReferencesWithoutPayload(t *testing.T) {
	refs := toServiceConfigRefs([]*dockerswarm.ConfigReference{
		{
			ConfigID:   "cfg-id",
			ConfigName: "prod_pomerium_config",
			File: &dockerswarm.ConfigReferenceFileTarget{
				Name: "/etc/pomerium/config.yaml",
			},
		},
	})

	assert.Equal(t, []ServiceConfig{
		{
			ConfigID:   "cfg-id",
			ConfigName: "prod_pomerium_config",
			Target:     "/etc/pomerium/config.yaml",
		},
	}, refs)
	assert.Empty(t, refs[0].Data, "service status must not load config payload")
}

func TestResolveServiceDeployModeReplicated(t *testing.T) {
	replicas := uint64(4)

	mode, count := resolveServiceDeployMode(dockerswarm.ServiceMode{
		Replicated: &dockerswarm.ReplicatedService{
			Replicas: &replicas,
		},
	})

	assert.Equal(t, "replicated", mode, "unexpected deploy mode")
	assert.Equal(t, uint64(4), count, "unexpected replicas count")
}

func TestResolveServiceDeployModeGlobal(t *testing.T) {
	mode, count := resolveServiceDeployMode(dockerswarm.ServiceMode{
		Global: &dockerswarm.GlobalService{},
	})

	assert.Equal(t, "global", mode, "unexpected deploy mode")
	assert.Equal(t, uint64(0), count, "global mode must not report replicas")
}

func TestStackServiceNameFromFullName(t *testing.T) {
	assert.Equal(
		t,
		"api",
		stackServiceNameFromFullName("payments", "payments_api"),
		"stack prefix must be removed",
	)
	assert.Equal(
		t,
		"foreign_api",
		stackServiceNameFromFullName("payments", "foreign_api"),
		"foreign service names must stay unchanged",
	)
}

func encodeDockerLogFrame(stream byte, payload []byte) []byte {
	frame := make([]byte, dockerLogFrameHeaderSize+len(payload))
	frame[0] = stream
	binary.BigEndian.PutUint32(frame[4:dockerLogFrameHeaderSize], uint32(len(payload)))
	copy(frame[dockerLogFrameHeaderSize:], payload)

	return frame
}
