package deployer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types/filters"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

func TestResourceReconcilerFiltersBulkListsByResourceName(t *testing.T) {
	var configNames []string
	var secretNames []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := dockerAPIPath(req.URL.Path)
		args, err := filters.FromJSON(req.URL.Query().Get("filters"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		switch {
		case req.Method == http.MethodGet && path == "/configs":
			configNames = args.Get("name")
			_ = json.NewEncoder(w).Encode([]dockerswarm.Config{
				{ID: "config-id", Spec: dockerswarm.ConfigSpec{Annotations: dockerswarm.Annotations{Name: "demo_app-config"}}},
				{ID: "external-config-id", Spec: dockerswarm.ConfigSpec{Annotations: dockerswarm.Annotations{Name: "shared-config"}}},
			})
		case req.Method == http.MethodGet && path == "/secrets":
			secretNames = args.Get("name")
			_ = json.NewEncoder(w).Encode([]dockerswarm.Secret{
				{ID: "secret-id", Spec: dockerswarm.SecretSpec{Annotations: dockerswarm.Annotations{Name: "demo_db-password"}}},
				{ID: "rotated-secret-id", Spec: dockerswarm.SecretSpec{Annotations: dockerswarm.Annotations{Name: "demo-api-token-abc123"}}},
			})
		default:
			http.Error(w, `{"message":"unexpected request"}`, http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)

	_, err := newResourceReconciler(newDockerTestClient(t, server)).Reconcile(
		context.Background(),
		"demo",
		filepath.Join(t.TempDir(), "compose.yaml"),
		compose.Configs{
			"app-config":    {Alias: "app-config", File: "unused"},
			"shared-config": {Alias: "shared-config", External: true},
		},
		compose.Secrets{
			"db-password": {Alias: "db-password", File: "unused"},
			"api-token":   {Alias: "api-token", Name: "demo-api-token-abc123", File: "unused"},
		},
	)
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"demo_app-config", "shared-config"}, configNames)
	assert.ElementsMatch(t, []string{"demo_db-password", "demo-api-token-abc123"}, secretNames)
}
