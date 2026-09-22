<script setup lang="ts">
import { onMounted, ref, watch } from "vue";

import { fetchAlerts } from "../api/overview";
import type { Alert, AlertStatus } from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import { useOverviewStore } from "../stores/overview";
import { formatDate } from "../utils/format";

const overviewStore = useOverviewStore();
const selectedStatus = ref<AlertStatus>("open");
const alerts = ref<Alert[]>([]);
const loading = ref(false);
const loadingError = ref("");
let requestID = 0;

async function loadAlerts() {
  loading.value = true;
  loadingError.value = "";
  const currentRequestID = ++requestID;
  try {
    const response = await fetchAlerts({ status: selectedStatus.value });
    if (currentRequestID === requestID) {
      alerts.value = Array.isArray(response.alerts) ? response.alerts : [];
    }
  } catch (error) {
    if (currentRequestID === requestID) {
      alerts.value = [];
      loadingError.value = error instanceof Error ? error.message : "Failed to load alerts";
    }
  } finally {
    if (currentRequestID === requestID) loading.value = false;
  }
}

watch(selectedStatus, () => void loadAlerts());
onMounted(() => void loadAlerts());
</script>

<template>
  <section class="services-page alerts-page">
    <header class="services-header events-header">
      <h2>Alerts</h2>
      <div class="manifest-tabs" role="tablist" aria-label="Alert status">
        <button type="button" class="manifest-tab-btn" :class="{ active: selectedStatus === 'open' }" @click="selectedStatus = 'open'">Active</button>
        <button type="button" class="manifest-tab-btn" :class="{ active: selectedStatus === 'resolved' }" @click="selectedStatus = 'resolved'">Resolved</button>
      </div>
    </header>

    <AppTableEmpty v-if="loading && alerts.length === 0" message="Loading alerts..." />
    <AppTableEmpty v-else-if="loadingError">Failed to load alerts: {{ loadingError }}</AppTableEmpty>
    <AppTableEmpty v-else-if="alerts.length === 0" :message="selectedStatus === 'open' ? 'No active alerts' : 'No resolved alerts'" />
    <AppTable v-else fixed table-class="alerts-table" wrap-class="alerts-table-wrap">
      <template #head><tr><th>Alert</th><th>Stack</th><th>Opened</th><th>{{ selectedStatus === "open" ? "Updated" : "Resolved" }}</th><th>Occurrences</th></tr></template>
      <tr v-for="alert in alerts" :key="alert.id" class="alerts-row" tabindex="0" @click="overviewStore.openAlertDetailsModal(alert)" @keydown.enter="overviewStore.openAlertDetailsModal(alert)">
        <td><strong>{{ alert.title }}</strong><span class="alerts-message">{{ alert.message || "No message" }}</span><span v-if="alert.resolution" class="alerts-message">{{ alert.resolution.reason }} · {{ alert.resolution.message }}</span></td>
        <td>{{ alert.resourceId || "unknown stack" }}</td>
        <td>{{ formatDate(alert.openedAt) }}</td>
        <td>{{ formatDate(selectedStatus === "open" ? alert.updatedAt : alert.resolvedAt) }}</td>
        <td>{{ alert.occurrences }}</td>
      </tr>
    </AppTable>
  </section>
</template>
