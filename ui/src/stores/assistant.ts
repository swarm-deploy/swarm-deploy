import { defineStore } from "pinia";

import { getAssistantChat, listAssistantChats, sendAssistantChat } from "../api/assistant";
import type {
  AssistantChatRequest,
  AssistantChatResponse,
  AssistantChatSummary,
} from "../api/types";
import { useUIStore } from "./ui";

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, ms);
  });
}

export type AssistantMessageRole = "user" | "assistant" | "system";

export interface AssistantMessage {
  role: AssistantMessageRole;
  text: string;
}

interface AssistantState {
  enabled: boolean;
  pending: boolean;
  conversationID: string;
  activeRequestID: string;
  messages: AssistantMessage[];
  chats: AssistantChatSummary[];
  historyOpen: boolean;
  historyLoading: boolean;
}

export const useAssistantStore = defineStore("assistant", {
  state: (): AssistantState => ({
    enabled: true,
    pending: false,
    conversationID: "",
    activeRequestID: "",
    messages: [],
    chats: [],
    historyOpen: false,
    historyLoading: false,
  }),
  actions: {
    setEnabled(enabled: boolean) {
      this.enabled = enabled;
      if (!enabled) {
        const uiStore = useUIStore();
        uiStore.closeAssistantDrawer();
      }
    },
    pushMessage(role: AssistantMessageRole, text: string) {
      this.messages.push({ role, text });
    },
    async requestAssistant(payload: AssistantChatRequest): Promise<AssistantChatResponse> {
      return sendAssistantChat(payload);
    },
    async loadChats() {
      this.historyLoading = true;
      try {
        const response = await listAssistantChats();
        this.chats = response.chats || [];
      } finally {
        this.historyLoading = false;
      }
    },
    async toggleHistory() {
      if (this.pending) {
        return;
      }

      this.historyOpen = !this.historyOpen;
      if (this.historyOpen) {
        await this.loadChats();
      }
    },
    newChat() {
      if (this.pending) {
        return;
      }

      this.conversationID = "";
      this.activeRequestID = "";
      this.messages = [];
      this.historyOpen = false;
    },
    async openChat(conversationID: string) {
      if (this.pending) {
        return;
      }

      const chat = await getAssistantChat(conversationID);
      this.conversationID = chat.id;
      this.activeRequestID = "";
      this.messages = chat.messages.map((message) => ({
        role: message.role,
        text: message.content,
      }));
      this.historyOpen = false;
    },
    async runAssistantMessage(message: string) {
      let payload: AssistantChatRequest = {
        conversation_id: this.conversationID || undefined,
        message,
        wait_timeout_ms: 12000,
      };

      for (let attempt = 0; attempt < 30; attempt += 1) {
        const response = await this.requestAssistant(payload);
        this.conversationID = response.conversation_id || this.conversationID;
        this.activeRequestID = response.request_id || this.activeRequestID;

        if (response.status === "in_progress") {
          const delay = Number(response.poll_after_ms) > 0 ? Number(response.poll_after_ms) : 1000;
          payload = {
            conversation_id: this.conversationID || undefined,
            request_id: this.activeRequestID || undefined,
            wait_timeout_ms: 12000,
          };
          await sleep(delay);
          continue;
        }

        this.activeRequestID = "";
        if (response.status === "completed") {
          this.pushMessage("assistant", response.answer || "Assistant returned empty answer.");
          await this.loadChats();
          return;
        }

        if (response.status === "disabled") {
          this.setEnabled(false);
        }

        this.pushMessage("system", response.error_message || `Assistant status: ${response.status}`);
        return;
      }

      this.activeRequestID = "";
      this.pushMessage("system", "Assistant request timeout. Try again.");
    },
    async sendMessage(message: string) {
      if (this.pending) {
        return;
      }

      const normalizedMessage = message.trim();
      if (!normalizedMessage) {
        return;
      }

      this.pushMessage("user", normalizedMessage);
      this.pending = true;

      try {
        await this.runAssistantMessage(normalizedMessage);
      } catch (error) {
        this.activeRequestID = "";
        const messageText = error instanceof Error ? error.message : "Unexpected assistant error";
        this.pushMessage("system", `Assistant failed: ${messageText}`);
      } finally {
        this.pending = false;
      }
    },
  },
});
