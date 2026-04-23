<script setup lang="ts">
import { computed, onMounted, onUnmounted } from "vue";

import type { ServiceSpecNetworkResponse, ServiceSpecSecretResponse } from "../../api/types";
import { useOverviewStore } from "../../stores/overview";
import { formatBytes } from "../../utils/format";

const overviewStore = useOverviewStore();

const serviceSpec = computed(() => overviewStore.serviceStatusData?.spec);
const serviceLabels = computed(() => {
  const labels = serviceSpec.value?.labels;
  if (!labels || typeof labels !== "object") {
    return [];
  }

  return Object.entries(labels).sort(([left], [right]) => left.localeCompare(right));
});
const serviceSecrets = computed(() => {
  const secrets = serviceSpec.value?.secrets;
  return Array.isArray(secrets) ? secrets : [];
});
const serviceNetworks = computed(() => {
  const network = serviceSpec.value?.network;
  return Array.isArray(network) ? network : [];
});

function formatSecretMeta(secret: ServiceSpecSecretResponse): string {
  const parts: string[] = [];
  if (secret.secret_id) {
    parts.push(`id=${secret.secret_id}`);
  }
  if (secret.target) {
    parts.push(`target=${secret.target}`);
  }
  return parts.join(", ") || "-";
}

function formatNetworkMeta(network: ServiceSpecNetworkResponse): string {
  const aliases = Array.isArray(network.aliases) && network.aliases.length > 0 ? network.aliases.join(", ") : "-";
  return `aliases=${aliases}`;
}

function closeServiceStatusModal() {
  overviewStore.closeServiceStatusModal();
}

function handleEscape(event: KeyboardEvent) {
  if (event.key === "Escape" && overviewStore.serviceStatusModalOpen) {
    closeServiceStatusModal();
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
  <div class="modal" :class="{ hidden: !overviewStore.serviceStatusModalOpen }" aria-hidden="true">
    <div class="modal-overlay" @click="closeServiceStatusModal" />
    <div class="modal-card" role="dialog" aria-modal="true" aria-labelledby="service-status-title">
      <div class="modal-header">
        <h2 id="service-status-title">
          {{
            overviewStore.serviceStatusData
              ? `${overviewStore.serviceStatusData.stack} / ${overviewStore.serviceStatusData.service}`
              : "Service status"
          }}
        </h2>
        <button class="modal-close" type="button" aria-label="Close modal" @click="closeServiceStatusModal">x</button>
      </div>

      <div class="modal-body">
        <p v-if="overviewStore.serviceStatusLoading" class="meta">Loading service status...</p>
        <p v-else-if="overviewStore.serviceStatusError" class="meta">
          Failed to load service status: {{ overviewStore.serviceStatusError }}
        </p>
        <div v-else-if="serviceSpec" class="service-metrics">
          <p><strong>Image:</strong> {{ serviceSpec.image || "n/a" }}</p>
          <p><strong>Deploy Mode:</strong> {{ serviceSpec.mode || "n/a" }}</p>
          <p><strong>Replicas:</strong> {{ Number.isFinite(serviceSpec.replicas) ? serviceSpec.replicas : "n/a" }}</p>
          <p><strong>Requested RAM:</strong> {{ formatBytes(serviceSpec.requested_ram_bytes) }}</p>
          <p><strong>Requested CPU:</strong> {{ serviceSpec.requested_cpu_nano || 0 }} nano-CPUs</p>
          <p><strong>RAM Limit:</strong> {{ formatBytes(serviceSpec.limit_ram_bytes) }}</p>
          <p><strong>CPU Limit:</strong> {{ serviceSpec.limit_cpu_nano || 0 }} nano-CPUs</p>
          <p><strong>Labels</strong></p>
          <ul v-if="serviceLabels.length > 0" class="event-details">
            <li v-for="[key, value] in serviceLabels" :key="key" class="event-detail">
              <span class="event-detail-key">{{ key }}</span>
              <code class="event-detail-value">{{ value }}</code>
            </li>
          </ul>
          <p v-else class="meta">No labels.</p>
          <p><strong>Secrets</strong></p>
          <ul v-if="serviceSecrets.length > 0" class="event-details">
            <li v-for="secret in serviceSecrets" :key="`${secret.secret_name}-${secret.secret_id}`" class="event-detail">
              <span class="event-detail-key">{{ secret.secret_name || "unknown" }}</span>
              <code class="event-detail-value">{{ formatSecretMeta(secret) }}</code>
            </li>
          </ul>
          <p v-else class="meta">No secrets.</p>
          <p><strong>Network</strong></p>
          <ul v-if="serviceNetworks.length > 0" class="event-details">
            <li v-for="network in serviceNetworks" :key="`${network.target}-${formatNetworkMeta(network)}`" class="event-detail">
              <span class="event-detail-key">{{ network.target || "unknown" }}</span>
              <code class="event-detail-value">{{ formatNetworkMeta(network) }}</code>
            </li>
          </ul>
          <p v-else class="meta">No network attachments.</p>
        </div>
      </div>
    </div>
  </div>
</template>
