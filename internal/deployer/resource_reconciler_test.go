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
		listPath      string
		createPath    string
		resourceID    string
		resourceName  string
		fileName      string
		fileContent   string
		configs       compose.Configs
		secrets       compose.Secrets
		serviceConfig []compose.ObjectRef
		serviceSecret []compose.ObjectRef
		resolved      func(InitJobSpec) ResolvedResource
	}{
		{
			name:         "config",
			listPath:     "/configs",
			createPath:   "/configs/create",
			resourceID:   "config-id",
			resourceName: "demo_app-config",
			fileName:     "config.yaml",
			fileContent:  "log_level: info",
			configs: compose.Configs{
				"app-config": {Alias: "app-config", File: "config.yaml", Data: []byte("log_level: info")},
			},
			serviceConfig: []compose.ObjectRef{{Source: "app-config", Target: "/etc/app/config.yaml"}},
			resolved: func(spec InitJobSpec) ResolvedResource {
				return spec.ResolvedConfigs["app-config"]
			},
		},
		{
			name:         "secret",
			listPath:     "/secrets",
			createPath:   "/secrets/create",
			resourceID:   "secret-id",
			resourceName: "demo_db-password",
			fileName:     "password.txt",
			fileContent:  "secret-value",
			secrets: compose.Secrets{
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
				case req.Method == http.MethodGet && path == tt.listPath:
					_ = json.NewEncoder(w).Encode([]any{})
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
				resources:       newResourceReconciler(swarm.NewSwarm(dockerClient, "")),
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

			err := deployer.DeployStack(
				context.Background(),
				"demo",
				filepath.Join(dir, "compose.yaml"),
				filepath.Join(dir, "rendered", "compose.yaml"),
				desired,
			)
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
		case req.Method == http.MethodGet && path == "/configs":
			_ = json.NewEncoder(w).Encode([]dockerswarm.Config{{
				ID:   "existing-id",
				Spec: dockerswarm.ConfigSpec{Annotations: dockerswarm.Annotations{Name: "demo_app-config"}},
			}})
		case req.Method == http.MethodPost && path == "/configs/create":
			createCalls++
			http.Error(w, `{"message":"must not create"}`, http.StatusInternalServerError)
		default:
			http.Error(w, `{"message":"unexpected request"}`, http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)

	resolved, err := newResourceReconciler(swarm.NewSwarm(newDockerTestClient(t, server), "")).Reconcile(
		context.Background(),
		"demo",
		filepath.Join(t.TempDir(), "compose.yaml"),
		compose.Configs{"app-config": {Alias: "app-config", File: "unused.yaml"}},
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
		case req.Method == http.MethodGet && path == "/configs" && !created:
			_ = json.NewEncoder(w).Encode([]any{})
		case req.Method == http.MethodGet && path == "/configs":
			_ = json.NewEncoder(w).Encode([]dockerswarm.Config{{
				ID:   "rotated-id",
				Spec: dockerswarm.ConfigSpec{Annotations: dockerswarm.Annotations{Name: rotatedName}},
			}})
		case req.Method == http.MethodPost && path == "/configs/create":
			createCalls++
			created = true
			_ = json.NewEncoder(w).Encode(map[string]string{"ID": "rotated-id"})
		default:
			http.Error(w, `{"message":"unexpected request"}`, http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)

	reconciler := newResourceReconciler(swarm.NewSwarm(newDockerTestClient(t, server), ""))
	objects := compose.Configs{
		"app-config": {Alias: "app-config", Name: rotatedName, File: "config.yaml", Data: []byte("v2")},
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
		case req.Method == http.MethodGet && path == "/configs":
			_ = json.NewEncoder(w).Encode([]any{})
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
		resources:       newResourceReconciler(swarm.NewSwarm(newDockerTestClient(t, server), "")),
		initJobRunner:   initJobs,
	}
	desired := compose.Compose{
		Configs: compose.Configs{
			"app-config": {Alias: "app-config", Name: "demo-app-config-new", File: "config.yaml", Data: []byte("new")},
		},
		Services: []compose.Service{{
			Name:     "api",
			Configs:  []compose.ObjectRef{{Source: "app-config"}},
			InitJobs: []compose.InitJob{{Name: "migrate", Image: "example/migrate:latest"}},
		}},
	}

	err := deployer.DeployStack(
		context.Background(),
		"demo",
		filepath.Join(dir, "compose.yaml"),
		filepath.Join(dir, "rendered", "compose.yaml"),
		desired,
	)
	require.ErrorIs(t, err, initErr, "init job failure must be preserved")
	assert.Zero(t, deleteCalls, "old resources must not be pruned when init job fails")
	assert.Empty(t, runner.calls, "regular services must not deploy after init job failure")
}

func TestResourceReconcilerListsMultipleResourcesInBulk(t *testing.T) {
	configListCalls := 0
	secretListCalls := 0
	inspectCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := dockerAPIPath(req.URL.Path)
		switch {
		case req.Method == http.MethodGet && path == "/configs":
			configListCalls++
			_ = json.NewEncoder(w).Encode([]dockerswarm.Config{
				{ID: "config-a-id", Spec: dockerswarm.ConfigSpec{Annotations: dockerswarm.Annotations{Name: "demo_config-a"}}},
				{ID: "config-b-id", Spec: dockerswarm.ConfigSpec{Annotations: dockerswarm.Annotations{Name: "demo_config-b"}}},
			})
		case req.Method == http.MethodGet && path == "/secrets":
			secretListCalls++
			_ = json.NewEncoder(w).Encode([]dockerswarm.Secret{
				{ID: "secret-a-id", Spec: dockerswarm.SecretSpec{Annotations: dockerswarm.Annotations{Name: "demo_secret-a"}}},
				{ID: "secret-b-id", Spec: dockerswarm.SecretSpec{Annotations: dockerswarm.Annotations{Name: "demo_secret-b"}}},
			})
		case req.Method == http.MethodGet:
			inspectCalls++
			http.Error(w, `{"message":"inspect must not be called"}`, http.StatusInternalServerError)
		default:
			http.Error(w, `{"message":"unexpected request"}`, http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)

	resolved, err := newResourceReconciler(swarm.NewSwarm(newDockerTestClient(t, server), "")).Reconcile(
		context.Background(),
		"demo",
		filepath.Join(t.TempDir(), "compose.yaml"),
		compose.Configs{
			"config-b": {Alias: "config-b", File: "unused-b"},
			"config-a": {Alias: "config-a", File: "unused-a"},
		},
		compose.Secrets{
			"secret-b": {Alias: "secret-b", File: "unused-b"},
			"secret-a": {Alias: "secret-a", File: "unused-a"},
		},
	)
	require.NoError(t, err, "reconcile resources")

	assert.Equal(t, 1, configListCalls, "configs must be loaded in one request")
	assert.Equal(t, 1, secretListCalls, "secrets must be loaded in one request")
	assert.Zero(t, inspectCalls, "individual resource inspect must not be used")
	assert.Equal(t, "config-a-id", resolved.configs["config-a"].ID)
	assert.Equal(t, "config-b-id", resolved.configs["config-b"].ID)
	assert.Equal(t, "secret-a-id", resolved.secrets["secret-a"].ID)
	assert.Equal(t, "secret-b-id", resolved.secrets["secret-b"].ID)
}

func TestResourceReconcilerPreservesComposeResourceOptions(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.txt"), []byte("config-data"), 0o600), "write config")

	var createdConfig dockerswarm.ConfigSpec
	var createdSecret dockerswarm.SecretSpec
	var handlerErr error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := dockerAPIPath(req.URL.Path)
		switch {
		case req.Method == http.MethodGet && (path == "/configs" || path == "/secrets"):
			_ = json.NewEncoder(w).Encode([]any{})
		case req.Method == http.MethodPost && path == "/configs/create":
			if decodeErr := json.NewDecoder(req.Body).Decode(&createdConfig); handlerErr == nil {
				handlerErr = decodeErr
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"ID": "config-id"})
		case req.Method == http.MethodPost && path == "/secrets/create":
			if decodeErr := json.NewDecoder(req.Body).Decode(&createdSecret); handlerErr == nil {
				handlerErr = decodeErr
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"ID": "secret-id"})
		default:
			http.Error(w, `{"message":"unexpected request"}`, http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)

	_, err := newResourceReconciler(swarm.NewSwarm(newDockerTestClient(t, server), "")).Reconcile(
		context.Background(),
		"demo",
		filepath.Join(dir, "compose.yaml"),
		compose.Configs{
			"app-config": {
				Alias:          "app-config",
				File:           "config.txt",
				Data:           []byte("config-data"),
				Labels:         *compose.NewLabels(map[string]string{"purpose": "init"}),
				TemplateDriver: "golang",
			},
		},
		compose.Secrets{
			"app-secret": {
				Alias:          "app-secret",
				Driver:         "vault",
				DriverOpts:     map[string]string{"key": "apps/demo"},
				Labels:         *compose.NewLabels(map[string]string{"sensitivity": "high"}),
				TemplateDriver: "golang",
			},
		},
	)
	require.NoError(t, err, "reconcile resources")
	require.NoError(t, handlerErr, "decode create spec")

	assert.Equal(t, "demo_app-config", createdConfig.Name)
	assert.Equal(t, []byte("config-data"), createdConfig.Data)
	assert.Equal(t, map[string]string{"purpose": "init", "com.docker.stack.namespace": "demo"}, createdConfig.Labels)
	require.NotNil(t, createdConfig.Templating, "config template driver must be preserved")
	assert.Equal(t, "golang", createdConfig.Templating.Name)

	assert.Equal(t, "demo_app-secret", createdSecret.Name)
	assert.Empty(t, createdSecret.Data, "driver-backed secrets must not contain file data")
	assert.Equal(t, map[string]string{"sensitivity": "high", "com.docker.stack.namespace": "demo"}, createdSecret.Labels)
	require.NotNil(t, createdSecret.Driver, "secret driver must be preserved")
	assert.Equal(t, "vault", createdSecret.Driver.Name)
	assert.Equal(t, map[string]string{"key": "apps/demo"}, createdSecret.Driver.Options)
	require.NotNil(t, createdSecret.Templating, "secret template driver must be preserved")
	assert.Equal(t, "golang", createdSecret.Templating.Name)
}

func TestResourceReconcilerDoesNotCreateMissingExternalResource(t *testing.T) {
	tests := []struct {
		name       string
		listPath   string
		configs    compose.Configs
		secrets    compose.Secrets
		errorMatch string
	}{
		{
			name:       "config",
			listPath:   "/configs",
			configs:    compose.Configs{"external-config": {Alias: "external-config", External: true}},
			errorMatch: "external config external-config does not exist",
		},
		{
			name:       "secret",
			listPath:   "/secrets",
			secrets:    compose.Secrets{"external-secret": {Alias: "external-secret", External: true}},
			errorMatch: "external secret external-secret does not exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			createCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				path := dockerAPIPath(req.URL.Path)
				switch {
				case req.Method == http.MethodGet && path == tt.listPath:
					_ = json.NewEncoder(w).Encode([]any{})
				case req.Method == http.MethodPost:
					createCalls++
					http.Error(w, `{"message":"external resource must not be created"}`, http.StatusInternalServerError)
				default:
					http.Error(w, `{"message":"unexpected request"}`, http.StatusInternalServerError)
				}
			}))
			t.Cleanup(server.Close)

			_, err := newResourceReconciler(swarm.NewSwarm(newDockerTestClient(t, server), "")).Reconcile(
				context.Background(),
				"demo",
				filepath.Join(t.TempDir(), "compose.yaml"),
				tt.configs,
				tt.secrets,
			)
			require.ErrorContains(t, err, tt.errorMatch)
			assert.Zero(t, createCalls, "external resources must never be created")
		})
	}
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
