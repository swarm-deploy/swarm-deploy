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
  chatGeneration: number;
  chatHistoryStale: boolean;
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
    chatGeneration: 0,
    chatHistoryStale: false,
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
    // Starts a new chat selection. Results of older loads and polls are ignored, so they never reach another chat.
    beginChatGeneration(): number {
      this.chatGeneration += 1;
      return this.chatGeneration;
    },
    isCurrentChat(generation: number): boolean {
      return generation === this.chatGeneration;
    },
    newChat() {
      this.beginChatGeneration();
      this.pending = false;
      this.chatHistoryStale = false;
      this.conversationID = "";
      this.activeRequestID = "";
      this.activity = [];
      this.messages = [];
      this.historyOpen = false;
    },
    async openChat(conversationID: string, signal?: AbortSignal) {
      const previousGeneration = this.chatGeneration;
      const chat = await getAssistantChat(conversationID, signal);
      if (signal?.aborted || !this.isCurrentChat(previousGeneration)) {
        return;
      }
      const generation = this.beginChatGeneration();
      const active = chat.active_request;
      // Set pending together with the messages, so the chat never shows an unanswered question while a reply is running.
      this.pending = Boolean(active);
      this.chatHistoryStale = false;
      this.conversationID = chat.id;
      this.activeRequestID = active?.request_id || "";
      this.activity = active?.activity || [];
      this.messages = chat.messages.map((message) => ({
        role: message.role,
        text: message.content,
        activity: message.activity,
      }));
      this.historyOpen = false;
      if (active) {
        void this.resumeAssistantMessage(chat.id, active.request_id, generation);
      }
    },
    // Continues polling a reply that was started before the chat was opened, for example before a page reload.
    async resumeAssistantMessage(conversationID: string, requestID: string, generation: number) {
      this.pending = true;
      try {
        const response = await this.pollAssistantMessage(
          { conversation_id: conversationID, request_id: requestID },
          generation,
        );
        if (this.isCurrentChat(generation)) {
          await this.finishAssistantMessage(response);
        }
      } catch (error) {
        if (this.isCurrentChat(generation)) {
          this.showRequestError(error);
        }
      } finally {
        if (this.isCurrentChat(generation)) {
          this.pending = false;
        }
      }
    },
    // Polls a request and applies progress only while its chat stays selected.
    async pollAssistantMessage(
      payload: AssistantChatRequest,
      generation: number,
      onProgress?: (response: AssistantChatResponse) => void,
    ): Promise<AssistantChatResponse | undefined> {
      const applyProgress = onProgress ?? ((response: AssistantChatResponse) => this.updateAssistantProgress(response));
      for (let attempt = 0; attempt < 150; attempt += 1) {
        // On a new chat, wait for the saved ID and start polling for the answer separately.
        const waitTimeout = attempt === 0 && payload.message && !payload.conversation_id ? 1 : 750;
        const response = await this.requestAssistant({ ...payload, wait_timeout_ms: waitTimeout });
        if (!this.isCurrentChat(generation)) {
          return undefined;
        }
        applyProgress(response);

        if (response.status !== "in_progress") {
          return response;
        }

        payload = {
          conversation_id: response.conversation_id || payload.conversation_id,
          request_id: response.request_id || payload.request_id,
        };
        await sleep(Math.max(100, response.poll_after_ms || 1000));
        if (!this.isCurrentChat(generation)) {
          return undefined;
        }
      }
    },
    updateAssistantProgress(response: AssistantChatResponse) {
      this.conversationID = response.conversation_id || this.conversationID;
      this.activeRequestID = response.request_id || this.activeRequestID;
      this.activity = response.activity || this.activity;
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
      this.chatHistoryStale = true;
      this.activeRequestID = "";
      this.activity = [];
      const message = error instanceof Error ? error.message : "Unexpected assistant error";
      this.pushMessage("system", `Assistant failed: ${message}`);
    },
    async sendFirstMessage(message: string, requestID: string, onStarted: (conversationID: string) => void) {
      // The new chat replaces any chat that is still loading.
      const generation = this.beginChatGeneration();
      this.pending = true;
      let started = false;
      try {
        const response = await this.pollAssistantMessage({ message, request_id: requestID }, generation, (progress) => {
          if (!started) {
            if (!progress.conversation_id || progress.status === "disabled") {
              return;
            }
            started = true;
            this.chatHistoryStale = false;
            this.messages = [];
            this.activity = [];
            this.pushMessage("user", message);
            this.updateAssistantProgress(progress);
            onStarted(progress.conversation_id);
            return;
          }
          this.updateAssistantProgress(progress);
        });
        if (!this.isCurrentChat(generation)) {
          return undefined;
        }
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
        if (this.isCurrentChat(generation)) {
          this.showRequestError(error);
        }
      } finally {
        if (this.isCurrentChat(generation)) {
          this.pending = false;
        }
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

      // Sending keeps the shown chat and drops any chat that is still loading.
      const generation = this.beginChatGeneration();
      this.pushMessage("user", normalizedMessage);
      this.activity = [];
      this.pending = true;

      try {
        const response = await this.pollAssistantMessage({
          conversation_id: this.conversationID || undefined,
          message: normalizedMessage,
        }, generation);
        if (this.isCurrentChat(generation)) {
          await this.finishAssistantMessage(response);
        }
      } catch (error) {
        if (this.isCurrentChat(generation)) {
          this.showRequestError(error);
        }
      } finally {
        if (this.isCurrentChat(generation)) {
          this.pending = false;
        }
      }
    },
  },
});
