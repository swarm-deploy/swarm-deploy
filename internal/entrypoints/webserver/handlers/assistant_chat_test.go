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
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
)

func TestAssistantChatRejectsOversizedRequest(t *testing.T) {
	localizer := newTestAssistantTexts(t, "en")
	h := &handler{assistant: assistant.NewDisabledAssistant(localizer), assistantTexts: localizer}

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
	localizer := newTestAssistantTexts(t, "en")
	h := &handler{assistant: assistant.NewDisabledAssistant(localizer), assistantTexts: localizer}

	response, err := h.AssistantChat(context.Background(), &generated.AssistantChatRequest{
		Message:  generated.NewOptString(strings.Repeat("x", assistantChatRequestMaxBytes)),
		ReadOnly: generated.NewOptBool(true),
	})

	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, generated.AssistantChatResponseStatusDisabled, response.Status)
	assert.False(t, response.ReadOnly.IsSet())
}

func TestGeneratedAssistantChatResponseIncludesSelection(t *testing.T) {
	response := toGeneratedAssistantChatResponse(assistant.ChatResponse{
		Status: assistant.StatusCompleted,
		Selection: &assistant.Selection{Options: []assistant.SelectionOption{{
			Label: "api", Description: "core", Value: "core/api",
		}}},
		Artifacts: []assistant.Artifact{{
			Type: conversation.ArtifactTypePullRequest, Label: "PR #42", URL: "https://example.com/pull/42",
		}},
	})

	selection, ok := response.Selection.Get()
	require.True(t, ok)
	require.Len(t, selection.Options, 1)
	assert.Equal(t, "api", selection.Options[0].Label)
	assert.Equal(t, "core", selection.Options[0].Description.Value)
	assert.Equal(t, "core/api", selection.Options[0].Value)
	require.Len(t, response.Artifacts, 1)
	assert.Equal(t, "pull_request", response.Artifacts[0].Type)
	assert.Equal(t, "PR #42", response.Artifacts[0].Label)
	assert.Equal(t, "https://example.com/pull/42", response.Artifacts[0].URL.Value)
}
