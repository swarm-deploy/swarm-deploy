package handlers

import (
	"context"
	"math"

	"github.com/swarm-deploy/swarm-deploy/internal/assistant"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) AssistantChat(
	ctx context.Context,
	req *generated.AssistantChatRequest,
) (*generated.AssistantChatResponse, error) {
	request := assistant.ChatRequest{
		ConversationID: req.ConversationID.Value,
		RequestID:      req.RequestID.Value,
		Message:        req.Message.Value,
		WaitTimeoutMS:  int(req.WaitTimeoutMs.Value),
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
