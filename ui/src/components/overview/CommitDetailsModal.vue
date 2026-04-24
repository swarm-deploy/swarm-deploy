<script setup lang="ts">
import { computed, onMounted, onUnmounted } from "vue";

import { useAssistantStore } from "../../stores/assistant";
import { useOverviewStore } from "../../stores/overview";
import { useUIStore } from "../../stores/ui";
import { formatDate, shortCommitHash } from "../../utils/format";

const overviewStore = useOverviewStore();
const assistantStore = useAssistantStore();
const uiStore = useUIStore();

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

  closeCommitDetailsModal();
  uiStore.openAssistantDrawer();
  await assistantStore.sendMessage(`Explain changes in git commit: ${commitHash}`);
}

function handleEscape(event: KeyboardEvent) {
  if (event.key === "Escape" && overviewStore.commitDetailsModalOpen) {
    closeCommitDetailsModal();
  }
}

onMounted(() => {
  document.addEventListener("keydown", handleEscape);
});

onUnmounted(() => {
  document.removeEventListener("keydown", handleEscape);
});
</script>

<template>
  <div class="modal" :class="{ hidden: !overviewStore.commitDetailsModalOpen }" aria-hidden="true">
    <div class="modal-overlay" @click="closeCommitDetailsModal" />
    <div class="modal-card" role="dialog" aria-modal="true" aria-labelledby="commit-details-title">
      <div class="modal-header">
        <h2 id="commit-details-title">{{ titleText }}</h2>
        <button class="modal-close" type="button" aria-label="Close modal" @click="closeCommitDetailsModal">x</button>
      </div>

      <div class="modal-body">
        <p v-if="overviewStore.commitDetailsLoading" class="meta">Loading commit details...</p>
        <p v-else-if="overviewStore.commitDetailsError" class="meta">
          Failed to load commit details: {{ overviewStore.commitDetailsError }}
        </p>
        <table v-else class="service-status-summary-table" aria-label="Commit details">
          <tbody>
            <tr>
              <th scope="row">Commit Hash</th>
              <td>{{ currentCommitHash || "n/a" }}</td>
            </tr>
            <tr>
              <th scope="row">Author</th>
              <td>{{ commitDetails?.author || "n/a" }}</td>
            </tr>
            <tr>
              <th scope="row">Date</th>
              <td>{{ formatDate(commitDetails?.date) }}</td>
            </tr>
            <tr>
              <th scope="row">Changed files</th>
              <td>{{ changedFilesText }}</td>
            </tr>
          </tbody>
        </table>
        <div class="services-link-panel service-status-link-panel">
          <button type="button" class="service-status-open-details-btn" :disabled="explainDisabled" @click="explainWithAI">
            Explain with AI
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
