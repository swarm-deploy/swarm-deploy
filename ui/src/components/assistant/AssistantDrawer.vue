<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import { openNewAssistantChat } from "../../router/assistant";
import { getAssistantSuggestions } from "../../api/assistant";
import { formatNumber, locale, t } from "../../i18n";
import type { AssistantSelectionOption, AssistantSuggestion } from "../../api/types";
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
const pageVisible = ref(document.visibilityState === "visible");
const newReplySequence = ref<number>();
const latestReplySequence = computed(
  () => messages.value.filter((message) => message.role === "assistant").at(-1)?.sequence,
);
const suggestions = ref<AssistantSuggestion[]>([]);
let suggestionsGeneration = 0;

const isOpen = computed(() => uiStore.assistantDrawerOpen && assistantStore.enabled);
const messages = computed(() => assistantStore.messages);
const pending = computed(() => assistantStore.pending);
const activity = computed(() => assistantStore.activity);
// A question followed by another question has no reply; the last question has none unless a reply is running.
// Client errors are shown as system messages, so they are not marked again.
function replyMissing(index: number): boolean {
  if (messages.value[index]?.role !== "user") {
    return false;
  }
  const next = messages.value[index + 1];
  return next ? next.role === "user" : !pending.value;
}

function selectionResponse(index: number): string | undefined {
  return messages.value.slice(index + 1).find((message) => message.role === "user")?.text;
}

function selectionDisabled(index: number): boolean {
  return pending.value || selectionResponse(index) !== undefined;
}

function selectionSelected(index: number, option: AssistantSelectionOption): boolean {
  return selectionResponse(index) === option.value;
}
const chats = computed(() => assistantStore.chats);
const usageOpen = computed(() => isOpen.value && route.query.assistant === "usage");
const historyOpen = computed(() => assistantStore.historyOpen && !usageOpen.value);
const chatOpen = computed(() => !historyOpen.value && !usageOpen.value);
const historyLoading = computed(() => assistantStore.historyLoading);
const usageRoute = computed(() => ({ query: assistantQuery("usage"), hash: route.hash }));
const usageOpenChatID = ref("");

const loadingChatID = ref("");
const existingChatLoading = computed(() => {
  const assistant = typeof route.query.assistant === "string" ? route.query.assistant : "";
  return assistant !== "" && assistant !== "new" && assistant !== "chats" &&
    (assistantStore.conversationID !== assistant || loadingChatID.value === assistant);
});

const composerDisabled = computed(() => pending.value || existingChatLoading.value);

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
    return t('assistant.newChat');
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
    return t('assistant.today');
  }

  const options: Intl.DateTimeFormatOptions = {
    day: "numeric",
    month: "long",
  };
  if (createdAt.getFullYear() !== now.getFullYear()) {
    options.year = "numeric";
  }

  return createdAt.toLocaleDateString(locale(), options);
});

const groupedChats = computed(() => {
  const groups = [
    { label: t('assistant.today'), chats: [] as typeof chats.value },
    { label: t('assistant.lastSevenDays'), chats: [] as typeof chats.value },
    { label: t('assistant.earlier'), chats: [] as typeof chats.value },
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
      focusComposer();
    }
    if (chatOpen.value && bodyRef.value) scrollToBottom(bodyRef.value);
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

watch(bodyRef, (body) => {
  if (body) scrollToBottom(body);
}, { flush: "post" });

watch([isOpen, chatOpen, () => route.query.assistant], () => {
  newReplySequence.value = undefined;
});

watch(
  [() => assistantStore.conversationID, messages, () => messages.value.length],
  async ([id, current, length], [previousID, previous, previousLength]) => {
    const body = bodyRef.value;
    if (!body) return;

    const atBottom = body.scrollHeight - body.clientHeight - body.scrollTop <= 1;
    const latest = current.at(-1);
    if (current === previous && id === previousID && length > previousLength && latest?.role === "assistant" && !atBottom) {
      newReplySequence.value = latest.sequence;
    }
    if (id !== previousID || atBottom) {
      await nextTick();
      if (body === bodyRef.value && current === messages.value) scrollToBottom(body);
    }
  },
);

watch(isOpen, (opened, _, onCleanup) => {
  if (!opened) return;
  const update = () => { pageVisible.value = document.visibilityState === "visible"; };
  update();
  document.addEventListener("visibilitychange", update);
  onCleanup(() => document.removeEventListener("visibilitychange", update));
}, { immediate: true });

const showingConversationID = computed(() => {
  const id = assistantStore.conversationID;
  return isOpen.value && chatOpen.value && !existingChatLoading.value && route.query.assistant === id ? id : "";
});

watch(
  [bodyRef, showingConversationID, latestReplySequence, pageVisible],
  ([body, id, sequence], _, onCleanup) => {
    if (!body || !id || sequence === undefined || !globalThis.IntersectionObserver) return;
    const reply = findReply(body, sequence);
    if (!reply) return;

    let observing = true;
    const observer = new IntersectionObserver(([entry]) => {
      if (!observing || !entry?.isIntersecting || entry.intersectionRatio <= 0 ||
          id !== showingConversationID.value || sequence !== latestReplySequence.value) return;

      if (newReplySequence.value === sequence) newReplySequence.value = undefined;
      if (document.visibilityState === "visible" && assistantStore.unreadReplySequence === sequence) {
        void assistantStore.markReplyRead(id, sequence);
      }
    }, { root: body, threshold: Number.EPSILON });
    observer.observe(reply);
    onCleanup(() => {
      observing = false;
      observer.disconnect();
    });
  },
  { flush: "post" },
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

function scrollToBottom(body: HTMLElement) {
  body.scrollTop = body.scrollHeight;
}

function focusComposer() {
  if (!window.matchMedia("(pointer: coarse)").matches) {
    inputRef.value?.focus();
  }
}

function findReply(body: HTMLElement, sequence: number) {
  return body.querySelector<HTMLElement>(`[data-reply-sequence="${sequence}"]`);
}

function scrollToNewReply() {
  const body = bodyRef.value;
  const sequence = newReplySequence.value;
  if (!body || sequence === undefined) return;
  const reply = findReply(body, sequence);
  if (!reply) return;
  body.scrollTop += reply.getBoundingClientRect().top - body.getBoundingClientRect().top;
}

function toggleChatUsage(chatID: string) {
  usageOpenChatID.value = usageOpenChatID.value === chatID ? "" : chatID;
}

function formatTokens(value: number | undefined): string {
  if (value === undefined) {
    return "—";
  }

  return formatNumber(value);
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
  if (pending.value && assistantStore.conversationID !== chatID) {
    return;
  }

  await router.push({ query: assistantQuery(chatID) });
}

async function newChat() {
  if (pending.value) {
    return;
  }

  await openNewAssistantChat(router);
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
  async (assistant, _, onCleanup) => {
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
      } else {
        await assistantStore.loadChats();
      }
      return;
    }

    if (value === "new") {
      assistantStore.newChat();
      assistantStore.historyOpen = false;
      return;
    }

    // An unread row may refer to a newer answer generated while this chat was cached.
    const unreadInList = historyOpen.value && !pending.value && chats.value.some((chat) => chat.id === value && chat.unread);
    if (assistantStore.conversationID === value && !assistantStore.chatHistoryStale && !unreadInList) {
      assistantStore.historyOpen = false;
    } else {
      const controller = new AbortController();
      loadingChatID.value = value;
      onCleanup(() => {
        controller.abort();
        loadingChatID.value = "";
      });
      try {
        await assistantStore.openChat(value, controller.signal);
      } catch (error) {
        if (!controller.signal.aborted) throw error;
      } finally {
        if (!controller.signal.aborted) loadingChatID.value = "";
      }
    }
  },
  { immediate: true },
);

async function submitHistoryMessage() {
  const message = historyMessage.value.trim();
  if (!message || historySending.value || composerDisabled.value) {
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
    const origin = router.currentRoute.value;
    const response = await assistantStore.createChat(message, historyRequest.id);
    historyRequest = undefined;
    if (!response.conversation_id || response.status === "disabled") {
      historyError.value = response.error_message || t('assistant.startChatFailed');
      return;
    }
    historyMessage.value = "";
    if (router.currentRoute.value === origin) {
      assistantStore.openCreatedChat(message, response);
      await router.push({ query: assistantQuery(response.conversation_id), hash: route.hash });
    } else if (historyOpen.value) {
      await assistantStore.loadChats();
    }
  } catch (error) {
    historyError.value = error instanceof Error ? error.message : t('assistant.startChatFailed');
  } finally {
    historySending.value = false;
  }
}

watch(
  () => assistantStore.conversationID,
  (conversationID, previousID) => {
    if (conversationID && !previousID && pending.value && route.query.assistant === "new") {
      void router.replace({ query: assistantQuery(conversationID), hash: route.hash });
    }
  },
);

async function submitMessage() {
  const text = messageInput.value.trim();
  if (!text || composerDisabled.value) {
    return;
  }

  messageInput.value = "";
  await assistantStore.sendMessage(text);
  await nextTick();
  focusComposer();
}

async function submitSuggestion(suggestion: AssistantSuggestion) {
  if (composerDisabled.value) {
    return;
  }

  await assistantStore.sendMessage(suggestion.prompt);
  await nextTick();
  focusComposer();
}

async function submitSelection(index: number, option: AssistantSelectionOption) {
  if (selectionDisabled(index)) {
    return;
  }

  await assistantStore.sendMessage(option.value);
  await nextTick();
  focusComposer();
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
    <section class="assistant-drawer" :aria-label="t('assistant.drawer')" :inert="!isOpen">
      <header class="assistant-drawer-header">
        <template v-if="historyOpen">
          <div v-if="searchOpen" class="assistant-history-search">
            <input
              ref="searchRef"
              :value="searchQuery"
              @input="searchQuery = ($event.target as HTMLInputElement).value"
              type="search"
              :placeholder="t('assistant.searchChats')"
              :aria-label="t('assistant.searchChats')"
            />
            <button
              v-if="searchQuery"
              type="button"
              class="assistant-icon-button"
              :aria-label="t('assistant.clearSearch')"
              :title="t('assistant.clearSearch')"
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
              <h2>{{ t('assistant.name') }}</h2>
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
              :aria-label="t('assistant.backToChats')"
              :title="t('assistant.backToChats')"
              @click="openHistory"
            >
              <svg viewBox="0 0 24 24" aria-hidden="true">
                <path d="m15 18-6-6 6-6" />
              </svg>
            </button>
            <h2 v-if="usageOpen" id="assistant-usage-title" class="assistant-chat-title">{{ t('assistant.usage') }}</h2>
            <template v-else>
              <span class="assistant-avatar assistant-avatar-small" aria-hidden="true">AI</span>
              <h2 class="assistant-chat-title" :title="currentChatTitle">{{ currentChatTitle }}</h2>
            </template>
          </div>
        </template>

        <div class="assistant-drawer-actions">
          <button
            v-if="historyOpen"
            ref="searchButtonRef"
            type="button"
            class="assistant-icon-button assistant-search-button"
            :aria-expanded="searchOpen"
            :aria-label="searchOpen ? t('assistant.closeSearch') : t('assistant.searchChats')"
            :title="searchOpen ? t('assistant.closeSearch') : t('assistant.searchChats')"
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
          <RouterLink
            v-if="historyOpen"
            :to="usageRoute"
            class="assistant-icon-button"
            :aria-label="t('assistant.assistantUsage')"
            :title="t('assistant.usage')"
          >
            <svg class="assistant-usage-icon" viewBox="0 0 24 24" aria-hidden="true">
              <path :d="tokenUsageIconPath" />
            </svg>
          </RouterLink>
          <button
            v-if="chatOpen"
            type="button"
            class="assistant-icon-button"
            :aria-label="t('assistant.newChat')"
            :title="t('assistant.newChat')"
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
            :aria-label="uiStore.assistantPinned ? t('assistant.unpin') : t('assistant.pin')"
            :title="uiStore.assistantPinned ? t('assistant.overlayMode') : t('assistant.keepOpen')"
            @click="uiStore.toggleAssistantPinned"
          >
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path d="M9 3h6l-1 5 3 3v2H7v-2l3-3-1-5Z" />
              <path d="M12 13v8" />
            </svg>
          </button>
          <button type="button" class="assistant-icon-button" :aria-label="t('assistant.close')" :title="t('common.close')" @click="closeDrawer">
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
          <span>{{ t('assistant.newChat') }}</span>
        </button>

        <p v-if="historyLoading" class="assistant-history-empty">{{ t('assistant.loadingChats') }}</p>
        <p v-else-if="groupedChats.length === 0" class="assistant-history-empty">
          {{ searchQuery.trim() ? t('assistant.noMatchingChats') : t('assistant.noPreviousChats') }}
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
              :aria-label="chat.unread ? t('assistant.unreadChat', { title: chat.title }) : undefined"
              @click="openChat(chat.id)"
            >
              <span v-if="chat.unread" class="assistant-history-unread-dot" aria-hidden="true" />
              <span class="assistant-history-title" :class="{ unread: chat.unread }">{{ chat.title }}</span>
            </button>
            <div class="assistant-history-usage">
              <button
                type="button"
                class="assistant-history-stats"
                :class="{ active: usageOpenChatID === chat.id }"
                :aria-expanded="usageOpenChatID === chat.id"
                :aria-label="t('assistant.tokenUsage')"
                :title="t('assistant.tokenUsage')"
                @click.stop="toggleChatUsage(chat.id)"
              >
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <path :d="tokenUsageIconPath" />
                </svg>
              </button>
              <div v-if="usageOpenChatID === chat.id" class="assistant-history-usage-popover">
                <strong>{{ t('assistant.tokenUsage') }}</strong>
                <dl>
                  <div>
                    <dt>{{ t('assistant.total') }}</dt>
                    <dd>{{ formatTokens(chat.token_usage?.total_tokens) }}</dd>
                  </div>
                  <div>
                    <dt>{{ t('assistant.input') }}</dt>
                    <dd>{{ formatTokens(chat.token_usage?.input_tokens) }}</dd>
                  </div>
                  <div>
                    <dt>{{ t('assistant.output') }}</dt>
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
          <strong>{{ t('assistant.howCanIHelp') }}</strong>
          <span>{{ t('assistant.emptyHint') }}</span>
          <div v-if="suggestions.length > 0" class="assistant-suggestions" :aria-label="t('assistant.suggestions')">
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
          <template v-for="(message, index) in messages" :key="`${message.role}-${index}`">
            <article class="assistant-chat-message" :class="`assistant-chat-message-${message.role}`">
              <div
                class="assistant-chat-markdown"
                :data-reply-sequence="message.role === 'assistant' ? message.sequence : undefined"
                v-html="renderMessageText(message.role, message.text)"
              />
              <div
                v-if="message.selection?.options.length"
                class="assistant-selection"
                role="group"
                :aria-label="t('assistant.responseOptions')"
              >
                <button
                  v-for="option in message.selection.options"
                  :key="option.value"
                  type="button"
                  class="assistant-selection-option"
                  :class="{ selected: selectionSelected(index, option) }"
                  :disabled="selectionDisabled(index)"
                  @click="submitSelection(index, option)"
                >
                  <strong>{{ option.label }}</strong>
                  <span v-if="option.description">{{ option.description }}</span>
                </button>
              </div>
              <details v-if="message.activity?.length" class="assistant-activity-details">
                <summary>{{ t('assistant.activity') }}</summary>
                <ol>
                  <li v-for="(item, activityIndex) in message.activity" :key="activityIndex + '-' + item">
                    {{ item }}
                  </li>
                </ol>
              </details>
            </article>
            <article v-if="replyMissing(index)" class="assistant-chat-message assistant-chat-message-system">
              <p class="assistant-chat-text">{{ t('assistant.noReply') }}</p>
            </article>
          </template>
          <div v-if="pending" class="assistant-activity-live" role="status" aria-live="polite">
            <span class="assistant-activity-spinner" aria-hidden="true" />
            <div>
              <strong>{{ t('assistant.working') }}</strong>
              <span>{{ activity[activity.length - 1] || t('assistant.analyzing') }}</span>
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
            :value="historyMessage"
            rows="1"
            :placeholder="t('assistant.placeholder')"
            :aria-label="t('assistant.ask')"
            :readonly="composerDisabled || historySending"
            @input="historyMessage = ($event.target as HTMLTextAreaElement).value; historyError = ''"
          />
          <button
            v-if="historyMessage.trim()"
            class="assistant-send-button"
            type="submit"
            :aria-label="historySending ? t('assistant.sending') : t('assistant.send')"
            :disabled="composerDisabled || historySending"
          >
            <span v-if="historySending" class="assistant-activity-spinner" aria-hidden="true" />
            <svg v-else viewBox="0 0 24 24" aria-hidden="true">
              <path d="M12 19V5M6 11l6-6 6 6" />
            </svg>
          </button>
        </div>
      </form>

      <form v-if="chatOpen" class="assistant-chat-form" @submit.prevent="submitMessage">
        <button
          v-if="newReplySequence !== undefined && showingConversationID"
          type="button"
          class="assistant-new-reply"
          @click="scrollToNewReply"
        >
          {{ t('assistant.newReply') }}
        </button>
        <div class="assistant-composer">
          <textarea
            ref="inputRef"
            :value="messageInput"
            @input="messageInput = ($event.target as HTMLTextAreaElement).value"
            rows="1"
            :placeholder="t('assistant.placeholder')"
            :aria-label="t('assistant.ask')"
            :disabled="composerDisabled"
          />
          <button
            v-if="messageInput.trim()"
            id="assistant-chat-send"
            class="assistant-send-button"
            type="submit"
            :aria-label="t('assistant.send')"
            :disabled="composerDisabled"
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
