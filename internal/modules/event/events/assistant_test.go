package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantPromptInjectionDetectedDetails(t *testing.T) {
	event := &AssistantPromptInjectionDetected{
		ChatID:   "chat-123",
		Detector: AssistantPromptInjectionDetectorRegexp,
	}

	details := event.Details()

	assert.Equal(t, "regexp", details["detector"])
	assert.Equal(t, "chat-123", details["chat_id"])
	assert.NotContains(t, details, "prompt")
}

func TestAssistantPromptInjectionDetectedWithUsernameKeepsChatID(t *testing.T) {
	event := &AssistantPromptInjectionDetected{
		ChatID:   "chat-123",
		Detector: AssistantPromptInjectionDetectorRegexp,
	}

	withUsername, ok := event.WithUsername("artem").(*AssistantPromptInjectionDetected)
	require.True(t, ok)

	assert.Equal(t, "chat-123", withUsername.ChatID)
	assert.Equal(t, AssistantPromptInjectionDetectorRegexp, withUsername.Detector)
	assert.Equal(t, "artem", withUsername.Username)
	assert.NotContains(t, withUsername.Details(), "prompt")
}
