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

const defaultSnapshotRetryDelay = 5 * time.Second

// Keep newly discovered identities long enough to match a Create arriving
// while snapshot reconciliation is in progress. Expiration bounds the cache.
const observedJoinTTL = 10 * time.Minute

// Collector collects and persists swarm nodes snapshot.
type Collector struct {
	db           *storage.Database
	inspector    swarm.NodeManager
	store        *SQLStore
	dispatcher   dispatcher.Dispatcher
	subscription swarm.EventSubscription[swarm.NodeEvent]
	retryDelay   time.Duration
	pendingEvent *swarm.NodeEvent
	joinCandidates map[string]time.Time
}

// NewNodeCollector creates node collector.
func NewNodeCollector(
	inspector swarm.NodeManager,
	store *SQLStore,
	eventDispatcher dispatcher.Dispatcher,
	db *storage.Database,
	subscription swarm.EventSubscription[swarm.NodeEvent],
) *Collector {
	return &Collector{
		db:           db,
		inspector:    inspector,
		store:        store,
		dispatcher:   eventDispatcher,
		subscription: subscription,
		retryDelay:   defaultSnapshotRetryDelay,
		joinCandidates: make(map[string]time.Time),
	}
}

// Run performs initial refresh and subscribes to Docker node events.
func (c *Collector) Run(ctx context.Context) error {
	defer c.subscription.Close()

	err := c.refresh(ctx)
	if err != nil {
		slog.WarnContext(ctx, "[nodes] initial refresh failed", slog.Any("err", err))
	}
	return c.watch(ctx, err != nil)
}

func (c *Collector) watch(ctx context.Context, retryPending bool) error {
	retryTicker := time.NewTicker(c.retryDelay)
	defer retryTicker.Stop()

	// A failed snapshot must be retried even if Docker sends no more events.
	// A successful reconciliation clears the retry state to avoid needless polling.
	reconcile := func(reason string) {
		retryPending = !c.reconcileWithWarning(ctx, reason)
	}

	for {
		// Preserve a failed event for retry; do not consume later events before it.
		eventsCh := c.subscription.Events
		if c.pendingEvent != nil {
			eventsCh = nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-retryTicker.C:
			if retryPending {
				reconcile("retry")
			}
		case event, ok := <-eventsCh:
			if !ok {
				return closedSubscription(ctx, "docker node events channel closed")
			}
			if err := c.handleEvent(ctx, event); err != nil {
				c.pendingEvent = &event
				retryPending = true
				slog.WarnContext(ctx, "[nodes] process event failed; retry scheduled",
					slog.String("node_id", event.NodeID),
					slog.Any("err", err),
				)
			}
		case resync, ok := <-c.subscription.Resync:
			if !ok {
				return closedSubscription(ctx, "docker events resync channel closed")
			}
			reconcile(string(resync.Reason))
		}
	}
}

func closedSubscription(ctx context.Context, message string) error {
	if ctx.Err() != nil {
		return nil
	}
	return errors.New(message)
}

func (c *Collector) reconcileWithWarning(ctx context.Context, reason string) bool {
	if err := c.reconcileAfterResync(ctx); err != nil {
		slog.WarnContext(ctx, "[nodes] snapshot reconciliation failed",
			slog.String("reason", reason),
			slog.Any("err", err),
		)
		return false
	}
	return true
}

// reconcileAfterResync processes events already accepted into the bounded queue
// before replacing the snapshot. Otherwise a queued Create can be overtaken by
// a resync and lose its NodeJoined fact when ObserveIdentity sees the node.
// Events actually dropped on overflow cannot be reconstructed as historical
// joins from a snapshot; reconciliation restores state, not missing facts.
func (c *Collector) reconcileAfterResync(ctx context.Context) error {
	// A failed Create must be retried before a snapshot can consume its identity.
	if c.pendingEvent != nil {
		if err := c.handleEvent(ctx, *c.pendingEvent); err != nil {
			return err
		}
		c.pendingEvent = nil
	}
	// Snapshot the queue length to avoid starving reconciliation when producers
	// keep publishing new events. FIFO preserves the order of accepted events.
	for pending := len(c.subscription.Events); pending > 0; pending-- {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-c.subscription.Events:
			if !ok {
				return errors.New("docker node events channel closed")
			}
			if err := c.handleEvent(ctx, event); err != nil {
				c.pendingEvent = &event
				return err
			}
		}
	}
	return c.refresh(ctx)
}

func (c *Collector) refresh(ctx context.Context) error {
	nodes, err := c.inspector.List(ctx)
	if err != nil {
		return fmt.Errorf("inspect nodes: %w", err)
	}
	if err = c.persistEvent(ctx, dockerevents.Message{}, nodes); err != nil {
		return fmt.Errorf("save nodes snapshot: %w", err)
	}
	slog.InfoContext(ctx, "[nodes] snapshot refreshed", slog.Int("count", len(nodes)))
	return nil
}

func (c *Collector) handleEvent(ctx context.Context, event swarm.NodeEvent) error {
	slog.DebugContext(ctx, "[nodes] docker node event received",
		slog.String("action", string(event.Action)),
		slog.String("node_id", event.NodeID),
		slog.Any("node_attributes", event.Attributes),
	)

	currentNodes, err := c.inspector.List(ctx)
	if err != nil {
		return fmt.Errorf("list nodes for event %s: %w", event.NodeID, err)
	}
	if err = c.persistEvent(ctx, dockerevents.Message{
		Action: dockerevents.Action(event.Action),
		Actor: dockerevents.Actor{
			ID:         event.NodeID,
			Attributes: event.Attributes,
		},
	}, currentNodes); err != nil {
		return fmt.Errorf("persist node event %s: %w", event.NodeID, err)
	}
	return nil
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
	freshIDs := []string{}
	err := c.db.WithinTransaction(ctx, func(ctx context.Context) error {
		return c.persistEventTx(ctx, event, currentNodes, &freshIDs)
	})
	if err != nil {
		return err
	}
	if event.Action == dockerevents.ActionCreate {
		delete(c.joinCandidates, event.Actor.ID)
	}
	c.rememberFreshNodes(freshIDs)
	return nil
}

func (c *Collector) persistEventTx(
	ctx context.Context, event dockerevents.Message, currentNodes []swarm.Node, freshIDs *[]string,
) error {
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
	reconnected, err := c.reappearedNodes(ctx, previousByID, currentNodes, skipID, freshIDs)
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
}

func (c *Collector) rememberFreshNodes(ids []string) {
	now := time.Now()
	for id, observed := range c.joinCandidates {
		if now.Sub(observed) > observedJoinTTL {
			delete(c.joinCandidates, id)
		}
	}
	for _, id := range ids {
		c.joinCandidates[id] = now
	}
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
	ctx context.Context, previous map[string]swarm.Node, current []swarm.Node, skipID string, freshIDs *[]string,
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
		if fresh {
			*freshIDs = append(*freshIDs, node.ID)
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
	_, recentlyObserved := c.joinCandidates[id]
	if fresh || recentlyObserved {
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
