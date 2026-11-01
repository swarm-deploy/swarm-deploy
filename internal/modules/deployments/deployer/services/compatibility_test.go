package services

import (
	"fmt"
	"strings"
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestImageResolutionEquivalence(t *testing.T) {
	digest1 := "sha256:" + strings.Repeat("a", 64)
	digest2 := "sha256:" + strings.Repeat("b", 64)
	for _, tc := range []struct {
		name, desired, actual string
		equal                 bool
	}{
		{"matching", "alpine:3", "alpine:3", true},
		{"normalized", "alpine", "docker.io/library/alpine:latest", true},
		{"resolved", "alpine:3", "alpine:3@" + digest1, true},
		{"repository drift", "alpine:3", "other:3@" + digest1, false},
		{"tag drift", "alpine:3", "alpine:wrong@" + digest1, false},
		{"matching pinned", "alpine@" + digest1, "alpine@" + digest1, true},
		{"digest drift", "alpine@" + digest1, "alpine@" + digest2, false},
		{"pinned repository drift", "alpine@" + digest1, "other@" + digest1, false},
		{"lost digest", "alpine@" + digest1, "alpine:latest", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			desired := serviceSpec("api", tc.desired)
			current := cloneSpec(desired) // Always keep identical stack image labels.
			current.TaskTemplate.ContainerSpec.Image = tc.actual
			assert.Equal(t, tc.equal, serviceSpecsEqual(current, desired))
			assert.Equal(t, tc.desired, desired.TaskTemplate.ContainerSpec.Image)
		})
	}
}

func TestForceUpdateAndResolvedImageSurviveUnrelatedUpdateAndConflict(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint(conflict), func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := serviceSpec("api", "alpine:3")
			current := cloneSpec(desired)
			current.TaskTemplate.ForceUpdate = 7
			current.TaskTemplate.ContainerSpec.Image += "@sha256:" + strings.Repeat("a", 64)
			current.TaskTemplate.Placement = &dockerswarm.Placement{Platforms: []dockerswarm.Platform{{OS: "linux", Architecture: "amd64"}}}
			assert.True(t, serviceSpecsEqual(current, desired), "force update alone must not cause mutation")
			desired.TaskTemplate.ContainerSpec.Env = []string{"CHANGED=yes"}
			manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(service("api-id", 1, current), nil)
			update := func(version, force uint64, result error) {
				manager.EXPECT().Update(gomock.Any(), "api-id", dockerswarm.Version{Index: version}, gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ any, _ string, _ dockerswarm.Version, spec dockerswarm.ServiceSpec, options dockerswarm.ServiceUpdateOptions) error {
						assert.Equal(t, force, spec.TaskTemplate.ForceUpdate)
						assert.Equal(t, current.TaskTemplate.ContainerSpec.Image, spec.TaskTemplate.ContainerSpec.Image)
						assert.Equal(t, current.TaskTemplate.Placement.Platforms, spec.TaskTemplate.Placement.Platforms)
						assert.False(t, options.QueryRegistry, "unchanged tag must not be resolved on env update")
						return result
					})
			}
			if conflict {
				update(1, 7, swarm.ErrServiceConflict)
				newer := cloneSpec(current)
				newer.TaskTemplate.ForceUpdate = 8
				manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(service("api-id", 2, newer), nil)
				update(2, 8, nil)
			} else {
				update(1, 7, nil)
			}
			changed, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired})
			require.NoError(t, err)
			assert.Equal(t, []string{"api"}, changed)
			assert.Zero(t, desired.TaskTemplate.ForceUpdate)
		})
	}
}

func TestRegistryResolutionDecisions(t *testing.T) {
	for _, tc := range []struct {
		name, before, after string
		query               bool
	}{
		{"new tag", "alpine:3", "alpine:4", true},
		{"drift with unchanged label", "other:3", "alpine:3", true},
		{"pinned", "alpine:3", "alpine@sha256:" + strings.Repeat("a", 64), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := serviceSpec("api", tc.after)
			current := cloneSpec(desired)
			current.TaskTemplate.ContainerSpec.Image = tc.before
			manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(service("id", 1, current), nil)
			manager.EXPECT().Update(gomock.Any(), "id", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ any, _ string, _ dockerswarm.Version, spec dockerswarm.ServiceSpec, options dockerswarm.ServiceUpdateOptions) error {
					assert.Equal(t, tc.query, options.QueryRegistry)
					assert.Equal(t, tc.after, spec.TaskTemplate.ContainerSpec.Image)
					return nil
				})
			_, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired})
			require.NoError(t, err)
		})
	}
}

func TestExternalForceUpdateDoesNotUpdateUnchangedService(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	desired := serviceSpec("api", "alpine:3")
	current := cloneSpec(desired)
	current.TaskTemplate.ForceUpdate = 42
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(service("id", 7, current), nil)
	changed, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired})
	require.NoError(t, err)
	assert.Empty(t, changed)
}
