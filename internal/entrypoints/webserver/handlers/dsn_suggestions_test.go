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
				Environment: map[string]string{"POSTGRES_PASSWORD": "must-not-leak", "PGPORT": "5544"},
				Spec:        swarm.ServiceSpec{Secrets: []swarm.ServiceSecret{{SecretName: "must-not-leak"}}}},
			{Name: "mysql", Image: "mysql:8.4", Metadata: metadata.Metadata{Type: stype.Database}},
			{Name: "custom", Image: "custom:v1", Metadata: metadata.Metadata{Type: stype.Database}},
			{Name: "primary-postgres", Image: "custom:v1", Metadata: metadata.Metadata{Type: stype.Database}},
			{Name: "excluded-postgres", Image: "postgres:18", Metadata: metadata.Metadata{Type: stype.Application}},
			{Name: "unclassified-postgres", Image: "postgres:18"},
		}, want: `{"suggestions":[{"service_name":"infra_custom","stack":"infra","engine":null,"port":null},{"service_name":"infra_mysql","stack":"infra","engine":"mysql","port":3306},{"service_name":"infra_postgres","stack":"infra","engine":"postgresql","port":5544},{"service_name":"infra_primary-postgres","stack":"infra","engine":"postgresql","port":5432}]}`},
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

func TestSuggestDSNPort(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, image, engine string
		environment         map[string]string
		want                int
	}{
		{name: "postgres default", image: "postgres:18", engine: "postgresql", want: 5432},
		{name: "mysql default", image: "mysql:8", engine: "mysql", want: 3306},
		{name: "unknown engine", image: "custom:1", environment: map[string]string{"PGPORT": "6543"}},
		{name: "postgres environment", image: "postgres:18", engine: "postgresql", environment: map[string]string{"PGPORT": "6543"}, want: 6543},
		{name: "mysql environment", image: "mysql:8", engine: "mysql", environment: map[string]string{"MYSQL_TCP_PORT": "13306"}, want: 13306},
		{name: "mariadb environment", image: "mariadb:11", engine: "mysql", environment: map[string]string{"MYSQL_TCP_PORT": "3307"}, want: 3307},
		{name: "bitnami postgres", image: "docker.io/bitnami/postgresql:18@sha256:abc", engine: "postgresql", environment: map[string]string{"POSTGRESQL_PORT_NUMBER": "5544", "PGPORT": "6543"}, want: 5544},
		{name: "bitnami postgres alias", image: "bitnamilegacy/postgresql:17", engine: "postgresql", environment: map[string]string{"POSTGRES_PORT_NUMBER": "5545"}, want: 5545},
		{name: "bitnami postgres alias precedence", image: "bitnami/postgresql:18", engine: "postgresql", environment: map[string]string{"POSTGRESQL_PORT_NUMBER": "5544", "POSTGRES_PORT_NUMBER": "5545"}, want: 5544},
		{name: "bitnami mysql", image: "registry.example/bitnami/mysql:8", engine: "mysql", environment: map[string]string{"MYSQL_PORT_NUMBER": "3307", "MYSQL_TCP_PORT": "3308"}, want: 3307},
		{name: "bitnami mariadb", image: "bitnami/mariadb:11", engine: "mysql", environment: map[string]string{"MARIADB_PORT_NUMBER": "3308"}, want: 3308},
		{name: "bitnami writes config over PGPORT", image: "bitnami/postgresql:18", engine: "postgresql", environment: map[string]string{"PGPORT": "6543"}, want: 5432},
		{name: "bitnami variable ignored on official image", image: "postgres:18", engine: "postgresql", environment: map[string]string{"POSTGRESQL_PORT_NUMBER": "5544"}, want: 5432},
		{name: "not a bitnami namespace", image: "example/bitnami-mysql:8", engine: "mysql", environment: map[string]string{"MYSQL_PORT_NUMBER": "3307"}, want: 3306},
		{name: "generic and other engine variables ignored", image: "postgres:18", engine: "postgresql", environment: map[string]string{"PORT": "8080", "MYSQL_TCP_PORT": "3307"}, want: 5432},
		{name: "empty value", image: "postgres:18", engine: "postgresql", environment: map[string]string{"PGPORT": ""}, want: 5432},
		{name: "whitespace", image: "postgres:18", engine: "postgresql", environment: map[string]string{"PGPORT": " 5544 "}, want: 5544},
		{name: "zero", image: "postgres:18", engine: "postgresql", environment: map[string]string{"PGPORT": "0"}, want: 5432},
		{name: "negative", image: "mysql:8", engine: "mysql", environment: map[string]string{"MYSQL_TCP_PORT": "-1"}, want: 3306},
		{name: "overflow", image: "postgres:18", engine: "postgresql", environment: map[string]string{"PGPORT": "65536"}, want: 5432},
		{name: "non number", image: "postgres:18", engine: "postgresql", environment: map[string]string{"PGPORT": "invalid"}, want: 5432},
		{name: "fractional", image: "postgres:18", engine: "postgresql", environment: map[string]string{"PGPORT": "5432.5"}, want: 5432},
		{name: "minimum", image: "postgres:18", engine: "postgresql", environment: map[string]string{"PGPORT": "1"}, want: 1},
		{name: "maximum", image: "mysql:8", engine: "mysql", environment: map[string]string{"MYSQL_TCP_PORT": "65535"}, want: 65535},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, suggestDSNPort(tc.image, tc.engine, tc.environment))
		})
	}
}
