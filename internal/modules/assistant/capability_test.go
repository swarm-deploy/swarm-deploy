package assistant

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOperationalToolCapabilitiesAndDefaultRoutes(t *testing.T) {
	serviceRuntimeProfile, ok := capabilityProfile(CapabilityServiceRuntime)
	require.True(t, ok)
	assert.Contains(t, serviceRuntimeProfile.Tools, "service_task_list")

	deploymentProfile, ok := capabilityProfile(CapabilityDeploymentHistory)
	require.True(t, ok)
	assert.Contains(t, deploymentProfile.Tools, "deployment_list")
	assert.Contains(t, deploymentProfile.Tools, "deployment_get")
	assert.Contains(t, deploymentProfile.Tools, "history_event_list")

	alertProfile, ok := capabilityProfile(CapabilityAlertManagement)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"alert_list", "alert_get"}, alertProfile.Tools)

	testCases := []struct {
		name         string
		route        Route
		capabilities []Capability
	}{
		{name: "deployments", route: RouteDeployments,
			capabilities: []Capability{CapabilityDeploymentHistory, CapabilityAlertManagement}},
		{name: "diagnostics", route: RouteDiagnostics,
			capabilities: []Capability{CapabilityDeploymentHistory, CapabilityAlertManagement}},
		{name: "cluster", route: RouteCluster,
			capabilities: []Capability{CapabilityAlertManagement}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := defaultCapabilitiesForRoute(testCase.route)
			for _, capability := range testCase.capabilities {
				assert.Contains(t, actual, capability)
			}
		})
	}
}

func TestServiceDeploymentFailureCanComposeOperationalCapabilities(t *testing.T) {
	capabilities, ok := normalizeCapabilities([]Capability{
		CapabilityServiceContext,
		CapabilityServiceRuntime,
		CapabilityDeploymentHistory,
		CapabilityAlertManagement,
	})
	require.True(t, ok)
	assert.True(t, hasCapability(capabilities, CapabilityDeploymentHistory))
	assert.True(t, hasCapability(capabilities, CapabilityAlertManagement))
	assert.Contains(t, routerSystemPrompt, "Почему упал деплой infra-monitoring?")
}
