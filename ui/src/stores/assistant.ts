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
  activity?: string[];
}

interface AssistantState {
  enabled: boolean;
  pending: boolean;
  conversationID: string;
  activeRequestID: string;
  activity: string[];
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
    activity: [],
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
    pushMessage(role: AssistantMessageRole, text: string, activity?: string[]) {
      this.messages.push({ role, text, activity });
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
      this.activity = [];
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
      this.activity = [];
      this.messages = chat.messages.map((message) => ({
        role: message.role,
        text: message.content,
        activity: message.activity,
      }));
      this.historyOpen = false;
    },
    async pollAssistantMessage(
      payload: AssistantChatRequest,
      onProgress?: (response: AssistantChatResponse) => void,
    ): Promise<AssistantChatResponse | undefined> {
      for (let attempt = 0; attempt < 150; attempt += 1) {
        // On a new chat, wait for the saved ID and start polling for the answer separately.
        const waitTimeout = attempt === 0 && payload.message && !payload.conversation_id ? 1 : 750;
        const response = await this.requestAssistant({ ...payload, wait_timeout_ms: waitTimeout });
        onProgress?.(response);

        if (response.status !== "in_progress") {
          return response;
        }

        payload = {
          conversation_id: response.conversation_id || payload.conversation_id,
          request_id: response.request_id || payload.request_id,
        };
        await sleep(Math.max(100, response.poll_after_ms || 1000));
      }
    },
    updateAssistantProgress(response: AssistantChatResponse) {
      this.conversationID = response.conversation_id || this.conversationID;
      this.activeRequestID = response.request_id || this.activeRequestID;
      this.activity = response.activity || this.activity;
    },
    async runAssistantMessage(message: string) {
      const response = await this.pollAssistantMessage({
        conversation_id: this.conversationID || undefined,
        message,
      }, (response) => this.updateAssistantProgress(response));

      await this.finishAssistantMessage(response);
    },
    async finishAssistantMessage(response?: AssistantChatResponse) {
      this.activeRequestID = "";
      if (response?.status === "completed") {
        this.pushMessage(
          "assistant",
          response.answer || "Assistant returned empty answer.",
          this.activity.length > 0 ? [...this.activity] : undefined,
        );
        this.activity = [];
        await this.loadChats();
        return;
      }

      if (response?.status === "disabled") {
        this.setEnabled(false);
      }

      this.activity = [];
      this.pushMessage("system", response
        ? response.error_message || `Assistant status: ${response.status}`
        : "Assistant request timeout. Try again.");
    },
    showRequestError(error: unknown) {
      this.activeRequestID = "";
      this.activity = [];
      const message = error instanceof Error ? error.message : "Unexpected assistant error";
      this.pushMessage("system", `Assistant failed: ${message}`);
    },
    async sendFirstMessage(message: string, requestID: string, onStarted: (conversationID: string) => void) {
      this.pending = true;
      let started = false;
      try {
        const response = await this.pollAssistantMessage({ message, request_id: requestID }, (progress) => {
          if (!started) {
            if (!progress.conversation_id || progress.status === "disabled") {
              return;
            }
            started = true;
            this.messages = [];
            this.activity = [];
            this.pushMessage("user", message);
            this.updateAssistantProgress(progress);
            onStarted(progress.conversation_id);
            return;
          }
          this.updateAssistantProgress(progress);
        });
        if (!started) {
          if (!response) {
            throw new Error("Assistant request timeout. Try again.");
          }
          return response;
        }
        await this.finishAssistantMessage(response);
      } catch (error) {
        if (!started) {
          throw error;
        }
        this.showRequestError(error);
      } finally {
        this.pending = false;
      }
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
      this.activity = [];
      this.pending = true;

      try {
        await this.runAssistantMessage(normalizedMessage);
      } catch (error) {
        this.showRequestError(error);
      } finally {
        this.pending = false;
      }
    },
  },
});
