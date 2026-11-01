package services

import (
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestRepeatedApplyIgnoresSetOrderingAndDaemonDefaults(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	desired := serviceSpec("api", "alpine:3")
	desired.EndpointSpec = nil
	desired.TaskTemplate.ContainerSpec.Env = []string{"B=2", "A=1"}
	desired.TaskTemplate.ContainerSpec.CapabilityAdd = []string{"CAP_SYS_ADMIN", "CAP_NET_ADMIN"}
	desired.TaskTemplate.Placement = &dockerswarm.Placement{Constraints: []string{"node.role==worker", "node.labels.zone==a"}}
	desired.TaskTemplate.Networks = []dockerswarm.NetworkAttachmentConfig{
		{Target: "net-b", Aliases: []string{"second", "api"}}, {Target: "net-a"},
	}
	current := cloneSpec(desired)
	normalizeDockerDefaults(&current)
	normalizeServiceSets(&current)
	current.TaskTemplate.ForceUpdate = 7
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(service("api-id", 42, current), nil).Times(3)
	applier := New(manager, &registry.NopAuthManager{})
	original := cloneSpec(desired)
	for range 3 {
		changed, err := applier.Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
		require.NoError(t, err)
		assert.Empty(t, changed.ChangedServices) // An unexpected Update call fails via gomock.
	}
	assert.Equal(t, original, desired, "comparison must not mutate caller-owned slices")
	for _, tc := range []struct {
		name   string
		change func(*dockerswarm.ServiceSpec)
	}{
		{"environment", func(s *dockerswarm.ServiceSpec) { s.TaskTemplate.ContainerSpec.Env[0] = "B=3" }},
		{"network", func(s *dockerswarm.ServiceSpec) { s.TaskTemplate.Networks[0].Target = "other" }},
		{"capability", func(s *dockerswarm.ServiceSpec) { s.TaskTemplate.ContainerSpec.CapabilityAdd[0] = "CAP_CHOWN" }},
		{"constraint", func(s *dockerswarm.ServiceSpec) { s.TaskTemplate.Placement.Constraints[0] = "node.role==manager" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := cloneSpec(desired)
			tc.change(&changed)
			assert.False(t, SpecsEqual(current, changed))
		})
	}
}

func TestComparisonPreservesOrderSensitiveFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values func(*dockerswarm.ContainerSpec, bool)
	}{
		{"command", func(c *dockerswarm.ContainerSpec, reversed bool) {
			c.Command = []string{"one", "two"}
			if reversed {
				c.Command = []string{"two", "one"}
			}
		}},
		{"duplicate environment", func(c *dockerswarm.ContainerSpec, reversed bool) {
			c.Env = []string{"A=1", "A=2"}
			if reversed {
				c.Env = []string{"A=2", "A=1"}
			}
		}},
		{"DNS", func(c *dockerswarm.ContainerSpec, reversed bool) {
			c.DNSConfig = &dockerswarm.DNSConfig{Nameservers: []string{"1.1.1.1", "8.8.8.8"}}
			if reversed {
				c.DNSConfig.Nameservers = []string{"8.8.8.8", "1.1.1.1"}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := serviceSpec("api", "alpine:3"), serviceSpec("api", "alpine:3")
			tc.values(before.TaskTemplate.ContainerSpec, false)
			tc.values(after.TaskTemplate.ContainerSpec, true)
			assert.False(t, SpecsEqual(before, after))
		})
	}
}
