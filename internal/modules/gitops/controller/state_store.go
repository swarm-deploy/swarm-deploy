package controller

import (
	"context"
	"log/slog"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
)

func (c *Controller) snapshotState(ctx context.Context) (model.Runtime, error) {
	return c.stateStore.Read(ctx)
}

func (c *Controller) updateState(ctx context.Context, fn func(*model.Runtime)) error {
	if err := c.stateStore.Update(ctx, fn); err != nil {
		slog.ErrorContext(ctx, "persist controller status failed", "err", err)
		return err
	}
	return nil
}
