package handlers

import (
	"context"
	"errors"
	"math"
	"net/http"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
)

const assistantChatRequestMaxBytes = 64 * 1024

func (h *handler) AssistantChat(
	ctx context.Context,
	req *generated.AssistantChatRequest,
) (*generated.AssistantChatResponse, error) {
	requestSize := len(req.ConversationID.Value) + len(req.RequestID.Value) + len(req.Message.Value)
	if requestSize > assistantChatRequestMaxBytes {
		return nil, withStatusError(
			http.StatusRequestEntityTooLarge,
			errors.New(h.assistantTexts.Text("http.request_too_large")),
		)
	}

	request := assistant.ChatRequest{
		ConversationID: req.ConversationID.Value,
		RequestID:      req.RequestID.Value,
		Message:        req.Message.Value,
		WaitTimeoutMS:  int(req.WaitTimeoutMs.Value),
	}
	if user, ok := security.UserFromContext(ctx); ok {
		request.CreatedBy = user.Name
	}

	return toGeneratedAssistantChatResponse(h.assistant.Chat(ctx, request)), nil
}

func toGeneratedAssistantChatResponse(resp assistant.ChatResponse) *generated.AssistantChatResponse {
	generatedResp := &generated.AssistantChatResponse{
		Status:         toGeneratedAssistantStatus(resp.Status),
		RequestID:      resp.RequestID,
		ConversationID: resp.ConversationID,
		Answer:         toOptString(resp.Answer),
		ErrorMessage:   toOptString(resp.ErrorMessage),
		Activity:       append([]string(nil), resp.Activity...),
		Selection:      toGeneratedAssistantSelection(resp.Selection),
		Artifacts:      toGeneratedAssistantArtifacts(resp.Artifacts),
	}
	if resp.AnswerSequence != nil {
		generatedResp.AnswerSequence = generated.NewOptInt(*resp.AnswerSequence)
	}
	if resp.PollAfterMS > 0 {
		pollAfterMS := resp.PollAfterMS
		if pollAfterMS > math.MaxInt32 {
			pollAfterMS = math.MaxInt32
		}

		generatedResp.PollAfterMs = generated.NewOptInt32(int32(pollAfterMS))
	}

	return generatedResp
}

func toGeneratedAssistantArtifacts(artifacts []assistant.Artifact) []generated.AssistantArtifact {
	if len(artifacts) == 0 {
		return nil
	}
	result := make([]generated.AssistantArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		result = append(result, generated.AssistantArtifact{
			Type: string(artifact.Type), Label: artifact.Label, URL: toOptString(artifact.URL),
		})
	}
	return result
}

func toGeneratedAssistantArtifact(artifact *assistant.Artifact) generated.OptAssistantArtifact {
	if artifact == nil {
		return generated.OptAssistantArtifact{}
	}
	return generated.NewOptAssistantArtifact(toGeneratedAssistantArtifacts([]assistant.Artifact{*artifact})[0])
}

func toGeneratedAssistantSelection(selection *assistant.Selection) generated.OptAssistantSelection {
	if selection == nil {
		return generated.OptAssistantSelection{}
	}

	options := make([]generated.AssistantSelectionOption, 0, len(selection.Options))
	for _, option := range selection.Options {
		options = append(options, generated.AssistantSelectionOption{
			Label: option.Label, Description: toOptString(option.Description), Value: option.Value,
		})
	}
	return generated.NewOptAssistantSelection(generated.AssistantSelection{Options: options})
}

func toGeneratedAssistantStatus(status assistant.Status) generated.AssistantChatResponseStatus {
	switch status {
	case assistant.StatusInProgress:
		return generated.AssistantChatResponseStatusInProgress
	case assistant.StatusCompleted:
		return generated.AssistantChatResponseStatusCompleted
	case assistant.StatusRejected:
		return generated.AssistantChatResponseStatusRejected
	case assistant.StatusDisabled:
		return generated.AssistantChatResponseStatusDisabled
	case assistant.StatusFailed:
		fallthrough
	default:
		return generated.AssistantChatResponseStatusFailed
	}
}
