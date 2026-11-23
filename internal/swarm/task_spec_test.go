package swarm

import (
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskSpecEquivalentDockerRepresentations(t *testing.T) {
	stopGrace := 10 * time.Second
	restartDelay := 5 * time.Second
	maxAttempts := uint64(0)
	template := dockerswarm.TaskSpec{
		Runtime: dockerswarm.RuntimeContainer,
		ContainerSpec: &dockerswarm.ContainerSpec{
			Image: "alpine:3", Isolation: container.IsolationDefault, StopGracePeriod: &stopGrace,
			DNSConfig: &dockerswarm.DNSConfig{},
		},
		Resources: &dockerswarm.ResourceRequirements{},
		RestartPolicy: &dockerswarm.RestartPolicy{
			Condition: dockerswarm.RestartPolicyConditionAny, Delay: &restartDelay, MaxAttempts: &maxAttempts,
		},
		Placement: &dockerswarm.Placement{},
	}

	comparison := NewTaskSpec(dockerswarm.TaskSpec{
		ContainerSpec: &dockerswarm.ContainerSpec{Image: "alpine:3"},
	}).CompareServiceTemplate(template)

	assert.True(t, comparison.Equal())
	assert.False(t, comparison.OlderForceUpdateOnly())
	assert.False(t, comparison.PlacementOnly())
	assert.Empty(t, comparison.MismatchPaths())
}

func TestTaskSpecMeaningfulDifferencesRemainStrict(t *testing.T) {
	base := dockerswarm.TaskSpec{
		ForceUpdate: 7,
		ContainerSpec: &dockerswarm.ContainerSpec{
			Image: "alpine:3", Command: []string{"sh"}, Args: []string{"-c", "true"},
			Env:    []string{"TOKEN=secret-value"},
			Labels: map[string]string{"registry.password": "secret-value"},
			Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: "data", Target: "/data"}},
		},
		Networks:      []dockerswarm.NetworkAttachmentConfig{{Target: "network-id"}},
		RestartPolicy: &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone},
		Placement:     &dockerswarm.Placement{Constraints: []string{"node.role == worker"}},
	}

	for _, tc := range []struct {
		name                 string
		path                 string
		edit                 func(*dockerswarm.TaskSpec)
		olderForceUpdateOnly bool
		placementOnly        bool
	}{
		{name: "newer force update", path: "TaskTemplate.ForceUpdate", edit: func(spec *dockerswarm.TaskSpec) { spec.ForceUpdate++ }},
		{name: "older force update", path: "TaskTemplate.ForceUpdate", edit: func(spec *dockerswarm.TaskSpec) { spec.ForceUpdate-- },
			olderForceUpdateOnly: true},
		{name: "image", path: "TaskTemplate.ContainerSpec.Image", edit: func(spec *dockerswarm.TaskSpec) { spec.ContainerSpec.Image = "alpine:4" }},
		{name: "command", path: "TaskTemplate.ContainerSpec.Command", edit: func(spec *dockerswarm.TaskSpec) { spec.ContainerSpec.Command = []string{"bash"} }},
		{name: "environment", path: "TaskTemplate.ContainerSpec.Env", edit: func(spec *dockerswarm.TaskSpec) { spec.ContainerSpec.Env = []string{"TOKEN=other-secret"} }},
		{name: "labels", path: "TaskTemplate.ContainerSpec.Labels", edit: func(spec *dockerswarm.TaskSpec) { spec.ContainerSpec.Labels["registry.password"] = "other-secret" }},
		{name: "mounts", path: "TaskTemplate.ContainerSpec.Mounts", edit: func(spec *dockerswarm.TaskSpec) { spec.ContainerSpec.Mounts[0].Target = "/other" }},
		{name: "networks", path: "TaskTemplate.Networks", edit: func(spec *dockerswarm.TaskSpec) { spec.Networks[0].Target = "other-network" }},
		{name: "restart policy", path: "TaskTemplate.RestartPolicy.Condition", edit: func(spec *dockerswarm.TaskSpec) { spec.RestartPolicy.Condition = dockerswarm.RestartPolicyConditionAny }},
		{name: "restart delay", path: "TaskTemplate.RestartPolicy.Delay", edit: func(spec *dockerswarm.TaskSpec) { value := 6 * time.Second; spec.RestartPolicy.Delay = &value }},
		{name: "restart attempts", path: "TaskTemplate.RestartPolicy.MaxAttempts", edit: func(spec *dockerswarm.TaskSpec) { value := uint64(1); spec.RestartPolicy.MaxAttempts = &value }},
		{name: "placement", path: "TaskTemplate.Placement.Constraints", edit: func(spec *dockerswarm.TaskSpec) {
			spec.Placement = &dockerswarm.Placement{Constraints: []string{"node.role == manager"}}
		}, placementOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual := cloneDockerTaskSpec(base)
			tc.edit(&actual)
			comparison := NewTaskSpec(actual).CompareServiceTemplate(base)
			require.False(t, comparison.Equal())
			assert.Equal(t, tc.olderForceUpdateOnly, comparison.OlderForceUpdateOnly())
			assert.Equal(t, tc.placementOnly, comparison.PlacementOnly())
			assert.Contains(t, comparison.MismatchPaths(), tc.path)
			diagnostic := strings.Join(comparison.MismatchPaths(), ",")
			assert.NotContains(t, diagnostic, "secret-value")
			assert.NotContains(t, diagnostic, "other-secret")
			assert.NotContains(t, diagnostic, "registry.password")
		})
	}
}

func TestTaskSpecUnprovenNilAndEmptyCollectionsRemainStrict(t *testing.T) {
	template := dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{Image: "alpine:3"}}
	actual := template
	containerSpec := *template.ContainerSpec
	containerSpec.Env = []string{}
	actual.ContainerSpec = &containerSpec

	comparison := NewTaskSpec(actual).CompareServiceTemplate(template)

	require.False(t, comparison.Equal())
	assert.Equal(t, []string{"TaskTemplate.ContainerSpec.Env"}, comparison.MismatchPaths())
}

func cloneDockerTaskSpec(spec dockerswarm.TaskSpec) dockerswarm.TaskSpec {
	clone := spec
	if spec.ContainerSpec != nil {
		containerSpec := *spec.ContainerSpec
		containerSpec.Command = append([]string(nil), spec.ContainerSpec.Command...)
		containerSpec.Args = append([]string(nil), spec.ContainerSpec.Args...)
		containerSpec.Env = append([]string(nil), spec.ContainerSpec.Env...)
		containerSpec.Labels = maps.Clone(spec.ContainerSpec.Labels)
		containerSpec.Mounts = append([]mount.Mount(nil), spec.ContainerSpec.Mounts...)
		clone.ContainerSpec = &containerSpec
	}
	clone.Networks = append([]dockerswarm.NetworkAttachmentConfig(nil), spec.Networks...)
	if spec.RestartPolicy != nil {
		restartPolicy := *spec.RestartPolicy
		clone.RestartPolicy = &restartPolicy
	}
	return clone
}
