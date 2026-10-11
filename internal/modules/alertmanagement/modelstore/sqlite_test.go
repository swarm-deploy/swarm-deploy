package modelstore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func TestSQLStoreListAppliesAlertFiltersAndNewestOrdering(t *testing.T) {
	store := NewSQLStore(testutil.OpenSQLite(t))
	base := time.Date(2026, time.October, 11, 10, 0, 0, 0, time.UTC)
	alerts := []model.Alert{
		{ID: "old-stack", Fingerprint: "old-stack", Kind: model.AlertKindDeployFailed,
			ResourceType: model.ResourceTypeStack, ResourceID: "api", Status: model.AlertStatusOpen,
			OpenedAt: base, UpdatedAt: base},
		{ID: "new-stack", Fingerprint: "new-stack", Kind: model.AlertKindDeployFailed,
			ResourceType: model.ResourceTypeStack, ResourceID: "web", Status: model.AlertStatusResolved,
			OpenedAt: base, UpdatedAt: base.Add(2 * time.Minute)},
		{ID: "node", Fingerprint: "node", Kind: model.AlertKindNodeDisconnected,
			ResourceType: model.ResourceTypeNode, ResourceID: "node-1", Status: model.AlertStatusOpen,
			OpenedAt: base, UpdatedAt: base.Add(time.Minute)},
	}
	for _, alert := range alerts {
		require.NoError(t, store.Create(t.Context(), alert))
	}

	testCases := []struct {
		name   string
		filter ListFilter
		want   []string
	}{
		{name: "all newest first", filter: ListFilter{}, want: []string{"new-stack", "node", "old-stack"}},
		{name: "status", filter: ListFilter{Status: model.AlertStatusOpen}, want: []string{"node", "old-stack"}},
		{name: "kind", filter: ListFilter{Kind: model.AlertKindDeployFailed}, want: []string{"new-stack", "old-stack"}},
		{name: "resource type", filter: ListFilter{ResourceType: model.ResourceTypeNode}, want: []string{"node"}},
		{name: "resource id", filter: ListFilter{ResourceID: "api"}, want: []string{"old-stack"}},
		{name: "combined", filter: ListFilter{Status: model.AlertStatusResolved, Kind: model.AlertKindDeployFailed,
			ResourceType: model.ResourceTypeStack, ResourceID: "web"}, want: []string{"new-stack"}},
		{name: "limit", filter: ListFilter{Limit: 1}, want: []string{"new-stack"}},
		{name: "empty", filter: ListFilter{ResourceID: "missing"}, want: []string{}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := store.List(t.Context(), testCase.filter)
			require.NoError(t, err)
			ids := make([]string, 0, len(result))
			for _, alert := range result {
				ids = append(ids, alert.ID)
			}
			assert.Equal(t, testCase.want, ids)
		})
	}
}
