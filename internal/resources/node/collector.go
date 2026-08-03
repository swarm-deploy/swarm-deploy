package node

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
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

// Run performs initial refresh and subscribes to docker node events.
func (c *Collector) Run(ctx context.Context) error {
	if _, err := c.refresh(ctx); err != nil {
		slog.WarnContext(ctx, "[nodes] initial refresh failed", slog.Any("err", err))
	}

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

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-eventsCh:
			if !ok {
				return errors.New("docker node events channel closed")
			}

			slog.DebugContext(ctx, "[nodes] docker node event received",
				slog.String("action", string(event.Action)),
				slog.String("node_id", event.Actor.ID),
			)

			previousNodes := c.store.List()
			currentNodes, refreshErr := c.refresh(ctx)
			if refreshErr != nil {
				slog.WarnContext(ctx, "[nodes] refresh after event failed", slog.Any("err", refreshErr))
				continue
			}

			c.dispatchConnectionEvents(ctx, previousNodes, currentNodes)
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
			if nodeConnected(currentNode) {
				c.dispatcher.Dispatch(ctx, &events.NodeConnected{
					NodeID:   currentNode.ID,
					NodeName: currentNode.Hostname,
					Status:   currentNode.Status,
				})
			}
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
