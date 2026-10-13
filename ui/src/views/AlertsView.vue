<script setup lang="ts">
import { onMounted, ref, watch } from "vue";

import { fetchAlerts } from "../api/overview";
import type { Alert } from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import { useOverviewStore } from "../stores/overview";
import { formatDate } from "../utils/format";

const overviewStore = useOverviewStore();
const showResolved = ref(false);
const alerts = ref<Alert[]>([]);
const loading = ref(false);
const loadingError = ref("");
let requestID = 0;

async function loadAlerts() {
  loading.value = true;
  loadingError.value = "";
  const currentRequestID = ++requestID;
  try {
    const response = await fetchAlerts(showResolved.value ? {} : { status: "open" });
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

watch(showResolved, () => void loadAlerts());
onMounted(() => void loadAlerts());
</script>

<template>
  <section class="services-page alerts-page">
    <header class="services-header events-header">
      <label class="alerts-resolved-filter">
        <input v-model="showResolved" type="checkbox" />
        <span>Show resolved alerts</span>
      </label>
    </header>

    <AppTableEmpty v-if="loading && alerts.length === 0" message="Loading alerts..." />
    <AppTableEmpty v-else-if="loadingError">Failed to load alerts: {{ loadingError }}</AppTableEmpty>
    <div v-else-if="alerts.length === 0 && !showResolved" class="app-table-empty alerts-empty">
      <strong>No active alerts</strong>
      <p class="meta">All alerts are resolved.</p>
    </div>
    <AppTableEmpty v-else-if="alerts.length === 0" message="No alerts." />
    <AppTable v-else fixed min-width="800px" table-class="alerts-table" wrap-class="alerts-table-wrap">
      <template #head><tr><th>Alert</th><th>Resource</th><th>Opened</th><th>{{ showResolved ? "Updated / Resolved" : "Updated" }}</th><th>Occurrences</th></tr></template>
      <tr v-for="alert in alerts" :key="alert.id" class="alerts-row" tabindex="0" @click="overviewStore.openAlertDetailsModal(alert)" @keydown.enter="overviewStore.openAlertDetailsModal(alert)">
        <td><strong>{{ alert.title }}</strong><span v-if="alert.resolution" class="alerts-message">{{ alert.resolution.reason }} · {{ alert.resolution.message }}</span></td>
        <td>{{ alert.resourceId || "unknown resource" }}</td>
        <td>{{ formatDate(alert.openedAt) }}</td>
        <td>{{ formatDate(alert.status === "resolved" ? alert.resolvedAt : alert.updatedAt) }}</td>
        <td>{{ alert.occurrences }}</td>
      </tr>
    </AppTable>
  </section>
</template>

<style scoped>
.alerts-resolved-filter {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  width: fit-content;
  color: var(--text);
  cursor: pointer;
}

.alerts-resolved-filter input {
  width: 16px;
  height: 16px;
  margin: 0;
  accent-color: var(--accent);
}

.alerts-empty {
  display: grid;
  gap: 4px;
}

.alerts-empty .meta {
  margin: 0;
}
</style>
