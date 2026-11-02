package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
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
	databaseLabel := map[string]string{labelsdict.ServiceType: "database"}
	for _, tc := range []struct {
		name     string
		services []swarm.StackService
		want     string
	}{
		{name: "empty", want: `{"suggestions":[]}`},
		{name: "filtered and nullable", services: []swarm.StackService{
			{ID: "pg", Name: "postgres", FullName: "infra_postgres", Stack: "infra", Image: "postgres:18"},
			{ID: "my", Name: "mysql", FullName: "infra_mysql", Stack: "infra", Image: "mysql:8.4"},
			{ID: "custom", Name: "custom", FullName: "custom_database", Labels: databaseLabel, Image: "custom:v1"},
			{ID: "container", Name: "postgres", FullName: "other_postgres", Image: "custom:v1", ServiceSpec: dockerswarm.ServiceSpec{TaskTemplate: dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{
				Labels: databaseLabel, Env: []string{"PASSWORD=must-not-leak"}, Secrets: []*dockerswarm.SecretReference{{SecretName: "must-not-leak"}},
			}}}},
			{ID: "app", Name: "postgres", FullName: "app_postgres", Image: "custom:v1"},
			{ID: "overridden", Name: "postgres", FullName: "override_postgres", Image: "postgres:18", Labels: map[string]string{labelsdict.ServiceType: "application"}},
		}, want: `{"suggestions":[{"service_id":"pg","service_name":"infra_postgres","stack":"infra","engine":"postgresql"},{"service_id":"my","service_name":"infra_mysql","stack":"infra","engine":"mysql"},{"service_id":"custom","service_name":"custom_database","stack":"","engine":null},{"service_id":"container","service_name":"other_postgres","stack":"","engine":"postgresql"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inspector := swarm.NewMockServiceManager(gomock.NewController(t))
			inspector.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{}).Return(tc.services, nil)
			h := &handler{serviceInspector: inspector}
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

func TestListDSNSuggestionsFailure(t *testing.T) {
	t.Parallel()
	inspector := swarm.NewMockServiceManager(gomock.NewController(t))
	inspector.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{}).Return(nil, assert.AnError)
	h := &handler{serviceInspector: inspector}
	_, err := h.ListDSNSuggestions(t.Context())
	require.Error(t, err)
	var statusErr *statusError
	require.ErrorAs(t, err, &statusErr)
	assert.Equal(t, http.StatusInternalServerError, statusErr.code)
	// The public error must not contain upstream details.
	encoded, err := json.Marshal(statusErr.Error())
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), assert.AnError.Error())
}
