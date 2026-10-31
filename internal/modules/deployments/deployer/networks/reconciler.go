// Package networks reconciles Compose-owned networks required by deployments.
package networks

import (
	"context"
	"errors"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
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
	current, inspectErr := r.manager.Get(ctx, desired.Spec.Name)
	if inspectErr == nil {
		return validateNetwork(stackName, desired, current)
	}
	if createErr != nil {
		return fmt.Errorf("create network %s: %w", desired.Spec.Name, createErr)
	}
	return fmt.Errorf("inspect created network %s: %w", desired.Spec.Name, inspectErr)
}

func validateNetwork(stackName string, desired converter.NetworkSpec, current swarm.Network) error {
	if current.ID == "" || current.Name != desired.Spec.Name || current.Scope != "swarm" {
		return fmt.Errorf("expected an existing swarm network with exact name %s", desired.Spec.Name)
	}
	if desired.External {
		return nil
	}
	if owner := current.Labels[labelsdict.StackNamespace]; owner != stackName {
		return fmt.Errorf("network %s is not owned by stack %s", desired.Spec.Name, stackName)
	}
	if current.Driver != desired.Spec.Driver {
		return fmt.Errorf("network %s has driver %q, desired %q", desired.Spec.Name, current.Driver, desired.Spec.Driver)
	}
	if current.Internal != desired.Spec.Internal {
		return fmt.Errorf(
			"network %s has internal=%t, desired %t",
			desired.Spec.Name,
			current.Internal,
			desired.Spec.Internal,
		)
	}
	if current.Attachable != desired.Spec.Attachable {
		return fmt.Errorf(
			"network %s has attachable=%t, desired %t",
			desired.Spec.Name,
			current.Attachable,
			desired.Spec.Attachable,
		)
	}
	if err := ensureMapContains(current.Labels, desired.Spec.Labels, "label"); err != nil {
		return fmt.Errorf("network %s: %w", desired.Spec.Name, err)
	}
	if err := ensureMapContains(current.Options, desired.Spec.Options, "driver option"); err != nil {
		return fmt.Errorf("network %s: %w", desired.Spec.Name, err)
	}
	return nil
}

func ensureMapContains(actual, desired map[string]string, kind string) error {
	for key, desiredValue := range desired {
		actualValue, exists := actual[key]
		if !exists {
			return fmt.Errorf("missing %s %q", kind, key)
		}
		if actualValue != desiredValue {
			return fmt.Errorf("%s %q=%q, desired %q", kind, key, actualValue, desiredValue)
		}
	}
	return nil
}
