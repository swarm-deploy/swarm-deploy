package services

import (
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestTerminalServiceTaskFailurePlacement(t *testing.T) {
	for _, replicated := range []bool{false, true} {
		mode := "global"
		if replicated {
			mode = "replicated"
		}
		for _, tc := range []struct {
			name        string
			constraints []string
			changeNode  func(*swarm.Node)
			missingNode bool
			desired     swarm.TaskState
			maxAttempts uint64
			restarts    uint64
			eligible    bool
		}{
			{name: "eligible shutdown failure", eligible: true},
			{name: "label changed", changeNode: func(n *swarm.Node) { n.Labels["zone"] = "west" }},
			{name: "label removed", changeNode: func(n *swarm.Node) { delete(n.Labels, "zone") }},
			{name: "desired running on ineligible node", desired: swarm.TaskStateRunning, changeNode: func(n *swarm.Node) { n.Labels["zone"] = "west" }},
			{name: "eligible exhausted attempts", maxAttempts: 2, restarts: 2, eligible: true},
			{name: "eligible attempts remaining", maxAttempts: 2, restarts: 1, eligible: true},
			{name: "ineligible exhausted attempts", maxAttempts: 2, restarts: 2, changeNode: func(n *swarm.Node) { n.Labels["zone"] = "west" }},
			{name: "drained matching node", changeNode: func(n *swarm.Node) { n.Availability = "drain" }},
			{name: "down matching node", changeNode: func(n *swarm.Node) { n.Status = "down" }},
			{name: "desired running on down node", desired: swarm.TaskStateRunning, changeNode: func(n *swarm.Node) { n.Status = "down" }},
			{name: "down exhausted attempts", maxAttempts: 2, restarts: 2, changeNode: func(n *swarm.Node) { n.Status = "down" }},
			{name: "disconnected matching node", changeNode: func(n *swarm.Node) { n.Status = "disconnected" }, eligible: true},
			{name: "unknown matching node", changeNode: func(n *swarm.Node) { n.Status = "unknown" }, eligible: true},
			{name: "missing node status", changeNode: func(n *swarm.Node) { n.Status = "" }, eligible: true},
			{name: "paused matching node", changeNode: func(n *swarm.Node) { n.Availability = "pause" }, eligible: true},
			{name: "removed node", missingNode: true},
			{name: "role changed", constraints: []string{"node.role == manager"}},
			{name: "hostname changed", constraints: []string{"node.hostname == previous-name"}},
			{name: "OS changed", constraints: []string{"node.platform.os == windows"}},
			{name: "architecture changed", constraints: []string{"node.platform.arch == arm64"}},
			{name: "engine label changed", constraints: []string{"engine.labels.storage == hdd"}},
			{name: "address changed", constraints: []string{"node.ip == 192.0.2.0/24"}},
			{name: "different node ID", constraints: []string{"node.id == node-b"}},
			{name: "inequality no longer matches", constraints: []string{"node.labels.zone != east"}},
			{name: "all current attributes match", eligible: true, constraints: []string{
				"node.labels.zone == east", "node.role == worker", "node.hostname == worker-a",
				"node.platform.os == linux", "node.platform.arch == amd64", "engine.labels.storage == ssd",
				"node.ip == 10.0.0.0/24", "node.id == node-a",
			}},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				now := time.Now()
				node := &swarm.Node{ID: "node-a", Availability: "active", Status: "ready", Role: "worker", Hostname: "worker-a",
					PlatformOS: "linux", PlatformArchitecture: "amd64", Addr: "10.0.0.10",
					Labels: map[string]string{"zone": "east"}, EngineLabels: map[string]string{"storage": "ssd"}}
				if tc.changeNode != nil {
					tc.changeNode(node)
				}
				if tc.missingNode {
					node = nil
				}
				spec := serviceSpec("api", "alpine:3")
				if !replicated {
					spec.Mode = dockerswarm.ServiceMode{Global: &dockerswarm.GlobalService{}}
				}
				constraints := tc.constraints
				if constraints == nil {
					constraints = []string{"node.labels.zone == east"}
				}
				spec.TaskTemplate.Placement = &dockerswarm.Placement{Constraints: constraints}
				spec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
				if tc.maxAttempts > 0 {
					spec.TaskTemplate.RestartPolicy.Condition = dockerswarm.RestartPolicyConditionOnFailure
					spec.TaskTemplate.RestartPolicy.MaxAttempts = &tc.maxAttempts
				}
				var tasks []swarm.ServiceTask
				for attempt := uint64(0); attempt <= tc.restarts; attempt++ {
					tasks = append(tasks, swarm.ServiceTask{ID: "attempt-" + time.Duration(attempt).String(),
						Slot: 1, Node: "node-a", DesiredState: swarm.TaskStateShutdown, CurrentState: swarm.TaskStateFailed,
						CreatedAt: now.Add(time.Duration(attempt) * time.Second), UpdatedAt: now.Add(time.Duration(attempt+1) * time.Second),
						Spec: swarm.NewTaskSpec(spec.TaskTemplate)})
				}
				last := &tasks[len(tasks)-1]
				last.NodeInfo = node
				if tc.desired != "" {
					last.DesiredState = tc.desired
				}
				want := (replicated || tc.eligible) && (tc.maxAttempts == 0 || tc.restarts >= tc.maxAttempts)
				failed, found := TerminalServiceTaskFailure(spec, tasks, now.Add(-time.Minute))
				assert.Equal(t, want, found)
				if want {
					assert.Equal(t, last.ID, failed.ID)
				}
				manager := swarm.NewMockServiceManager(gomock.NewController(t))
				current := service("api-id", 7, spec)
				current.CreatedAt = now.Add(-time.Minute)
				manager.EXPECT().ListTasks(gomock.Any(), current.ID).Return(tasks, nil)
				required, err := New(manager, &registry.NopAuthManager{}).retryRequired(t.Context(), current, true)
				require.NoError(t, err)
				assert.Equal(t, want, required)
			})
		}
	}
}
