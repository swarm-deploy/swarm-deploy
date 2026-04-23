<script setup lang="ts">
import { computed, onMounted, onUnmounted } from "vue";
import { useRouter } from "vue-router";

import { useOverviewStore } from "../../stores/overview";
import { formatDate } from "../../utils/format";

const overviewStore = useOverviewStore();
const router = useRouter();

const serviceSpec = computed(() => overviewStore.serviceStatusData?.spec);
const canOpenDetails = computed(
  () => overviewStore.serviceStatusStack.trim().length > 0 && overviewStore.serviceStatusService.trim().length > 0,
);

function closeServiceStatusModal() {
  overviewStore.closeServiceStatusModal();
}

async function openServiceDetails() {
  if (!canOpenDetails.value) {
    return;
  }

  const stackName = overviewStore.serviceStatusStack;
  const serviceName = overviewStore.serviceStatusService;
  closeServiceStatusModal();

  await router.push({
    name: "service-details",
    params: {
      stack: stackName,
      service: serviceName,
    },
  });
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
              : canOpenDetails
                ? `${overviewStore.serviceStatusStack} / ${overviewStore.serviceStatusService}`
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
          <table class="service-status-summary-table" aria-label="Service summary">
            <tbody>
              <tr>
                <th scope="row">Stack</th>
                <td>{{ overviewStore.serviceStatusData?.stack || overviewStore.serviceStatusStack || "n/a" }}</td>
              </tr>
              <tr>
                <th scope="row">Image</th>
                <td>{{ serviceSpec.image || "n/a" }}</td>
              </tr>
              <tr>
                <th scope="row">Latest Deployment</th>
                <td>{{ formatDate(overviewStore.serviceStatusLatestDeploymentAt) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-if="canOpenDetails" class="services-link-panel service-status-link-panel">
          <button type="button" class="service-status-open-details-btn" @click="openServiceDetails">Open details</button>
        </div>
      </div>
    </div>
  </div>
</template>
