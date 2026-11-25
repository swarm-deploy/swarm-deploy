<script setup lang="ts">
import { formatNumber, locale, plural, t } from "../i18n";
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
import { alertKindLabel, alertMessage, alertResourceLabel, alertStatusLabel, alertTitle } from "../utils/alertPresentation";
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

interface AlertGroup {
  key: string;
  label: string;
  alerts: Alert[];
}

function localDateKey(date: Date): string {
  return `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`;
}

function alertDateLabel(date: Date): string {
  const dateLabel = date.toLocaleDateString(locale(), {
    day: "numeric",
    month: "long",
    year: date.getFullYear() === new Date().getFullYear() ? undefined : "numeric",
  });
  return localDateKey(date) === localDateKey(new Date())
    ? t("deployments.todayDate", { date: dateLabel })
    : dateLabel;
}

function alertGroups(items: Alert[]): AlertGroup[] {
  const groups = new Map<string, AlertGroup>();
  for (const alert of items) {
    const rawDate = updatedAt(alert);
    const date = new Date(rawDate);
    const validDate = !Number.isNaN(date.valueOf());
    const key = validDate ? localDateKey(date) : rawDate;
    const group = groups.get(key) ?? {
      key,
      label: validDate ? alertDateLabel(date) : rawDate,
      alerts: [],
    };
    group.alerts.push(alert);
    groups.set(key, group);
  }
  return [...groups.values()];
}

function formatAlertTime(raw: string): string {
  return formatDate(raw, { hour: "2-digit", minute: "2-digit" });
}

function resourceKey(alert: Alert): string {
  return `${alert.resourceType}:${alert.resourceId}`;
}

function updatedAt(alert: Alert): string {
  return alert.status === "resolved" ? alert.resolvedAt || alert.updatedAt : alert.updatedAt;
}

function alertDetailsLocation(id: string) {
  return { query: { ...route.query, tab: "alerts", alert_id: id }, hash: route.hash };
}

function openAlertFromKeyboard(event: KeyboardEvent, id: string): void {
  if (event.target !== event.currentTarget || (event.key !== "Enter" && event.key !== " ")) return;
  event.preventDefault();
  void router.push(alertDetailsLocation(id));
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
    <AppTable v-else fixed table-class="alerts-table" wrap-class="alerts-table-wrap">
      <template #colgroup>
        <colgroup>
          <col />
          <col class="alerts-status-column" />
          <col class="alerts-resource-column" />
          <col class="alerts-updated-column" />
          <col class="alerts-occurrences-column" />
        </colgroup>
      </template>
      <template #head>
        <tr>
          <th>{{ t('alerts.alert') }}</th>
          <th>{{ t('common.status') }}</th>
          <th>{{ t('alerts.resource') }}</th>
          <th :aria-sort="newestFirst ? 'descending' : 'ascending'">
            <button class="app-table-sort" type="button" @click="newestFirst = !newestFirst">
              {{ t('common.updated') }} <span class="app-table-sort-indicator" aria-hidden="true">{{ newestFirst ? "↓" : "↑" }}</span>
            </button>
          </th>
          <th>{{ t('alerts.occurrences') }}</th>
        </tr>
      </template>
      <template v-for="group in alertGroups(visibleAlerts)" :key="group.key">
        <tr class="alerts-date-row"><th colspan="5" scope="rowgroup">{{ group.label }}</th></tr>
        <tr
          v-for="alert in group.alerts"
          :key="alert.id"
          class="app-table-row--clickable alerts-row"
          tabindex="0"
          @click="router.push(alertDetailsLocation(alert.id))"
          @keydown="openAlertFromKeyboard($event, alert.id)"
        >
          <td class="alerts-alert">
            <RouterLink :to="alertDetailsLocation(alert.id)" class="alerts-title" @click.stop>{{ alertTitle(alert) }}</RouterLink>
            <span v-if="alert.message && alert.message !== alert.title" class="alerts-message">{{ alertMessage(alert) }}</span>
          </td>
          <td class="alerts-status">
            <AppStatusBadge :variant="alert.status === 'resolved' ? 'neutral' : 'warning'" dot>
              {{ alertStatusLabel(alert.status) }}
            </AppStatusBadge>
          </td>
          <td class="alerts-resource">{{ alert.resourceId || t('alerts.unknownResource') }}</td>
          <td class="alerts-updated">
            <time class="alerts-updated-full" :datetime="updatedAt(alert)">{{ formatDate(updatedAt(alert)) }}</time>
            <time class="alerts-updated-compact" :datetime="updatedAt(alert)">{{ formatAlertTime(updatedAt(alert)) }}</time>
          </td>
          <td class="alerts-occurrences">
            <span class="alerts-occurrences-full">{{ formatNumber(alert.occurrences) }}</span>
            <span class="alerts-occurrences-compact">{{ plural('alerts.occurrenceCount', alert.occurrences) }}</span>
          </td>
        </tr>
      </template>
    </AppTable>
  </section>
</template>

<style scoped>
.alerts-page { min-width: 0; }
.alerts-date-row,
.alerts-updated-compact,
.alerts-occurrences-compact { display: none; }
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
.alerts-updated,
.alerts-status,
.alerts-occurrences { white-space: nowrap; }
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
  :deep(.alerts-table-wrap) { overflow-x: visible; }
  :deep(.alerts-table) { min-width: 0; table-layout: auto; }
  :deep(.alerts-table thead) { display: none; }
  :deep(.alerts-table tbody) { display: block; }
  :deep(.alerts-date-row) { display: block; }
  :deep(.alerts-table .alerts-row > td) { width: auto; }
  :deep(.alerts-table .alerts-date-row > th:first-child) {
    display: block;
    width: 100%;
    border-bottom: 1px solid var(--border-subtle);
    padding: 14px 0 8px;
    color: var(--muted);
    font-size: 0.78rem;
    font-weight: 600;
    letter-spacing: 0;
    text-transform: none;
  }
  :deep(.alerts-row) {
    display: grid;
    grid-template-columns: 64px minmax(0, 1fr) auto;
    grid-template-rows: auto auto;
    gap: 5px 12px;
    align-items: start;
    padding: 12px 10px;
    border-bottom: 1px solid var(--border-subtle);
  }
  :deep(.alerts-table .alerts-row > td) {
    border-bottom: 0;
    padding: 0;
  }
  .alerts-alert {
    grid-column: 2;
    grid-row: 1;
    min-width: 0;
  }
  .alerts-title { line-height: 1.25; }
  .alerts-message {
    margin-top: 3px;
    -webkit-line-clamp: 2;
    line-height: 1.3;
  }
  .alerts-status {
    grid-column: 3;
    grid-row: 1;
  }
  .alerts-resource {
    grid-column: 2;
    grid-row: 2;
    color: var(--muted);
    font-size: 0.78rem;
    line-height: 1.3;
  }
  .alerts-updated {
    grid-column: 1;
    grid-row: 1 / span 2;
    padding-top: 1px;
    color: var(--text);
    font-size: 0.84rem;
    font-weight: 500;
  }
  .alerts-occurrences {
    grid-column: 3;
    grid-row: 2;
    color: var(--muted);
    font-size: 0.74rem;
    text-align: right;
  }
  .alerts-updated-full,
  .alerts-occurrences-full { display: none; }
  .alerts-updated-compact,
  .alerts-occurrences-compact { display: inline; }
}
</style>
