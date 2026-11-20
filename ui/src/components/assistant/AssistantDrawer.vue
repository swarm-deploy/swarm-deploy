<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import { getAssistantSuggestions } from "../../api/assistant";
import type { AssistantSuggestion } from "../../api/types";
import { useAssistantStore } from "../../stores/assistant";
import { useUIStore } from "../../stores/ui";
import { renderAssistantMarkdown } from "../../utils/assistantMarkdown";
import { escapeHtml } from "../../utils/escape";

const AssistantUsage = defineAsyncComponent(() => import("./AssistantUsage.vue"));
const tokenUsageIconPath = "M4 19V10M10 19V5M16 19v-7M22 19H2";

const uiStore = useUIStore();
const assistantStore = useAssistantStore();
const route = useRoute();
const router = useRouter();

const messageInput = ref("");
const inputRef = ref<HTMLTextAreaElement | null>(null);
const historyMessage = ref("");
const historyMessageRef = ref<HTMLTextAreaElement | null>(null);
const historyError = ref("");
const historySending = ref(false);
let historyRequest: { message: string; id: string } | undefined;
const searchOpen = ref(false);
const searchQuery = ref("");
const searchRef = ref<HTMLInputElement | null>(null);
const searchButtonRef = ref<HTMLButtonElement | null>(null);
const backButtonRef = ref<HTMLButtonElement | null>(null);
const viewportStyle = ref<Record<string, string>>();
const bodyRef = ref<HTMLElement | null>(null);
const suggestions = ref<AssistantSuggestion[]>([]);
let suggestionsGeneration = 0;

const isOpen = computed(() => uiStore.assistantDrawerOpen && assistantStore.enabled);
const messages = computed(() => assistantStore.messages);
const pending = computed(() => assistantStore.pending);
const activity = computed(() => assistantStore.activity);
const chats = computed(() => assistantStore.chats);
const usageOpen = computed(() => isOpen.value && route.query.assistant === "usage");
const historyOpen = computed(() => assistantStore.historyOpen && !usageOpen.value);
const chatOpen = computed(() => !historyOpen.value && !usageOpen.value);
const historyLoading = computed(() => assistantStore.historyLoading);
const usageRoute = computed(() => ({ query: assistantQuery("usage"), hash: route.hash }));
const usageOpenChatID = ref("");

const existingChatLoading = computed(() => {
  const assistant = typeof route.query.assistant === "string" ? route.query.assistant : "";
  return assistant !== "" && assistant !== "new" && assistant !== "chats" && assistantStore.conversationID !== assistant;
});

const emptyChatVisible = computed(
  () => isOpen.value && chatOpen.value && messages.value.length === 0 && !existingChatLoading.value,
);

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

const currentChatDateLabel = computed(() => {
  const persisted = chats.value.find((chat) => chat.id === assistantStore.conversationID);
  const createdAt = persisted?.created_at ? new Date(persisted.created_at) : new Date();

  if (Number.isNaN(createdAt.getTime())) {
    return "";
  }

  const now = new Date();
  const isToday =
    createdAt.getFullYear() === now.getFullYear() &&
    createdAt.getMonth() === now.getMonth() &&
    createdAt.getDate() === now.getDate();

  if (isToday) {
    return "Today";
  }

  const options: Intl.DateTimeFormatOptions = {
    day: "numeric",
    month: "long",
  };
  if (createdAt.getFullYear() !== now.getFullYear()) {
    options.year = "numeric";
  }

  return createdAt.toLocaleDateString(undefined, options);
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
  const query = searchQuery.value.trim().toLocaleLowerCase();

  for (const chat of chats.value) {
    if (query && !chat.title.toLocaleLowerCase().includes(query)) {
      continue;
    }

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
    if (chatOpen.value && route.query.assistant !== "chats") {
      inputRef.value?.focus();
    }
    scrollToBottom();
  },
);

watch(usageOpen, (opened) => {
  if (opened) backButtonRef.value?.focus();
}, { flush: "post" });

watch(isOpen, (opened, _, onCleanup) => {
  const viewport = window.visualViewport;
  if (!opened || !viewport) {
    return;
  }

  function updateViewport() {
    if (viewport && viewport.scale === 1) {
      viewportStyle.value = {
        "--assistant-viewport-top": `${viewport.offsetTop}px`,
        "--assistant-viewport-height": `${viewport.height}px`,
      };
    } else {
      viewportStyle.value = undefined;
    }
  }

  updateViewport();
  viewport.addEventListener("resize", updateViewport);
  viewport.addEventListener("scroll", updateViewport);
  onCleanup(() => {
    viewport.removeEventListener("resize", updateViewport);
    viewport.removeEventListener("scroll", updateViewport);
    viewportStyle.value = undefined;
  });
}, { immediate: true });

watch([historyMessage, historyMessageRef], () => {
  const input = historyMessageRef.value;
  if (input && !window.CSS?.supports("field-sizing", "content")) {
    input.style.height = "auto";
    input.style.height = `${input.scrollHeight + input.offsetHeight - input.clientHeight}px`;
  }
}, { flush: "post" });

watch(
  () => messages.value.length,
  async () => {
    await nextTick();
    scrollToBottom();
  },
);

watch(
  emptyChatVisible,
  async (visible) => {
    const generation = ++suggestionsGeneration;
    suggestions.value = [];
    if (!visible) {
      return;
    }

    try {
      const response = await getAssistantSuggestions();
      if (generation === suggestionsGeneration && emptyChatVisible.value) {
        suggestions.value = response.suggestions || [];
      }
    } catch {
      // Suggested prompts are optional; the composer remains fully functional.
    }
  },
  { immediate: true },
);

function scrollToBottom() {
  bodyRef.value?.scrollTo({
    top: bodyRef.value.scrollHeight,
    behavior: "smooth",
  });
}

function toggleChatUsage(chatID: string) {
  usageOpenChatID.value = usageOpenChatID.value === chatID ? "" : chatID;
}

function formatTokens(value: number | undefined): string {
  if (value === undefined) {
    return "—";
  }

  return new Intl.NumberFormat().format(value);
}

function assistantQuery(value: string | undefined) {
  const query = { ...route.query };
  if (value) {
    query.assistant = value;
  } else {
    delete query.assistant;
  }
  return query;
}

async function openHistory() {
  await router.push({ query: assistantQuery("chats"), hash: route.hash });
}

async function openChat(chatID: string) {
  if (pending.value) {
    return;
  }

  await router.push({ query: assistantQuery(chatID) });
}

async function newChat() {
  if (pending.value) {
    return;
  }

  await router.push({ query: assistantQuery("new") });
}

async function closeDrawer() {
  usageOpenChatID.value = "";
  uiStore.closeAssistantDrawer();
  await router.push({ query: assistantQuery(undefined), hash: route.hash });
}

async function toggleSearch() {
  searchOpen.value = !searchOpen.value;
  searchQuery.value = "";
  usageOpenChatID.value = "";
  await nextTick();
  if (searchOpen.value) {
    searchRef.value?.focus();
  } else {
    searchButtonRef.value?.focus();
  }
}

function clearSearch() {
  searchQuery.value = "";
  searchRef.value?.focus();
}

function handleEscape() {
  if (searchOpen.value) {
    void toggleSearch();
  } else {
    void closeDrawer();
  }
}

watch(
  () => route.query.assistant,
  async (assistant) => {
    const value = typeof assistant === "string" ? assistant : "";

    if (value !== "chats") {
      searchOpen.value = false;
      searchQuery.value = "";
    }

    if (!value) {
      uiStore.closeAssistantDrawer();
      return;
    }

    uiStore.openAssistantDrawer();

    if (value === "usage") return;

    if (value === "chats") {
      if (!assistantStore.historyOpen) {
        await assistantStore.toggleHistory();
      }
      return;
    }

    if (value === "new") {
      assistantStore.newChat();
      return;
    }

    if (assistantStore.conversationID !== value || assistantStore.historyOpen) {
      await assistantStore.openChat(value);
    }
  },
  { immediate: true },
);

async function submitHistoryMessage() {
  const message = historyMessage.value.trim();
  if (!message || pending.value) {
    return;
  }

  historyError.value = "";
  // Reuse the request ID when a transport error leaves the outcome unknown.
  if (historyRequest?.message !== message) {
    historyRequest = {
      message,
      id: crypto.getRandomValues(new Uint32Array(4)).join("-"),
    };
  }

  try {
    historySending.value = true;
    const response = await assistantStore.sendFirstMessage(message, historyRequest.id);
    historyRequest = undefined;
    if (response.status !== "completed" || !response.conversation_id) {
      historyError.value = response.error_message || "Could not start a chat. Try again.";
      return;
    }

    historyMessage.value = "";
    if (route.query.assistant === "chats") {
      assistantStore.newChat();
      assistantStore.conversationID = response.conversation_id;
      assistantStore.pushMessage("user", message);
      assistantStore.pushMessage(
        "assistant",
        response.answer || "Assistant returned empty answer.",
        response.activity?.length ? response.activity : undefined,
      );
      await router.push({ query: assistantQuery(response.conversation_id) });
    }
  } catch (error) {
    historyError.value = error instanceof Error ? error.message : "Could not start a chat. Try again.";
  } finally {
    historySending.value = false;
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

async function submitSuggestion(suggestion: AssistantSuggestion) {
  if (pending.value) {
    return;
  }

  await assistantStore.sendMessage(suggestion.prompt);
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
  <div
    class="assistant-drawer-layer"
    :class="{ open: isOpen, pinned: uiStore.assistantPinned }"
    :style="viewportStyle"
    @keydown.esc.prevent="handleEscape"
  >
    <div class="assistant-drawer-overlay" @click="closeDrawer" />
    <section class="assistant-drawer" aria-label="Assistant Drawer" :inert="!isOpen">
      <header class="assistant-drawer-header">
        <template v-if="historyOpen">
          <div v-if="searchOpen" class="assistant-history-search">
            <input
              ref="searchRef"
              v-model="searchQuery"
              type="search"
              placeholder="Search chats"
              aria-label="Search chats"
            />
            <button
              v-if="searchQuery"
              type="button"
              class="assistant-icon-button"
              aria-label="Clear search"
              title="Clear search"
              @click="clearSearch"
            >
              <svg viewBox="0 0 24 24" aria-hidden="true">
                <path d="M6 6l12 12M18 6 6 18" />
              </svg>
            </button>
          </div>
          <div v-else class="assistant-header-identity">
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
              ref="backButtonRef"
              type="button"
              class="assistant-icon-button"
              aria-label="Back to chats"
              title="Back to chats"
              @click="openHistory"
            >
              <svg viewBox="0 0 24 24" aria-hidden="true">
                <path d="m15 18-6-6 6-6" />
              </svg>
            </button>
            <h2 v-if="usageOpen" id="assistant-usage-title" class="assistant-chat-title">Usage</h2>
            <template v-else>
              <span class="assistant-avatar assistant-avatar-small" aria-hidden="true">AI</span>
              <h2 class="assistant-chat-title" :title="currentChatTitle">{{ currentChatTitle }}</h2>
            </template>
          </div>
        </template>

        <div class="assistant-drawer-actions">
          <RouterLink
            v-if="historyOpen"
            :to="usageRoute"
            class="assistant-icon-button"
            aria-label="Assistant usage"
            title="Usage"
          >
            <svg class="assistant-usage-icon" viewBox="0 0 24 24" aria-hidden="true">
              <path :d="tokenUsageIconPath" />
            </svg>
          </RouterLink>
          <button
            v-if="historyOpen"
            ref="searchButtonRef"
            type="button"
            class="assistant-icon-button assistant-search-button"
            :aria-expanded="searchOpen"
            :aria-label="searchOpen ? 'Close search' : 'Search chats'"
            :title="searchOpen ? 'Close search' : 'Search chats'"
            @click="toggleSearch"
          >
            <svg v-if="searchOpen" viewBox="0 0 24 24" aria-hidden="true">
              <path d="m15 18-6-6 6-6" />
            </svg>
            <svg v-else viewBox="0 0 24 24" aria-hidden="true">
              <circle cx="10.5" cy="10.5" r="5.5" />
              <path d="m15 15 4 4" />
            </svg>
          </button>
          <button
            v-if="chatOpen"
            type="button"
            class="assistant-icon-button"
            aria-label="New chat"
            title="New chat"
            :disabled="pending"
            @click="newChat"
          >
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path d="M12 5v14M5 12h14" />
            </svg>
          </button>
          <button
            type="button"
            class="assistant-icon-button assistant-pin-button"
            :class="{ active: uiStore.assistantPinned }"
            :aria-pressed="uiStore.assistantPinned"
            :aria-label="uiStore.assistantPinned ? 'Unpin assistant' : 'Pin assistant'"
            :title="uiStore.assistantPinned ? 'Use overlay mode' : 'Keep assistant open while navigating'"
            @click="uiStore.toggleAssistantPinned"
          >
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path d="M9 3h6l-1 5 3 3v2H7v-2l3-3-1-5Z" />
              <path d="M12 13v8" />
            </svg>
          </button>
          <button type="button" class="assistant-icon-button" aria-label="Close assistant" title="Close" @click="closeDrawer">
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path d="M6 6l12 12M18 6 6 18" />
            </svg>
          </button>
        </div>
      </header>

      <AssistantUsage v-if="usageOpen" class="assistant-drawer-body" />
      <div v-else-if="historyOpen" class="assistant-history">
        <button
          type="button"
          class="assistant-new-chat-row"
          :disabled="pending"
          @click="newChat"
        >
          <span class="assistant-new-chat-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24">
              <path d="M12 5v14M5 12h14" />
            </svg>
          </span>
          <span>New chat</span>
        </button>

        <p v-if="historyLoading" class="assistant-history-empty">Loading chats...</p>
        <p v-else-if="groupedChats.length === 0" class="assistant-history-empty">
          {{ searchQuery.trim() ? "No matching chats." : "No previous chats yet." }}
        </p>

        <section v-for="group in groupedChats" v-else :key="group.label" class="assistant-history-group">
          <h3>{{ group.label }}</h3>
          <div
            v-for="chat in group.chats"
            :key="chat.id"
            class="assistant-history-item"
          >
            <button
              type="button"
              class="assistant-history-open"
              :title="chat.title"
              @click="openChat(chat.id)"
            >
              <span class="assistant-history-title">{{ chat.title }}</span>
            </button>
            <div class="assistant-history-usage">
              <button
                type="button"
                class="assistant-history-stats"
                :class="{ active: usageOpenChatID === chat.id }"
                :aria-expanded="usageOpenChatID === chat.id"
                aria-label="Token usage"
                title="Token usage"
                @click.stop="toggleChatUsage(chat.id)"
              >
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <path :d="tokenUsageIconPath" />
                </svg>
              </button>
              <div v-if="usageOpenChatID === chat.id" class="assistant-history-usage-popover">
                <strong>Token usage</strong>
                <dl>
                  <div>
                    <dt>Total</dt>
                    <dd>{{ formatTokens(chat.token_usage?.total_tokens) }}</dd>
                  </div>
                  <div>
                    <dt>Input</dt>
                    <dd>{{ formatTokens(chat.token_usage?.input_tokens) }}</dd>
                  </div>
                  <div>
                    <dt>Output</dt>
                    <dd>{{ formatTokens(chat.token_usage?.output_tokens) }}</dd>
                  </div>
                </dl>
              </div>
            </div>
          </div>
        </section>
      </div>

      <div
        v-else
        ref="bodyRef"
        class="assistant-drawer-body"
      >
        <div v-if="messages.length === 0 && !existingChatLoading" class="assistant-empty-state">
          <span class="assistant-avatar" aria-hidden="true">AI</span>
          <strong>How can I help?</strong>
          <span>Ask about services, deployments, events, or the Swarm cluster.</span>
          <div v-if="suggestions.length > 0" class="assistant-suggestions" aria-label="Suggested prompts">
            <button
              v-for="suggestion in suggestions"
              :key="suggestion.id"
              type="button"
              class="assistant-suggestion"
              :disabled="pending"
              @click="submitSuggestion(suggestion)"
            >
              <span>{{ suggestion.title }}</span>
              <svg viewBox="0 0 24 24" aria-hidden="true">
                <path d="m9 18 6-6-6-6" />
              </svg>
            </button>
          </div>
        </div>

        <div v-else class="assistant-chat-list">
          <div v-if="currentChatDateLabel" class="assistant-chat-date">{{ currentChatDateLabel }}</div>
          <article
            v-for="(message, index) in messages"
            :key="`${message.role}-${index}`"
            class="assistant-chat-message"
            :class="`assistant-chat-message-${message.role}`"
          >
            <div class="assistant-chat-markdown" v-html="renderMessageText(message.role, message.text)" />
            <details v-if="message.activity?.length" class="assistant-activity-details">
              <summary>Activity</summary>
              <ol>
                <li v-for="(item, activityIndex) in message.activity" :key="activityIndex + '-' + item">
                  {{ item }}
                </li>
              </ol>
            </details>
          </article>
          <div v-if="pending" class="assistant-activity-live" role="status" aria-live="polite">
            <span class="assistant-activity-spinner" aria-hidden="true" />
            <div>
              <strong>Working</strong>
              <span>{{ activity[activity.length - 1] || "Analyzing request" }}</span>
            </div>
          </div>
        </div>
      </div>

      <form
        v-if="historyOpen && !searchOpen"
        class="assistant-history-form"
        :aria-busy="historySending"
        @submit.prevent="submitHistoryMessage"
      >
        <p v-if="historyError" class="assistant-history-error" role="alert">{{ historyError }}</p>
        <div class="assistant-composer">
          <textarea
            ref="historyMessageRef"
            v-model="historyMessage"
            rows="1"
            placeholder="Ask the assistant…"
            aria-label="Ask the assistant"
            :readonly="pending"
            @input="historyError = ''"
          />
          <button
            v-if="historyMessage.trim()"
            class="assistant-send-button"
            type="submit"
            :aria-label="historySending ? 'Sending message' : 'Send message'"
            :disabled="pending"
          >
            <span v-if="historySending" class="assistant-activity-spinner" aria-hidden="true" />
            <svg v-else viewBox="0 0 24 24" aria-hidden="true">
              <path d="M12 19V5M6 11l6-6 6 6" />
            </svg>
          </button>
        </div>
      </form>

      <form v-if="chatOpen" class="assistant-chat-form" @submit.prevent="submitMessage">
        <div class="assistant-composer">
          <textarea
            ref="inputRef"
            v-model="messageInput"
            rows="1"
            placeholder="Ask the assistant…"
            aria-label="Ask the assistant"
            :disabled="pending"
            @keydown="handleComposerKeydown"
          />
          <button
            v-if="messageInput.trim()"
            id="assistant-chat-send"
            class="assistant-send-button"
            type="submit"
            aria-label="Send message"
            :disabled="pending"
          >
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path d="M12 19V5M6 11l6-6 6 6" />
            </svg>
          </button>
        </div>
      </form>
    </section>
  </div>
</template>
