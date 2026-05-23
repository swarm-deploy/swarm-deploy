package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestStackReconcilerPruneOrphanedServicesUsesServiceLabelPriority(t *testing.T) {
	reconciler := &stackReconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				Sync: config.SyncSpec{
					Policy: config.SyncPolicySpec{Prune: false},
				},
			},
		},
		services: &fakeStackServiceManager{
			list: []swarm.StackService{
				{
					ID:   "svc-1",
					Name: "old",
					Labels: map[string]string{
						managedServiceLabelKey:         managedServiceLabelValue,
						serviceSyncPolicyPruneLabelKey: "true",
					},
				},
			},
		},
	}

	stackCfg := config.StackSpec{
		Name: "payments",
		Sync: config.StackSyncSpec{
			Policy: config.StackSyncPolicySpec{
				Prune: boolPointer(false),
			},
		},
	}

	pruned, err := reconciler.pruneOrphanedServices(context.Background(), stackCfg, []compose.Service{{Name: "api"}})
	require.NoError(t, err, "prune orphaned services")
	assert.Equal(t, []string{"old"}, pruned, "service-level prune policy must have highest priority")

	manager := reconciler.services.(*fakeStackServiceManager)
	assert.Equal(t, []string{"svc-1"}, manager.removed, "expected orphaned service removal")
}

func TestStackReconcilerPruneOrphanedServicesUsesStackPolicyFallback(t *testing.T) {
	reconciler := &stackReconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				Sync: config.SyncSpec{
					Policy: config.SyncPolicySpec{Prune: false},
				},
			},
		},
		services: &fakeStackServiceManager{
			list: []swarm.StackService{
				{
					ID:   "svc-1",
					Name: "old",
					Labels: map[string]string{
						managedServiceLabelKey: managedServiceLabelValue,
					},
				},
			},
		},
	}

	stackCfg := config.StackSpec{
		Name: "payments",
		Sync: config.StackSyncSpec{
			Policy: config.StackSyncPolicySpec{
				Prune: boolPointer(true),
			},
		},
	}

	pruned, err := reconciler.pruneOrphanedServices(context.Background(), stackCfg, nil)
	require.NoError(t, err, "prune orphaned services")
	assert.Equal(t, []string{"old"}, pruned, "stack-level policy must be used when service label is missing")
}

func TestStackReconcilerPruneOrphanedServicesUsesGlobalPolicyFallback(t *testing.T) {
	reconciler := &stackReconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				Sync: config.SyncSpec{
					Policy: config.SyncPolicySpec{Prune: true},
				},
			},
		},
		services: &fakeStackServiceManager{
			list: []swarm.StackService{
				{
					ID:   "svc-1",
					Name: "old",
					Labels: map[string]string{
						managedServiceLabelKey: managedServiceLabelValue,
					},
				},
			},
		},
	}

	pruned, err := reconciler.pruneOrphanedServices(context.Background(), config.StackSpec{Name: "payments"}, nil)
	require.NoError(t, err, "prune orphaned services")
	assert.Equal(t, []string{"old"}, pruned, "global policy must be used when stack/service overrides are missing")
}

func TestStackReconcilerPruneOrphanedServicesReturnsErrorOnInvalidPolicyLabel(t *testing.T) {
	reconciler := &stackReconciler{
		cfg: &config.Config{},
		services: &fakeStackServiceManager{
			list: []swarm.StackService{
				{
					ID:   "svc-1",
					Name: "old",
					Labels: map[string]string{
						managedServiceLabelKey:         managedServiceLabelValue,
						serviceSyncPolicyPruneLabelKey: "not-a-bool",
					},
				},
			},
		},
	}

	_, err := reconciler.pruneOrphanedServices(context.Background(), config.StackSpec{Name: "payments"}, nil)
	require.Error(t, err, "invalid service policy label must fail reconciliation")
	assert.Contains(t, err.Error(), serviceSyncPolicyPruneLabelKey, "error must reference invalid label")
}

func TestStackReconcilerPruneOrphanedServicesIgnoresUnmanagedService(t *testing.T) {
	manager := &fakeStackServiceManager{
		list: []swarm.StackService{
			{
				ID:   "svc-1",
				Name: "old",
				Labels: map[string]string{
					managedServiceLabelKey: "false",
				},
			},
		},
	}
	reconciler := &stackReconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				Sync: config.SyncSpec{
					Policy: config.SyncPolicySpec{Prune: true},
				},
			},
		},
		services: manager,
	}

	pruned, err := reconciler.pruneOrphanedServices(context.Background(), config.StackSpec{Name: "payments"}, nil)
	require.NoError(t, err, "prune orphaned services")
	assert.Empty(t, pruned, "unmanaged services must not be removed")
	assert.Empty(t, manager.removed, "unmanaged services must not trigger remove call")
}

func TestStackReconcilerPruneOrphanedServicesIgnoresAlreadyRemovedService(t *testing.T) {
	manager := &fakeStackServiceManager{
		list: []swarm.StackService{
			{
				ID:   "svc-1",
				Name: "old",
				Labels: map[string]string{
					managedServiceLabelKey: managedServiceLabelValue,
				},
			},
		},
		removeErr: swarm.ErrServiceNotFound,
	}
	reconciler := &stackReconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				Sync: config.SyncSpec{
					Policy: config.SyncPolicySpec{Prune: true},
				},
			},
		},
		services: manager,
	}

	pruned, err := reconciler.pruneOrphanedServices(context.Background(), config.StackSpec{Name: "payments"}, nil)
	require.NoError(t, err, "prune orphaned services")
	assert.Empty(t, pruned, "already removed services must not fail reconcile")
	assert.Equal(t, []string{"svc-1"}, manager.removed, "remove should be attempted once")
}

func TestResolveServicePrunePolicyRejectsInvalidLabel(t *testing.T) {
	_, err := resolveServicePrunePolicy(
		map[string]string{serviceSyncPolicyPruneLabelKey: "invalid"},
		config.StackSpec{},
		config.SyncPolicySpec{Prune: true},
	)
	require.Error(t, err, "invalid label value must fail")
}

func boolPointer(value bool) *bool {
	result := value
	return &result
}

type fakeStackServiceManager struct {
	list      []swarm.StackService
	removed   []string
	listErr   error
	removeErr error
}

func (f *fakeStackServiceManager) ListStackServices(_ context.Context, _ string) ([]swarm.StackService, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}

	out := make([]swarm.StackService, len(f.list))
	copy(out, f.list)
	return out, nil
}

func (f *fakeStackServiceManager) Remove(_ context.Context, serviceIDOrName string) error {
	f.removed = append(f.removed, serviceIDOrName)
	if f.removeErr != nil {
		return f.removeErr
	}
	return nil
}

var _ stackServiceManager = (*fakeStackServiceManager)(nil)

func TestStackReconcilerPruneOrphanedServicesReturnsListError(t *testing.T) {
	reconciler := &stackReconciler{
		cfg: &config.Config{},
		services: &fakeStackServiceManager{
			listErr: errors.New("list failed"),
		},
	}

	_, err := reconciler.pruneOrphanedServices(context.Background(), config.StackSpec{Name: "payments"}, nil)
	require.Error(t, err, "list error must be returned")
	assert.Contains(t, err.Error(), "list failed", "unexpected list error")
}
