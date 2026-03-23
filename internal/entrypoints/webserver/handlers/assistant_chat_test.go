package handlers

import (
	"context"
	"testing"

	generated "github.com/artarts36/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantChatReturnsDisabledWhenFeatureOff(t *testing.T) {
	h := &handler{
		assistantEnabled: false,
	}

	response, err := h.AssistantChat(context.Background(), &generated.AssistantChatRequest{
		Message: generated.NewOptString("hello"),
	})
	require.NoError(t, err, "assistant chat response")
	assert.Equal(t, generated.AssistantChatResponseStatusDisabled, response.Status, "expected disabled status")
	assert.Contains(t, response.ErrorMessage.Or(""), "disabled", "expected disabled message")
}
