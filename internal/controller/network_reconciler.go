package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/controller/statem"
	"github.com/swarm-deploy/swarm-deploy/internal/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type networkReconciler struct {
	manager swarm.NetworkManager
}

func newNetworkReconciler(manager swarm.NetworkManager) *networkReconciler {
	return &networkReconciler{
		manager: manager,
	}
}

func (r *networkReconciler) Reconcile(ctx context.Context, networkCfg config.NetworkSpec) (bool, error) {
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

	managedErr := ensureManagedNetwork(current)
	if managedErr != nil {
		return false, managedErr
	}
	matchErr := ensureNetworkMatches(current, desired)
	if matchErr != nil {
		return false, matchErr
	}

	return true, nil
}

func withManagedNetworkLabel(labels map[string]string) (map[string]string, error) {
	normalized := cloneStringMap(labels)

	if labelValue, exists := normalized[labelsdict.ManagedNetworkKey]; exists {
		if strings.TrimSpace(labelValue) != labelsdict.ManagedNetworkValue {
			return nil, fmt.Errorf("label %q must be %q",
				labelsdict.ManagedNetworkKey,
				labelsdict.ManagedNetworkValue,
			)
		}
	}

	normalized[labelsdict.ManagedNetworkKey] = labelsdict.ManagedNetworkValue
	return normalized, nil
}

func ensureManagedNetwork(network swarm.Network) error {
	labelValue := strings.TrimSpace(network.Labels[labelsdict.ManagedNetworkKey])
	if labelValue == labelsdict.ManagedNetworkValue {
		return nil
	}

	return fmt.Errorf(
		"network %s already exists but is not managed by swarm-deploy: missing label %s=%s",
		network.Name,
		labelsdict.ManagedNetworkKey,
		labelsdict.ManagedNetworkValue,
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

func (c *Controller) reloadNetworks() (string, error) {
	if c.cfg.Spec.NetworksSource.File == "" {
		c.cfg.Spec.Networks = nil
		return "", nil
	}

	return c.cfg.ReloadNetworks(c.git.WorkingDir())
}

func (c *Controller) syncNetworks(ctx context.Context, commit string) error {
	if len(c.cfg.Spec.Networks) == 0 {
		c.stateStore.Update(func(s *statem.Runtime) {
			s.Networks = map[string]statem.Network{}
		})
		return nil
	}

	currentState := c.snapshotState()
	syncedAt := time.Now()
	nextState := make(map[string]statem.Network, len(c.cfg.Spec.Networks))
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

		networkState := statem.Network{
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

	c.stateStore.Update(func(s *statem.Runtime) {
		s.Networks = nextState
	})

	return errors.Join(reconcileErrs...)
}
