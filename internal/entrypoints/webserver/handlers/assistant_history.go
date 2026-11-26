package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant"
)

func (h *handler) ListAssistantChats(ctx context.Context) (*generated.AssistantChatsResponse, error) {
	chats, err := h.assistant.ListChats(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", h.assistantTexts.Text("http.chat_list_failed"), err)
	}

	items := make([]generated.AssistantChatSummary, 0, len(chats))
	for _, chat := range chats {
		items = append(items, toGeneratedAssistantChatSummary(chat))
	}

	return &generated.AssistantChatsResponse{Chats: items}, nil
}

func (h *handler) GetAssistantChat(
	ctx context.Context,
	params generated.GetAssistantChatParams,
) (*generated.AssistantChatHistory, error) {
	chat, ok, err := h.assistant.GetChat(ctx, params.ConversationID)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", h.assistantTexts.Text("http.chat_read_failed"), err)
	}
	if !ok {
		return nil, withStatusError(
			http.StatusNotFound,
			errors.New(h.assistantTexts.Format("http.chat_not_found", map[string]any{"ID": params.ConversationID})),
		)
	}

	messages := make([]generated.AssistantChatMessage, 0, len(chat.Messages))
	for _, message := range chat.Messages {
		messages = append(messages, generated.AssistantChatMessage{
			Sequence:  message.Sequence,
			Role:      toGeneratedAssistantChatMessageRole(message.Role),
			Content:   message.Content,
			Activity:  append([]string(nil), message.Activity...),
			Selection: toGeneratedAssistantSelection(message.Selection),
			Artifacts: toGeneratedAssistantArtifacts(message.Artifacts),
		})
	}

	return &generated.AssistantChatHistory{
		ID:            chat.ID,
		Title:         chat.Title,
		CreatedAt:     chat.CreatedAt,
		UpdatedAt:     chat.UpdatedAt,
		Unread:        chat.Unread,
		TokenUsage:    toGeneratedAssistantTokenUsage(chat.Usage),
		Messages:      messages,
		ActiveRequest: toGeneratedAssistantActiveChatRequest(chat.ActiveRequest),
	}, nil
}

func (h *handler) MarkAssistantChatRead(
	ctx context.Context,
	request *generated.AssistantChatReadRequest,
	params generated.MarkAssistantChatReadParams,
) error {
	if err := h.assistant.MarkChatRead(ctx, params.ConversationID, request.Sequence); err != nil {
		return fmt.Errorf("%s: %w", h.assistantTexts.Text("http.chat_mark_read_failed"), err)
	}
	return nil
}

func toGeneratedAssistantActiveChatRequest(
	request *assistant.ActiveChatRequest,
) generated.OptAssistantActiveChatRequest {
	if request == nil {
		return generated.OptAssistantActiveChatRequest{}
	}

	return generated.NewOptAssistantActiveChatRequest(generated.AssistantActiveChatRequest{
		RequestID: request.RequestID,
		Activity:  append([]string(nil), request.Activity...),
	})
}

// GetAssistantUsage returns recorded token usage across chats.
func (h *handler) GetAssistantUsage(
	ctx context.Context,
	params generated.GetAssistantUsageParams,
) (*generated.AssistantUsageResponse, error) {
	if !params.From.Before(params.To) {
		return nil, withStatusError(http.StatusBadRequest, errors.New(h.assistantTexts.Text("http.usage_invalid_range")))
	}

	buckets, err := h.assistant.GetUsage(ctx, params.From, params.To, assistant.UsageInterval(params.Interval))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", h.assistantTexts.Text("http.usage_failed"), err)
	}

	items := make([]generated.AssistantUsageBucket, 0, len(buckets))
	for _, bucket := range buckets {
		items = append(items, generated.AssistantUsageBucket{
			Start: bucket.Start, InputTokens: bucket.InputTokens,
			OutputTokens: bucket.OutputTokens, TotalTokens: bucket.TotalTokens,
		})
	}
	return &generated.AssistantUsageResponse{Buckets: items}, nil
}

func toGeneratedAssistantChatSummary(chat assistant.ChatSummary) generated.AssistantChatSummary {
	return generated.AssistantChatSummary{
		ID:              chat.ID,
		Title:           chat.Title,
		CreatedAt:       chat.CreatedAt,
		UpdatedAt:       chat.UpdatedAt,
		Unread:          chat.Unread,
		TokenUsage:      toGeneratedAssistantTokenUsage(chat.Usage),
		PrimaryArtifact: toGeneratedAssistantArtifact(chat.PrimaryArtifact),
	}
}

func toGeneratedAssistantTokenUsage(usage *assistant.TokenUsage) generated.OptAssistantTokenUsage {
	if usage == nil {
		return generated.OptAssistantTokenUsage{}
	}

	return generated.NewOptAssistantTokenUsage(generated.AssistantTokenUsage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.TotalTokens,
	})
}

func toGeneratedAssistantChatMessageRole(role string) generated.AssistantChatMessageRole {
	switch role {
	case "assistant":
		return generated.AssistantChatMessageRoleAssistant
	case "system":
		return generated.AssistantChatMessageRoleSystem
	case "user":
		fallthrough
	default:
		return generated.AssistantChatMessageRoleUser
	}
}
