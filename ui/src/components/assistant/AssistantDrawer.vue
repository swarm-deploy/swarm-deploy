<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";

import { useAssistantStore } from "../../stores/assistant";
import { useUIStore } from "../../stores/ui";
import { renderAssistantMarkdown } from "../../utils/assistantMarkdown";
import { escapeHtml } from "../../utils/escape";

const uiStore = useUIStore();
const assistantStore = useAssistantStore();

const messageInput = ref("");
const inputRef = ref<HTMLInputElement | null>(null);
const bodyRef = ref<HTMLElement | null>(null);

const isOpen = computed(() => uiStore.assistantDrawerOpen && assistantStore.enabled);
const messages = computed(() => assistantStore.messages);
const pending = computed(() => assistantStore.pending);

watch(
  () => isOpen.value,
  async (opened) => {
    if (!opened) {
      return;
    }

    await nextTick();
    inputRef.value?.focus();
    bodyRef.value?.scrollTo({
      top: bodyRef.value.scrollHeight,
      behavior: "smooth",
    });
  },
);

watch(
  () => messages.value.length,
  async () => {
    await nextTick();
    bodyRef.value?.scrollTo({
      top: bodyRef.value.scrollHeight,
      behavior: "smooth",
    });
  },
);

function closeDrawer() {
  uiStore.closeAssistantDrawer();
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
        <h2>Assistant</h2>
        <button type="button" class="modal-close" @click="closeDrawer">x</button>
      </header>

      <div ref="bodyRef" class="assistant-drawer-body">
        <p v-if="messages.length === 0" class="meta">Assistant is ready.</p>
        <div v-else class="assistant-chat-list">
          <article
            v-for="(message, index) in messages"
            :key="`${message.role}-${index}`"
            class="assistant-chat-message"
            :class="`assistant-chat-message-${message.role}`"
          >
            <p class="assistant-chat-role">
              {{ message.role === "user" ? "You" : message.role === "assistant" ? "Assistant" : "System" }}
            </p>
            <div class="assistant-chat-markdown" v-html="renderMessageText(message.role, message.text)" />
          </article>
        </div>
      </div>

      <form class="assistant-chat-form" @submit.prevent="submitMessage">
        <input
          ref="inputRef"
          v-model="messageInput"
          type="text"
          placeholder="Ask about services, events, or sync issues..."
          autocomplete="off"
          :disabled="pending"
        />
        <button id="assistant-chat-send" type="submit" :disabled="pending">
          {{ pending ? "Sending..." : "Send" }}
        </button>
      </form>
    </section>
  </div>
</template>
