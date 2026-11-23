<script setup lang="ts">
import { t } from "../../i18n";
import { computed } from "vue";
import { useRouter } from "vue-router";

import AppButton from "../common/AppButton.vue";
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
              : t('service.serviceStatus')
        }}
      </h2>
    </template>

    <div class="modal-body">
      <p v-if="overviewStore.serviceStatusLoading" class="meta">{{ t('service.loadingStatus') }}</p>
      <p v-else-if="overviewStore.serviceStatusError" class="meta">
        {{ t('service.statusError', { error: overviewStore.serviceStatusError }) }}
      </p>
      <div v-else-if="serviceSpec" class="service-metrics">
        <AppTable summary :aria-label="t('service.summary')">
            <tr>
              <th scope="row">{{ t('common.stack') }}</th>
              <td>{{ overviewStore.serviceStatusData?.stack || overviewStore.serviceStatusStack || t('common.notApplicable') }}</td>
            </tr>
            <tr>
              <th scope="row">{{ t('common.image') }}</th>
              <td>{{ serviceSpec.image || t('common.notApplicable') }}</td>
            </tr>
            <tr>
              <th scope="row">{{ t('service.latestDeployment') }}</th>
              <td>{{ formatDate(overviewStore.serviceStatusLatestDeploymentAt) }}</td>
            </tr>
        </AppTable>
      </div>
      <div v-if="canOpenDetails" class="services-link-panel service-status-link-panel">
        <AppButton class="service-status-open-details-btn" @click="openServiceDetails">{{ t('service.openDetails') }}</AppButton>
      </div>
    </div>
  </AppModal>
</template>
