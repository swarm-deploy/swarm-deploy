package node

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const defaultCollectorReconnectDelay = 5 * time.Second

// Collector collects and persists swarm nodes snapshot.
type Collector struct {
	inspector swarm.NodeManager
	store     *Store

	reconnectDelay time.Duration
}

// NewNodeCollector creates node collector.
func NewNodeCollector(inspector swarm.NodeManager, store *Store) *Collector {
	return &Collector{
		inspector:      inspector,
		store:          store,
		reconnectDelay: defaultCollectorReconnectDelay,
	}
}

// Run performs initial refresh and subscribes to docker node events.
func (c *Collector) Run(ctx context.Context) error {
	if err := c.refresh(ctx); err != nil {
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

func (c *Collector) refresh(ctx context.Context) error {
	nodes, err := c.inspector.List(ctx)
	if err != nil {
		return fmt.Errorf("inspect nodes: %w", err)
	}
	if err = c.store.Replace(nodes); err != nil {
		return fmt.Errorf("save nodes snapshot: %w", err)
	}

	slog.InfoContext(ctx, "[nodes] snapshot refreshed", slog.Int("count", len(nodes)))
	return nil
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

			if refreshErr := c.refresh(ctx); refreshErr != nil {
				slog.WarnContext(ctx, "[nodes] refresh after event failed", slog.Any("err", refreshErr))
			}
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
