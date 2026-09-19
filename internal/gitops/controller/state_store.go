package controller

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/gitops/model"
)

func (c *Controller) snapshotState() model.Runtime {
	return c.stateStore.Get()
}

func (c *Controller) updateState(ctx context.Context, fn func(*model.Runtime)) {
	c.stateStore.Update(ctx, fn)
}
