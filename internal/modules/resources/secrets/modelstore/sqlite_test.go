package modelstore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func newStore(t *testing.T) (*SQLStore, storage.Database) {
	t.Helper()
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
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
	got, err = store.GetByID(t.Context(), "id-b")
	require.NoError(t, err)
	assert.Equal(t, full, got)

	_, err = store.GetByName(t.Context(), "missing")
	assert.ErrorIs(t, err, ErrSecretNotFound)
	_, err = store.GetByID(t.Context(), "missing")
	assert.ErrorIs(t, err, ErrSecretNotFound)

	var row struct {
		ID        string    `db:"id"`
		Name      string    `db:"name"`
		CreatedAt time.Time `db:"created_at"`
		UpdatedAt time.Time `db:"updated_at"`
		Payload   string    `db:"payload"`
	}
	require.NoError(t, db.Get(t.Context(), &row,
		"SELECT id,name,created_at,updated_at,payload FROM secret_metadata WHERE name='b'"))
	assert.Equal(t, "id-b", row.ID)
	assert.Equal(t, "b", row.Name)
	assert.Equal(t, ts, row.CreatedAt.UTC())
	assert.Equal(t, ts, row.UpdatedAt.UTC())
	for _, key := range []string{"id", "name", "description", "managed", "external_path", "external_version_id"} {
		var count int
		require.NoError(t, db.Get(t.Context(), &count,
			"SELECT json_type(payload,'$.'||?) IS NOT NULL FROM secret_metadata WHERE name='b'", key))
		assert.Zero(t, count, "payload must not contain %q", key)
	}
	assert.JSONEq(t, `{"version_id":7,"driver":"drv","labels":{"description":"desc","external_path":"kv/b","external_version_id":"3",
		"cloud-secrets.secret.managed":"true"}}`, row.Payload)

	var payload string
	require.NoError(t, db.Get(t.Context(), &payload, "SELECT payload FROM secret_metadata WHERE name='a'"))
	assert.NotContains(t, payload, "labels")
}

func TestSecretsReplaceEmptyAndRollback(t *testing.T) {
	store, db := newStore(t)
	now := time.Now().UTC()
	list, err := store.List(t.Context())
	require.NoError(t, err)
	assert.Empty(t, list)

	require.NoError(t, store.Replace(t.Context(), []model.Secret{{ID: "1", Name: "x", CreatedAt: now, UpdatedAt: now}}))
	// duplicate names violate UNIQUE and must roll the whole replacement back.
	err = store.Replace(t.Context(), []model.Secret{
		{ID: "2", Name: "y", CreatedAt: now, UpdatedAt: now},
		{ID: "3", Name: "y", CreatedAt: now, UpdatedAt: now},
	})
	require.Error(t, err)
	var name string
	require.NoError(t, db.Get(t.Context(), &name, "SELECT name FROM secret_metadata"))
	assert.Equal(t, "x", name)

	require.NoError(t, store.Replace(t.Context(), nil))
	list, err = store.List(t.Context())
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestSecretsReplaceRejectsFieldsNotBackedByLabels(t *testing.T) {
	now := time.Now().UTC()
	for name, secret := range map[string]model.Secret{
		"description":         {ID: "1", Name: "x", Description: "d", CreatedAt: now, UpdatedAt: now},
		"external_path":       {ID: "1", Name: "x", ExternalPath: "p", CreatedAt: now, UpdatedAt: now},
		"external_version_id": {ID: "1", Name: "x", ExternalVersionID: "v", CreatedAt: now, UpdatedAt: now},
		"managed":             {ID: "1", Name: "x", Managed: true, CreatedAt: now, UpdatedAt: now},
		"label mismatch": {ID: "1", Name: "x", Description: "d",
			CreatedAt: now, UpdatedAt: now, Labels: map[string]string{model.DescriptionLabel: "other"}},
	} {
		t.Run(name, func(t *testing.T) {
			store, db := newStore(t)
			keep := model.Secret{ID: "0", Name: "keep", CreatedAt: now, UpdatedAt: now}
			require.NoError(t, store.Replace(t.Context(), []model.Secret{keep}))
			require.Error(t, store.Replace(t.Context(), []model.Secret{secret}))
			var count int
			require.NoError(t, db.Get(t.Context(), &count,
				"SELECT count(*) FROM secret_metadata WHERE name='keep'"))
			assert.Equal(t, 1, count, "failed Replace must roll back")
		})
	}
}
