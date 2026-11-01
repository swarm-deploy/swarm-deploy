<script setup lang="ts">
import { computed } from "vue";
import { useRouter } from "vue-router";

import AppModal from "../common/AppModal.vue";
import AppTable from "../common/AppTable.vue";
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
</script>

<template>
  <AppModal
    :open="overviewStore.serviceStatusModalOpen"
    labelledby="service-status-title"
    keep-mounted
    @close="closeServiceStatusModal"
  >
    <template #title>
      <h2 id="service-status-title">
        {{
          overviewStore.serviceStatusData
            ? `${overviewStore.serviceStatusData.stack} / ${overviewStore.serviceStatusData.service}`
            : canOpenDetails
              ? `${overviewStore.serviceStatusStack} / ${overviewStore.serviceStatusService}`
              : "Service status"
        }}
      </h2>
    </template>

    <div class="modal-body">
      <p v-if="overviewStore.serviceStatusLoading" class="meta">Loading service status...</p>
      <p v-else-if="overviewStore.serviceStatusError" class="meta">
        Failed to load service status: {{ overviewStore.serviceStatusError }}
      </p>
      <div v-else-if="serviceSpec" class="service-metrics">
        <AppTable summary aria-label="Service summary">
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
        </AppTable>
      </div>
      <div v-if="canOpenDetails" class="services-link-panel service-status-link-panel">
        <button type="button" class="service-status-open-details-btn" @click="openServiceDetails">Open details</button>
      </div>
    </div>
  </AppModal>
</template>
