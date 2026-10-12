package modelstore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func rec(stack, service, typ string, at time.Time) model.Recommendation {
	return model.Recommendation{
		Severity:       model.SeverityHigh,
		Type:           model.Type(typ),
		Source:         model.Source{File: "stack.yaml", Digest: "dg", Commit: "c1"},
		Subject:        model.Subject{Stack: stack, Service: service},
		Title:          "t-" + service,
		Recommendation: "r-" + service,
		CreatedAt:      at,
	}
}

func TestRecommendationsRelationalStorage(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewSQLStore(db)

	empty, err := store.List(t.Context(), ListFilter{})
	require.NoError(t, err)
	assert.Empty(t, empty)

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 5, time.UTC)
	t1 := t0.Add(time.Hour)
	a := rec("app", "web", "service.image.latest", t0)
	b := rec("app", "api", "service.image.latest", t0)
	c := rec("other", "db", "service.image.latest", t0)
	require.NoError(t, store.UpdateStack(t.Context(), "app", []model.Recommendation{a, b}))
	require.NoError(t, store.UpdateStack(t.Context(), "other", []model.Recommendation{c}))

	all, err := store.List(t.Context(), ListFilter{})
	require.NoError(t, err)
	assert.Equal(t, []model.Recommendation{a, b, c}, all, "persisted order")
	app, err := store.List(t.Context(), ListFilter{Stack: "app"})
	require.NoError(t, err)
	assert.Equal(t, []model.Recommendation{a, b}, app)
	limited, err := store.List(t.Context(), ListFilter{Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, []model.Recommendation{a}, limited)

	var row struct {
		Severity  string `db:"severity"`
		Type      string `db:"type"`
		Stack     string `db:"stack"`
		Service   string `db:"service"`
		File      string `db:"source_file"`
		Digest    string `db:"source_digest"`
		Commit    string `db:"source_commit"`
		Title     string `db:"title"`
		Text      string `db:"recommendation"`
		CreatedAt string `db:"created_at"`
	}
	require.NoError(t, db.Get(t.Context(), &row, `SELECT severity,type,stack,service,source_file,source_digest,
		source_commit,title,recommendation,created_at FROM recommendations WHERE service='web'`))
	assert.Equal(t, "high", row.Severity)
	assert.Equal(t, "app", row.Stack)
	assert.Equal(t, "stack.yaml", row.File)
	assert.Equal(t, "dg", row.Digest)
	assert.Equal(t, "c1", row.Commit)
	assert.Equal(t, "t-web", row.Title)
	assert.Equal(t, "r-web", row.Text)
	assert.Equal(t, t0.Format(time.RFC3339Nano), row.CreatedAt)

	var payloadColumns int
	require.NoError(t, db.Get(t.Context(), &payloadColumns,
		"SELECT count(*) FROM pragma_table_info('recommendations') WHERE name='payload'"))
	assert.Zero(t, payloadColumns)

	// update: keep first-seen time for known IDs, drop vanished, add new; other stacks untouched.
	aLater := rec("app", "web", "service.image.latest", t1)
	d := rec("app", "cache", "service.image.latest", t1)
	require.NoError(t, store.UpdateStack(t.Context(), "app", []model.Recommendation{d, aLater, aLater}))
	app, err = store.List(t.Context(), ListFilter{Stack: "app"})
	require.NoError(t, err)
	require.Len(t, app, 2)
	assert.Equal(t, "cache", app[0].Subject.Service)
	assert.Equal(t, "web", app[1].Subject.Service)
	assert.True(t, app[1].CreatedAt.Equal(t0), "original appearance time preserved")

	require.NoError(t, store.UpdateStack(t.Context(), "app", nil))
	all, err = store.List(t.Context(), ListFilter{})
	require.NoError(t, err)
	assert.Equal(t, []model.Recommendation{c}, all)
}

func TestRecommendationsUpdateStackRollsBack(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewSQLStore(db)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := rec("app", "web", "service.image.latest", at)
	require.NoError(t, store.UpdateStack(t.Context(), "app", []model.Recommendation{a}))

	_, err = db.Exec(t.Context(), `CREATE TRIGGER fail_insert BEFORE INSERT ON recommendations
		WHEN NEW.service='boom' BEGIN SELECT RAISE(ABORT,'boom'); END`)
	require.NoError(t, err)
	err = store.UpdateStack(t.Context(), "app", []model.Recommendation{rec("app", "boom", "t", at)})
	require.Error(t, err)

	got, err := store.List(t.Context(), ListFilter{Stack: "app"})
	require.NoError(t, err)
	assert.Equal(t, []model.Recommendation{a}, got)
}

func TestRecommendationsUpdateStackCompositeKeyCollision(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewSQLStore(db)

	// These recommendations have different composite keys but collide under the old
	// string ID: "app-api-one-two.yamlservice.image.latest".
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := rec("app", "api-one", "service.image.latest", t0)
	a.Source.File = "two.yaml"
	b := rec("app", "api", "service.image.latest", t0.Add(time.Hour))
	b.Source.File = "one-two.yaml"

	require.NoError(t, store.UpdateStack(t.Context(), "app", []model.Recommendation{a, b}))

	aLater := a
	aLater.CreatedAt = t0.Add(2 * time.Hour)
	bLater := b
	bLater.CreatedAt = t0.Add(2 * time.Hour)
	require.NoError(t, store.UpdateStack(t.Context(), "app", []model.Recommendation{bLater, aLater}))

	got, err := store.List(t.Context(), ListFilter{Stack: "app"})
	require.NoError(t, err)
	assert.Equal(t, []model.Recommendation{b, a}, got, "both identities and first-seen timestamps must survive")
}
