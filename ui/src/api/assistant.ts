import { apiRequest } from "./client";
import type {
  AssistantChatHistory,
  AssistantChatRequest,
  AssistantChatResponse,
  AssistantChatsResponse,
} from "./types";

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

export function getAssistantChat(conversationID: string): Promise<AssistantChatHistory> {
  return apiRequest<AssistantChatHistory>(`/api/v1/assistant/chats/${encodeURIComponent(conversationID)}`);
}
