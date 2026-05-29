package swarm

import (
	"context"
	"fmt"
	"sort"

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

func (*nodeManager) sortInfos(nodes []Node) {
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Hostname < nodes[j].Hostname
	})
}
