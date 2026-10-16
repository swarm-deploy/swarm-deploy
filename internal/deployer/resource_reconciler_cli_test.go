package deployer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// This exercises the real CLI against a stateful Docker API test server. It checks
// the CLI's inspect/update path, not Swarm task execution or daemon validation.
func TestDockerStackDeployReusesReconciledResources(t *testing.T) {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("Docker CLI is required for the stack deploy compatibility test")
	}

	for _, tt := range []struct {
		name       string
		configName string
		secretName string
	}{
		{name: "scoped", configName: "demo_app", secretName: "demo_password"},
		{name: "explicit", configName: "custom-config", secretName: "custom-secret"},
		{name: "rotated", configName: "demo-config-abc123", secretName: "demo-secret-abc123"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("DOCKER_CONFIG", t.TempDir())
			t.Setenv("DOCKER_API_VERSION", "1.48")
			t.Setenv("DOCKER_TLS_VERIFY", "")
			t.Setenv("DOCKER_CERT_PATH", "")
			t.Setenv("DOCKER_CONTEXT", "")
			require.NoError(t, os.WriteFile(filepath.Join(dir, "config.txt"), []byte("config-data"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "password.txt"), []byte("secret-data"), 0o600))
			names := ""
			secretName := ""
			if tt.name != "scoped" {
				names = "    name: " + tt.configName + "\n"
				secretName = "    name: " + tt.secretName + "\n"
			}
			path := filepath.Join(dir, "compose.yaml")
			require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf(`version: '3.8'
services:
  api:
    image: example/api:latest
    configs:
      - source: app
        target: /etc/app.conf
    secrets:
      - password
configs:
  app:
%s    file: ./config.txt
    labels:
      purpose: app
secrets:
  password:
%s    file: ./password.txt
    labels:
      purpose: password
`, names, secretName)), 0o600))
			desired, err := compose.NewFileLoader().Load(t.Context(), path)
			require.NoError(t, err)

			var mu sync.Mutex
			var config *dockerswarm.Config
			var secret *dockerswarm.Secret
			var services []dockerswarm.ServiceSpec
			creates := make(map[string]int)
			updates := make(map[string]int)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				path := dockerAPIPath(req.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				switch {
				case path == "/_ping":
					w.Header().Set("API-Version", "1.48")
					_, _ = w.Write([]byte("OK"))
				case req.Method == http.MethodGet && path == "/info":
					_ = json.NewEncoder(w).Encode(map[string]any{"Swarm": map[string]bool{"ControlAvailable": true}})
				case req.Method == http.MethodGet && path == "/networks":
					_ = json.NewEncoder(w).Encode([]map[string]string{{"Id": "network-id", "Name": "demo_default"}})
				case req.Method == http.MethodGet && path == "/services":
					_ = json.NewEncoder(w).Encode([]any{})
				case req.Method == http.MethodGet && path == "/configs":
					result := []dockerswarm.Config{}
					if config != nil {
						result = append(result, *config)
					}
					_ = json.NewEncoder(w).Encode(result)
				case req.Method == http.MethodGet && path == "/secrets":
					result := []dockerswarm.Secret{}
					if secret != nil {
						result = append(result, *secret)
					}
					_ = json.NewEncoder(w).Encode(result)
				case req.Method == http.MethodPost && path == "/configs/create":
					creates["config"]++
					if config != nil {
						http.Error(w, `{"message":"config already exists"}`, http.StatusConflict)
						return
					}
					config = &dockerswarm.Config{ID: "config-id", Meta: dockerswarm.Meta{Version: dockerswarm.Version{Index: 1}}}
					assert.NoError(t, json.NewDecoder(req.Body).Decode(&config.Spec))
					_ = json.NewEncoder(w).Encode(map[string]string{"ID": config.ID})
				case req.Method == http.MethodPost && path == "/secrets/create":
					creates["secret"]++
					if secret != nil {
						http.Error(w, `{"message":"secret already exists"}`, http.StatusConflict)
						return
					}
					secret = &dockerswarm.Secret{ID: "secret-id", Meta: dockerswarm.Meta{Version: dockerswarm.Version{Index: 1}}}
					assert.NoError(t, json.NewDecoder(req.Body).Decode(&secret.Spec))
					_ = json.NewEncoder(w).Encode(map[string]string{"ID": secret.ID})
				case req.Method == http.MethodGet && path == "/configs/"+tt.configName && config != nil:
					_ = json.NewEncoder(w).Encode(config)
				case req.Method == http.MethodGet && path == "/secrets/"+tt.secretName && secret != nil:
					// Docker never returns secret data from inspect.
					inspected := *secret
					inspected.Spec.Data = nil
					_ = json.NewEncoder(w).Encode(inspected)
				case req.Method == http.MethodPost && path == "/configs/config-id/update" && config != nil:
					updates["config"]++
					var spec dockerswarm.ConfigSpec
					assert.NoError(t, json.NewDecoder(req.Body).Decode(&spec))
					assert.Equal(t, config.Spec, spec, "CLI must use the same resource spec")
					assert.Equal(t, "1", req.URL.Query().Get("version"))
				case req.Method == http.MethodPost && path == "/secrets/secret-id/update" && secret != nil:
					updates["secret"]++
					var spec dockerswarm.SecretSpec
					assert.NoError(t, json.NewDecoder(req.Body).Decode(&spec))
					assert.Equal(t, secret.Spec, spec, "CLI must use the same resource spec")
					assert.Equal(t, "1", req.URL.Query().Get("version"))
				case req.Method == http.MethodPost && path == "/services/create":
					var spec dockerswarm.ServiceSpec
					assert.NoError(t, json.NewDecoder(req.Body).Decode(&spec))
					services = append(services, spec)
					_ = json.NewEncoder(w).Encode(map[string]string{"ID": "service-id"})
				default:
					t.Errorf("unexpected Docker API request: %s %s", req.Method, path)
					http.Error(w, `{"message":"unexpected request"}`, http.StatusInternalServerError)
				}
			}))
			t.Cleanup(server.Close)
			t.Setenv("DOCKER_HOST", strings.Replace(server.URL, "http://", "tcp://", 1))
			swarmService := swarm.NewSwarm(newDockerTestClient(t, server), dockerPath)
			deployer := &Deployer{
				// Avoid registry access and task convergence polling in this API contract test.
				stackDeployArgs: []string{"stack", "deploy", "--detach=true", "--resolve-image=never"},
				runner:          swarmService.BinaryRunner,
				resources:       newResourceReconciler(swarmService),
			}
			for range 2 {
				require.NoError(t, deployer.DeployStack(t.Context(), "demo", path, path, desired.Compose))
			}

			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, map[string]int{"config": 1, "secret": 1}, creates, "only API reconciliation should create resources")
			assert.Equal(t, map[string]int{"config": 2, "secret": 2}, updates, "CLI should reuse resources on both deploys")
			require.Len(t, services, 2)
			for _, service := range services {
				require.Len(t, service.TaskTemplate.ContainerSpec.Configs, 1)
				require.Len(t, service.TaskTemplate.ContainerSpec.Secrets, 1)
				assert.Equal(t, "config-id", service.TaskTemplate.ContainerSpec.Configs[0].ConfigID)
				assert.Equal(t, tt.configName, service.TaskTemplate.ContainerSpec.Configs[0].ConfigName)
				assert.Equal(t, "secret-id", service.TaskTemplate.ContainerSpec.Secrets[0].SecretID)
				assert.Equal(t, tt.secretName, service.TaskTemplate.ContainerSpec.Secrets[0].SecretName)
			}
		})
	}
}
