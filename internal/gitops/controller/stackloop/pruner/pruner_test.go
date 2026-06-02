package pruner

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

type removeExpectation struct {
	serviceID string
	err       error
}

func TestServicePrunerPrune(t *testing.T) {
	errListFailed := errors.New("list failed")
	errRemoveFailed := errors.New("remove failed")

	tests := []struct {
		name           string
		syncCfg        config.SyncPolicySpec
		stackCfg       config.StackSpec
		desired        []compose.Service
		stackServices  []swarm.StackService
		listErr        error
		removeCalls    []removeExpectation
		expectedPruned []string
		expectedErr    error
	}{
		{
			name:    "removes managed orphan services",
			syncCfg: config.SyncPolicySpec{Prune: true},
			stackCfg: config.StackSpec{
				Name: "app",
			},
			desired: []compose.Service{{Name: "api"}},
			stackServices: []swarm.StackService{
				{
					ID:     "service-api-id",
					Name:   "api",
					Labels: map[string]string{labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue},
				},
				{
					ID:     "service-worker-id",
					Name:   "worker",
					Labels: map[string]string{labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue},
				},
				{
					ID:     "service-legacy-id",
					Name:   "legacy",
					Labels: map[string]string{},
				},
			},
			removeCalls:    []removeExpectation{{serviceID: "service-worker-id"}},
			expectedPruned: []string{"worker"},
		},
		{
			name:    "skips service when prune label disabled",
			syncCfg: config.SyncPolicySpec{Prune: true},
			stackCfg: config.StackSpec{
				Name: "app",
			},
			stackServices: []swarm.StackService{
				{
					ID:   "service-worker-id",
					Name: "worker",
					Labels: map[string]string{
						labelsdict.ServiceManagedLabelKey:         labelsdict.ServiceManagedLabelValue,
						labelsdict.ServiceSyncPolicyPruneLabelKey: "false",
					},
				},
			},
			expectedPruned: []string{},
		},
		{
			name:    "skips service when stack prune policy disabled",
			syncCfg: config.SyncPolicySpec{Prune: true},
			stackCfg: config.StackSpec{
				Name: "app",
				Sync: config.StackSyncSpec{
					Policy: config.StackSyncPolicySpec{
						Prune: boolRef(false),
					},
				},
			},
			stackServices: []swarm.StackService{
				{
					ID:     "service-worker-id",
					Name:   "worker",
					Labels: map[string]string{labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue},
				},
			},
			expectedPruned: []string{},
		},
		{
			name:    "removes service when prune label enabled",
			syncCfg: config.SyncPolicySpec{Prune: false},
			stackCfg: config.StackSpec{
				Name: "app",
			},
			stackServices: []swarm.StackService{
				{
					ID:   "service-worker-id",
					Name: "worker",
					Labels: map[string]string{
						labelsdict.ServiceManagedLabelKey:         labelsdict.ServiceManagedLabelValue,
						labelsdict.ServiceSyncPolicyPruneLabelKey: "true",
					},
				},
			},
			removeCalls:    []removeExpectation{{serviceID: "service-worker-id"}},
			expectedPruned: []string{"worker"},
		},
		{
			name:    "ignores service not found on remove",
			syncCfg: config.SyncPolicySpec{Prune: true},
			stackCfg: config.StackSpec{
				Name: "app",
			},
			stackServices: []swarm.StackService{
				{
					ID:     "service-worker-id",
					Name:   "worker",
					Labels: map[string]string{labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue},
				},
			},
			removeCalls:    []removeExpectation{{serviceID: "service-worker-id", err: swarm.ErrServiceNotFound}},
			expectedPruned: []string{},
		},
		{
			name:    "returns list error",
			syncCfg: config.SyncPolicySpec{Prune: true},
			stackCfg: config.StackSpec{
				Name: "app",
			},
			listErr:     errListFailed,
			expectedErr: errListFailed,
		},
		{
			name:    "returns remove error",
			syncCfg: config.SyncPolicySpec{Prune: true},
			stackCfg: config.StackSpec{
				Name: "app",
			},
			stackServices: []swarm.StackService{
				{
					ID:     "service-worker-id",
					Name:   "worker",
					Labels: map[string]string{labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue},
				},
			},
			removeCalls: []removeExpectation{
				{
					serviceID: "service-worker-id",
					err:       errRemoveFailed,
				},
			},
			expectedErr: errRemoveFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			serviceManager := swarm.NewMockServiceManager(ctrl)
			pruner := NewServicePruner(serviceManager, tt.syncCfg)

			serviceManager.EXPECT().
				ListStackServices(gomock.Any(), tt.stackCfg.Name).
				Return(tt.stackServices, tt.listErr)

			for _, call := range tt.removeCalls {
				serviceManager.EXPECT().Remove(gomock.Any(), call.serviceID).Return(call.err)
			}

			prunedServices, err := pruner.Prune(
				context.Background(),
				PruneServicesRequest{
					tt.stackCfg,
					tt.desired,
				},
			)

			if tt.expectedErr != nil {
				require.Error(t, err, "expected prune error")
				assert.ErrorIs(t, err, tt.expectedErr, "unexpected error")
				assert.Nil(t, prunedServices, "expected no prune result")
				return
			}

			require.NoError(t, err, "prune services")
			assert.Equal(t, tt.expectedPruned, prunedServices, "unexpected pruned services")
		})
	}
}

func boolRef(v bool) *bool {
	return &v
}
