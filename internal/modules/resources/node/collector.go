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
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const defaultCollectorReconnectDelay = 5 * time.Second

// Collector collects and persists swarm nodes snapshot.
type Collector struct {
	db         *storage.Database
	inspector  swarm.NodeManager
	store      *SQLStore
	dispatcher dispatcher.Dispatcher

	reconnectDelay time.Duration
	// synced reports whether the snapshot was refreshed at least once.
	synced bool
}

// NewNodeCollector creates node collector.
func NewNodeCollector(
	inspector swarm.NodeManager, store *SQLStore, eventDispatcher dispatcher.Dispatcher, db *storage.Database,
) *Collector {
	return &Collector{db: db,
		inspector:      inspector,
		store:          store,
		dispatcher:     eventDispatcher,
		reconnectDelay: defaultCollectorReconnectDelay,
	}
}

// Run subscribes to docker node events; every (re)subscription starts with a snapshot refresh.
// If the very first subscription fails, the snapshot is still loaded once.
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

func (c *Collector) refresh(ctx context.Context) error {
	nodes, err := c.inspector.List(ctx)
	if err != nil {
		return fmt.Errorf("inspect nodes: %w", err)
	}
	if err = c.persistEvent(ctx, dockerevents.Message{}, nodes); err != nil {
		return fmt.Errorf("save nodes snapshot: %w", err)
	}
	c.synced = true

	slog.InfoContext(ctx, "[nodes] snapshot refreshed", slog.Int("count", len(nodes)))
	return nil
}

func (c *Collector) watchOnce(parent context.Context) error {
	// Cancel the subscription on every exit path so reconnects never leave orphaned streams.
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	eventsCh, errorsCh, err := c.inspector.Watch(ctx)
	if err != nil {
		if !c.synced {
			if refreshErr := c.refresh(ctx); refreshErr != nil {
				slog.WarnContext(ctx, "[nodes] initial refresh failed", slog.Any("err", refreshErr))
			}
		}

		return fmt.Errorf("subscribe docker node events: %w", err)
	}

	// Refresh reconciles missed connection transitions without inventing joined events.
	if err = c.refresh(ctx); err != nil {
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

	currentNodes, err := c.inspector.List(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "refresh node snapshot", "err", err)
		return
	}
	err = c.persistEvent(ctx, event, currentNodes)
	if err != nil {
		slog.ErrorContext(ctx, "persist node snapshot and events", "err", err)
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

func (c *Collector) persistEvent(ctx context.Context, event dockerevents.Message, currentNodes []swarm.Node) error {
	return c.db.WithinTransaction(ctx, func(ctx context.Context) error {
		previousNodes, err := c.store.ReadAll(ctx)
		if err != nil {
			return err
		}
		currentByID := nodesByID(currentNodes)
		previousByID := nodesByID(previousNodes)
		facts := []events.Event{}
		skipID := ""
		if event.Action == dockerevents.ActionCreate {
			skipID = event.Actor.ID
			fact, createErr := c.creationFact(ctx, skipID, previousByID, currentByID)
			if createErr != nil {
				return createErr
			}
			if fact != nil {
				facts = append(facts, fact)
			}
		}
		reconnected, err := c.reappearedNodes(ctx, previousByID, currentNodes, skipID)
		if err != nil {
			return err
		}
		facts = append(facts, reconnected...)
		facts = append(facts, connectionEvents(previousNodes, currentNodes)...)
		if err = c.store.ReplaceSnapshot(ctx, currentNodes); err != nil {
			return err
		}
		for _, fact := range facts {
			if err = c.dispatcher.Publish(ctx, fact); err != nil {
				return err
			}
		}
		return nil
	})
}

func connectionEvents(previousNodes, currentNodes []swarm.Node) []events.Event {
	previousByID, currentByID := nodesByID(previousNodes), nodesByID(currentNodes)
	facts := []events.Event{}
	for _, current := range currentNodes {
		previous, exists := previousByID[current.ID]
		if !exists {
			continue
		}
		if !nodeConnected(previous) && nodeConnected(current) {
			facts = append(facts, &events.NodeConnected{NodeID: current.ID, NodeName: current.Hostname, Status: current.Status})
		}
		if nodeConnected(previous) && !nodeConnected(current) {
			facts = append(facts, &events.NodeDisconnected{
				NodeID: current.ID, NodeName: current.Hostname, Status: current.Status,
			})
		}
	}
	for _, previous := range previousNodes {
		if _, exists := currentByID[previous.ID]; !exists && nodeConnected(previous) {
			facts = append(facts, &events.NodeDisconnected{NodeID: previous.ID, NodeName: previous.Hostname, Status: "missing"})
		}
	}

	return facts
}
func (c *Collector) reappearedNodes(
	ctx context.Context, previous map[string]swarm.Node, current []swarm.Node, skipID string,
) ([]events.Event, error) {
	facts := []events.Event{}
	for _, node := range current {
		if _, exists := previous[node.ID]; exists || node.ID == skipID {
			continue
		}
		fresh, err := c.store.ObserveIdentity(ctx, node.ID)
		if err != nil {
			return nil, err
		}
		if !fresh && nodeConnected(node) {
			facts = append(facts, &events.NodeConnected{NodeID: node.ID, NodeName: node.Hostname, Status: node.Status})
		}
	}
	return facts, nil
}

func (c *Collector) creationFact(
	ctx context.Context, id string, previous, current map[string]swarm.Node,
) (events.Event, error) {
	fresh, err := c.store.ObserveIdentity(ctx, id)
	if err != nil {
		return nil, err
	}
	n, exists := current[id]
	if fresh {
		joined := &events.NodeJoined{NodeID: id}
		if exists {
			joined.NodeName, joined.Role = n.Hostname, nodeRole(n)
		}
		return joined, nil
	}
	if _, wasPresent := previous[id]; exists && !wasPresent && nodeConnected(n) {
		return &events.NodeConnected{NodeID: id, NodeName: n.Hostname, Status: n.Status}, nil
	}
	return nil, nil //nolint:nilnil // A known node without a state transition produces no fact.
}
