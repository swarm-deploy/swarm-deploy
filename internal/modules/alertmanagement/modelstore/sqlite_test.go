package modelstore

import (
	"database/sql"
	"encoding/json"
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
			DeploymentID: "deployment-2", OpenedAt: base, UpdatedAt: base.Add(2 * time.Minute)},
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
		{name: "deployment", filter: ListFilter{DeploymentID: "deployment-2"}, want: []string{"new-stack"}},
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
	alert.DeploymentID = "deployment-1"
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
	assert.Equal(t, now, resolved.OpenedAt, "repeated updates must preserve the incident opening time")
	assert.Equal(t, alert.DeploymentID, resolved.DeploymentID)
}

func TestSQLStoreRejectsOpenedAtChangeAtomically(t *testing.T) {
	db := testutil.OpenSQLite(t)
	store := NewSQLStore(db)
	openedAt := time.Date(2026, time.October, 11, 10, 0, 0, 0, time.UTC)
	alert := model.Alert{
		ID: "immutable-opened-at", Fingerprint: "deploy_failed:stack:api", Kind: model.AlertKindDeployFailed,
		ResourceType: model.ResourceTypeStack, ResourceID: "api", Status: model.AlertStatusOpen,
		Occurrences: 1, OpenedAt: openedAt, UpdatedAt: openedAt,
	}
	require.NoError(t, store.Create(t.Context(), alert))

	alert.UpdatedAt = openedAt.Add(time.Minute)
	alert.Occurrences++
	require.NoError(t, store.Update(t.Context(), alert), "the original opening time must be accepted")
	before, err := store.Get(t.Context(), alert.ID)
	require.NoError(t, err)

	changed := before
	changed.OpenedAt = openedAt.Add(time.Second)
	changed.UpdatedAt = openedAt.Add(2 * time.Minute)
	changed.ResourceID = "changed-resource"
	changed.DeploymentID = "changed-deployment"
	require.ErrorIs(t, store.Update(t.Context(), changed), ErrAlertOpenedAtChanged)

	after, err := store.Get(t.Context(), alert.ID)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a rejected update must not modify columns or payload")
	assertIndexedFields(t, db, before)
}

func TestAlertIndexesSupportFiltering(t *testing.T) {
	db := testutil.OpenSQLite(t)
	testCases := []struct {
		name      string
		query     string
		wantIndex string
	}{
		{
			name:      "deployment correlation",
			query:     `SELECT payload FROM alerts WHERE deployment_id='deployment-1'`,
			wantIndex: "idx_alert_deployment_updated",
		},
		{
			name:      "open alerts newest first",
			query:     `SELECT payload FROM alerts WHERE status='open'`,
			wantIndex: "idx_alert_status_updated",
		},
		{
			name:      "kind",
			query:     `SELECT payload FROM alerts WHERE kind='deploy_failed'`,
			wantIndex: "idx_alert_kind_status_updated",
		},
		{
			name:      "resource",
			query:     `SELECT payload FROM alerts WHERE resource_type='stack' AND resource_id='api'`,
			wantIndex: "idx_alert_resource_status_updated",
		},
		{
			name: "open resource",
			query: `SELECT payload FROM alerts WHERE resource_type='stack' AND resource_id='api'
				AND status='open'`,
			wantIndex: "idx_alert_resource_status_updated",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			plan := explainQueryPlan(t, db, testCase.query)
			assert.Contains(t, plan, testCase.wantIndex)
		})
	}
}

func TestAlertIndexesOrderingCoverage(t *testing.T) {
	db := testutil.OpenSQLite(t)
	testCases := []struct {
		name          string
		query         string
		coversSorting bool
	}{
		{name: "deployment", query: `SELECT payload FROM alerts WHERE deployment_id='deployment-1'
			ORDER BY updated_at_ns DESC,id`, coversSorting: true},
		{name: "status", query: `SELECT payload FROM alerts WHERE status='open'
			ORDER BY updated_at_ns DESC,id`, coversSorting: true},
		{name: "kind without status", query: `SELECT payload FROM alerts WHERE kind='deploy_failed'
			ORDER BY updated_at_ns DESC,id`, coversSorting: false},
		{name: "resource without status", query: `SELECT payload FROM alerts
			WHERE resource_type='stack' AND resource_id='api' ORDER BY updated_at_ns DESC,id`, coversSorting: false},
		{name: "open resource", query: `SELECT payload FROM alerts
			WHERE resource_type='stack' AND resource_id='api' AND status='open'
			ORDER BY updated_at_ns DESC,id`, coversSorting: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			plan := explainQueryPlan(t, db, testCase.query)
			if testCase.coversSorting {
				assert.NotContains(t, plan, "USE TEMP B-TREE FOR ORDER BY")
				return
			}
			assert.Contains(t, plan, "USE TEMP B-TREE FOR ORDER BY")
		})
	}
}

func explainQueryPlan(t *testing.T, db *storage.Database, query string) string {
	t.Helper()
	var rows []struct {
		ID     int    `db:"id"`
		Parent int    `db:"parent"`
		Unused int    `db:"notused"`
		Detail string `db:"detail"`
	}
	require.NoError(t, db.Select(t.Context(), &rows, "EXPLAIN QUERY PLAN "+query))
	var plan strings.Builder
	for _, row := range rows {
		plan.WriteString(row.Detail)
		plan.WriteByte('\n')
	}
	return plan.String()
}

func assertIndexedFields(t *testing.T, db *storage.Database, alert model.Alert) {
	t.Helper()
	var row struct {
		Kind         model.AlertKind    `db:"kind"`
		ResourceType model.ResourceType `db:"resource_type"`
		ResourceID   string             `db:"resource_id"`
		DeploymentID sql.NullString     `db:"deployment_id"`
		OpenedAt     int64              `db:"opened_at_ns"`
		Payload      string             `db:"payload"`
	}
	require.NoError(t, db.Get(t.Context(), &row,
		"SELECT kind,resource_type,resource_id,deployment_id,opened_at_ns,payload FROM alerts WHERE id=?", alert.ID))
	assert.Equal(t, alert.Kind, row.Kind)
	assert.Equal(t, alert.ResourceType, row.ResourceType)
	assert.Equal(t, alert.ResourceID, row.ResourceID)
	assert.Equal(t, alert.DeploymentID, row.DeploymentID.String)
	assert.Equal(t, alert.OpenedAt.UnixNano(), row.OpenedAt)
	var persisted map[string]any
	require.NoError(t, json.Unmarshal([]byte(row.Payload), &persisted))
	for _, field := range []string{"id", "fingerprint", "kind", "resourceType", "resourceId",
		"deploymentId", "status", "openedAt", "updatedAt", "resolvedAt"} {
		assert.NotContains(t, persisted, field, "SQL column %q must not repeat in JSON", field)
	}
	stored, err := NewSQLStore(db).Get(t.Context(), alert.ID)
	require.NoError(t, err)
	assert.Equal(t, alert, stored)
}

func TestAlertStorageColumnPayloadSeparationAndNullableFields(t *testing.T) {
	db := testutil.OpenSQLite(t)
	store := NewSQLStore(db)
	now := time.Date(2026, 10, 12, 11, 12, 13, 123, time.UTC)
	alert := model.Alert{
		ID: "alert-one", Fingerprint: "fp", Kind: model.AlertKindDeployFailed,
		ResourceType: model.ResourceTypeStack, ResourceID: "app", Status: model.AlertStatusOpen,
		Title: "Failed", Message: "Docker API rejected the update", Occurrences: 1,
		OpenedAt: now, UpdatedAt: now,
	}
	require.NoError(t, store.Create(t.Context(), alert))
	var rawRow struct {
		Raw          string         `db:"payload"`
		DeploymentID sql.NullString `db:"deployment_id"`
		ResolvedAt   sql.NullInt64  `db:"resolved_at_ns"`
	}
	require.NoError(t, db.Get(t.Context(), &rawRow,
		"SELECT payload,deployment_id,resolved_at_ns FROM alerts WHERE id=?", alert.ID))
	assert.False(t, rawRow.DeploymentID.Valid)
	assert.False(t, rawRow.ResolvedAt.Valid)
	assert.JSONEq(t, `{"title":"Failed","message":"Docker API rejected the update",
		"occurrences":1,"openEventId":"","latestEventId":""}`, rawRow.Raw)
	got, err := store.Get(t.Context(), alert.ID)
	require.NoError(t, err)
	assert.Equal(t, alert, got)

	resolved := now.Add(time.Minute)
	alert.Status = model.AlertStatusResolved
	alert.UpdatedAt = resolved
	alert.ResolvedAt = &resolved
	alert.DeploymentID = "deploy-one"
	alert.Resolution = &model.AlertResolution{Reason: model.ResolutionReasonRecovered,
		Message: "Recovered", EventID: "event-one"}
	require.NoError(t, store.Update(t.Context(), alert))
	got, err = store.Get(t.Context(), alert.ID)
	require.NoError(t, err)
	assert.Equal(t, alert, got)
	assertIndexedFields(t, db, alert)
}
