<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";

import AppTable from "../common/AppTable.vue";
import { useAssistantStore } from "../../stores/assistant";
import { useOverviewStore } from "../../stores/overview";
import { useUIStore } from "../../stores/ui";
import { formatDate, shortCommitHash } from "../../utils/format";

const overviewStore = useOverviewStore();
const assistantStore = useAssistantStore();
const uiStore = useUIStore();
const dialog = ref<HTMLElement | null>(null);
const closeButton = ref<HTMLButtonElement | null>(null);
let returnFocus: HTMLElement | null = null;
const focusableSelector = [
  "a[href]", "button:not([disabled])", "input:not([disabled])", "select:not([disabled])",
  "textarea:not([disabled])", "[tabindex]:not([tabindex=\"-1\"])",
].join(", ");

const commitDetails = computed(() => overviewStore.commitDetailsData);
const currentCommitHash = computed(() => commitDetails.value?.full_hash || overviewStore.commitDetailsHash);
const shortHash = computed(() => {
  return shortCommitHash(currentCommitHash.value);
});
const titleText = computed(() => {
  const author = commitDetails.value?.author?.trim() || "unknown";
  if (!currentCommitHash.value.trim()) {
    return "Commit details";
  }

  return `Commit ${shortHash.value} by ${author}`;
});
const changedFilesText = computed(() => {
  const files = Array.isArray(commitDetails.value?.changed_files) ? commitDetails.value?.changed_files : [];
  if (!files || files.length === 0) {
    return "n/a";
  }

  return files.join(", ");
});
const commitMessageText = computed(() => {
  const message = commitDetails.value?.message?.trim();
  if (!message) {
    return "n/a";
  }

  return message;
});
const explainDisabled = computed(() => {
  return (
    !assistantStore.enabled ||
    assistantStore.pending ||
    !commitDetails.value ||
    !currentCommitHash.value.trim() ||
    overviewStore.commitDetailsLoading ||
    overviewStore.commitDetailsError.length > 0
  );
});

function closeCommitDetailsModal() {
  overviewStore.closeCommitDetailsModal();
}

async function explainWithAI() {
  const commitHash = currentCommitHash.value.trim();
  if (!commitHash || explainDisabled.value) {
    return;
  }

  returnFocus = null;
  closeCommitDetailsModal();
  assistantStore.newChat();
  uiStore.openAssistantDrawer();
  await assistantStore.sendMessage(`Explain changes in git commit: ${commitHash}`);
}

function handleEscape(event: KeyboardEvent) {
  if (event.key === "Escape" && overviewStore.commitDetailsModalOpen) {
    event.preventDefault();
    event.stopImmediatePropagation();
    closeCommitDetailsModal();
  }
}

function focusableElements(): HTMLElement[] {
  return dialog.value ? [...dialog.value.querySelectorAll<HTMLElement>(focusableSelector)] : [];
}

function handleKeydown(event: KeyboardEvent) {
  if (!overviewStore.commitDetailsModalOpen) return;
  if (event.key === "Escape") { handleEscape(event); return; }
  if (event.key !== "Tab") return;
  const focusable = focusableElements();
  if (focusable.length === 0) { event.preventDefault(); dialog.value?.focus(); return; }
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  const activeIndex = focusable.indexOf(document.activeElement as HTMLElement);
  if (activeIndex === -1 || (event.shiftKey && activeIndex === 0) || (!event.shiftKey && activeIndex === focusable.length - 1)) {
    event.preventDefault();
    (event.shiftKey ? last : first).focus();
  }
}

watch(() => overviewStore.commitDetailsModalOpen, async (open, wasOpen) => {
  if (open && !wasOpen) {
    returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    await nextTick();
    closeButton.value?.focus();
  } else if (!open && wasOpen) {
    await nextTick();
    if (returnFocus?.isConnected) returnFocus.focus();
    returnFocus = null;
  }
});

onMounted(() => {
  document.addEventListener("keydown", handleKeydown, true);
});

onUnmounted(() => {
  document.removeEventListener("keydown", handleKeydown, true);
});
</script>

<template>
  <div v-if="overviewStore.commitDetailsModalOpen" class="modal">
    <div class="modal-overlay" @click="closeCommitDetailsModal" />
    <div ref="dialog" class="modal-card" role="dialog" aria-modal="true" aria-labelledby="commit-details-title" tabindex="-1">
      <div class="modal-header commit-details-modal-header">
        <h2 id="commit-details-title">{{ titleText }}</h2>
        <button
          class="commit-header-icon-button commit-explain-button"
          type="button"
          aria-label="Explain commit"
          title="Explain commit"
          :disabled="explainDisabled"
          @click="explainWithAI"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="m12 3 1.15 3.1a4 4 0 0 0 2.35 2.35L18.6 9.6l-3.1 1.15a4 4 0 0 0-2.35 2.35L12 16.2l-1.15-3.1a4 4 0 0 0-2.35-2.35L5.4 9.6l3.1-1.15a4 4 0 0 0 2.35-2.35L12 3Z" />
            <path d="m18.5 15 .55 1.45L20.5 17l-1.45.55L18.5 19l-.55-1.45L16.5 17l1.45-.55L18.5 15Z" />
          </svg>
        </button>
        <button ref="closeButton" class="modal-close commit-header-icon-button" type="button" aria-label="Close modal" @click="closeCommitDetailsModal">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" aria-hidden="true"><path d="m7 7 10 10M17 7 7 17" /></svg>
        </button>
      </div>

      <div class="modal-body">
        <p v-if="overviewStore.commitDetailsLoading" class="meta">Loading commit details...</p>
        <p v-else-if="overviewStore.commitDetailsError" class="meta">
          Failed to load commit details: {{ overviewStore.commitDetailsError }}
        </p>
        <AppTable v-else summary aria-label="Commit details">
            <tr>
              <th scope="row">Commit Hash</th>
              <td>{{ currentCommitHash || "n/a" }}</td>
            </tr>
            <tr>
              <th scope="row">Author</th>
              <td>{{ commitDetails?.author || "n/a" }}</td>
            </tr>
            <tr>
              <th scope="row">Message</th>
              <td class="commit-details-message">{{ commitMessageText }}</td>
            </tr>
            <tr>
              <th scope="row">Date</th>
              <td>{{ formatDate(commitDetails?.date) }}</td>
            </tr>
            <tr>
              <th scope="row">Changed files</th>
              <td>{{ changedFilesText }}</td>
            </tr>
        </AppTable>
      </div>
    </div>
  </div>
</template>

<style scoped>
.commit-details-modal-header {
  padding-right: 92px;
}

.commit-header-icon-button {
  width: 36px;
  height: 36px;
  display: grid;
  place-items: center;
  padding: 0;
  border: 0;
  border-radius: 9px;
  background: transparent;
  color: var(--muted);
  transition: background-color 150ms ease, color 150ms ease, opacity 150ms ease;
}

.commit-header-icon-button svg {
  width: 20px;
  height: 20px;
}

.commit-header-icon-button:hover:not(:disabled) {
  background: var(--surface-hover-strong);
  color: var(--text);
  filter: none;
  transform: none;
}

.commit-header-icon-button:focus-visible {
  outline: 2px solid var(--accent-focus);
  outline-offset: 2px;
}

.commit-header-icon-button:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

.commit-header-icon-button:disabled:hover {
  background: transparent;
  filter: none;
  transform: none;
}

.commit-explain-button {
  position: absolute;
  z-index: 2;
  top: 8px;
  right: 52px;
}

@media (max-width: 650px) {
  .commit-details-modal-header {
    padding-right: 88px;
  }

  .commit-explain-button {
    top: 6px;
    right: 50px;
  }
}
</style>
