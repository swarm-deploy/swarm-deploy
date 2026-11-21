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
		return nil, fmt.Errorf("list assistant chats: %w", err)
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
		return nil, fmt.Errorf("get assistant chat: %w", err)
	}
	if !ok {
		return nil, withStatusError(
			http.StatusNotFound,
			fmt.Errorf("assistant chat %q not found", params.ConversationID),
		)
	}

	messages := make([]generated.AssistantChatMessage, 0, len(chat.Messages))
	for _, message := range chat.Messages {
		messages = append(messages, generated.AssistantChatMessage{
			Role:     toGeneratedAssistantChatMessageRole(message.Role),
			Content:  message.Content,
			Activity: append([]string(nil), message.Activity...),
		})
	}

	return &generated.AssistantChatHistory{
		ID:            chat.ID,
		Title:         chat.Title,
		CreatedAt:     chat.CreatedAt,
		UpdatedAt:     chat.UpdatedAt,
		TokenUsage:    toGeneratedAssistantTokenUsage(chat.Usage),
		Messages:      messages,
		ActiveRequest: toGeneratedAssistantActiveChatRequest(chat.ActiveRequest),
	}, nil
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
		return nil, withStatusError(http.StatusBadRequest, errors.New("from must be before to"))
	}

	buckets, err := h.assistant.GetUsage(ctx, params.From, params.To, assistant.UsageInterval(params.Interval))
	if err != nil {
		return nil, fmt.Errorf("get assistant usage: %w", err)
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
		ID:         chat.ID,
		Title:      chat.Title,
		CreatedAt:  chat.CreatedAt,
		UpdatedAt:  chat.UpdatedAt,
		TokenUsage: toGeneratedAssistantTokenUsage(chat.Usage),
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
