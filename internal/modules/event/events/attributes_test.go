package events

import (
	"errors"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSharedAttributeNames(t *testing.T) {
	assert.Equal(t, "stack_name", AttributeStackName)
	assert.Equal(t, "service_name", AttributeServiceName)
	assert.Equal(t, "deployment_id", AttributeDeploymentID)
	assert.Equal(t, "commit", AttributeCommit)
	assert.Equal(t, "username", AttributeUsername)
	assert.Equal(t, "node_id", AttributeNodeID)
	assert.Equal(t, "node_name", AttributeNodeName)
	assert.Equal(t, "network_id", AttributeNetworkID)
	assert.Equal(t, "network_name", AttributeNetworkName)
	assert.Equal(t, "status", AttributeStatus)
	assert.Equal(t, "error", AttributeError)
	assert.Equal(t, "error_code", AttributeErrorCode)
	assert.Equal(t, "stage", AttributeStage)
}

func TestEventDetailsUseStandardAttributeNames(t *testing.T) {
	tests := []struct {
		name  string
		event Event
		keys  []string
	}{
		{name: "deploy success", event: &DeploySuccess{DeployEvent: DeployEvent{}},
			keys: []string{AttributeStackName, AttributeCommit, AttributeDeploymentID, AttributeStatus}},
		{name: "deploy failed", event: &DeployFailed{},
			keys: []string{AttributeStackName, AttributeCommit, AttributeDeploymentID, AttributeStatus, AttributeErrorCode, AttributeStage}},
		{name: "preparation failed", event: &DeployPreparationFailed{},
			keys: []string{AttributeStackName, AttributeCommit, AttributeStatus, AttributeErrorCode, AttributeStage}},
		{name: "deploy interrupted", event: &DeployInterrupted{},
			keys: []string{AttributeStackName, AttributeCommit, AttributeDeploymentID, AttributeStatus, AttributeErrorCode, AttributeStage}},
		{name: "service missed", event: &ServiceMissed{},
			keys: []string{AttributeStackName, AttributeServiceName, AttributeCommit}},
		{name: "service pruned", event: &ServicePruned{},
			keys: []string{AttributeStackName, AttributeServiceName, AttributeCommit}},
		{name: "replicas increased", event: &ServiceReplicasIncreased{Username: "operator"},
			keys: []string{AttributeStackName, AttributeServiceName, AttributeUsername,
				"previous_replicas", "current_replicas"}},
		{name: "replicas decreased", event: &ServiceReplicasDecreased{Username: "operator"},
			keys: []string{AttributeStackName, AttributeServiceName, AttributeUsername,
				"previous_replicas", "current_replicas"}},
		{name: "service restarted", event: &ServiceRestarted{Username: "operator"},
			keys: []string{AttributeStackName, AttributeServiceName, AttributeUsername}},
		{name: "service catalog", event: &ServiceCatalogUpdated{}, keys: []string{AttributeStackName}},
		{name: "manual sync", event: &SyncManualStarted{Username: "operator"}, keys: []string{AttributeUsername}},
		{name: "webhook", event: &WebhookReceived{}, keys: []string{"queued"}},
		{name: "node joined", event: &NodeJoined{NodeID: "node", NodeName: "worker", Role: "worker"},
			keys: []string{AttributeNodeID, AttributeNodeName, "role"}},
		{name: "node connected", event: &NodeConnected{NodeID: "node", NodeName: "worker", Status: "ready"},
			keys: []string{AttributeNodeID, AttributeNodeName, AttributeStatus}},
		{name: "node disconnected", event: &NodeDisconnected{NodeID: "node", NodeName: "worker", Status: "down"},
			keys: []string{AttributeNodeID, AttributeNodeName, AttributeStatus}},
		{name: "network", event: &NetworkCreated{}, keys: []string{AttributeNetworkName, AttributeNetworkID, "driver"}},
		{name: "authenticated", event: &UserAuthenticated{}, keys: []string{AttributeUsername}},
		{name: "prompt injection", event: &AssistantPromptInjectionDetected{Username: "operator"},
			keys: []string{"detector", AttributeUsername}},
		{name: "notification failed", event: &SendNotificationFailed{},
			keys: []string{"destination", "channel", "event_type"}},
		{name: "configuration", event: &ConfigurationChanged{}, keys: nil},
		{name: "alert opened", event: &AlertOpened{}, keys: nil},
		{name: "alert resolved", event: &AlertResolved{}, keys: nil},
	}

	snakeCase := regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			details := test.event.Details()
			assert.ElementsMatch(t, test.keys, mapKeys(details))
			for key := range details {
				assert.Regexp(t, snakeCase, key)
				assert.NotEqual(t, "stack", key)
				assert.NotEqual(t, "service", key)
				assert.NotEqual(t, "triggered_by", key)
				assert.NotEqual(t, "diagnostic", key)
			}
		})
	}
}

func TestFailureDetailsMaskSensitiveDiagnostics(t *testing.T) {
	const secret = "private"
	tests := []struct {
		name  string
		event Event
	}{
		{name: "deploy failed", event: &DeployFailed{Error: errors.New("token=" + secret)}},
		{name: "preparation failed", event: &DeployPreparationFailed{Error: errors.New("token=" + secret)}},
		{name: "interrupted", event: &DeployInterrupted{Error: errors.New("token=" + secret)}},
		{name: "notification failed", event: &SendNotificationFailed{Error: errors.New("token=" + secret)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diagnostic := test.event.Details()[AttributeError]
			assert.NotEmpty(t, diagnostic)
			assert.NotContains(t, diagnostic, secret)
			assert.Contains(t, diagnostic, "****")
		})
	}
}

func mapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	return keys
}
