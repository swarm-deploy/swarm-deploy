package services

import (
	"errors"
	"testing"

	dockerregistry "github.com/docker/docker/api/types/registry"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestRegistryAuthMaintenanceRefreshesMatchingServices(t *testing.T) {
	t.Setenv("DOCKER_AUTH_CONFIG", "")
	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	applier := New(manager, registry.NewAuthManager())
	expected := serviceSpec("api", "registry.example/api:1")
	current := service("api-id", 7, cloneSpec(expected))
	current.Spec.TaskTemplate.ForceUpdate = 42
	current.Spec.TaskTemplate.ContainerSpec.Image += "@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	manager.EXPECT().Inspect(gomock.Any(), expected.Name).Return(current, nil).AnyTimes()
	for _, token := range []string{"first-token", "rotated-token", ""} {
		writeRegistryAuth(t, dir, token)
		manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, current.Spec, gomock.Any()).DoAndReturn(
			func(_ any, _ string, _ dockerswarm.Version, applied dockerswarm.ServiceSpec, options dockerswarm.ServiceUpdateOptions) error {
				assert.Equal(t, current.Spec, applied)
				assert.False(t, options.QueryRegistry)
				assert.Equal(t, dockerswarm.RegistryAuthFromSpec, options.RegistryAuthFrom)
				auth, err := dockerregistry.DecodeAuthConfig(options.EncodedRegistryAuth)
				require.NoError(t, err)
				assert.Equal(t, token, auth.Password)
				return nil
			})
		require.NoError(t, applier.RefreshRegistryAuth(t.Context(), "demo", []dockerswarm.ServiceSpec{expected}))
		require.NoError(t, applier.RefreshRegistryAuth(t.Context(), "demo", []dockerswarm.ServiceSpec{expected}),
			"acknowledged credentials must not be written on every unchanged reconciliation")
	}
}

func TestRegistryAuthMaintenanceNeverAppliesDriftOrStartsExecutions(t *testing.T) {
	for _, reason := range []string{
		"missing", "image drift", "label drift", "environment drift", "paused", "updating", "failed", "rollback_completed",
		"replicated-job", "global-job", "none", "on-failure", "finite retries", "global none", "ownership",
	} {
		t.Run(reason, func(t *testing.T) {
			t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"registry.example":{"username":"robot","password":"synthetic-token"}}}`)
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			expected := authExecutionSpec(reason)
			current := service("api-id", 7, cloneSpec(expected))
			var inspectErr error
			switch reason {
			case "missing":
				current, inspectErr = dockerswarm.Service{}, swarm.ErrServiceNotFound
			case "image drift":
				current.Spec.TaskTemplate.ContainerSpec.Image = "registry.example/other:1"
			case "label drift":
				current.Spec.Labels["drift"] = "external"
			case "environment drift":
				current.Spec.TaskTemplate.ContainerSpec.Env = []string{"EXTERNAL=change"}
			case "paused", "updating", "failed", "rollback_completed":
				current.UpdateStatus = &dockerswarm.UpdateStatus{State: dockerswarm.UpdateState(reason)}
			case "ownership":
				current.Spec.Labels[labelsdict.StackNamespace] = "other"
			}
			manager.EXPECT().Inspect(gomock.Any(), expected.Name).Return(current, inspectErr)
			err := New(manager, registry.NewAuthManager()).RefreshRegistryAuth(t.Context(), "demo", []dockerswarm.ServiceSpec{expected})
			if reason == "ownership" {
				require.ErrorContains(t, err, "not owned")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestRegistryAuthMaintenanceRechecksEveryGuardAfterConflict(t *testing.T) {
	for _, change := range []string{"force update", "spec drift", "paused", "unsafe policy", "missing", "ownership"} {
		t.Run(change, func(t *testing.T) {
			t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"registry.example":{"username":"robot","password":"synthetic-token"}}}`)
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			expected := serviceSpec("api", "registry.example/api:1")
			current := service("api-id", 7, cloneSpec(expected))
			newer := service("api-id", 8, cloneSpec(expected))
			var inspectErr error
			switch change {
			case "force update":
				newer.Spec.TaskTemplate.ForceUpdate = 99
			case "spec drift":
				newer.Spec.TaskTemplate.ContainerSpec.Env = []string{"EXTERNAL=change"}
			case "paused":
				newer.UpdateStatus = &dockerswarm.UpdateStatus{State: dockerswarm.UpdateStatePaused}
			case "unsafe policy":
				newer.Spec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
			case "missing":
				inspectErr = swarm.ErrServiceNotFound
			case "ownership":
				newer.Spec.Labels[labelsdict.StackNamespace] = "other"
			}
			gomock.InOrder(
				manager.EXPECT().Inspect(gomock.Any(), expected.Name).Return(current, nil),
				manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, current.Spec, gomock.Any()).Return(swarm.ErrServiceConflict),
				manager.EXPECT().Inspect(gomock.Any(), expected.Name).Return(newer, inspectErr),
			)
			if change == "force update" {
				manager.EXPECT().Update(gomock.Any(), newer.ID, newer.Version, newer.Spec, gomock.Any()).Return(nil)
			}
			err := New(manager, registry.NewAuthManager()).RefreshRegistryAuth(t.Context(), "demo", []dockerswarm.ServiceSpec{expected})
			if change == "ownership" {
				require.ErrorContains(t, err, "not owned")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestRegistryAuthMaintenanceDoesNotAcknowledgeLookupOrUpdateFailure(t *testing.T) {
	for _, failure := range []string{"lookup", "update"} {
		t.Run(failure, func(t *testing.T) {
			t.Setenv("DOCKER_AUTH_CONFIG", "")
			dir := t.TempDir()
			t.Setenv("DOCKER_CONFIG", dir)
			writeRegistryAuth(t, dir, "synthetic-token")
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			expected := serviceSpec("api", "registry.example/api:1")
			current := service("api-id", 7, cloneSpec(expected))
			manager.EXPECT().Inspect(gomock.Any(), expected.Name).Return(current, nil).AnyTimes()
			if failure == "lookup" {
				t.Setenv("DOCKER_CONFIG", "/dev/null")
			} else {
				manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, current.Spec, gomock.Any()).Return(errors.New("synthetic API failure"))
			}
			applier := New(manager, registry.NewAuthManager())
			require.Error(t, applier.RefreshRegistryAuth(t.Context(), "demo", []dockerswarm.ServiceSpec{expected}))
			t.Setenv("DOCKER_CONFIG", dir)
			manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, current.Spec, gomock.Any()).Return(nil)
			require.NoError(t, applier.RefreshRegistryAuth(t.Context(), "demo", []dockerswarm.ServiceSpec{expected}))
		})
	}
}
