package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/swarm-deploy/swarm-deploy/internal/assistant"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
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
			Role:    toGeneratedAssistantChatMessageRole(message.Role),
			Content: message.Content,
		})
	}

	return &generated.AssistantChatHistory{
		ID:         chat.ID,
		Title:      chat.Title,
		CreatedAt:  chat.CreatedAt,
		UpdatedAt:  chat.UpdatedAt,
		TokenUsage: toGeneratedAssistantTokenUsage(chat.Usage),
		Messages:   messages,
	}, nil
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
