package modelstore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func newStore(t *testing.T) (*SQLStore, *storage.Database) {
	t.Helper()
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return NewSQLStore(db), db
}

func TestSecretsRoundTripAndPhysicalPayload(t *testing.T) {
	store, db := newStore(t)
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	full := model.Secret{
		ID: "id-b", Name: "b", Description: "desc", VersionID: 7, CreatedAt: ts, UpdatedAt: ts,
		Driver: "drv", ExternalPath: "kv/b", ExternalVersionID: "3", Managed: true,
		Labels: map[string]string{
			model.DescriptionLabel: "desc", model.ExternalPathLabel: "kv/b",
			model.ExternalVersionIDLabel: "3", model.ManagedLabel: "true",
		},
	}
	empty := model.Secret{ID: "id-a", Name: "a", CreatedAt: ts, UpdatedAt: ts}
	require.NoError(t, store.Replace(t.Context(), []model.Secret{full, empty}))

	list, err := store.List(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []model.Secret{empty, full}, list, "sorted by name")

	got, err := store.GetByName(t.Context(), "b")
	require.NoError(t, err)
	assert.Equal(t, full, got)

	_, err = store.GetByName(t.Context(), "missing")
	assert.ErrorIs(t, err, ErrSecretNotFound)

	var id, name, payload string
	require.NoError(t, db.QueryRow(t.Context(),
		"SELECT id,name,payload FROM secret_metadata WHERE name='b'").Scan(&id, &name, &payload))
	assert.Equal(t, "id-b", id)
	assert.Equal(t, "b", name)
	for _, key := range []string{"id", "name", "description", "managed", "external_path", "external_version_id"} {
		var count int
		require.NoError(t, db.QueryRow(t.Context(),
			"SELECT json_type(payload,'$.'||?) IS NOT NULL FROM secret_metadata WHERE name='b'", key).Scan(&count))
		assert.Zero(t, count, "payload must not contain %q", key)
	}
	assert.JSONEq(t, `{"version_id":7,"created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:05Z",
		"driver":"drv","labels":{"description":"desc","external_path":"kv/b","external_version_id":"3",
		"cloud-secrets.secret.managed":"true"}}`, payload)

	require.NoError(t, db.QueryRow(t.Context(),
		"SELECT payload FROM secret_metadata WHERE name='a'").Scan(&payload))
	assert.NotContains(t, payload, "labels")
}

func TestSecretsReplaceEmptyAndRollback(t *testing.T) {
	store, db := newStore(t)
	list, err := store.List(t.Context())
	require.NoError(t, err)
	assert.Empty(t, list)

	require.NoError(t, store.Replace(t.Context(), []model.Secret{{ID: "1", Name: "x"}}))
	// duplicate names violate UNIQUE and must roll the whole replacement back.
	err = store.Replace(t.Context(), []model.Secret{{ID: "2", Name: "y"}, {ID: "3", Name: "y"}})
	require.Error(t, err)
	var name string
	require.NoError(t, db.QueryRow(t.Context(), "SELECT name FROM secret_metadata").Scan(&name))
	assert.Equal(t, "x", name)

	require.NoError(t, store.Replace(t.Context(), nil))
	list, err = store.List(t.Context())
	require.NoError(t, err)
	assert.Empty(t, list)
}
