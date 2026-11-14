package services

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	dockerregistry "github.com/docker/docker/api/types/registry"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestCredentialsRefreshUnchangedServicesWithoutChangingTasks(t *testing.T) {
	for _, image := range []string{"registry.example/api:1", "registry.example/api:1@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		t.Run(image, func(t *testing.T) {
			t.Setenv("DOCKER_AUTH_CONFIG", "")
			dir := t.TempDir()
			t.Setenv("DOCKER_CONFIG", dir)
			writeRegistryAuth(t, dir, "first-token")
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			applier := New(manager, registry.NewAuthManager())
			specs := []dockerswarm.ServiceSpec{serviceSpec("api", image), serviceSpec("worker", image)}
			for _, spec := range specs {
				spec.TaskTemplate.ForceUpdate = 9
				manager.EXPECT().Inspect(gomock.Any(), spec.Name).Return(service(spec.Name, 7, spec), nil).AnyTimes()
			}
			for _, token := range []string{"first-token", "rotated-token", ""} {
				writeRegistryAuth(t, dir, token)
				for _, desired := range specs {
					manager.EXPECT().Update(gomock.Any(), desired.Name, dockerswarm.Version{Index: 7}, gomock.Any(), gomock.Any()).DoAndReturn(
						func(_ any, _ string, _ dockerswarm.Version, applied dockerswarm.ServiceSpec, options dockerswarm.ServiceUpdateOptions) error {
							assert.Equal(t, uint64(9), applied.TaskTemplate.ForceUpdate)
							assert.Equal(t, desired.TaskTemplate.ContainerSpec, applied.TaskTemplate.ContainerSpec)
							assert.False(t, options.QueryRegistry, "auth-only refresh must not resolve tags or change tasks")
							require.NotEmpty(t, options.EncodedRegistryAuth, "credential removal needs an explicit anonymous payload")
							auth, err := dockerregistry.DecodeAuthConfig(options.EncodedRegistryAuth)
							require.NoError(t, err)
							assert.Equal(t, token, auth.Password)
							return nil
						})
				}
				result, err := applier.Apply(t.Context(), "demo", specs, ApplyOptions{})
				require.NoError(t, err)
				assert.Equal(t, []string{"api", "worker"}, result.ChangedServices)
				result, err = applier.Apply(t.Context(), "demo", specs, ApplyOptions{})
				require.NoError(t, err)
				assert.Empty(t, result.ChangedServices, "unchanged credentials must not trigger repeated writes")
			}
		})
	}
}

func TestAuthRefreshSurvivesConflictsFailuresAndProcessRestart(t *testing.T) {
	for _, failure := range []string{"conflict", "api failure", "restart"} {
		t.Run(failure, func(t *testing.T) {
			t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"registry.example":{"username":"robot","password":"secret-token"}}}`)
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := serviceSpec("api", "registry.example/api:1")
			current := service("api-id", 1, desired)
			manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(current, nil).AnyTimes()
			applier := New(manager, registry.NewAuthManager())
			switch failure {
			case "conflict":
				manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, desired, gomock.Any()).Return(swarm.ErrServiceConflict)
			case "api failure":
				manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, desired, gomock.Any()).Return(errors.New("update failed"))
				_, err := applier.Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
				require.ErrorContains(t, err, "update failed")
			case "restart":
				manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, desired, gomock.Any()).Return(nil)
				_, err := applier.Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
				require.NoError(t, err)
				applier = New(manager, registry.NewAuthManager())
			}
			manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, desired, gomock.Any()).Return(nil)
			_, err := applier.Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
			require.NoError(t, err)
			result, err := applier.Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
			require.NoError(t, err)
			assert.Empty(t, result.ChangedServices)
		})
	}
}

func TestUnchangedServiceAuthLookupFailureIsNotIgnored(t *testing.T) {
	t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":`)
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	desired := serviceSpec("api", "registry.example/api:1")
	manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(service("api-id", 1, desired), nil)
	_, err := New(manager, registry.NewAuthManager()).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
	require.ErrorContains(t, err, "registry authentication")
}

func writeRegistryAuth(t *testing.T, dir, token string) {
	t.Helper()
	content := `{"auths":{}}`
	if token != "" {
		content = `{"auths":{"registry.example":{"username":"robot","password":"` + token + `"}}}`
	}
	temporary := filepath.Join(dir, "config.json.new")
	require.NoError(t, os.WriteFile(temporary, []byte(content), 0o600))
	require.NoError(t, os.Rename(temporary, filepath.Join(dir, "config.json")))
}

func TestCredentialRefreshDoesNotStartUnrequestedExecutions(t *testing.T) {
	for _, mode := range []string{"replicated-job", "global-job", "none", "on-failure", "finite retries", "global none"} {
		for _, known := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/unknown", true: "/rotated"}[known], func(t *testing.T) {
				t.Setenv("DOCKER_AUTH_CONFIG", "")
				dir := t.TempDir()
				t.Setenv("DOCKER_CONFIG", dir)
				writeRegistryAuth(t, dir, "old-token")
				manager := swarm.NewMockServiceManager(gomock.NewController(t))
				applier := New(manager, registry.NewAuthManager())
				desired := authExecutionSpec(mode)
				if known {
					manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(dockerswarm.Service{}, swarm.ErrServiceNotFound)
					manager.EXPECT().Create(gomock.Any(), desired, gomock.Any()).Return("api-id", nil)
					_, err := applier.Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
					require.NoError(t, err)
				}
				writeRegistryAuth(t, dir, "new-token")
				current := service("api-id", 7, cloneSpec(desired))
				// A nil JobStatus also covers the window before native-job initialization.
				manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(current, nil).AnyTimes()
				for range 2 {
					result, err := applier.Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
					require.NoError(t, err)
					assert.Empty(t, result.ChangedServices, "credential rotation alone must not create an execution")
				}
				// An intentional specification update carries the deferred latest credentials.
				desired.Labels["release"] = "new"
				manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ any, _ string, _ dockerswarm.Version, _ dockerswarm.ServiceSpec, options dockerswarm.ServiceUpdateOptions) error {
						auth, err := dockerregistry.DecodeAuthConfig(options.EncodedRegistryAuth)
						require.NoError(t, err)
						assert.Equal(t, "new-token", auth.Password)
						return nil
					})
				result, err := applier.Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
				require.NoError(t, err)
				assert.Equal(t, []string{"api"}, result.ChangedServices)
			})
		}
	}
}

func TestDeferredCredentialRefreshDoesNotRewriteConcurrentlyAcceptedSpec(t *testing.T) {
	for _, mode := range []string{"replicated-job", "global-job", "none", "on-failure", "finite retries"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"registry.example":{"username":"robot","password":"new-token"}}}`)
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := authExecutionSpec(mode)
			current := service("api-id", 7, cloneSpec(desired))
			desired.Labels["release"] = "new"
			newer := service(current.ID, 8, cloneSpec(desired))
			gomock.InOrder(
				manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(current, nil),
				manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, gomock.Any(), gomock.Any()).Return(swarm.ErrServiceConflict),
				manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(newer, nil),
			)
			_, err := New(manager, registry.NewAuthManager()).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
			require.NoError(t, err, "a second auth-only update could start an unintended execution")
		})
	}
}

func TestZeroReplicaJobLikeServiceCanRefreshCredentials(t *testing.T) {
	for _, mode := range []string{"none", "on-failure", "finite retries"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"registry.example":{"username":"robot","password":"new-token"}}}`)
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := authExecutionSpec(mode)
			desired.Mode.Replicated.Replicas = new(uint64)
			current := service("api-id", 7, desired)
			manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(current, nil)
			manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, current.Spec, gomock.Any()).DoAndReturn(
				func(_ any, _ string, _ dockerswarm.Version, _ dockerswarm.ServiceSpec, options dockerswarm.ServiceUpdateOptions) error {
					assert.False(t, options.QueryRegistry)
					assert.NotEmpty(t, options.EncodedRegistryAuth)
					return nil
				})
			result, err := New(manager, registry.NewAuthManager()).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
			require.NoError(t, err)
			assert.Equal(t, []string{"api"}, result.ChangedServices)
		})
	}
}

func authExecutionSpec(mode string) dockerswarm.ServiceSpec {
	spec := serviceSpec("api", "registry.example/api:1")
	switch mode {
	case "replicated-job":
		spec.Mode = dockerswarm.ServiceMode{ReplicatedJob: &dockerswarm.ReplicatedJob{}}
	case "global-job":
		spec.Mode = dockerswarm.ServiceMode{GlobalJob: &dockerswarm.GlobalJob{}}
	case "none", "global none":
		spec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
		if mode == "global none" {
			spec.Mode = dockerswarm.ServiceMode{Global: &dockerswarm.GlobalService{}}
		}
	case "on-failure":
		spec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionOnFailure}
	case "finite retries":
		spec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionAny, MaxAttempts: new(uint64(3))}
	}
	return spec
}

func TestExplicitFailedExecutionRetrySendsDeferredCredentials(t *testing.T) {
	for _, mode := range []string{"replicated-job", "global-job", "none"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"registry.example":{"username":"robot","password":"new-token"}}}`)
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := authExecutionSpec(mode)
			desired.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
			current := service("api-id", 7, cloneSpec(desired))
			failed := jobTask("failed", swarm.TaskStateFailed, 9, time.Now())
			failed.ExitCode = new(1)
			if mode == "none" {
				failed.Slot = 1
			} else {
				current.JobStatus = &dockerswarm.JobStatus{JobIteration: dockerswarm.Version{Index: 9}}
			}
			manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(current, nil)
			manager.EXPECT().ListTasks(gomock.Any(), current.ID).Return([]swarm.ServiceTask{failed}, nil).Times(2)
			manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ any, _ string, _ dockerswarm.Version, applied dockerswarm.ServiceSpec, options dockerswarm.ServiceUpdateOptions) error {
					assert.Equal(t, uint64(1), applied.TaskTemplate.ForceUpdate)
					auth, err := dockerregistry.DecodeAuthConfig(options.EncodedRegistryAuth)
					require.NoError(t, err)
					assert.Equal(t, "new-token", auth.Password)
					return nil
				})
			result, err := New(manager, registry.NewAuthManager()).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{RetryFailedUpdates: true})
			require.NoError(t, err)
			assert.Equal(t, []string{"api"}, result.ChangedServices)
		})
	}
}
