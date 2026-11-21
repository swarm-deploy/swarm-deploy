import { apiRequest } from "./client";
import type {
  AssistantChatHistory,
  AssistantChatRequest,
  AssistantChatResponse,
  AssistantChatsResponse,
  AssistantSuggestionsResponse,
  AssistantUsageQuery,
  AssistantUsageResponse,
} from "./types";

export function getAssistantSuggestions(): Promise<AssistantSuggestionsResponse> {
  return apiRequest<AssistantSuggestionsResponse>("/api/assistant/suggestions");
}

export function sendAssistantChat(payload: AssistantChatRequest): Promise<AssistantChatResponse> {
  return apiRequest<AssistantChatResponse>("/api/v1/assistant/chat", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(payload),
  });
}

export function listAssistantChats(): Promise<AssistantChatsResponse> {
  return apiRequest<AssistantChatsResponse>("/api/v1/assistant/chats");
}

export function getAssistantChat(conversationID: string, signal?: AbortSignal): Promise<AssistantChatHistory> {
  return apiRequest<AssistantChatHistory>(`/api/v1/assistant/chats/${encodeURIComponent(conversationID)}`, { signal });
}

export function getAssistantUsage(
  query: AssistantUsageQuery,
  signal?: AbortSignal,
): Promise<AssistantUsageResponse> {
  const params = new URLSearchParams({ from: query.from, to: query.to, interval: query.interval });
  return apiRequest<AssistantUsageResponse>(`/api/v1/assistant/usage?${params}`, { signal });
}
