<script setup lang="ts">
import { formatNumber, t } from "../i18n";
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import { fetchAlerts } from "../api/overview";
import type { Alert, AlertStatus } from "../api/types";
import AppButton from "../components/common/AppButton.vue";
import AppFilterPanel from "../components/common/AppFilterPanel.vue";
import AppSelect from "../components/common/AppSelect.vue";
import AppStatusBadge from "../components/common/AppStatusBadge.vue";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import { alertKindLabel, alertResourceLabel, alertStatusLabel, alertTitle } from "../utils/alertPresentation";
import { formatDate } from "../utils/format";

const route = useRoute();
const router = useRouter();
const stackFilter = computed(() => typeof route.query.stack_name === "string" ? route.query.stack_name : "");

const alerts = ref<Alert[]>([]);
const loading = ref(false);
const loadingError = ref("");
const selectedStatus = ref<AlertStatus | "">("");
const selectedType = ref<Alert["kind"] | "">("");
const selectedResource = ref("");
const newestFirst = ref(true);
let requestID = 0;

const statuses: AlertStatus[] = ["open", "resolved"];
const statusOptions = computed(() => statuses.map((value) => ({ value, label: alertStatusLabel(value) })));
const activeFilterCount = computed(() => [selectedStatus.value, selectedType.value, selectedResource.value].filter(Boolean).length);
const typeOptions = computed(() => [...new Set(alerts.value.map((alert) => alert.kind))].sort()
  .map((value) => ({ value, label: alertKindLabel(value) })));
const resourceOptions = computed(() => {
  const resources = new Map<string, string>();
  for (const alert of alerts.value) {
    if (alert.resourceId) {
      resources.set(resourceKey(alert), `${alert.resourceId} (${alertResourceLabel(alert.resourceType)})`);
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
  }));

function resourceKey(alert: Alert): string {
  return `${alert.resourceType}:${alert.resourceId}`;
}

function updatedAt(alert: Alert): string {
  return alert.status === "resolved" ? alert.resolvedAt || alert.updatedAt : alert.updatedAt;
}

function alertDetailsLocation(id: string) {
  return { query: { ...route.query, tab: "alerts", alert_id: id }, hash: route.hash };
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
      loadingError.value = error instanceof Error ? error.message : t("common.unknownError");
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
      <AppSelect
        v-model="selectedStatus"
        :options="statusOptions"
        :reset-option="{ value: '', label: t('alerts.allStatuses') }"
        :label="t('alerts.filterStatus')"
      >
        <template #icon>
          <svg class="events-filter-icon" viewBox="0 0 20 20" aria-hidden="true">
            <circle cx="10" cy="10" r="6.5" />
            <path d="m7.5 10 1.7 1.7 3.3-3.4" />
          </svg>
        </template>
      </AppSelect>

      <AppSelect
        v-model="selectedType"
        :options="typeOptions"
        :reset-option="{ value: '', label: t('activity.allTypes') }"
        :label="t('alerts.filterType')"
      >
        <template #icon>
          <svg class="events-filter-icon" viewBox="0 0 20 20" aria-hidden="true">
            <path d="M3.5 4.5v5.3l6.7 6.7 6.3-6.3-6.7-6.7H4.5a1 1 0 0 0-1 1Z" />
            <circle cx="7" cy="7" r="1" />
          </svg>
        </template>
      </AppSelect>

      <AppSelect
        v-model="selectedResource"
        :options="resourceOptions"
        :reset-option="{ value: '', label: t('alerts.allResources') }"
        :label="t('alerts.filterResource')"
        searchable
        :search-label="t('alerts.searchResources')"
        :search-placeholder="t('alerts.searchResources')"
        :empty-message="t('alerts.noMatchingResources')"
      >
        <template #icon>
          <svg class="events-filter-icon" viewBox="0 0 20 20" aria-hidden="true">
            <path d="m10 2.8 6.5 3.6v7.2L10 17.2l-6.5-3.6V6.4L10 2.8Z" />
            <path d="m3.8 6.5 6.2 3.4 6.2-3.4M10 9.9v7" />
          </svg>
        </template>
      </AppSelect>

      <template #after>
        <p v-if="stackFilter" class="meta">
          {{ t('alerts.stackFilter', { stack: stackFilter }) }}
          <AppButton variant="secondary" compact @click="clearStackFilter">{{ t('alerts.clearStack') }}</AppButton>
        </p>
      </template>
    </AppFilterPanel>

    <AppTableEmpty v-if="loading" :message="t('alerts.loading')" />
    <AppTableEmpty v-else-if="loadingError" role="alert">{{ t('alerts.loadError', { error: loadingError }) }}</AppTableEmpty>
    <AppTableEmpty v-else-if="visibleAlerts.length === 0" :message="t(activeFilterCount ? 'alerts.emptyFiltered' : 'alerts.empty')" />
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
              {{ t('common.updated') }} <span class="app-table-sort-indicator" aria-hidden="true">{{ newestFirst ? "↓" : "↑" }}</span>
            </button>
          </th>
          <th>{{ t('common.status') }}</th>
          <th>{{ t('alerts.alert') }}</th>
          <th>{{ t('alerts.resource') }}</th>
          <th>{{ t('alerts.occurrences') }}</th>
        </tr>
      </template>
      <tr v-for="alert in visibleAlerts" :key="alert.id" class="app-table-row--clickable" @click="router.push(alertDetailsLocation(alert.id))">
        <td>{{ formatDate(updatedAt(alert)) }}</td>
        <td>
          <AppStatusBadge :variant="alert.status === 'resolved' ? 'neutral' : 'warning'" dot>
            {{ alertStatusLabel(alert.status) }}
          </AppStatusBadge>
        </td>
        <td>
          <RouterLink :to="alertDetailsLocation(alert.id)" class="alerts-title" @click.stop>{{ alertTitle(alert) }}</RouterLink>
          <span v-if="alert.message && alert.message !== alert.title" class="alerts-message">{{ alert.message }}</span>
        </td>
        <td class="alerts-resource">{{ alert.resourceId || t('alerts.unknownResource') }}</td>
        <td>{{ formatNumber(alert.occurrences) }}</td>
      </tr>
    </AppTable>
  </section>
</template>

<style scoped>
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
  color: inherit;
  font-weight: 600;
  text-decoration: none;
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
@media (max-width: 720px) {
  .alerts-updated-column {
    width: 117px;
  }
  .alerts-status-column {
    width: 94px;
  }
}
</style>
