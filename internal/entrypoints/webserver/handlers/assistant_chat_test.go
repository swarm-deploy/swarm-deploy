package handlers

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant"
)

func TestAssistantChatRejectsOversizedRequest(t *testing.T) {
	h := &handler{assistant: &assistant.DisabledAssistant{}}

	response, err := h.AssistantChat(context.Background(), &generated.AssistantChatRequest{
		Message: generated.NewOptString(strings.Repeat("x", assistantChatRequestMaxBytes+1)),
	})

	require.Error(t, err)
	assert.Nil(t, response)
	var statusErr *statusError
	require.ErrorAs(t, err, &statusErr)
	assert.Equal(t, http.StatusRequestEntityTooLarge, statusErr.code)
	assert.Contains(t, err.Error(), "too large")
}

func TestAssistantChatAcceptsRequestAtLimit(t *testing.T) {
	h := &handler{assistant: &assistant.DisabledAssistant{}}

	response, err := h.AssistantChat(context.Background(), &generated.AssistantChatRequest{
		Message: generated.NewOptString(strings.Repeat("x", assistantChatRequestMaxBytes)),
	})

	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, generated.AssistantChatResponseStatusDisabled, response.Status)
}
