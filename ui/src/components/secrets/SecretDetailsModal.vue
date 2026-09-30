<script setup lang="ts">
import { computed, onMounted, onUnmounted } from "vue";
import { useRouter } from "vue-router";

import AppTable from "../common/AppTable.vue";
import type { SecretServiceUsage } from "../../api/types";
import { useSecretDetailsStore } from "../../stores/secretDetails";
import { formatDate } from "../../utils/format";

const secretDetailsStore = useSecretDetailsStore();
const router = useRouter();

const secretLabels = computed(() => {
  const labels = secretDetailsStore.secret?.labels;
  if (!labels || typeof labels !== "object") {
    return [];
  }

  return Object.entries(labels).sort(([left], [right]) => left.localeCompare(right));
});

const usedByServices = computed(() => {
  const usedBy = secretDetailsStore.secret?.used_by;
  return Array.isArray(usedBy) ? usedBy : [];
});

function closeSecretDetailsModal() {
  secretDetailsStore.closeSecretDetails();
}

async function openService(usage: SecretServiceUsage) {
  closeSecretDetailsModal();
  await router.push({
    name: "service-details",
    params: { stack: usage.stack, service: usage.service },
  });
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
          <AppTable summary aria-label="Secret summary">
              <tr>
                <th scope="row">ID</th>
                <td><code>{{ secretDetailsStore.secret.id }}</code></td>
              </tr>
              <tr>
                <th scope="row">Name</th>
                <td>{{ secretDetailsStore.secret.name }}</td>
              </tr>
              <tr>
                <th scope="row">Version ID</th>
                <td>{{ secretDetailsStore.secret.version_id }}</td>
              </tr>
              <tr>
                <th scope="row">Created At</th>
                <td>{{ formatDate(secretDetailsStore.secret.created_at) }}</td>
              </tr>
              <tr>
                <th scope="row">Updated At</th>
                <td>{{ formatDate(secretDetailsStore.secret.updated_at) }}</td>
              </tr>
              <tr>
                <th scope="row">Driver</th>
                <td>{{ secretDetailsStore.secret.driver || "n/a" }}</td>
              </tr>
              <tr>
                <th scope="row">External Path</th>
                <td>{{ secretDetailsStore.secret.external?.path || "n/a" }}</td>
              </tr>
              <tr>
                <th scope="row">External Version ID</th>
                <td>{{ secretDetailsStore.secret.external?.version_id || "n/a" }}</td>
              </tr>
              <tr>
                <th scope="row">Labels</th>
                <td>
                  <ul v-if="secretLabels.length > 0" class="event-details">
                    <li v-for="[key, value] in secretLabels" :key="key" class="event-detail">
                      <span class="event-detail-key">{{ key }}</span>
                      <code class="event-detail-value">{{ value }}</code>
                    </li>
                  </ul>
                  <span v-else class="meta">No labels.</span>
                </td>
              </tr>
          </AppTable>

          <section class="secret-used-by" aria-labelledby="secret-used-by-title">
            <h3 id="secret-used-by-title">Used by services</h3>
            <ul v-if="usedByServices.length > 0" class="secret-used-by-list">
              <li v-for="usage in usedByServices" :key="`${usage.stack}/${usage.service}/${usage.target || ''}`">
                <button type="button" class="secret-used-by-service" @click="openService(usage)">
                  <span>{{ usage.stack }} / {{ usage.service }}</span>
                  <code v-if="usage.target">{{ usage.target }}</code>
                </button>
              </li>
            </ul>
            <p v-else class="meta">Not used by any service</p>
          </section>
        </div>
      </div>
    </div>
  </div>
</template>
