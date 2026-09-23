package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func (c *Controller) reloadNetworks() (string, error) {
	if c.cfg.Spec.NetworksSource.File == "" {
		c.cfg.Spec.Networks = nil
		return "", nil
	}

	return c.cfg.ReloadNetworks(c.git.WorkingDir())
}

func (c *Controller) syncNetworks(ctx context.Context, commit string) error {
	ctx, span := c.tracer.Start(ctx, "controller.SyncNetworks", trace.WithAttributes(
		tracing.SyncCommitSha.String(commit),
	))
	defer span.End()

	if len(c.cfg.Spec.Networks) == 0 {
		c.stateStore.Update(ctx, func(s *model.Runtime) {
			s.Networks = map[string]model.Network{}
		})
		return nil
	}

	currentState := c.snapshotState()
	syncedAt := time.Now()
	nextState := make(map[string]model.Network, len(c.cfg.Spec.Networks))
	var reconcileErrs []error
	for _, networkCfg := range c.cfg.Spec.Networks {
		if previousState, exists := currentState.Networks[networkCfg.Name]; exists {
			if previousState.LastCommit == commit &&
				(previousState.LastStatus == "success" || previousState.LastStatus == "no_change") {
				nextState[networkCfg.Name] = previousState
				continue
			}
		}

		skipped, err := c.networkReconciler.Reconcile(ctx, networkCfg)

		networkState := model.Network{
			Driver:     networkCfg.Driver,
			LastCommit: commit,
			LastStatus: "success",
			LastError:  "",
			LastSyncAt: syncedAt,
		}
		if err != nil {
			reconcileErrs = append(
				reconcileErrs,
				fmt.Errorf("network %s: %w", networkCfg.Name, err),
			)
			networkState.LastStatus = "failed"
			networkState.LastError = err.Error()
		} else if skipped {
			networkState.LastStatus = "no_change"
		}

		nextState[networkCfg.Name] = networkState
	}

	c.stateStore.Update(ctx, func(s *model.Runtime) {
		s.Networks = nextState
	})

	joinedErr := errors.Join(reconcileErrs...)

	if joinedErr != nil {
		span.RecordError(joinedErr)
		span.SetStatus(codes.Error, joinedErr.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}

	return joinedErr
}
