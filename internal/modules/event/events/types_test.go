package events

import (
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestEventTypeIdentifiersUsePascalCase(t *testing.T) {
	allTypes := append([]Type{TypeAlertOpened, TypeAlertResolved, TypeConfigurationChanged,
		TypeDeployFailed, TypeDeployPreparationFailed, TypeDeployInterrupted}, Types...)

	seen := make(map[TypeName]bool, len(allTypes))
	for _, typ := range allTypes {
		name := typ.Name()
		first, _ := utf8.DecodeRuneInString(string(name))
		assert.Truef(t, unicode.IsUpper(first), "event type %q must start with an uppercase letter", name)
		assert.NotContains(t, string(name), "_")
		assert.False(t, seen[name], "event type %q is duplicated", name)
		seen[name] = true
	}

	assert.Equal(t, map[TypeName]bool{
		TypeNameServiceCatalogUpdated:            true,
		TypeNameDeploySuccess:                    true,
		TypeNameSyncFailed:                       true,
		TypeNameAlertOpened:                      true,
		TypeNameAlertResolved:                    true,
		TypeNameDeployFailed:                     true,
		TypeNameConfigurationChanged:             true,
		TypeNameDeployPreparationFailed:          true,
		TypeNameDeployInterrupted:                true,
		TypeNameSendNotificationFailed:           true,
		TypeNameSyncManualStarted:                true,
		TypeNameWebhookReceived:                  true,
		TypeNameNodeJoined:                       true,
		TypeNameNodeConnected:                    true,
		TypeNameNodeDisconnected:                 true,
		TypeNameServiceMissed:                    true,
		TypeNameServiceReplicasIncreased:         true,
		TypeNameServiceReplicasDecreased:         true,
		TypeNameServiceRestarted:                 true,
		TypeNameServicePruned:                    true,
		TypeNameNetworkCreated:                   true,
		TypeNameUserAuthenticated:                true,
		TypeNameAssistantPromptInjectionDetected: true,
	}, seen)
}
