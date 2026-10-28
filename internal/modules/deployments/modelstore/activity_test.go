package modelstore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func TestDeploymentActivityAggregation(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name     string
		interval ActivityInterval
		start    time.Time
		end      time.Time
		rows     []activityTestDeployment
		expected []ActivityBucket
	}{
		{
			name: "daily UTC buckets including zero days", interval: ActivityIntervalDaily,
			start: activityTime(2026, time.October, 1, 0), end: activityTime(2026, time.October, 4, 0),
			rows: []activityTestDeployment{
				{id: "before", status: deploymentmodel.Succeeded, startedAt: activityTime(2026, time.September, 30, 23)},
				{id: "success", status: deploymentmodel.Succeeded, startedAt: activityTime(2026, time.October, 1, 23)},
				{id: "interrupted", status: deploymentmodel.Interrupted, startedAt: activityTime(2026, time.October, 3, 0)},
				{id: "at-end", status: deploymentmodel.Succeeded, startedAt: activityTime(2026, time.October, 4, 0)},
			},
			expected: []ActivityBucket{
				{Start: activityTime(2026, time.October, 1, 0), Succeeded: 1},
				{Start: activityTime(2026, time.October, 2, 0)},
				{Start: activityTime(2026, time.October, 3, 0), Superseded: 1},
			},
		},
		{
			name: "Monday based weekly buckets", interval: ActivityIntervalWeekly,
			start: activityTime(2026, time.September, 28, 0), end: activityTime(2026, time.October, 12, 0),
			rows: []activityTestDeployment{
				{id: "sunday", status: deploymentmodel.Succeeded, startedAt: activityTime(2026, time.October, 4, 23)},
				{id: "monday", status: deploymentmodel.Failed, startedAt: activityTime(2026, time.October, 5, 0)},
			},
			expected: []ActivityBucket{
				{Start: activityTime(2026, time.September, 28, 0), Succeeded: 1},
				{Start: activityTime(2026, time.October, 5, 0), Failed: 1},
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			db := testutil.OpenSQLite(t)
			for _, row := range testCase.rows {
				insertActivityTestDeployment(t, db, "app", row)
			}
			actual, err := NewStore(db).Activity(t.Context(), "app", testCase.interval, testCase.start, testCase.end)
			require.NoError(t, err)
			assert.Equal(t, testCase.expected, actual)
		})
	}
}

func TestDeploymentActivityUsesLatestStatusWithoutDoubleCounting(t *testing.T) {
	t.Parallel()
	for _, terminal := range []deploymentmodel.Status{deploymentmodel.Succeeded, deploymentmodel.Failed} {
		t.Run(string(terminal), func(t *testing.T) {
			t.Parallel()
			db := testutil.OpenSQLite(t)
			startedAt := activityTime(2026, time.October, 2, 12)
			insertActivityTestDeployment(t, db, "app", activityTestDeployment{
				id: "deployment", status: deploymentmodel.Running, startedAt: startedAt,
			})
			buckets, err := NewStore(db).Activity(t.Context(), "app", ActivityIntervalDaily,
				activityTime(2026, time.October, 2, 0), activityTime(2026, time.October, 3, 0))
			require.NoError(t, err)
			assert.Equal(t, 1, buckets[0].Running)

			finishedAt := startedAt.Add(time.Minute)
			_, err = db.Exec(t.Context(), `UPDATE deployments SET status=?, finished_at=? WHERE id=?`,
				terminal, finishedAt, "deployment")
			require.NoError(t, err)
			buckets, err = NewStore(db).Activity(t.Context(), "app", ActivityIntervalDaily,
				activityTime(2026, time.October, 2, 0), activityTime(2026, time.October, 3, 0))
			require.NoError(t, err)
			assert.Zero(t, buckets[0].Running)
			assert.Equal(t, 1, buckets[0].Succeeded+buckets[0].Failed)
		})
	}
}

func TestDeploymentActivityClassifiesSupersededDeployments(t *testing.T) {
	t.Parallel()
	db := testutil.OpenSQLite(t)
	insertActivityTestDeployment(t, db, "app", activityTestDeployment{
		id: "failed", status: deploymentmodel.Failed, startedAt: activityTime(2026, time.October, 1, 12),
	})
	insertActivityTestDeployment(t, db, "app", activityTestDeployment{
		id: "newer", status: deploymentmodel.Succeeded, startedAt: activityTime(2026, time.October, 2, 12),
	})
	buckets, err := NewStore(db).Activity(t.Context(), "app", ActivityIntervalDaily,
		activityTime(2026, time.October, 1, 0), activityTime(2026, time.October, 3, 0))
	require.NoError(t, err)
	assert.Equal(t, 1, buckets[0].Superseded)
	assert.Zero(t, buckets[0].Failed)
	assert.Equal(t, 1, buckets[1].Succeeded)
}

func TestDeploymentActivityPreservesInterruptedStatus(t *testing.T) {
	t.Parallel()
	db := testutil.OpenSQLite(t)
	insertActivityTestDeployment(t, db, "app", activityTestDeployment{
		id: "interrupted", status: deploymentmodel.Interrupted, startedAt: activityTime(2026, time.October, 1, 12),
	})
	buckets, err := NewStore(db).Activity(t.Context(), "app", ActivityIntervalDaily,
		activityTime(2026, time.October, 1, 0), activityTime(2026, time.October, 2, 0))
	require.NoError(t, err)
	assert.Equal(t, 1, buckets[0].Interrupted)
}

type activityTestDeployment struct {
	id        string
	status    deploymentmodel.Status
	startedAt time.Time
}

func insertActivityTestDeployment(t *testing.T, db storage.Database, stack string, item activityTestDeployment) {
	t.Helper()
	var finishedAt *time.Time
	phase := deploymentmodel.PhaseApply
	if item.status != deploymentmodel.Running {
		finished := item.startedAt.Add(time.Minute)
		finishedAt = &finished
		if item.status == deploymentmodel.Succeeded {
			phase = deploymentmodel.PhaseCompleted
		}
	}
	_, err := db.Exec(t.Context(), `INSERT INTO deployments
		(id,stack,"commit",status,error_code,phase,started_at,finished_at,
		desired_payload,details_payload,changes_payload) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		item.id, stack, "commit", item.status, "", phase, item.startedAt, finishedAt,
		`{"definition":{"compose":{"services":[]}}}`, `{}`, `[]`)
	require.NoError(t, err)
}

func activityTime(year int, month time.Month, day int, hour int) time.Time {
	return time.Date(year, month, day, hour, 0, 0, 0, time.UTC)
}
