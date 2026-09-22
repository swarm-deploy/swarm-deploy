package networkloop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Reconciler applies a desired network state to swarm.
type Reconciler struct {
	manager swarm.NetworkManager
}

// New builds a network reconciler.
func New(manager swarm.NetworkManager) *Reconciler {
	return &Reconciler{
		manager: manager,
	}
}

// Reconcile creates a missing network or validates an existing managed network.
func (r *Reconciler) Reconcile(ctx context.Context, networkCfg config.NetworkSpec) (bool, error) {
	desiredLabels, err := withManagedNetworkLabel(networkCfg.Labels)
	if err != nil {
		return false, err
	}

	desired := swarm.CreateNetworkRequest{
		Name:       networkCfg.Name,
		Driver:     networkCfg.Driver,
		Attachable: networkCfg.Attachable,
		Internal:   networkCfg.Internal,
		Labels:     desiredLabels,
		Options:    networkCfg.Options,
	}

	current, err := r.manager.Get(ctx, networkCfg.Name)
	if err != nil {
		if errors.Is(err, swarm.ErrNetworkNotFound) {
			slog.InfoContext(ctx, "[network-reconciler] creating network", slog.String("network.name", networkCfg.Name))

			_, createErr := r.manager.Create(ctx, desired)
			if createErr != nil {
				return false, fmt.Errorf("create network: %w", createErr)
			}
			return false, nil
		}

		return false, fmt.Errorf("get network: %w", err)
	}

	if err := ensureManagedNetwork(current); err != nil {
		return false, err
	}
	if err := ensureNetworkMatches(current, desired); err != nil {
		return false, err
	}

	return true, nil
}

func withManagedNetworkLabel(labels map[string]string) (map[string]string, error) {
	normalized := cloneStringMap(labels)

	if labelValue, exists := normalized[labelsdict.NetworkManagedKey]; exists {
		if strings.TrimSpace(labelValue) != labelsdict.NetworkManagedValue {
			return nil, fmt.Errorf("label %q must be %q",
				labelsdict.NetworkManagedKey,
				labelsdict.NetworkManagedValue,
			)
		}
	}

	normalized[labelsdict.NetworkManagedKey] = labelsdict.NetworkManagedValue
	return normalized, nil
}

func ensureManagedNetwork(network swarm.Network) error {
	labelValue := strings.TrimSpace(network.Labels[labelsdict.NetworkManagedKey])
	if labelValue == labelsdict.NetworkManagedValue {
		return nil
	}

	return fmt.Errorf(
		"network %s already exists but is not managed by swarm-deploy: missing label %s=%s",
		network.Name,
		labelsdict.NetworkManagedKey,
		labelsdict.NetworkManagedValue,
	)
}

func ensureNetworkMatches(current swarm.Network, desired swarm.CreateNetworkRequest) error {
	if current.Driver != desired.Driver {
		return fmt.Errorf("network drift: driver=%q, desired=%q", current.Driver, desired.Driver)
	}
	if current.Internal != desired.Internal {
		return fmt.Errorf("network drift: internal=%t, desired=%t", current.Internal, desired.Internal)
	}
	if current.Attachable != desired.Attachable {
		return fmt.Errorf("network drift: attachable=%t, desired=%t", current.Attachable, desired.Attachable)
	}
	if err := ensureMapContains(current.Labels, desired.Labels, "label"); err != nil {
		return err
	}
	if err := ensureMapContains(current.Options, desired.Options, "option"); err != nil {
		return err
	}

	return nil
}

func ensureMapContains(actual map[string]string, expected map[string]string, itemName string) error {
	for expectedKey, expectedValue := range expected {
		actualValue, exists := actual[expectedKey]
		if !exists {
			return fmt.Errorf("network drift: missing %s %q", itemName, expectedKey)
		}
		if actualValue != expectedValue {
			return fmt.Errorf(
				"network drift: %s %q=%q, desired=%q",
				itemName,
				expectedKey,
				actualValue,
				expectedValue,
			)
		}
	}

	return nil
}

func cloneStringMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}

	return cloned
}
