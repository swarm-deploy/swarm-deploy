package swarm

import (
	"context"
	"fmt"

	dockerevents "github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	dockerswarm "github.com/docker/docker/api/types/swarm"
)

// InspectNodes returns current swarm nodes snapshot.
func (i *Inspector) InspectNodes(ctx context.Context) ([]NodeInfo, error) {
	nodes, err := i.dockerClient.NodeList(ctx, dockerswarm.NodeListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list swarm nodes: %w", err)
	}

	mapped := make([]NodeInfo, len(nodes))
	for i, node := range nodes {
		mapped[i] = toNodeInfo(node)
	}
	sortNodeInfos(mapped)

	return mapped, nil
}

// WatchNodeEvents subscribes to Docker node events stream.
func (i *Inspector) WatchNodeEvents(
	ctx context.Context,
) (<-chan dockerevents.Message, <-chan error, error) {
	eventsFilter := filters.NewArgs(filters.Arg("type", string(dockerevents.NodeEventType)))
	messages, errs := i.dockerClient.Events(ctx, dockerevents.ListOptions{
		Filters: eventsFilter,
	})

	return messages, errs, nil
}

func toNodeInfo(node dockerswarm.Node) NodeInfo {
	managerStatus := "worker"
	if node.ManagerStatus != nil {
		switch {
		case node.ManagerStatus.Leader:
			managerStatus = "leader"
		case node.ManagerStatus.Reachability != "":
			managerStatus = string(node.ManagerStatus.Reachability)
		}
	}

	return NodeInfo{
		ID:            node.ID,
		Hostname:      node.Description.Hostname,
		Status:        string(node.Status.State),
		Availability:  string(node.Spec.Availability),
		ManagerStatus: managerStatus,
		EngineVersion: node.Description.Engine.EngineVersion,
		Addr:          node.Status.Addr,
	}
}
