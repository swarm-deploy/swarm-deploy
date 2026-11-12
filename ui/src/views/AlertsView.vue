<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import { fetchAlerts } from "../api/overview";
import type { Alert, AlertStatus } from "../api/types";
import AppButton from "../components/common/AppButton.vue";
import AppFilterPanel from "../components/common/AppFilterPanel.vue";
import AppSearchFilter from "../components/common/AppSearchFilter.vue";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import AlertStatusBadge from "../components/overview/AlertStatusBadge.vue";
import { useOverviewStore } from "../stores/overview";
import { alertResourceRoute } from "../utils/alertResource";
import { formatDate } from "../utils/format";

const overviewStore = useOverviewStore();
const route = useRoute();
const router = useRouter();
const stackFilter = computed(() => typeof route.query.stack_name === "string" ? route.query.stack_name : "");

const alerts = ref<Alert[]>([]);
const loading = ref(false);
const loadingError = ref("");
const selectedStatus = ref<AlertStatus | "">("");
const selectedType = ref("");
const selectedResource = ref("");
const newestFirst = ref(true);
let requestID = 0;

const activeFilterCount = computed(() => [selectedStatus.value, selectedType.value, selectedResource.value].filter(Boolean).length);
const alertTypes = computed(() => [...new Set(alerts.value.map((alert) => alert.kind))].sort());
const resourceOptions = computed(() => {
  const resources = new Map<string, string>();
  for (const alert of alerts.value) {
    if (alert.resourceId) {
      resources.set(resourceKey(alert), `${alert.resourceId} (${alert.resourceType})`);
    }
  }
  return [...resources].map(([value, label]) => ({ value, label }))
    .sort((left, right) => left.label.localeCompare(right.label));
});
const visibleAlerts = computed(() => alerts.value
  .filter((alert) => (!selectedStatus.value || alert.status === selectedStatus.value)
    && (!selectedType.value || alert.kind === selectedType.value)
    && (!selectedResource.value || resourceKey(alert) === selectedResource.value))
  .sort((left, right) => {
    const difference = Date.parse(updatedAt(right)) - Date.parse(updatedAt(left));
    return newestFirst.value ? difference : -difference;
  })
  .map((alert) => ({ alert, resourceTarget: alertResourceRoute(alert) })));

function resourceKey(alert: Alert): string {
  return `${alert.resourceType}:${alert.resourceId}`;
}

function updatedAt(alert: Alert): string {
  return alert.status === "resolved" ? alert.resolvedAt || alert.updatedAt : alert.updatedAt;
}

function clearStackFilter() {
  const query = { ...route.query };
  delete query.stack_name;
  void router.replace({ query });
}

async function loadAlerts() {
  loading.value = true;
  alerts.value = [];
  loadingError.value = "";
  const currentRequestID = ++requestID;
  try {
    const response = await fetchAlerts(stackFilter.value ? { stack: stackFilter.value } : {});
    if (currentRequestID === requestID) {
      alerts.value = Array.isArray(response.alerts) ? response.alerts : [];
    }
  } catch (error) {
    if (currentRequestID === requestID) {
      loadingError.value = error instanceof Error ? error.message : "Failed to load alerts";
    }
  } finally {
    if (currentRequestID === requestID) loading.value = false;
  }
}

watch(stackFilter, () => void loadAlerts());
onMounted(() => void loadAlerts());
</script>

<template>
  <section class="services-page alerts-page">
    <AppFilterPanel panel-id="alerts-filter-panel" :active-count="activeFilterCount" :columns="3">
      <label class="events-filter-field events-filter-field--select">
        <svg class="events-filter-icon" viewBox="0 0 20 20" aria-hidden="true">
          <circle cx="10" cy="10" r="6.5" />
          <path d="m7.5 10 1.7 1.7 3.3-3.4" />
        </svg>
        <select v-model="selectedStatus" aria-label="Filter alerts by status">
          <option value="">All statuses</option>
          <option value="open">Active</option>
          <option value="resolved">Resolved</option>
        </select>
      </label>

      <label class="events-filter-field events-filter-field--select">
        <svg class="events-filter-icon" viewBox="0 0 20 20" aria-hidden="true">
          <path d="M3.5 4.5v5.3l6.7 6.7 6.3-6.3-6.7-6.7H4.5a1 1 0 0 0-1 1Z" />
          <circle cx="7" cy="7" r="1" />
        </svg>
        <select v-model="selectedType" aria-label="Filter alerts by type">
          <option value="">All types</option>
          <option v-for="type in alertTypes" :key="type" :value="type">{{ type.replaceAll("_", " ") }}</option>
        </select>
      </label>

      <AppSearchFilter
        v-model="selectedResource"
        class="alerts-resource-filter"
        :options="resourceOptions"
        label="Filter alerts by resource"
        placeholder="All resources"
        search-label="Search resources"
        empty-message="No matching resources"
      >
        <template #icon>
          <svg class="events-filter-icon" viewBox="0 0 20 20" aria-hidden="true">
            <path d="m10 2.8 6.5 3.6v7.2L10 17.2l-6.5-3.6V6.4L10 2.8Z" />
            <path d="m3.8 6.5 6.2 3.4 6.2-3.4M10 9.9v7" />
          </svg>
        </template>
      </AppSearchFilter>

      <template #after>
        <p v-if="stackFilter" class="meta">Stack: <strong>{{ stackFilter }}</strong> <AppButton variant="secondary" compact @click="clearStackFilter">Clear stack filter</AppButton></p>
      </template>
    </AppFilterPanel>

    <AppTableEmpty v-if="loading" message="Loading alerts..." />
    <AppTableEmpty v-else-if="loadingError">Failed to load alerts: {{ loadingError }}</AppTableEmpty>
    <AppTableEmpty v-else-if="visibleAlerts.length === 0" :message="activeFilterCount ? 'No alerts match the selected filters.' : 'No alerts.'" />
    <AppTable v-else fixed min-width="720px" table-class="alerts-table">
      <template #colgroup>
        <colgroup>
          <col class="alerts-updated-column" />
          <col class="alerts-status-column" />
          <col />
          <col class="alerts-resource-column" />
          <col class="alerts-occurrences-column" />
        </colgroup>
      </template>
      <template #head>
        <tr>
          <th :aria-sort="newestFirst ? 'descending' : 'ascending'">
            <button class="app-table-sort" type="button" @click="newestFirst = !newestFirst">
              Updated <span class="app-table-sort-indicator" aria-hidden="true">{{ newestFirst ? "↓" : "↑" }}</span>
            </button>
          </th>
          <th>Status</th>
          <th>Alert</th>
          <th>Resource</th>
          <th>Occurrences</th>
        </tr>
      </template>
      <tr v-for="{ alert, resourceTarget } in visibleAlerts" :key="alert.id" class="app-table-row--clickable" @click="overviewStore.openAlertDetailsModal(alert)">
        <td>{{ formatDate(updatedAt(alert)) }}</td>
        <td>
          <AlertStatusBadge :status="alert.status" />
        </td>
        <td>
          <button class="alerts-title" type="button" @click.stop="overviewStore.openAlertDetailsModal(alert)">{{ alert.title }}</button>
          <span v-if="alert.message && alert.message !== alert.title" class="alerts-message">{{ alert.message }}</span>
        </td>
        <td class="alerts-resource">
          <RouterLink v-if="resourceTarget" :to="resourceTarget" class="alerts-resource-link" @click.stop>
            {{ alert.resourceId }}
          </RouterLink>
          <template v-else>{{ alert.resourceId || "unknown resource" }}</template>
        </td>
        <td>{{ alert.occurrences }}</td>
      </tr>
    </AppTable>
  </section>
</template>

<style scoped>
.alerts-resource-filter :deep(.events-search-filter-menu) {
  right: 0;
  left: auto;
}
.alerts-updated-column {
  width: 160px;
}
.alerts-status-column {
  width: 105px;
}
.alerts-resource-column {
  width: 23%;
}
.alerts-occurrences-column {
  width: 106px;
}
.alerts-title {
  display: block;
  border: 0;
  padding: 0;
  background: transparent;
  color: inherit;
  font: inherit;
  font-weight: 600;
  text-align: left;
  cursor: pointer;
  overflow-wrap: anywhere;
}
.alerts-title:focus-visible {
  outline: 1px solid var(--accent-border);
  outline-offset: 2px;
}
.alerts-message {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
  margin-top: 4px;
  color: var(--muted);
  font-size: 0.82rem;
  overflow-wrap: anywhere;
}
.alerts-resource {
  overflow-wrap: anywhere;
}
.alerts-resource-link {
  color: var(--accent-strong);
  text-decoration: underline;
  text-underline-offset: .18em;
}
.alerts-resource-link:focus-visible {
  outline: 2px solid var(--accent-focus);
  outline-offset: 2px;
}
@media (max-width: 720px) {
  .alerts-updated-column {
    width: 117px;
  }
  .alerts-status-column {
    width: 94px;
  }
}
</style>
