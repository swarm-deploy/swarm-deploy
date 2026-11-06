package swarm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceManagerTerminalGlobalTaskNodes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		availability dockerswarm.NodeAvailability
		slot         int
		state        dockerswarm.TaskState
		job          bool
		nodeMissing  bool
		nodeError    bool
		unlimited    bool
		maxAttempts  uint64
		lookups      int
	}{
		{name: "active failed node", availability: dockerswarm.NodeAvailabilityActive, state: dockerswarm.TaskStateFailed, lookups: 1},
		{name: "drained failed node", availability: dockerswarm.NodeAvailabilityDrain, state: dockerswarm.TaskStateFailed, lookups: 1},
		{name: "paused failed node", availability: dockerswarm.NodeAvailabilityPause, state: dockerswarm.TaskStateFailed, lookups: 1},
		{name: "active rejected node", availability: dockerswarm.NodeAvailabilityActive, state: dockerswarm.TaskStateRejected, lookups: 1},
		{name: "removed node", state: dockerswarm.TaskStateFailed, nodeMissing: true, lookups: 1},
		{name: "node lookup error", state: dockerswarm.TaskStateFailed, nodeError: true, lookups: 1},
		{name: "replicated task", slot: 1, state: dockerswarm.TaskStateFailed},
		{name: "global job", job: true, state: dockerswarm.TaskStateFailed},
		{name: "healthy global task", state: dockerswarm.TaskStateRunning},
		{name: "unlimited global restarts", state: dockerswarm.TaskStateFailed, unlimited: true},
		{name: "finite global restarts", availability: dockerswarm.NodeAvailabilityActive, state: dockerswarm.TaskStateFailed, maxAttempts: 2, lookups: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookups := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch req.URL.Path {
				case "/v1.48/tasks":
					task := dockerswarm.Task{ID: "task", NodeID: "node-a", Slot: tc.slot,
						DesiredState: dockerswarm.TaskStateShutdown, Status: dockerswarm.TaskStatus{State: tc.state}}
					task.Spec.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
					if tc.unlimited || tc.maxAttempts > 0 {
						task.Spec.RestartPolicy.Condition = dockerswarm.RestartPolicyConditionOnFailure
						task.Spec.RestartPolicy.MaxAttempts = &tc.maxAttempts
					}
					if tc.state == dockerswarm.TaskStateRunning {
						task.DesiredState = dockerswarm.TaskStateRunning
					}
					if tc.job {
						task.JobIteration = &dockerswarm.Version{Index: 1}
					}
					// Multiple historical attempts share one node snapshot.
					require.NoError(t, json.NewEncoder(w).Encode([]dockerswarm.Task{task, task}))
				case "/v1.48/nodes":
					lookups++
					if tc.nodeError {
						http.Error(w, `{"message":"nodes unavailable"}`, http.StatusInternalServerError)
						return
					}
					var nodes []dockerswarm.Node
					if !tc.nodeMissing {
						nodes = []dockerswarm.Node{{ID: "node-a",
							Spec: dockerswarm.NodeSpec{Availability: tc.availability, Role: dockerswarm.NodeRoleWorker,
								Annotations: dockerswarm.Annotations{Labels: map[string]string{"zone": "east"}}},
							Description: dockerswarm.NodeDescription{Hostname: "worker-a",
								Platform: dockerswarm.Platform{OS: "linux", Architecture: "amd64"},
								Engine:   dockerswarm.EngineDescription{Labels: map[string]string{"storage": "ssd"}}},
							Status: dockerswarm.NodeStatus{Addr: "10.0.0.10"},
						}}
					}
					require.NoError(t, json.NewEncoder(w).Encode(nodes))
				default:
					t.Errorf("unexpected Docker API request: %s", req.URL.Path)
					http.NotFound(w, req)
				}
			}))
			t.Cleanup(server.Close)
			tasks, err := newServiceManager(newTestDockerClient(t, server)).ListTasks(t.Context(), "service-id")
			assert.Equal(t, tc.lookups, lookups)
			if tc.nodeError {
				require.ErrorContains(t, err, "list nodes for service service-id tasks")
				return
			}
			require.NoError(t, err)
			require.Len(t, tasks, 2)
			for _, task := range tasks {
				if tc.lookups == 0 || tc.nodeMissing {
					assert.Nil(t, task.NodeInfo)
					continue
				}
				require.NotNil(t, task.NodeInfo)
				assert.Equal(t, "node-a", task.NodeInfo.ID)
				assert.Equal(t, string(tc.availability), task.NodeInfo.Availability)
				assert.Equal(t, map[string]string{"zone": "east"}, task.NodeInfo.Labels)
				assert.Equal(t, "worker", task.NodeInfo.Role)
				assert.Equal(t, "worker-a", task.NodeInfo.Hostname)
				assert.Equal(t, "linux", task.NodeInfo.PlatformOS)
				assert.Equal(t, "amd64", task.NodeInfo.PlatformArchitecture)
				assert.Equal(t, map[string]string{"storage": "ssd"}, task.NodeInfo.EngineLabels)
				assert.Equal(t, "10.0.0.10", task.NodeInfo.Addr)
			}
		})
	}
}
