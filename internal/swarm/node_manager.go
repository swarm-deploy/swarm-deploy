package swarm

import (
	"context"
	"fmt"
	"sort"

	cerrdefs "github.com/containerd/errdefs"
	dockerevents "github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
)

// NodeManager manages Docker Swarm nodes.
type nodeManager struct {
	dockerClient *client.Client
}

func newNodeManager(dockerClient *client.Client) NodeManager {
	return &nodeManager{
		dockerClient: dockerClient,
	}
}

// List returns current Docker Swarm nodes snapshot.
func (m *nodeManager) List(ctx context.Context) ([]Node, error) {
	nodes, err := m.dockerClient.NodeList(ctx, dockerswarm.NodeListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list swarm nodes: %w", err)
	}

	mapped := make([]Node, 0, len(nodes))
	for _, dockerNode := range nodes {
		mapped = append(mapped, m.mapNode(dockerNode))
	}
	m.sortInfos(mapped)

	return mapped, nil
}

// SetLabel sets Docker node label value while preserving the rest of the node spec.
func (m *nodeManager) SetLabel(ctx context.Context, req NodeLabelUpdateRequest) error {
	return m.updateLabels(ctx, req.NodeID, func(labels map[string]string) {
		labels[req.Key] = req.Value
	})
}

// DeleteLabel removes Docker node label while preserving the rest of the node spec.
func (m *nodeManager) DeleteLabel(ctx context.Context, nodeID string, key string) error {
	return m.updateLabels(ctx, nodeID, func(labels map[string]string) {
		delete(labels, key)
	})
}

// Watch subscribes to Docker node events stream.
func (m *nodeManager) Watch(
	ctx context.Context,
) (<-chan dockerevents.Message, <-chan error, error) {
	eventsFilter := filters.NewArgs(filters.Arg("type", string(dockerevents.NodeEventType)))
	messages, errs := m.dockerClient.Events(ctx, dockerevents.ListOptions{
		Filters: eventsFilter,
	})

	return messages, errs, nil
}

func (m *nodeManager) updateLabels(ctx context.Context, nodeID string, mutate func(labels map[string]string)) error {
	node, _, err := m.dockerClient.NodeInspectWithRaw(ctx, nodeID)
	if err != nil {
		if isNotFoundErr(err) {
			return ErrNodeNotFound
		}

		return fmt.Errorf("inspect node %s: %w", nodeID, err)
	}

	spec := node.Spec
	labels := cloneLabels(spec.Labels)
	mutate(labels)
	spec.Labels = labels

	if err = m.dockerClient.NodeUpdate(ctx, node.ID, node.Version, spec); err != nil {
		if cerrdefs.IsConflict(err) {
			return ErrNodeUpdateConflict
		}

		return fmt.Errorf("update node %s labels: %w", nodeID, err)
	}

	return nil
}

func (*nodeManager) mapNode(node dockerswarm.Node) Node {
	managerStatus := NodeManagerStatusWorker
	if node.ManagerStatus != nil {
		switch {
		case node.ManagerStatus.Leader:
			managerStatus = NodeManagerStatusLeader
		case node.ManagerStatus.Reachability != "":
			managerStatus = NodeManagerStatus(node.ManagerStatus.Reachability)
		default:
			managerStatus = NodeManagerStatusManager
		}
	}

	return Node{
		ID:            node.ID,
		Hostname:      node.Description.Hostname,
		Status:        string(node.Status.State),
		Availability:  string(node.Spec.Availability),
		ManagerStatus: managerStatus,
		EngineVersion: node.Description.Engine.EngineVersion,
		Addr:          node.Status.Addr,
		CPUNano:       node.Description.Resources.NanoCPUs,
		MemoryBytes:   node.Description.Resources.MemoryBytes,
		Labels:        node.Spec.Labels,
	}
}

func cloneLabels(labels map[string]string) map[string]string {
	cloned := make(map[string]string, len(labels))
	for key, value := range labels {
		cloned[key] = value
	}

	return cloned
}

func (*nodeManager) sortInfos(nodes []Node) {
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Hostname < nodes[j].Hostname
	})
}
