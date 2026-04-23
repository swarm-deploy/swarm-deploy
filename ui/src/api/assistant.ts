import { apiRequest } from "./client";
import type { AssistantChatRequest, AssistantChatResponse } from "./types";

export function sendAssistantChat(payload: AssistantChatRequest): Promise<AssistantChatResponse> {
  return apiRequest<AssistantChatResponse>("/api/v1/assistant/chat", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(payload),
  });
}
