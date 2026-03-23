package handlers

import (
	"context"

	"github.com/artarts36/swarm-deploy/internal/assistant"
	generated "github.com/artarts36/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) AssistantChat(
	ctx context.Context,
	req *generated.AssistantChatRequest,
) (*generated.AssistantChatResponse, error) {
	if req == nil {
		req = &generated.AssistantChatRequest{}
	}

	request := assistant.ChatRequest{
		ConversationID: req.ConversationID.Or(""),
		RequestID:      req.RequestID.Or(""),
		Message:        req.Message.Or(""),
		WaitTimeoutMS:  int(req.WaitTimeoutMs.Or(0)),
	}

	if !h.assistantEnabled || h.assistant == nil {
		return toGeneratedAssistantChatResponse(assistant.ChatResponse{
			Status:         assistant.StatusDisabled,
			ConversationID: request.ConversationID,
			RequestID:      request.RequestID,
			ErrorMessage:   "assistant is disabled in configuration",
		}), nil
	}

	return toGeneratedAssistantChatResponse(h.assistant.Chat(ctx, request)), nil
}

func toGeneratedAssistantChatResponse(resp assistant.ChatResponse) *generated.AssistantChatResponse {
	generatedToolCalls := make([]generated.AssistantToolCall, 0, len(resp.ToolCalls))
	for _, toolCall := range resp.ToolCalls {
		generatedCall := generated.AssistantToolCall{
			Name:      toolCall.Name,
			Arguments: toOptString(toolCall.Arguments),
			Result:    toOptString(toolCall.Result),
			Error:     toOptString(toolCall.Error),
		}
		generatedToolCalls = append(generatedToolCalls, generatedCall)
	}

	generatedResp := &generated.AssistantChatResponse{
		Status:         toGeneratedAssistantStatus(resp.Status),
		RequestID:      resp.RequestID,
		ConversationID: resp.ConversationID,
		Answer:         toOptString(resp.Answer),
		ToolCalls:      generatedToolCalls,
		ErrorMessage:   toOptString(resp.ErrorMessage),
	}
	if resp.PollAfterMS > 0 {
		generatedResp.PollAfterMs = generated.NewOptInt32(int32(resp.PollAfterMS))
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
