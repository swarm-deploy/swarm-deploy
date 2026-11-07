package services

import (
	"encoding/base64"
	"strings"
	"testing"

	dockerregistry "github.com/docker/docker/api/types/registry"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestDigestPinnedCreateQueriesRegistryWithCredentials(t *testing.T) {
	for _, repository := range []string{"registry.example/team/api", "registry.example/team/api:1"} {
		t.Run(repository, func(t *testing.T) {
			encoded := base64.StdEncoding.EncodeToString([]byte("robot:secret"))
			t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"registry.example":{"auth":"`+encoded+`"}}}`)
			t.Setenv("DOCKER_CONFIG", t.TempDir())
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := serviceSpec("api", repository+"@sha256:"+strings.Repeat("a", 64))
			manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(dockerswarm.Service{}, swarm.ErrServiceNotFound)
			manager.EXPECT().Create(gomock.Any(), desired, gomock.Any()).DoAndReturn(
				func(_ any, _ dockerswarm.ServiceSpec, options dockerswarm.ServiceCreateOptions) (string, error) {
					assert.True(t, options.QueryRegistry, "digest pinning does not supply platform metadata")
					auth, err := dockerregistry.DecodeAuthConfig(options.EncodedRegistryAuth)
					require.NoError(t, err)
					assert.Equal(t, "robot", auth.Username)
					assert.Equal(t, "secret", auth.Password)
					return "api-id", nil
				})
			result, err := New(manager, registry.NewAuthManager()).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
			require.NoError(t, err)
			assert.Equal(t, []string{"api"}, result.ChangedServices)
		})
	}
}

func TestDigestPinnedUpdateRecomputesRegistryDecisionAfterConflict(t *testing.T) {
	pinned := "alpine@sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct {
		name                 string
		before               string
		conflict             bool
		concurrentlyResolved bool
	}{
		{name: "tag to digest", before: "alpine:3"},
		{name: "digest to digest", before: "alpine@sha256:" + strings.Repeat("b", 64)},
		{name: "conflicting update still needs resolution", before: "alpine:3", conflict: true},
		{name: "conflict supplies compatible metadata", before: "alpine:3", conflict: true, concurrentlyResolved: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := serviceSpec("api", pinned)
			desired.TaskTemplate.ContainerSpec.Env = []string{"CHANGED=yes"}
			desired.TaskTemplate.Placement = &dockerswarm.Placement{Constraints: []string{"node.labels.pool==api"}}
			current := service("api-id", 1, serviceSpec("api", tc.before))
			current.Spec.TaskTemplate.Placement = &dockerswarm.Placement{Platforms: []dockerswarm.Platform{{OS: "linux", Architecture: "amd64"}}}
			manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(current, nil)
			update := func(version uint64, query bool, platforms []dockerswarm.Platform, result error) {
				manager.EXPECT().Update(gomock.Any(), current.ID, dockerswarm.Version{Index: version}, gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ any, _ string, _ dockerswarm.Version, spec dockerswarm.ServiceSpec, options dockerswarm.ServiceUpdateOptions) error {
						assert.Equal(t, query, options.QueryRegistry)
						assert.Equal(t, platforms, spec.TaskTemplate.Placement.Platforms)
						assert.Equal(t, desired.TaskTemplate.Placement.Constraints, spec.TaskTemplate.Placement.Constraints)
						assert.Equal(t, pinned, spec.TaskTemplate.ContainerSpec.Image)
						assert.Equal(t, pinned, spec.Labels[stackImageLabel], "desired image baseline must remain unchanged")
						assert.Equal(t, dockerswarm.RegistryAuthFromSpec, options.RegistryAuthFrom)
						return result
					})
			}
			if tc.conflict {
				update(1, true, nil, swarm.ErrServiceConflict)
				newer := current
				newer.Version.Index++
				newer.Spec = cloneSpec(current.Spec)
				var platforms []dockerswarm.Platform
				if tc.concurrentlyResolved {
					newer.Spec = serviceSpec("api", pinned)
					platforms = []dockerswarm.Platform{{OS: "linux", Architecture: "arm64"}}
					newer.Spec.TaskTemplate.Placement = &dockerswarm.Placement{Platforms: platforms}
				}
				manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(newer, nil)
				update(2, !tc.concurrentlyResolved, platforms, nil)
			} else {
				update(1, true, nil, nil)
			}
			_, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
			require.NoError(t, err)
			assert.Empty(t, desired.TaskTemplate.Placement.Platforms, "preparing writes must not mutate desired state")
		})
	}
}

func TestDigestPinnedMetadataReusedForUnchangedReference(t *testing.T) {
	pinned := "alpine@sha256:" + strings.Repeat("a", 64)
	for _, change := range []string{"none", "environment", "placement"} {
		t.Run(change, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := serviceSpec("api", pinned)
			current := cloneSpec(desired)
			current.TaskTemplate.Placement = &dockerswarm.Placement{Platforms: []dockerswarm.Platform{
				{OS: "linux", Architecture: "amd64"}, {OS: "linux", Architecture: "arm64"},
			}}
			manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(service("api-id", 7, current), nil)
			switch change {
			case "environment":
				desired.TaskTemplate.ContainerSpec.Env = []string{"CHANGED=yes"}
			case "placement":
				desired.TaskTemplate.Placement = &dockerswarm.Placement{Constraints: []string{"node.labels.pool==api"}}
			}
			if change != "none" {
				manager.EXPECT().Update(gomock.Any(), "api-id", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ any, _ string, _ dockerswarm.Version, spec dockerswarm.ServiceSpec, options dockerswarm.ServiceUpdateOptions) error {
						assert.False(t, options.QueryRegistry)
						assert.Equal(t, current.TaskTemplate.Placement.Platforms, spec.TaskTemplate.Placement.Platforms)
						assert.Equal(t, pinned, spec.TaskTemplate.ContainerSpec.Image)
						return nil
					})
			}
			result, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
			require.NoError(t, err)
			assert.Equal(t, change != "none", len(result.ChangedServices) > 0)
		})
	}
}

func TestMissingDigestRetriesResolutionOnApplicableUpdate(t *testing.T) {
	for _, change := range []string{"none", "environment", "service labels"} {
		t.Run(change, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := serviceSpec("api", "registry.example/api:1")
			current := cloneSpec(desired) // A prior registry warning left only repo:tag.
			manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(service("api-id", 7, current), nil)
			switch change {
			case "environment":
				desired.TaskTemplate.ContainerSpec.Env = []string{"NEW=yes"}
			case "service labels":
				desired.Labels["release"] = "new"
			}
			if change != "none" {
				manager.EXPECT().Update(gomock.Any(), "api-id", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ any, _ string, _ dockerswarm.Version, _ dockerswarm.ServiceSpec, options dockerswarm.ServiceUpdateOptions) error {
						assert.True(t, options.QueryRegistry)
						return nil
					})
			}
			result, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
			require.NoError(t, err)
			assert.Equal(t, change != "none", len(result.ChangedServices) > 0)
		})
	}
}

func TestMatchingTagWithoutDigestIsNotResolutionEvidence(t *testing.T) {
	assert.False(t, resolvedImageMatches("registry.example/api:1", "registry.example/api:1"))
	assert.False(t, resolvedImageMatches("docker.io/library/alpine:latest", "alpine"))
}

func TestSDKTaggedUnresolvedReferencePreservesNoopAndRegistryDecisions(t *testing.T) {
	for _, change := range []string{"none", "metadata", "credentials"} {
		t.Run(change, func(t *testing.T) {
			t.Setenv("DOCKER_AUTH_CONFIG", "")
			t.Setenv("DOCKER_CONFIG", t.TempDir())
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := serviceSpec("api", "registry.example/api")
			current := cloneSpec(desired)
			// The SDK adds :latest before resolution, including when lookup warns.
			current.TaskTemplate.ContainerSpec.Image += ":latest"
			assert.True(t, SpecsEqual(current, desired))
			manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(service("api-id", 7, current), nil)
			switch change {
			case "metadata":
				desired.Labels["release"] = "new"
			case "credentials":
				t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"registry.example":{"username":"robot","password":"new-token"}}}`)
			}
			if change != "none" {
				manager.EXPECT().Update(gomock.Any(), "api-id", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ any, _ string, _ dockerswarm.Version, applied dockerswarm.ServiceSpec, options dockerswarm.ServiceUpdateOptions) error {
						assert.Equal(t, change == "metadata", options.QueryRegistry)
						if change == "credentials" {
							assert.Equal(t, current, applied, "auth-only writes preserve the SDK-normalized task template")
						}
						return nil
					})
			}
			result, err := New(manager, registry.NewAuthManager()).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
			require.NoError(t, err)
			assert.Equal(t, change != "none", len(result.ChangedServices) > 0)
		})
	}
}
