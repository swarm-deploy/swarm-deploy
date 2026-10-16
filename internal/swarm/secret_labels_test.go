package swarm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateSecretLabelsPreservesMetadata(t *testing.T) {
	original := dockerswarm.Secret{ID: "secret-id", Meta: dockerswarm.Meta{Version: dockerswarm.Version{Index: 7}}, Spec: dockerswarm.SecretSpec{
		Annotations: dockerswarm.Annotations{Name: "password", Labels: map[string]string{"old": "label"}},
		Driver:      &dockerswarm.Driver{Name: "vault", Options: map[string]string{"key": "app/password"}},
		Templating:  &dockerswarm.Driver{Name: "golang"},
	}}
	labels := map[string]string{"purpose": "app"}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		switch req.URL.Path {
		case "/v1.48/secrets/secret-id":
			assert.Equal(t, http.MethodGet, req.Method)
			assert.NoError(t, json.NewEncoder(w).Encode(original))
		case "/v1.48/secrets/secret-id/update":
			assert.Equal(t, http.MethodPost, req.Method)
			assert.Equal(t, "7", req.URL.Query().Get("version"))
			var spec dockerswarm.SecretSpec
			assert.NoError(t, json.NewDecoder(req.Body).Decode(&spec))
			assert.Equal(t, labels, spec.Labels)
			assert.Equal(t, original.Spec.Name, spec.Name)
			assert.Equal(t, original.Spec.Driver, spec.Driver)
			assert.Equal(t, original.Spec.Templating, spec.Templating)
			assert.Empty(t, spec.Data)
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	docker, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithHTTPClient(server.Client()), client.WithVersion("1.48"))
	require.NoError(t, err)
	defer func() { require.NoError(t, docker.Close()) }()
	require.NoError(t, newSecretManager(docker).UpdateLabels(t.Context(), "secret-id", labels))
	assert.Equal(t, 2, calls)
}
