package modelstore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/enrichment/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestServiceStorePhysicalContentAndRoundTrip(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewSQLStore(db)

	empty, err := store.ReadAll(t.Context())
	require.NoError(t, err)
	assert.Empty(t, empty)
	_, found, err := store.Find(t.Context(), "s", "missing")
	require.NoError(t, err)
	assert.False(t, found)

	full := model.Info{
		Metadata:    metadata.Metadata{Description: "d", RepositoryURL: "https://x"},
		Name:        "web",
		Stack:       "app",
		Image:       "nginx:1",
		Environment: map[string]string{"A": "b"},
		Spec:        swarm.ServiceSpec{Image: "nginx:1", Mode: "replicated", Replicas: 2},
	}
	other := model.Info{Name: "api", Stack: "app"}
	foreign := model.Info{Name: "db", Stack: "zzz"}
	require.NoError(t, store.ReplaceStack(t.Context(), "zzz", []model.Info{foreign}))
	require.NoError(t, store.ReplaceStack(t.Context(), "app", []model.Info{full, other}))

	all, err := store.ReadAll(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []model.Info{other, full, foreign}, all, "sorted by stack/name")
	got, found, err := store.Find(t.Context(), "app", "web")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, full, got)

	var row struct {
		Stack    string `db:"stack"`
		Name     string `db:"name"`
		HasStack int    `db:"has_stack"`
		HasName  int    `db:"has_name"`
	}
	require.NoError(t, db.Get(t.Context(), &row, `SELECT stack,name,
		json_type(payload,'$.stack') IS NOT NULL AS has_stack,
		json_type(payload,'$.name') IS NOT NULL AS has_name
		FROM services WHERE stack='app' AND name='web'`))
	assert.Equal(t, "app", row.Stack)
	assert.Equal(t, "web", row.Name)
	assert.Zero(t, row.HasStack)
	assert.Zero(t, row.HasName)

	// replacing one stack leaves others intact and empty replacement removes the stack.
	require.NoError(t, store.ReplaceStack(t.Context(), "app", nil))
	all, err = store.ReadAll(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []model.Info{foreign}, all)
}

func TestServiceStoreReplaceStackRollsBack(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewSQLStore(db)
	keep := model.Info{Name: "web", Stack: "app", Image: "i"}
	require.NoError(t, store.ReplaceStack(t.Context(), "app", []model.Info{keep}))

	require.Error(t, store.ReplaceStack(t.Context(), "app", []model.Info{{Name: "x"}, {Name: ""}}))
	require.Error(t, store.ReplaceStack(t.Context(), "app", []model.Info{{Name: "x"}, {Name: "x"}}))
	all, err := store.ReadAll(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []model.Info{keep}, all)
}
