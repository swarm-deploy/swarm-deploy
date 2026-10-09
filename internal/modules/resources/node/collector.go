package node

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	dockerevents "github.com/docker/docker/api/types/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const defaultCollectorReconnectDelay = 5 * time.Second

// Collector collects and persists swarm nodes snapshot.
type Collector struct {
	inspector  swarm.NodeManager
	store      *Store
	dispatcher dispatcher.Dispatcher

	reconnectDelay time.Duration
}

// NewNodeCollector creates node collector.
func NewNodeCollector(inspector swarm.NodeManager, store *Store, eventDispatcher dispatcher.Dispatcher) *Collector {
	return &Collector{
		inspector:      inspector,
		store:          store,
		dispatcher:     eventDispatcher,
		reconnectDelay: defaultCollectorReconnectDelay,
	}
}

// Run subscribes to docker node events; every (re)subscription starts with a snapshot refresh.
func (c *Collector) Run(ctx context.Context) error {
	for {
		err := c.watchOnce(ctx)
		if err == nil {
			return nil
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		slog.WarnContext(ctx, "[nodes] watch stream failed", slog.Any("err", err))

		timer := time.NewTimer(c.reconnectDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (c *Collector) refresh(ctx context.Context) ([]swarm.Node, error) {
	nodes, err := c.inspector.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("inspect nodes: %w", err)
	}
	if err = c.store.Replace(nodes); err != nil {
		return nil, fmt.Errorf("save nodes snapshot: %w", err)
	}

	slog.InfoContext(ctx, "[nodes] snapshot refreshed", slog.Int("count", len(nodes)))
	return nodes, nil
}

func (c *Collector) watchOnce(ctx context.Context) error {
	eventsCh, errorsCh, err := c.inspector.Watch(ctx)
	if err != nil {
		return fmt.Errorf("subscribe docker node events: %w", err)
	}

	// Events missed while the stream was down are not replayed: only the snapshot is refreshed.
	if _, err = c.refresh(ctx); err != nil {
		return fmt.Errorf("refresh nodes after subscribe: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-eventsCh:
			if !ok {
				return errors.New("docker node events channel closed")
			}

			c.handleEvent(ctx, event)
		case watchErr, ok := <-errorsCh:
			if !ok {
				return errors.New("docker node events errors channel closed")
			}
			if watchErr == nil {
				continue
			}
			return fmt.Errorf("watch docker node events: %w", watchErr)
		}
	}
}

func (c *Collector) handleEvent(ctx context.Context, event dockerevents.Message) {
	slog.DebugContext(ctx, "[nodes] docker node event received",
		slog.String("action", string(event.Action)),
		slog.String("node_id", event.Actor.ID),
		slog.Any("node_attributes", event.Actor.Attributes),
	)

	previousNodes := c.store.List()
	currentNodes, refreshErr := c.refresh(ctx)
	if refreshErr != nil {
		slog.WarnContext(ctx, "[nodes] refresh after event failed", slog.Any("err", refreshErr))
	}

	if event.Action == dockerevents.ActionCreate {
		c.dispatchNodeJoined(ctx, event.Actor.ID, currentNodes)
	}

	if refreshErr != nil {
		return
	}

	c.dispatchConnectionEvents(ctx, previousNodes, currentNodes)
}

func (c *Collector) dispatchNodeJoined(ctx context.Context, nodeID string, currentNodes []swarm.Node) {
	joined := &events.NodeJoined{NodeID: nodeID}
	if node, found := nodesByID(currentNodes)[nodeID]; found {
		joined.NodeName = node.Hostname
		joined.Role = nodeRole(node)
	}

	c.dispatcher.Dispatch(ctx, joined)
}

func (c *Collector) dispatchConnectionEvents(
	ctx context.Context,
	previousNodes []swarm.Node,
	currentNodes []swarm.Node,
) {
	previousByID := nodesByID(previousNodes)
	currentByID := nodesByID(currentNodes)

	for _, currentNode := range currentNodes {
		previousNode, exists := previousByID[currentNode.ID]
		if !exists {
			continue
		}

		if !nodeConnected(previousNode) && nodeConnected(currentNode) {
			c.dispatcher.Dispatch(ctx, &events.NodeConnected{
				NodeID:   currentNode.ID,
				NodeName: currentNode.Hostname,
				Status:   currentNode.Status,
			})
			continue
		}

		if nodeConnected(previousNode) && !nodeConnected(currentNode) {
			c.dispatcher.Dispatch(ctx, &events.NodeDisconnected{
				NodeID:   currentNode.ID,
				NodeName: currentNode.Hostname,
				Status:   currentNode.Status,
			})
		}
	}

	for _, previousNode := range previousNodes {
		if _, exists := currentByID[previousNode.ID]; exists {
			continue
		}
		if !nodeConnected(previousNode) {
			continue
		}

		c.dispatcher.Dispatch(ctx, &events.NodeDisconnected{
			NodeID:   previousNode.ID,
			NodeName: previousNode.Hostname,
			Status:   "missing",
		})
	}
}

func nodesByID(nodes []swarm.Node) map[string]swarm.Node {
	mapped := make(map[string]swarm.Node, len(nodes))
	for _, node := range nodes {
		mapped[node.ID] = node
	}

	return mapped
}

func nodeConnected(node swarm.Node) bool {
	return node.Status == "ready"
}

func nodeRole(node swarm.Node) string {
	switch node.ManagerStatus {
	case swarm.NodeManagerStatusWorker:
		return "worker"
	case swarm.NodeManagerStatusLeader,
		swarm.NodeManagerStatusManager,
		swarm.NodeManagerStatusReachable,
		swarm.NodeManagerStatusUnreachable:
		return "manager"
	default:
		return ""
	}
}
