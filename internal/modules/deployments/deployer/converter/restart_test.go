package converter

import (
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestLegacyRestartEffectiveServiceSpec(t *testing.T) {
	for _, tc := range []struct {
		policy    string
		condition dockerswarm.RestartPolicyCondition
		attempts  *uint64
	}{
		{"", "", nil}, {"no", "", nil},
		{"always", dockerswarm.RestartPolicyConditionAny, nil},
		{"unless-stopped", dockerswarm.RestartPolicyConditionAny, nil},
		{"on-failure", dockerswarm.RestartPolicyConditionOnFailure, new(uint64(0))},
		{"on-failure:5", dockerswarm.RestartPolicyConditionOnFailure, new(uint64(5))},
	} {
		t.Run(tc.policy, func(t *testing.T) {
			c, networks, _, _ := newTestConverter(t)
			desired := parseStack(t, "services: {app: {image: alpine, restart: '"+tc.policy+"'}}")
			require.NoError(t, c.Validate("demo", desired))
			assert.Empty(t, desired.Services[0].IgnoredOptions)
			networks.EXPECT().Get(gomock.Any(), "demo_default").Return(existingNetwork("demo_default"), nil)
			result, err := c.Convert(t.Context(), "demo", desired)
			require.NoError(t, err)
			policy := result.Services[0].TaskTemplate.RestartPolicy
			if tc.condition == "" {
				assert.Nil(t, policy, "CLI leaves no/empty restart to Swarm defaults")
			} else {
				require.NotNil(t, policy)
				assert.Equal(t, tc.condition, policy.Condition)
				assert.Equal(t, tc.attempts, policy.MaxAttempts)
			}
		})
	}
}

func TestLegacyRestartValidationAndPrecedence(t *testing.T) {
	for _, policy := range []string{"invalid", ":5", "on-failure:secret-value", "on-failure:1:2"} {
		t.Run(policy, func(t *testing.T) {
			c, _, _, _ := newTestConverter(t)
			desired := parseStack(t, "services: {app: {image: alpine, restart: '"+policy+"'}}")
			err := c.Validate("demo", desired)
			require.ErrorContains(t, err, "services.app: restart:")
			assert.NotContains(t, err.Error(), "secret-value")
			desired.Services[0].Deploy = parseStack(t, "services: {app: {deploy: {restart_policy: {condition: none}}}}").Services[0].Deploy
			require.NoError(t, c.Validate("demo", desired), "deploy policy takes precedence even over invalid legacy input")
		})
	}
}
