package modelstore

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
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

func TestSQLStorePersistsAndSynchronizesIndexedFields(t *testing.T) {
	db := testutil.OpenSQLite(t)
	store := NewSQLStore(db)
	now := time.Date(2026, time.October, 11, 10, 0, 0, 0, time.UTC)
	alert := model.Alert{
		ID: "alert-1", Fingerprint: "deploy_failed:stack:api", Kind: model.AlertKindDeployFailed,
		ResourceType: model.ResourceTypeStack, ResourceID: "api", Status: model.AlertStatusOpen,
		Occurrences: 1, OpenedAt: now, UpdatedAt: now,
	}
	require.NoError(t, store.Create(t.Context(), alert))
	assertIndexedFields(t, db, alert)

	alert.Kind = model.AlertKindNodeDisconnected
	alert.ResourceType = model.ResourceTypeNode
	alert.ResourceID = "node-1"
	alert.UpdatedAt = now.Add(time.Minute)
	require.NoError(t, store.Update(t.Context(), alert))
	assertIndexedFields(t, db, alert)

	resolvedAt := now.Add(2 * time.Minute)
	alert.Status = model.AlertStatusResolved
	alert.UpdatedAt = resolvedAt
	alert.ResolvedAt = &resolvedAt
	require.NoError(t, store.Update(t.Context(), alert))
	assertIndexedFields(t, db, alert)

	resolved, err := store.Get(t.Context(), alert.ID)
	require.NoError(t, err)
	assert.Equal(t, alert.Kind, resolved.Kind)
	assert.Equal(t, alert.ResourceType, resolved.ResourceType)
	assert.Equal(t, alert.ResourceID, resolved.ResourceID)
}

func TestAlertIndexesSupportRepresentativeQueries(t *testing.T) {
	db := testutil.OpenSQLite(t)
	testCases := []struct {
		name      string
		query     string
		wantIndex string
	}{
		{
			name:      "open alerts newest first",
			query:     `SELECT payload FROM alerts WHERE status='open' ORDER BY updated_at_ns DESC,id`,
			wantIndex: "idx_alert_status_updated",
		},
		{
			name:      "kind",
			query:     `SELECT payload FROM alerts WHERE kind='deploy_failed' ORDER BY updated_at_ns DESC,id`,
			wantIndex: "idx_alert_kind_status_updated",
		},
		{
			name: "resource",
			query: `SELECT payload FROM alerts WHERE resource_type='stack' AND resource_id='api'
				ORDER BY updated_at_ns DESC,id`,
			wantIndex: "idx_alert_resource_status_updated",
		},
		{
			name: "open resource",
			query: `SELECT payload FROM alerts WHERE resource_type='stack' AND resource_id='api'
				AND status='open' ORDER BY updated_at_ns DESC,id`,
			wantIndex: "idx_alert_resource_status_updated",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rows, err := db.Get(t.Context()).QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+testCase.query)
			require.NoError(t, err)
			defer rows.Close()
			var plan strings.Builder
			for rows.Next() {
				var id, parent, unused int
				var detail string
				require.NoError(t, rows.Scan(&id, &parent, &unused, &detail))
				plan.WriteString(detail)
			}
			require.NoError(t, rows.Err())
			assert.Contains(t, plan.String(), testCase.wantIndex)
		})
	}
}

func assertIndexedFields(t *testing.T, db *storage.Database, alert model.Alert) {
	t.Helper()
	var kind model.AlertKind
	var resourceType model.ResourceType
	var resourceID string
	require.NoError(t, db.Get(t.Context()).QueryRowContext(t.Context(),
		"SELECT kind,resource_type,resource_id FROM alerts WHERE id=?", alert.ID).
		Scan(&kind, &resourceType, &resourceID))
	assert.Equal(t, alert.Kind, kind)
	assert.Equal(t, alert.ResourceType, resourceType)
	assert.Equal(t, alert.ResourceID, resourceID)
}
