package deployer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestDeployStackReconcilesResourcesBeforeInitJobs(t *testing.T) {
	tests := []struct {
		name          string
		resourcePath  string
		createPath    string
		resourceID    string
		resourceName  string
		fileName      string
		fileContent   string
		configs       compose.SharedObjects
		secrets       compose.SharedObjects
		serviceConfig []compose.ObjectRef
		serviceSecret []compose.ObjectRef
		resolved      func(InitJobSpec) ResolvedResource
	}{
		{
			name:         "config",
			resourcePath: "/configs/demo_app-config",
			createPath:   "/configs/create",
			resourceID:   "config-id",
			resourceName: "demo_app-config",
			fileName:     "config.yaml",
			fileContent:  "log_level: info",
			configs: compose.SharedObjects{
				"app-config": {Alias: "app-config", File: "config.yaml"},
			},
			serviceConfig: []compose.ObjectRef{{Source: "app-config", Target: "/etc/app/config.yaml"}},
			resolved: func(spec InitJobSpec) ResolvedResource {
				return spec.ResolvedConfigs["app-config"]
			},
		},
		{
			name:         "secret",
			resourcePath: "/secrets/demo_db-password",
			createPath:   "/secrets/create",
			resourceID:   "secret-id",
			resourceName: "demo_db-password",
			fileName:     "password.txt",
			fileContent:  "secret-value",
			secrets: compose.SharedObjects{
				"db-password": {Alias: "db-password", File: "password.txt"},
			},
			serviceSecret: []compose.ObjectRef{{Source: "db-password"}},
			resolved: func(spec InitJobSpec) ResolvedResource {
				return spec.ResolvedSecrets["db-password"]
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, tt.fileName), []byte(tt.fileContent), 0o600), "write resource")

			events := make([]string, 0, 3)
			createdName := ""
			var createdData []byte
			var handlerErr error
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				path := dockerAPIPath(req.URL.Path)
				switch {
				case req.Method == http.MethodGet && path == tt.resourcePath:
					http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
				case req.Method == http.MethodPost && path == tt.createPath:
					events = append(events, tt.name+"-create")
					var spec struct {
						Name string
						Data []byte
					}
					handlerErr = json.NewDecoder(req.Body).Decode(&spec)
					createdName = spec.Name
					createdData = spec.Data
					_ = json.NewEncoder(w).Encode(map[string]string{"ID": tt.resourceID})
				default:
					http.Error(w, `{"message":"unexpected request"}`, http.StatusInternalServerError)
				}
			}))
			t.Cleanup(server.Close)

			dockerClient := newDockerTestClient(t, server)
			initJobs := &fakeInitJobExecutor{events: &events}
			runner := &fakeRunner{events: &events}
			deployer := &Deployer{
				stackDeployArgs: []string{"stack", "deploy"},
				runner:          runner,
				resources:       newResourceReconciler(dockerClient),
				initJobRunner:   initJobs,
			}

			desired := compose.Compose{
				Configs: tt.configs,
				Secrets: tt.secrets,
				Services: []compose.Service{{
					Name:     "api",
					Configs:  tt.serviceConfig,
					Secrets:  tt.serviceSecret,
					InitJobs: []compose.InitJob{{Name: "migrate", Image: "example/migrate:latest"}},
				}},
			}

			err := deployer.DeployStack(context.Background(), "demo", filepath.Join(dir, "compose.yaml"), desired)
			require.NoError(t, err, "deploy stack")
			require.NoError(t, handlerErr, "decode create request")
			assert.Equal(t, tt.resourceName, createdName)
			assert.Equal(t, []byte(tt.fileContent), createdData)
			require.Len(t, initJobs.calls, 1, "expected one init job")
			assert.Equal(t, ResolvedResource{ID: tt.resourceID, Name: tt.resourceName}, tt.resolved(initJobs.calls[0]))
			assert.Equal(t, []string{tt.name + "-create", "init:api:migrate", "deploy"}, events, "resource must exist before init job")
		})
	}
}

func TestResourceReconcilerReusesExistingResource(t *testing.T) {
	createCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := dockerAPIPath(req.URL.Path)
		switch {
		case req.Method == http.MethodGet && path == "/configs/demo_app-config":
			_ = json.NewEncoder(w).Encode(dockerswarm.Config{
				ID:   "existing-id",
				Spec: dockerswarm.ConfigSpec{Annotations: dockerswarm.Annotations{Name: "demo_app-config"}},
			})
		case req.Method == http.MethodPost && path == "/configs/create":
			createCalls++
			http.Error(w, `{"message":"must not create"}`, http.StatusInternalServerError)
		default:
			http.Error(w, `{"message":"unexpected request"}`, http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)

	resolved, err := newResourceReconciler(newDockerTestClient(t, server)).Reconcile(
		context.Background(),
		"demo",
		filepath.Join(t.TempDir(), "compose.yaml"),
		compose.SharedObjects{"app-config": {Alias: "app-config", File: "unused.yaml"}},
		nil,
	)
	require.NoError(t, err, "reconcile resources")

	assert.Zero(t, createCalls, "existing config must not be created again")
	assert.Equal(t, ResolvedResource{ID: "existing-id", Name: "demo_app-config"}, resolved.configs["app-config"])
}

func TestResourceReconcilerUsesRotatedNameOnce(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("v2"), 0o600), "write config")

	const rotatedName = "demo-app-config-abc123"
	created := false
	createCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := dockerAPIPath(req.URL.Path)
		switch {
		case req.Method == http.MethodGet && path == "/configs/"+rotatedName && !created:
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
		case req.Method == http.MethodGet && path == "/configs/"+rotatedName:
			_ = json.NewEncoder(w).Encode(dockerswarm.Config{
				ID:   "rotated-id",
				Spec: dockerswarm.ConfigSpec{Annotations: dockerswarm.Annotations{Name: rotatedName}},
			})
		case req.Method == http.MethodPost && path == "/configs/create":
			createCalls++
			created = true
			_ = json.NewEncoder(w).Encode(map[string]string{"ID": "rotated-id"})
		default:
			http.Error(w, `{"message":"unexpected request"}`, http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)

	reconciler := newResourceReconciler(newDockerTestClient(t, server))
	objects := compose.SharedObjects{
		"app-config": {Alias: "app-config", Name: rotatedName, File: "config.yaml"},
	}

	first, err := reconciler.Reconcile(context.Background(), "demo", filepath.Join(dir, "compose.yaml"), objects, nil)
	require.NoError(t, err, "first reconcile")
	second, err := reconciler.Reconcile(context.Background(), "demo", filepath.Join(dir, "compose.yaml"), objects, nil)
	require.NoError(t, err, "second reconcile")

	assert.Equal(t, 1, createCalls, "rotated resource must be created once")
	assert.Equal(t, ResolvedResource{ID: "rotated-id", Name: rotatedName}, first.configs["app-config"])
	assert.Equal(t, first.configs["app-config"], second.configs["app-config"], "all consumers must reuse one rotated resource")
}

func TestDeployStackInitJobFailureDoesNotDeleteResources(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("new"), 0o600), "write config")

	deleteCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := dockerAPIPath(req.URL.Path)
		switch {
		case req.Method == http.MethodGet && path == "/configs/demo-app-config-new":
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
		case req.Method == http.MethodPost && path == "/configs/create":
			_ = json.NewEncoder(w).Encode(map[string]string{"ID": "new-id"})
		case req.Method == http.MethodDelete:
			deleteCalls++
		default:
			http.Error(w, `{"message":"unexpected request"}`, http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)

	initErr := errors.New("migration failed")
	initJobs := &fakeInitJobExecutor{errAt: 1, err: initErr}
	runner := &fakeRunner{}
	deployer := &Deployer{
		stackDeployArgs: []string{"stack", "deploy"},
		runner:          runner,
		resources:       newResourceReconciler(newDockerTestClient(t, server)),
		initJobRunner:   initJobs,
	}
	desired := compose.Compose{
		Configs: compose.SharedObjects{
			"app-config": {Alias: "app-config", Name: "demo-app-config-new", File: "config.yaml"},
		},
		Services: []compose.Service{{
			Name:     "api",
			Configs:  []compose.ObjectRef{{Source: "app-config"}},
			InitJobs: []compose.InitJob{{Name: "migrate", Image: "example/migrate:latest"}},
		}},
	}

	err := deployer.DeployStack(context.Background(), "demo", filepath.Join(dir, "compose.yaml"), desired)
	require.ErrorIs(t, err, initErr, "init job failure must be preserved")
	assert.Zero(t, deleteCalls, "old resources must not be pruned when init job fails")
	assert.Empty(t, runner.calls, "regular services must not deploy after init job failure")
}

func TestBuildInitServiceSpecUsesResolvedResourceIDs(t *testing.T) {
	runner := &InitJobRunner{swarmService: &swarm.Swarm{}}

	spec, err := runner.buildInitServiceSpec(context.Background(), InitJobSpec{
		StackName:   "demo",
		ServiceName: "api",
		ServiceConfigs: []compose.ObjectRef{{
			Source: "app-config",
			Target: "/etc/app/config.yaml",
		}},
		ServiceSecrets: []compose.ObjectRef{{Source: "db-password"}},
		ResolvedConfigs: map[string]ResolvedResource{
			"app-config": {ID: "config-id", Name: "demo_app-config"},
		},
		ResolvedSecrets: map[string]ResolvedResource{
			"db-password": {ID: "secret-id", Name: "demo_db-password"},
		},
		Job: compose.InitJob{Image: "example/migrate:latest"},
	}, "migrate")
	require.NoError(t, err, "build init job spec")

	container := spec.TaskTemplate.ContainerSpec
	require.Len(t, container.Configs, 1, "expected config reference")
	assert.Equal(t, "config-id", container.Configs[0].ConfigID)
	assert.Equal(t, "demo_app-config", container.Configs[0].ConfigName)
	require.Len(t, container.Secrets, 1, "expected secret reference")
	assert.Equal(t, "secret-id", container.Secrets[0].SecretID)
	assert.Equal(t, "demo_db-password", container.Secrets[0].SecretName)
	assert.Equal(t, "/run/secrets/db-password", container.Secrets[0].File.Name)
	assert.Equal(t, "0", container.Secrets[0].File.UID)
	assert.Equal(t, "0", container.Secrets[0].File.GID)
}

func newDockerTestClient(t *testing.T, server *httptest.Server) *client.Client {
	t.Helper()

	dockerClient, err := client.NewClientWithOpts(
		client.WithHost(server.URL),
		client.WithHTTPClient(server.Client()),
		client.WithVersion("1.41"),
	)
	require.NoError(t, err, "create Docker client")
	t.Cleanup(func() {
		require.NoError(t, dockerClient.Close(), "close Docker client")
	})

	return dockerClient
}

func dockerAPIPath(path string) string {
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)
	if len(parts) == 2 && strings.HasPrefix(parts[0], "v") {
		return "/" + parts[1]
	}

	return path
}
