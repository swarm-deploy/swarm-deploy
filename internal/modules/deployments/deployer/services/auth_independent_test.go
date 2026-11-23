package services

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestUnknownAnonymousAuthStatePreservesEngineCredentials(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	desired := serviceSpec("api", "registry.example/api:1")
	current := service("api-id", 3, desired)
	manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(current, nil).Times(2)
	applier := New(manager, &registry.NopAuthManager{})
	result, err := applier.Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
	require.NoError(t, err)
	assert.Empty(t, result.ChangedServices, "unknown anonymous state must not overwrite Engine's hidden credentials")
	desired = cloneSpec(desired)
	desired.Labels["metadata"] = "changed"
	manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, _ string, _ dockerswarm.Version, _ dockerswarm.ServiceSpec, options dockerswarm.ServiceUpdateOptions) error {
			assert.Empty(t, options.EncodedRegistryAuth)
			assert.Equal(t, dockerswarm.RegistryAuthFromSpec, options.RegistryAuthFrom)
			return nil
		})
	_, err = applier.Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
	require.NoError(t, err)
}

func TestAuthRefreshDiagnosticsExcludeCredentials(t *testing.T) {
	for _, failure := range []string{"lookup", "update"} {
		t.Run(failure, func(t *testing.T) {
			const token = "credential-must-not-appear-in-diagnostics"
			raw := `{"auths":{"registry.example":{"username":"robot","password":"` + token + `"}}}`
			if failure == "lookup" {
				raw = raw[:len(raw)-1]
			}
			t.Setenv("DOCKER_AUTH_CONFIG", raw)
			var logs bytes.Buffer
			previousLogger := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previousLogger) })
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := serviceSpec("api", "registry.example/api:1")
			current := service("api-id", 3, desired)
			manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(current, nil)
			if failure == "update" {
				manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, gomock.Any(), gomock.Any()).Return(errors.New("engine unavailable"))
			}
			_, err := New(manager, registry.NewAuthManager()).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
			require.Error(t, err)
			assert.NotContains(t, err.Error(), token)
			assert.NotContains(t, logs.String(), token)
			assert.NotContains(t, logs.String(), "EncodedRegistryAuth")
		})
	}
}
