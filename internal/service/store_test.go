package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreReplaceStackPersistsAndReplacesSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.json")

	store, err := NewStore(path)
	require.NoError(t, err, "new store")

	require.NoError(
		t,
		store.ReplaceStack("api", []Info{
			{
				Name:        "web",
				Stack:       "ignored",
				Description: "Frontend",
				Type:        TypeApplication,
				Image:       "ghcr.io/company/web:1.0.0",
			},
		}),
		"save first stack snapshot",
	)
	require.NoError(
		t,
		store.ReplaceStack("monitoring", []Info{
			{
				Name:        "prometheus",
				Stack:       "ignored",
				Description: "Prometheus",
				Type:        TypeMonitoring,
				Image:       "prom/prometheus:v3.0.0",
			},
		}),
		"save second stack snapshot",
	)
	require.NoError(
		t,
		store.ReplaceStack("api", []Info{
			{
				Name:        "web",
				Stack:       "ignored",
				Description: "Frontend v2",
				Type:        TypeDelivery,
				Image:       "ghcr.io/company/web:2.0.0",
			},
			{
				Name:        "db",
				Stack:       "ignored",
				Description: "PostgreSQL",
				Type:        TypeDatabase,
				Image:       "postgres:16",
			},
		}),
		"replace stack snapshot",
	)

	items := store.List()
	require.Len(t, items, 3, "expected merged services from two stacks")
	assert.Equal(
		t,
		[]Info{
			{
				Name:        "db",
				Stack:       "api",
				Description: "PostgreSQL",
				Type:        TypeDatabase,
				Image:       "postgres:16",
			},
			{
				Name:        "web",
				Stack:       "api",
				Description: "Frontend v2",
				Type:        TypeDelivery,
				Image:       "ghcr.io/company/web:2.0.0",
			},
			{
				Name:        "prometheus",
				Stack:       "monitoring",
				Description: "Prometheus",
				Type:        TypeMonitoring,
				Image:       "prom/prometheus:v3.0.0",
			},
		},
		items,
		"expected sorted persisted snapshot",
	)

	reloaded, err := NewStore(path)
	require.NoError(t, err, "reload store")
	assert.Equal(t, items, reloaded.List(), "expected persisted services on reload")
}

func TestStoreLoadNormalizesRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.json")

	payload := []byte(`[
  {"name":" cache ","stack":" data ","description":" redis cache ","type":"DATABASE","image":" redis:7 "},
  {"name":"worker","stack":"jobs","description":"queue worker","type":"unknown","image":"acme/worker:1"},
  {"name":"","stack":"jobs","type":"application","image":"acme/empty:1"},
  {"name":"api","stack":"","type":"application","image":"acme/api:1"}
]`)
	require.NoError(t, os.WriteFile(path, payload, fileModePrivate), "prepare services file")

	store, err := NewStore(path)
	require.NoError(t, err, "new store")

	assert.Equal(
		t,
		[]Info{
			{
				Name:        "cache",
				Stack:       "data",
				Description: "redis cache",
				Type:        TypeDatabase,
				Image:       "redis:7",
			},
			{
				Name:        "worker",
				Stack:       "jobs",
				Description: "queue worker",
				Type:        TypeApplication,
				Image:       "acme/worker:1",
			},
		},
		store.List(),
		"expected normalized and filtered rows",
	)
}
