import { defineStore } from "pinia";

import { getAssistantChat, listAssistantChats, markAssistantChatRead, sendAssistantChat } from "../api/assistant";
import type {
  AssistantChatRequest,
  AssistantChatResponse,
  AssistantChatSummary,
  AssistantSelection,
} from "../api/types";
import { t } from "../i18n";
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
  selection?: AssistantSelection;
  sequence?: number;
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
  listGeneration: number;
  unreadReplySequence?: number;
  readRequests: Set<string>;
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
    listGeneration: 0,
    unreadReplySequence: undefined,
    readRequests: new Set(),
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
    pushMessage(
      role: AssistantMessageRole,
      text: string,
      activity?: string[],
      sequence?: number,
      selection?: AssistantSelection,
    ) {
      this.messages.push({ role, text, activity, sequence, selection });
    },
    async requestAssistant(payload: AssistantChatRequest): Promise<AssistantChatResponse> {
      return sendAssistantChat(payload);
    },
    async loadChats(showLoading = true) {
      const generation = ++this.listGeneration;
      this.historyLoading = showLoading;
      try {
        const response = await listAssistantChats();
        if (generation === this.listGeneration) {
          this.chats = response.chats || [];
        }
      } finally {
        if (generation === this.listGeneration) {
          this.historyLoading = false;
        }
      }
    },
    async markReplyRead(conversationID: string, sequence: number) {
      const key = JSON.stringify([conversationID, sequence]);
      if (this.readRequests.has(key)) return;
      this.readRequests.add(key);
      try {
        await markAssistantChatRead(conversationID, sequence);
        if (this.conversationID === conversationID && this.unreadReplySequence === sequence) {
          this.unreadReplySequence = undefined;
        }
        // The server can ignore a stale acknowledgement. Refresh instead of clearing the dot locally.
        if (this.historyOpen) await this.loadChats(false);
      } catch {
        // Keep the reply eligible for acknowledgement on the next visibility change.
      } finally {
        this.readRequests.delete(key);
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
      this.unreadReplySequence = undefined;
      this.historyOpen = false;
    },
    async openChat(conversationID: string, signal?: AbortSignal) {
      // Selection takes effect even when the history read is slower than the previous chat's response.
      this.beginChatGeneration();
      if (this.pending) {
        this.chatHistoryStale = true;
        this.pending = false;
        this.activeRequestID = "";
        this.activity = [];
      }
      if (await this.refreshChat(conversationID, signal)) this.historyOpen = false;
    },
    async refreshChat(conversationID: string, signal?: AbortSignal, notice?: string) {
      const previousGeneration = this.chatGeneration;
      const chat = await getAssistantChat(conversationID, signal);
      if (signal?.aborted || !this.isCurrentChat(previousGeneration)) {
        return false;
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
        selection: message.selection,
        sequence: message.sequence,
      }));
      this.unreadReplySequence = chat.unread
        ? chat.messages.filter((message) => message.role === "assistant").at(-1)?.sequence
        : undefined;
      if (notice) this.pushMessage("system", notice);
      if (active) {
        void this.resumeAssistantMessage(chat.id, active.request_id, generation);
      }
      return true;
    },
    // Continues polling a reply that was started before the chat was opened, for example before a page reload.
    async resumeAssistantMessage(conversationID: string, requestID: string, generation: number) {
      this.pending = true;
      try {
        const response = await this.pollSelectedChat(
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
    async pollSelectedChat(payload: AssistantChatRequest, generation: number) {
      return this.pollAssistantMessage(payload, (response) => {
        this.updateAssistantProgress(response);
        return true;
      }, () => this.isCurrentChat(generation));
    },
    // onResponse returns false once the caller has received what it needs from the request.
    async pollAssistantMessage(
      payload: AssistantChatRequest,
      onResponse: (response: AssistantChatResponse) => boolean,
      isCurrent: () => boolean = () => true,
    ): Promise<AssistantChatResponse | undefined> {
      for (let attempt = 0; attempt < 150; attempt += 1) {
        // On a new chat, wait for the saved ID and start polling for the answer separately.
        const waitTimeout = attempt === 0 && payload.message && !payload.conversation_id ? 1 : 750;
        const response = await this.requestAssistant({ ...payload, wait_timeout_ms: waitTimeout });
        if (!isCurrent()) {
          return undefined;
        }
        if (!onResponse(response) || response.status !== "in_progress") {
          return response;
        }

        payload = {
          conversation_id: response.conversation_id || payload.conversation_id,
          request_id: response.request_id || payload.request_id,
        };
        await sleep(Math.max(100, response.poll_after_ms || 1000));
        if (!isCurrent()) {
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
          response.answer || t("assistant.emptyAnswer"),
          this.activity.length > 0 ? [...this.activity] : undefined,
          response.answer_sequence,
          response.selection,
        );
        this.unreadReplySequence = response.answer_sequence;
        this.activity = [];
        await this.loadChats(false);
        return;
      }

      if (response?.status === "disabled") {
        this.setEnabled(false);
      }

      this.activity = [];
      this.pushMessage("system", response
        ? response.error_message || t("assistant.status", { status: response.status })
        : t("assistant.timeout"));
    },
    showRequestError(error: unknown) {
      this.chatHistoryStale = true;
      this.activeRequestID = "";
      this.activity = [];
      const message = error instanceof Error ? error.message : t("assistant.unexpectedError");
      this.pushMessage("system", t("assistant.requestFailed", { message }));
    },
    // Return as soon as the server has accepted the first question and assigned a chat ID.
    async createChat(message: string, requestID: string) {
      const response = await this.pollAssistantMessage(
        { message, request_id: requestID },
        (progress) => !progress.conversation_id,
      );
      if (!response) throw new Error(t("assistant.timeout"));
      return response;
    },
    openCreatedChat(message: string, response: AssistantChatResponse) {
      this.newChat();
      this.pushMessage("user", message);
      this.updateAssistantProgress(response);
      const generation = this.chatGeneration;
      if (response.status === "in_progress") {
        void this.resumeAssistantMessage(response.conversation_id, response.request_id, generation);
      } else {
        void this.finishAssistantMessage(response).catch((error) => {
          if (this.isCurrentChat(generation)) this.showRequestError(error);
        });
      }
    },
    async sendMessage(message: string) {
      if (this.pending) {
        return;
      }

      if (!message.trim()) {
        return;
      }

      const conversationID = this.conversationID;
      // Sending keeps the shown chat and drops any chat that is still loading.
      const generation = this.beginChatGeneration();
      this.pushMessage("user", message);
      this.activity = [];
      this.pending = true;

      try {
        const response = await this.pollSelectedChat({
          conversation_id: conversationID || undefined,
          message,
        }, generation);
        if (!this.isCurrentChat(generation)) return;
        if (conversationID && (response?.status === "failed" || response?.status === "rejected")) {
          // Another client may own the running request, or the server may have rejected our optimistic turn.
          this.chatHistoryStale = true;
          const notice = response.error_message || t("assistant.status", { status: response.status });
          this.pushMessage("system", notice);
          await this.refreshChat(conversationID, undefined, notice);
        } else {
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
