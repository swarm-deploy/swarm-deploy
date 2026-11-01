<script setup lang="ts">
import { computed, ref } from "vue";

import AppModal from "../common/AppModal.vue";
import AppTable from "../common/AppTable.vue";
import { useAssistantStore } from "../../stores/assistant";
import { useOverviewStore } from "../../stores/overview";
import { useUIStore } from "../../stores/ui";
import { formatDate, shortCommitHash } from "../../utils/format";

const overviewStore = useOverviewStore();
const assistantStore = useAssistantStore();
const uiStore = useUIStore();
const modal = ref<InstanceType<typeof AppModal> | null>(null);

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

  modal.value?.forgetReturnFocus();
  closeCommitDetailsModal();
  assistantStore.newChat();
  uiStore.openAssistantDrawer();
  await assistantStore.sendMessage(`Explain changes in git commit: ${commitHash}`);
}
</script>

<template>
  <AppModal
    :open="overviewStore.commitDetailsModalOpen"
    labelledby="commit-details-title"
    header-class="commit-details-modal-header"
    ref="modal"
    close-class="commit-header-icon-button"
    @close="closeCommitDetailsModal"
  >
    <template #title>
      <h2 id="commit-details-title">{{ titleText }}</h2>
    </template>
    <template #actions>
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
    </template>

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
  </AppModal>
</template>

<style scoped>
:deep(.commit-details-modal-header) {
  padding-right: 92px;
}

:deep(.commit-header-icon-button) {
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

:deep(.commit-header-icon-button) svg {
  width: 20px;
  height: 20px;
}

:deep(.commit-header-icon-button):hover:not(:disabled) {
  background: var(--surface-hover-strong);
  color: var(--text);
  filter: none;
  transform: none;
}

:deep(.commit-header-icon-button):focus-visible {
  outline: 2px solid var(--accent-focus);
  outline-offset: 2px;
}

:deep(.commit-header-icon-button):disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

:deep(.commit-header-icon-button):disabled:hover {
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
  :deep(.commit-details-modal-header) {
    padding-right: 88px;
  }

  .commit-explain-button {
    top: 6px;
    right: 50px;
  }
}
</style>
