package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/enrichment/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/stype"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
	"go.uber.org/mock/gomock"
)

func TestInferDSNEngine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, image, service, want string }{
		{"postgres image", "postgres:18", "db", "postgresql"},
		{"namespaced postgres image", "registry.example/bitnami/postgresql:18@sha256:abc", "mysql", "postgresql"},
		{"mysql image", "mysql:8.4", "postgres", "mysql"},
		{"mariadb image", "mariadb:11", "db", "mysql"},
		{"postgres fallback", "registry.example/custom:v1", "primary-postgres", "postgresql"},
		{"mysql fallback", "registry.example/custom:v1", "MYSQL_primary", "mysql"},
		{"unknown", "redis:8", "cache", ""},
		{"ambiguous", "custom:v1", "mysql-postgres", ""},
		{"not a substring", "postgres-exporter:v1", "postgresqlbackup", ""},
	} {
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); assert.Equal(t, tc.want, inferDSNEngine(tc.image, tc.service)) })
	}
}

func TestListDSNSuggestions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		services []model.Info
		want     string
	}{
		{name: "empty", want: `{"suggestions":[]}`},
		{name: "no databases", services: []model.Info{
			{Name: "postgres", Image: "postgres:18", Metadata: metadata.Metadata{Type: stype.Application}},
		}, want: `{"suggestions":[]}`},
		{name: "catalog classification and nullable engines", services: []model.Info{
			{Name: "postgres", Image: "postgres:18", Metadata: metadata.Metadata{Type: stype.Database},
				Environment: map[string]string{"POSTGRES_PASSWORD": "must-not-leak"},
				Spec:        swarm.ServiceSpec{Secrets: []swarm.ServiceSecret{{SecretName: "must-not-leak"}}}},
			{Name: "mysql", Image: "mysql:8.4", Metadata: metadata.Metadata{Type: stype.Database}},
			{Name: "custom", Image: "custom:v1", Metadata: metadata.Metadata{Type: stype.Database}},
			{Name: "primary-postgres", Image: "custom:v1", Metadata: metadata.Metadata{Type: stype.Database}},
			{Name: "excluded-postgres", Image: "postgres:18", Metadata: metadata.Metadata{Type: stype.Application}},
			{Name: "unclassified-postgres", Image: "postgres:18"},
		}, want: `{"suggestions":[{"service_name":"infra_custom","stack":"infra","engine":null},{"service_name":"infra_mysql","stack":"infra","engine":"mysql"},{"service_name":"infra_postgres","stack":"infra","engine":"postgresql"},{"service_name":"infra_primary-postgres","stack":"infra","engine":"postgresql"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			services := modelstore.NewSQLStore(testutil.OpenSQLite(t))
			require.NoError(t, services.ReplaceStack(t.Context(), "infra", tc.services))
			// No Docker calls are expected; suggestions must work from catalog data alone.
			inspector := swarm.NewMockServiceManager(gomock.NewController(t))
			h := &handler{services: services, serviceInspector: inspector}
			server, err := generated.NewServer(h, h, generated.WithErrorHandler(HandleHTTPError))
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/secrets/suggestions/dsn", nil))
			require.Equal(t, http.StatusOK, recorder.Code)
			assert.JSONEq(t, tc.want, recorder.Body.String())
			assert.NotContains(t, recorder.Body.String(), "must-not-leak")
		})
	}
}

func TestListDSNSuggestionsKeepsSameServiceNamesInDifferentStacks(t *testing.T) {
	t.Parallel()
	services := modelstore.NewSQLStore(testutil.OpenSQLite(t))
	for _, stack := range []string{"infra", "other"} {
		require.NoError(t, services.ReplaceStack(t.Context(), stack, []model.Info{{
			Name: "postgres", Image: "postgres:18", Metadata: metadata.Metadata{Type: stype.Database},
		}}))
	}
	h := &handler{services: services}
	response, err := h.ListDSNSuggestions(t.Context())
	require.NoError(t, err)
	require.Len(t, response.Suggestions, 2)
	assert.Equal(t, "infra_postgres", response.Suggestions[0].ServiceName)
	assert.Equal(t, "other_postgres", response.Suggestions[1].ServiceName)
}

func TestListDSNSuggestionsFailure(t *testing.T) {
	t.Parallel()
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	services := modelstore.NewSQLStore(db)
	require.NoError(t, db.Close())
	h := &handler{services: services}
	_, err = h.ListDSNSuggestions(t.Context())
	require.Error(t, err)
	var statusErr *statusError
	require.ErrorAs(t, err, &statusErr)
	assert.Equal(t, http.StatusInternalServerError, statusErr.code)
	assert.EqualError(t, statusErr, "unable to list database services")
}
