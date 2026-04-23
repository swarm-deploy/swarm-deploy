<script setup lang="ts">
import { computed, onMounted, onUnmounted } from "vue";

import { useSecretDetailsStore } from "../../stores/secretDetails";
import { formatDate } from "../../utils/format";

const secretDetailsStore = useSecretDetailsStore();

const secretLabels = computed(() => {
  const labels = secretDetailsStore.secret?.labels;
  if (!labels || typeof labels !== "object") {
    return [];
  }

  return Object.entries(labels).sort(([left], [right]) => left.localeCompare(right));
});

function closeSecretDetailsModal() {
  secretDetailsStore.closeSecretDetails();
}

function handleEscape(event: KeyboardEvent) {
  if (event.key === "Escape" && secretDetailsStore.modalOpen) {
    closeSecretDetailsModal();
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
  <div class="modal" :class="{ hidden: !secretDetailsStore.modalOpen }" aria-hidden="true">
    <div class="modal-overlay" @click="closeSecretDetailsModal" />
    <div class="modal-card" role="dialog" aria-modal="true" aria-labelledby="secret-details-title">
      <div class="modal-header">
        <h2 id="secret-details-title">
          {{ secretDetailsStore.secret ? secretDetailsStore.secret.name : secretDetailsStore.selectedName || "Secret details" }}
        </h2>
        <button class="modal-close" type="button" aria-label="Close modal" @click="closeSecretDetailsModal">x</button>
      </div>

      <div class="modal-body">
        <p v-if="secretDetailsStore.loading" class="meta">Loading secret details...</p>
        <p v-else-if="secretDetailsStore.error" class="meta">
          Failed to load secret details: {{ secretDetailsStore.error }}
        </p>
        <div v-else-if="secretDetailsStore.secret" class="service-metrics">
          <p><strong>ID:</strong> <code>{{ secretDetailsStore.secret.id }}</code></p>
          <p><strong>Name:</strong> {{ secretDetailsStore.secret.name }}</p>
          <p><strong>Version ID:</strong> {{ secretDetailsStore.secret.version_id }}</p>
          <p><strong>Created At:</strong> {{ formatDate(secretDetailsStore.secret.created_at) }}</p>
          <p><strong>Updated At:</strong> {{ formatDate(secretDetailsStore.secret.updated_at) }}</p>
          <p><strong>Driver:</strong> {{ secretDetailsStore.secret.driver || "n/a" }}</p>
          <p><strong>External Path:</strong> {{ secretDetailsStore.secret.external?.path || "n/a" }}</p>
          <p><strong>External Version ID:</strong> {{ secretDetailsStore.secret.external?.version_id || "n/a" }}</p>
          <p><strong>Labels</strong></p>
          <ul v-if="secretLabels.length > 0" class="event-details">
            <li v-for="[key, value] in secretLabels" :key="key" class="event-detail">
              <span class="event-detail-key">{{ key }}</span>
              <code class="event-detail-value">{{ value }}</code>
            </li>
          </ul>
          <p v-else class="meta">No labels.</p>
        </div>
      </div>
    </div>
  </div>
</template>
