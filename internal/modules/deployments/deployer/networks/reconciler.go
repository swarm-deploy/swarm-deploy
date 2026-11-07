// Package networks reconciles Compose-owned networks required by deployments.
package networks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const (
	createdNetworkInspectAttempts = 40
	createdNetworkInspectDelay    = 50 * time.Millisecond
)

// Reconciler creates missing Compose-owned networks and validates existing networks.
type Reconciler struct {
	manager swarm.NetworkManager
}

// New creates a Compose network reconciler.
func New(manager swarm.NetworkManager) *Reconciler {
	return &Reconciler{manager: manager}
}

// Reconcile ensures all planned networks exist and are compatible with the desired stack.
func (r *Reconciler) Reconcile(ctx context.Context, stackName string, planned []converter.NetworkSpec) error {
	for _, network := range planned {
		if err := r.reconcile(ctx, stackName, network); err != nil {
			return fmt.Errorf("networks.%s: %w", network.Alias, err)
		}
	}
	return nil
}

func (r *Reconciler) reconcile(ctx context.Context, stackName string, desired converter.NetworkSpec) error {
	current, err := r.manager.Get(ctx, desired.Spec.Name)
	if err == nil {
		return validateNetwork(stackName, desired, current)
	}
	if !errors.Is(err, swarm.ErrNetworkNotFound) {
		return fmt.Errorf("inspect network %s: %w", desired.Spec.Name, err)
	}
	if desired.External {
		return fmt.Errorf("external network %s does not exist", desired.Spec.Name)
	}

	_, createErr := r.manager.Create(ctx, desired.Spec)
	if createErr == nil {
		return r.waitForCreatedNetwork(ctx, stackName, desired)
	}

	current, inspectErr := r.manager.Get(ctx, desired.Spec.Name)
	if inspectErr == nil {
		return validateNetwork(stackName, desired, current)
	}
	return fmt.Errorf("create network %s: %w", desired.Spec.Name, createErr)
}

func (r *Reconciler) waitForCreatedNetwork(
	ctx context.Context,
	stackName string,
	desired converter.NetworkSpec,
) error {
	var lastErr error
	for attempt := 0; attempt < createdNetworkInspectAttempts; attempt++ {
		current, err := r.manager.Get(ctx, desired.Spec.Name)
		if err == nil {
			err = validateNetwork(stackName, desired, current)
		}
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt == createdNetworkInspectAttempts-1 {
			break
		}
		timer := time.NewTimer(createdNetworkInspectDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for created network %s: %w", desired.Spec.Name, ctx.Err())
		case <-timer.C:
		}
	}
	return fmt.Errorf("validate created network %s: %w", desired.Spec.Name, lastErr)
}

func validateNetwork(stackName string, desired converter.NetworkSpec, current swarm.Network) error {
	if current.ID == "" || current.Name != desired.Spec.Name ||
		!swarm.ServiceNetworkScope(current.Name, current.Scope, desired.External) {
		return fmt.Errorf("expected an existing swarm network with exact name %s", desired.Spec.Name)
	}
	if desired.External {
		return nil
	}
	if owner := current.Labels[labelsdict.StackNamespace]; owner != stackName {
		return fmt.Errorf("network %s is not owned by stack %s", desired.Spec.Name, stackName)
	}
	// Network settings are immutable. Reuse the stack-owned identity, like the
	// stack CLI, without blocking application updates or recreating the network.
	return nil
}
