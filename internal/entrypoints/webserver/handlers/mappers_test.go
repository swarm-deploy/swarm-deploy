package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/model"
)

func TestToGeneratedStack(t *testing.T) {
	t.Parallel()

	stackCfg := config.StackSpec{
		Name:        "payments",
		ComposeFile: "stacks/payments.yaml",
	}

	testCases := []struct {
		name              string
		stack             model.Stack
		exists            bool
		expectedSynced    int64
		expectedOutOfSync int64
	}{
		{
			name: "uses persisted aggregated status",
			stack: model.Stack{
				Status: model.StackStatus{
					Synced:      3,
					OutOfSynced: 1,
				},
			},
			exists:            true,
			expectedSynced:    3,
			expectedOutOfSync: 1,
		},
		{
			name: "rebuilds status from legacy service state",
			stack: model.Stack{
				Services: map[string]model.Service{
					"api": {
						SyncStatus: model.SyncStatusSynced,
					},
					"worker": {
						SyncStatus: model.SyncStatusOutOfSync,
					},
				},
			},
			exists:            true,
			expectedSynced:    1,
			expectedOutOfSync: 1,
		},
		{
			name:              "returns zero counters when state is missing",
			exists:            false,
			expectedSynced:    0,
			expectedOutOfSync: 0,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			stack := toGeneratedStack(stackCfg, testCase.stack, testCase.exists)

			assert.Equal(t, testCase.expectedSynced, stack.Status.Synced, "unexpected synced counter")
			assert.Equal(t, testCase.expectedOutOfSync, stack.Status.OutOfSynced, "unexpected out-of-sync counter")
		})
	}
}
