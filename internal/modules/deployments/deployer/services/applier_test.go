package services

import (
	"encoding/base64"
	"errors"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	dockerregistry "github.com/docker/docker/api/types/registry"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestApplyCreatesServicesInDeterministicOrder(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	firstInspect := manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{}, swarm.ErrServiceNotFound)
	firstCreate := manager.EXPECT().Create(gomock.Any(), serviceSpec("api", "nginx:1"), gomock.Any()).After(firstInspect).
		Return("api-id", nil)
	secondInspect := manager.EXPECT().Inspect(gomock.Any(), "demo_worker").After(firstCreate).
		Return(dockerswarm.Service{}, swarm.ErrServiceNotFound)
	manager.EXPECT().Create(gomock.Any(), serviceSpec("worker", "alpine:3"), gomock.Any()).After(secondInspect).
		Return("worker-id", nil)

	changed, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{
		serviceSpec("worker", "alpine:3"), serviceSpec("api", "nginx:1"),
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"api", "worker"}, changed)
}

func TestApplySkipsSemanticallyUnchangedResolvedImage(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	desired := serviceSpec("api", "registry.example/api:latest")
	current := desired
	current.TaskTemplate.ContainerSpec.Image = "registry.example/api@sha256:abcdef"
	normalizeDockerDefaults(&current)
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{
		ID: "api-id", Spec: current, Meta: dockerswarm.Meta{Version: dockerswarm.Version{Index: 7}},
	}, nil)

	changed, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired})
	require.NoError(t, err)
	assert.Empty(t, changed)
}

func TestApplySkipsDaemonRuntimeAndIsolationDefaults(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	desired := serviceSpec("api", "busybox:1.36")
	current := cloneSpec(desired)
	current.TaskTemplate.Runtime = dockerswarm.RuntimeContainer
	current.TaskTemplate.ContainerSpec.Isolation = container.IsolationDefault
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(service("api-id", 7, current), nil)
	changed, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired})
	require.NoError(t, err)
	assert.Empty(t, changed) // Any update would be an unexpected gomock call.
	current.TaskTemplate.ContainerSpec.Isolation = container.IsolationHyperV
	assert.False(t, serviceSpecsEqual(current, desired), "non-default isolation must remain a real change")
	assert.Empty(t, desired.TaskTemplate.Runtime, "comparison must not mutate desired state")
	assert.Empty(t, desired.TaskTemplate.ContainerSpec.Isolation)
}

func TestApplyUpdatesChangedServiceAndPreservesMounts(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	desired := serviceSpec("api", "nginx:2")
	desired.TaskTemplate.ContainerSpec.Mounts = []mount.Mount{{
		Type: mount.TypeVolume, Source: "demo_data", Target: "/data", ReadOnly: true,
		VolumeOptions: &mount.VolumeOptions{NoCopy: true, Subpath: "tenant"},
	}}
	current := serviceSpec("api", "nginx:1")
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{
		ID: "api-id", Spec: current, Meta: dockerswarm.Meta{Version: dockerswarm.Version{Index: 4}},
	}, nil)
	manager.EXPECT().Update(gomock.Any(), "api-id", dockerswarm.Version{Index: 4}, desired, gomock.Any()).
		DoAndReturn(func(_ any, _ string, _ dockerswarm.Version, actual dockerswarm.ServiceSpec, _ dockerswarm.ServiceUpdateOptions) error {
			assert.Equal(t, desired.TaskTemplate.ContainerSpec.Mounts, actual.TaskTemplate.ContainerSpec.Mounts)
			return nil
		})

	changed, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired})
	require.NoError(t, err)
	assert.Equal(t, []string{"api"}, changed)
}

func TestApplyRetriesBoundedVersionConflicts(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	desired := serviceSpec("api", "nginx:2")
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(service("api-id", 1, serviceSpec("api", "nginx:1")), nil)
	for version := uint64(1); version <= serviceUpdateAttempts; version++ {
		manager.EXPECT().Update(gomock.Any(), "api-id", dockerswarm.Version{Index: version}, desired, gomock.Any()).
			Return(swarm.ErrServiceConflict)
		manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(
			service("api-id", version+1, serviceSpec("api", "nginx:1")), nil,
		)
	}

	changed, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired})
	require.Error(t, err)
	assert.Empty(t, changed)
	assert.ErrorContains(t, err, "version conflict after 3 attempts")
}

func TestApplyRejectsForeignServiceAndPreservesPartialApplication(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{}, swarm.ErrServiceNotFound)
	manager.EXPECT().Create(gomock.Any(), serviceSpec("api", "nginx:1"), gomock.Any()).Return("api-id", nil)
	foreign := service("worker-id", 1, serviceSpec("worker", "alpine:3"))
	foreign.Spec.Labels[labelsdict.StackNamespace] = "other"
	manager.EXPECT().Inspect(gomock.Any(), "demo_worker").Return(foreign, nil)

	changed, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{
		serviceSpec("api", "nginx:1"), serviceSpec("worker", "alpine:3"),
	})
	require.Error(t, err)
	assert.Equal(t, []string{"api"}, changed)
	assert.ErrorContains(t, err, "not owned")
}

func TestApplyPassesRegistryAuthenticationWithoutExposingItInSpec(t *testing.T) {
	rawAuth := base64.StdEncoding.EncodeToString([]byte("robot:secret"))
	t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"registry.example":{"auth":"`+rawAuth+`"}}}`)
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	desired := serviceSpec("api", "registry.example/team/api:1")
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{}, swarm.ErrServiceNotFound)
	manager.EXPECT().Create(gomock.Any(), desired, gomock.Any()).DoAndReturn(
		func(_ any, _ dockerswarm.ServiceSpec, options dockerswarm.ServiceCreateOptions) (string, error) {
			auth, err := dockerregistry.DecodeAuthConfig(options.EncodedRegistryAuth)
			require.NoError(t, err)
			assert.Equal(t, "robot", auth.Username)
			assert.Equal(t, "secret", auth.Password)
			assert.True(t, options.QueryRegistry)
			return "api-id", nil
		})

	_, err := New(manager, registry.NewAuthManager()).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired})
	require.NoError(t, err)
}

func TestApplyPassesRegistryAuthenticationOnUpdate(t *testing.T) {
	rawAuth := base64.StdEncoding.EncodeToString([]byte("robot:secret"))
	t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"registry.example":{"auth":"`+rawAuth+`"}}}`)
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	desired := serviceSpec("api", "registry.example/team/api:2")
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(
		service("api-id", 7, serviceSpec("api", "registry.example/team/api:1")), nil,
	)
	manager.EXPECT().Update(
		gomock.Any(), "api-id", dockerswarm.Version{Index: 7}, desired, gomock.Any(),
	).DoAndReturn(func(
		_ any,
		_ string,
		_ dockerswarm.Version,
		_ dockerswarm.ServiceSpec,
		options dockerswarm.ServiceUpdateOptions,
	) error {
		auth, err := dockerregistry.DecodeAuthConfig(options.EncodedRegistryAuth)
		require.NoError(t, err)
		assert.Equal(t, "robot", auth.Username)
		assert.Equal(t, "secret", auth.Password)
		assert.Equal(t, dockerswarm.RegistryAuthFromSpec, options.RegistryAuthFrom)
		assert.True(t, options.QueryRegistry)
		return nil
	})

	_, err := New(manager, registry.NewAuthManager()).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired})
	require.NoError(t, err)
}

func TestApplyReturnsDockerAPIFailureWithServiceOperation(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	desired := serviceSpec("api", "nginx:1")
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{}, swarm.ErrServiceNotFound)
	manager.EXPECT().Create(gomock.Any(), desired, gomock.Any()).Return("", errors.New("daemon unavailable"))

	_, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired})
	require.Error(t, err)
	assert.ErrorContains(t, err, "create service api")
}

func serviceSpec(name, image string) dockerswarm.ServiceSpec {
	return dockerswarm.ServiceSpec{
		Annotations: dockerswarm.Annotations{Name: "demo_" + name, Labels: map[string]string{
			labelsdict.StackNamespace: "demo", stackImageLabel: image,
		}},
		TaskTemplate: dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{Image: image}},
		Mode:         dockerswarm.ServiceMode{Replicated: &dockerswarm.ReplicatedService{}},
		EndpointSpec: &dockerswarm.EndpointSpec{},
	}
}

func service(id string, version uint64, spec dockerswarm.ServiceSpec) dockerswarm.Service {
	return dockerswarm.Service{ID: id, Meta: dockerswarm.Meta{Version: dockerswarm.Version{Index: version}}, Spec: spec}
}
