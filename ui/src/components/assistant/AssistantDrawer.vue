<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";

import { useAssistantStore } from "../../stores/assistant";
import { useUIStore } from "../../stores/ui";
import { renderAssistantMarkdown } from "../../utils/assistantMarkdown";
import { escapeHtml } from "../../utils/escape";

const uiStore = useUIStore();
const assistantStore = useAssistantStore();

const messageInput = ref("");
const inputRef = ref<HTMLTextAreaElement | null>(null);
const bodyRef = ref<HTMLElement | null>(null);

const isOpen = computed(() => uiStore.assistantDrawerOpen && assistantStore.enabled);
const messages = computed(() => assistantStore.messages);
const pending = computed(() => assistantStore.pending);
const chats = computed(() => assistantStore.chats);
const historyOpen = computed(() => assistantStore.historyOpen);
const historyLoading = computed(() => assistantStore.historyLoading);

const currentChatTitle = computed(() => {
  const persisted = chats.value.find((chat) => chat.id === assistantStore.conversationID);
  if (persisted?.title) {
    return persisted.title;
  }

  const firstUserMessage = messages.value.find((message) => message.role === "user")?.text.trim();
  if (!firstUserMessage) {
    return "New chat";
  }

  return firstUserMessage.length > 52 ? `${firstUserMessage.slice(0, 52)}…` : firstUserMessage;
});

const groupedChats = computed(() => {
  const groups = [
    { label: "Today", chats: [] as typeof chats.value },
    { label: "Last 7 days", chats: [] as typeof chats.value },
    { label: "Earlier", chats: [] as typeof chats.value },
  ];

  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const day = 24 * 60 * 60 * 1000;

  for (const chat of chats.value) {
    const updatedAt = new Date(chat.updated_at).getTime();
    if (Number.isNaN(updatedAt)) {
      groups[2].chats.push(chat);
      continue;
    }

    if (updatedAt >= today) {
      groups[0].chats.push(chat);
    } else if (updatedAt >= today - 6 * day) {
      groups[1].chats.push(chat);
    } else {
      groups[2].chats.push(chat);
    }
  }

  return groups.filter((group) => group.chats.length > 0);
});

watch(
  () => isOpen.value,
  async (opened) => {
    if (!opened) {
      return;
    }

    await nextTick();
    if (!historyOpen.value) {
      inputRef.value?.focus();
    }
    scrollToBottom();
  },
);

watch(
  () => messages.value.length,
  async () => {
    await nextTick();
    scrollToBottom();
  },
);

function scrollToBottom() {
  bodyRef.value?.scrollTo({
    top: bodyRef.value.scrollHeight,
    behavior: "smooth",
  });
}

function closeDrawer() {
  uiStore.closeAssistantDrawer();
}

async function openHistory() {
  if (!historyOpen.value) {
    await assistantStore.toggleHistory();
  }
}

async function submitMessage() {
  const text = messageInput.value.trim();
  if (!text || pending.value) {
    return;
  }

  messageInput.value = "";
  await assistantStore.sendMessage(text);
  await nextTick();
  inputRef.value?.focus();
}

function handleComposerKeydown(event: KeyboardEvent) {
  if (event.key !== "Enter" || event.shiftKey) {
    return;
  }

  event.preventDefault();
  void submitMessage();
}

function renderMessageText(role: string, text: string): string {
  if (role === "assistant") {
    return renderAssistantMarkdown(text);
  }

  return `<p class="assistant-chat-text">${escapeHtml(text)}</p>`;
}
</script>

<template>
  <div class="assistant-drawer-layer" :class="{ open: isOpen }" @keydown.esc="closeDrawer">
    <div class="assistant-drawer-overlay" @click="closeDrawer" />
    <section class="assistant-drawer" aria-label="Assistant Drawer">
      <header class="assistant-drawer-header">
        <template v-if="historyOpen">
          <div class="assistant-header-identity">
            <span class="assistant-avatar" aria-hidden="true">AI</span>
            <div class="assistant-header-copy">
              <h2>Assistant</h2>
              <span>Swarm Deploy</span>
            </div>
          </div>
        </template>

        <template v-else>
          <div class="assistant-chat-heading">
            <button
              type="button"
              class="assistant-icon-button"
              aria-label="Back to chats"
              title="Back to chats"
              :disabled="pending"
              @click="openHistory"
            >
              <svg viewBox="0 0 24 24" aria-hidden="true">
                <path d="m15 18-6-6 6-6" />
              </svg>
            </button>
            <span class="assistant-avatar assistant-avatar-small" aria-hidden="true">AI</span>
            <h2 class="assistant-chat-title" :title="currentChatTitle">{{ currentChatTitle }}</h2>
          </div>
        </template>

        <div class="assistant-drawer-actions">
          <button
            v-if="!historyOpen"
            type="button"
            class="assistant-icon-button"
            aria-label="New chat"
            title="New chat"
            :disabled="pending"
            @click="assistantStore.newChat"
          >
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path d="M12 5v14M5 12h14" />
            </svg>
          </button>
          <button type="button" class="assistant-icon-button" aria-label="Close assistant" title="Close" @click="closeDrawer">
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path d="M6 6l12 12M18 6 6 18" />
            </svg>
          </button>
        </div>
      </header>

      <div v-if="historyOpen" class="assistant-history">
        <button
          type="button"
          class="assistant-new-chat-row"
          :disabled="pending"
          @click="assistantStore.newChat"
        >
          <span class="assistant-new-chat-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24">
              <path d="M12 5v14M5 12h14" />
            </svg>
          </span>
          <span>New chat</span>
        </button>

        <p v-if="historyLoading" class="assistant-history-empty">Loading chats...</p>
        <p v-else-if="chats.length === 0" class="assistant-history-empty">No previous chats yet.</p>

        <section v-for="group in groupedChats" v-else :key="group.label" class="assistant-history-group">
          <h3>{{ group.label }}</h3>
          <button
            v-for="chat in group.chats"
            :key="chat.id"
            type="button"
            class="assistant-history-item"
            :class="{ active: chat.id === assistantStore.conversationID }"
            :title="chat.title"
            @click="assistantStore.openChat(chat.id)"
          >
            <span class="assistant-history-title">{{ chat.title }}</span>
          </button>
        </section>
      </div>

      <div v-else ref="bodyRef" class="assistant-drawer-body">
        <div v-if="messages.length === 0" class="assistant-empty-state">
          <span class="assistant-avatar" aria-hidden="true">AI</span>
          <strong>How can I help?</strong>
          <span>Ask about services, deployments, events, or the Swarm cluster.</span>
        </div>

        <div v-else class="assistant-chat-list">
          <article
            v-for="(message, index) in messages"
            :key="`${message.role}-${index}`"
            class="assistant-chat-message"
            :class="`assistant-chat-message-${message.role}`"
          >
            <div class="assistant-chat-markdown" v-html="renderMessageText(message.role, message.text)" />
          </article>
        </div>
      </div>

      <form v-if="!historyOpen" class="assistant-chat-form" @submit.prevent="submitMessage">
        <textarea
          ref="inputRef"
          v-model="messageInput"
          rows="1"
          placeholder="Ask something..."
          :disabled="pending"
          @keydown="handleComposerKeydown"
        />
        <button
          id="assistant-chat-send"
          class="assistant-send-button"
          type="submit"
          aria-label="Send message"
          :disabled="pending || !messageInput.trim()"
        >
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M12 19V5M6 11l6-6 6 6" />
          </svg>
        </button>
      </form>
    </section>
  </div>
</template>
