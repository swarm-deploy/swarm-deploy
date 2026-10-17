<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import { fetchAlerts } from "../api/overview";
import type { Alert } from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import { useOverviewStore } from "../stores/overview";
import { formatDate } from "../utils/format";

const overviewStore = useOverviewStore();
const showResolved = ref(false);
const route = useRoute();
const router = useRouter();
const stackFilter = computed(() => typeof route.query.stack_name === "string" ? route.query.stack_name : "");
function clearStackFilter() {
  const query = { ...route.query };
  delete query.stack_name;
  void router.replace({ query });
}
const alerts = ref<Alert[]>([]);
const loading = ref(false);
const loadingError = ref("");
let requestID = 0;

async function loadAlerts() {
  loading.value = true;
  alerts.value = [];
  loadingError.value = "";
  const currentRequestID = ++requestID;
  try {
    const options: Parameters<typeof fetchAlerts>[0] = showResolved.value ? {} : { status: "open" };
    if (stackFilter.value) options.stack = stackFilter.value;
    const response = await fetchAlerts(options);
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

watch([showResolved, stackFilter], () => void loadAlerts());
onMounted(() => void loadAlerts());
</script>

<template>
  <section class="services-page alerts-page">
    <header class="services-header events-header">
      <label class="alerts-resolved-filter">
        <input v-model="showResolved" type="checkbox" />
        <span>Show resolved alerts</span>
      </label>
      <p v-if="stackFilter" class="meta">Stack: <strong>{{ stackFilter }}</strong> <button type="button" class="button-ghost" @click="clearStackFilter">Clear stack filter</button></p>
    </header>

    <AppTableEmpty v-if="loading && alerts.length === 0" message="Loading alerts..." />
    <AppTableEmpty v-else-if="loadingError">Failed to load alerts: {{ loadingError }}</AppTableEmpty>
    <div v-else-if="alerts.length === 0 && !showResolved" class="app-table-empty alerts-empty">
      <strong>No active alerts</strong>
      <p class="meta">{{ stackFilter ? "No active alerts for this stack." : "All alerts are resolved." }}</p>
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
