<script setup lang="ts">
import { enumLabel, locale, plural, t } from "../i18n";
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import { fetchEventFilters, fetchEvents } from "../api/overview";
import type { EventCategory, EventFilterOptions, EventFilterService, EventHistoryItem, EventSeverity } from "../api/types";
import AppFilterPanel from "../components/common/AppFilterPanel.vue";
import AppSelect from "../components/common/AppSelect.vue";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import AppPagination from "../components/common/AppPagination.vue";
import SeverityIcon from "../components/common/SeverityIcon.vue";
import SidebarIcon from "../components/layout/SidebarIcon.vue";
import { eventFilterTypes } from "../utils/eventMetadata";
import { eventCategoryLabel, eventDetailLabel, eventMessage, eventSeverityLabel } from "../utils/eventPresentation";
import { formatDate } from "../utils/format";

const severityOptions: EventSeverity[] = ["info", "warn", "error"];

type SortKey = "time" | "severity";
type SortDirection = "asc" | "desc";

const categoryOptions: EventCategory[] = ["sync", "security", "swarm"];
const severitySelectOptions = computed(() => severityOptions.map((value) => ({
  value, label: eventSeverityLabel(value),
})));
const categorySelectOptions = computed(() => categoryOptions.map((value) => ({
  value, label: eventCategoryLabel(value),
})));

const route = useRoute();
const router = useRouter();
const selectedTypes = ref<string[]>(normalizeTypesQuery(route.query.types));
const selectedSeverity = ref(normalizeSeverityQuery(route.query.severity));
const selectedCategory = ref(normalizeCategoryQuery(route.query.category));
const selectedStack = ref(normalizeTextQuery(route.query.stack_name));
const selectedService = ref(normalizeTextQuery(route.query.service_name));
const sortKey = ref<SortKey>(normalizeSortQuery(route.query.sort));
const sortDirection = ref<SortDirection>(normalizeSortDirectionQuery(route.query.order));
const expandedKey = ref("");
const stackFilter = ref<{ close(): void } | null>(null);
const filterOptions = ref<EventFilterOptions>({ stacks: [], services: [] });

const typeFilterOptions = computed(() => {
  const types = new Set([...eventFilterTypes, ...selectedTypes.value]);
  return Array.from(types, (type) => ({
    value: type,
    label: type,
    description: enumLabel("eventTypeDescriptions", type, t("events.customType")),
  }));
});

const selectedTypeSummary = computed(() => {
  if (selectedTypes.value.length === 0) return t("activity.allTypes");
  if (selectedTypes.value.length <= 2) return selectedTypes.value.join(", ");
  return plural("events.typesSelected", selectedTypes.value.length);
});

const activeFilterCount = computed(() => [
  selectedTypes.value.length > 0,
  Boolean(selectedSeverity.value),
  Boolean(selectedCategory.value),
  Boolean(selectedStack.value),
  Boolean(selectedService.value),
].filter(Boolean).length);

const stackFilterOptions = computed(() => {
  const options = [...filterOptions.value.stacks];
  if (selectedStack.value && !options.includes(selectedStack.value)) {
    options.push(selectedStack.value);
  }
  return options.sort((left, right) => left.localeCompare(right))
    .map((stack) => ({ value: stack, label: stack }));
});

const serviceFilterOptions = computed(() => {
  const options = filterOptions.value.services.filter(
    (service) => !selectedStack.value || service.stack_name === selectedStack.value,
  );
  if (selectedService.value && selectedStack.value && !options.some(
    (service) => service.stack_name === selectedStack.value && service.service_name === selectedService.value,
  )) {
    options.push({ stack_name: selectedStack.value, service_name: selectedService.value });
  }
  if (selectedService.value && !selectedStack.value && !options.some(
    (service) => service.service_name === selectedService.value,
  )) {
    options.push({ stack_name: "", service_name: selectedService.value });
  }
  return options.sort((left, right) =>
    left.stack_name.localeCompare(right.stack_name) || left.service_name.localeCompare(right.service_name),
  );
});

const serviceSelectOptions = computed(() => serviceFilterOptions.value.map((service) => ({
  value: serviceKey(service),
  label: service.service_name,
  description: selectedStack.value ? undefined : service.stack_name,
  searchText: `${service.stack_name} ${service.service_name}`,
})));
const selectedServiceKey = computed(() => selectedService.value
  ? serviceKey({ stack_name: selectedStack.value, service_name: selectedService.value })
  : "");

const selectedServiceLabel = computed(() => {
  if (!selectedService.value) return t("deployments.allServices");
  if (selectedStack.value) return selectedService.value;
  const matches = filterOptions.value.services.filter((service) => service.service_name === selectedService.value);
  return matches.length === 1 ? `${matches[0].service_name} — ${matches[0].stack_name}` : selectedService.value;
});

function serviceKey(service: EventFilterService): string {
  return JSON.stringify([service.stack_name, service.service_name]);
}

function firstQueryValue(value: unknown): unknown {
  return Array.isArray(value) ? value[0] : value;
}

function normalizeSeverityQuery(value: unknown): EventSeverity | "" {
  const severity = firstQueryValue(value);
  return severityOptions.includes(severity as EventSeverity) ? (severity as EventSeverity) : "";
}

function normalizeCategoryQuery(value: unknown): EventCategory | "" {
  const category = firstQueryValue(value);
  return categoryOptions.includes(category as EventCategory) ? (category as EventCategory) : "";
}

function normalizeTextQuery(value: unknown): string {
  const text = firstQueryValue(value);
  return typeof text === "string" ? text.trim() : "";
}

function normalizeTypesQuery(value: unknown): string[] {
  const rawTypes = Array.isArray(value) ? value : value ? [value] : [];
  return [...new Set(rawTypes.filter((type): type is string => typeof type === "string" && type.length > 0))];
}

function normalizeSortQuery(value: unknown): SortKey {
  return firstQueryValue(value) === "severity" ? "severity" : "time";
}

function normalizeSortDirectionQuery(value: unknown): SortDirection {
  return firstQueryValue(value) === "asc" ? "asc" : "desc";
}

function sameStrings(left: string[], right: string[]): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

function normalizedSeverity(item: EventHistoryItem): EventSeverity {
  switch (item.severity) {
    case "warn":
    case "error":
    case "alert":
      return item.severity;
    case "info":
    default:
      return "info";
  }
}

interface EventGroup {
  key: string;
  label: string;
  events: EventHistoryItem[];
}

function localDateKey(date: Date): string {
  return `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`;
}

function eventDateLabel(date: Date): string {
  const dateLabel = date.toLocaleDateString(locale(), {
    day: "numeric",
    month: "long",
    year: date.getFullYear() === new Date().getFullYear() ? undefined : "numeric",
  });
  return localDateKey(date) === localDateKey(new Date())
    ? t("deployments.todayDate", { date: dateLabel })
    : dateLabel;
}

function eventGroups(items: EventHistoryItem[]): EventGroup[] {
  const groups = new Map<string, EventGroup>();
  for (const event of items) {
    const date = new Date(event.created_at);
    const validDate = !Number.isNaN(date.valueOf());
    const key = validDate ? localDateKey(date) : event.created_at;
    const group = groups.get(key) ?? {
      key,
      label: validDate ? eventDateLabel(date) : event.created_at,
      events: [],
    };
    group.events.push(event);
    groups.set(key, group);
  }
  return [...groups.values()];
}

function formatEventTime(raw: string): string {
  return formatDate(raw, { hour: "2-digit", minute: "2-digit" });
}

function sortedEventDetails(item: EventHistoryItem): [string, string][] {
  return Object.entries(item.details ?? {}).sort(([leftKey], [rightKey]) => leftKey.localeCompare(rightKey));
}

function toggleDetails(event: EventHistoryItem): void {
  const key = event.id;
  expandedKey.value = expandedKey.value === key ? "" : key;
}

function selectTypes(types: string[]): void {
  selectedTypes.value = types;
}

function applyDetailFilter(key: string, value: string, event: EventHistoryItem): void {
  if (key === "stack_name") {
    selectStack(value);
    stackFilter.value?.close();
  } else if (key === "service_name") {
    const stackName = event.details?.stack_name?.trim() ?? "";
    if (stackName) selectedStack.value = stackName;
    selectedService.value = value;
  }
}

function canFilterDetail(key: string): boolean {
  return key === "stack_name" || key === "service_name";
}

function selectStack(stackName: string): void {
  if (stackName !== selectedStack.value && selectedService.value) {
    const compatible = Boolean(stackName) && filterOptions.value.services.some(
      (service) => service.stack_name === stackName && service.service_name === selectedService.value,
    );
    if (!compatible) selectedService.value = "";
  }
  selectedStack.value = stackName;
}

function selectService(value: string): void {
  if (!value) {
    selectedService.value = "";
    return;
  }
  const service = serviceFilterOptions.value.find((option) => serviceKey(option) === value);
  if (!service) return;
  if (!selectedStack.value && service.stack_name) selectedStack.value = service.stack_name;
  selectedService.value = service.service_name;
}

function changeSort(key: SortKey): void {
  if (sortKey.value === key) {
    sortDirection.value = sortDirection.value === "desc" ? "asc" : "desc";
    return;
  }

  sortKey.value = key;
  sortDirection.value = "desc";
}

function ariaSort(key: SortKey): "ascending" | "descending" | "none" {
  if (sortKey.value !== key) {
    return "none";
  }
  return sortDirection.value === "asc" ? "ascending" : "descending";
}

function sortIndicator(key: SortKey): string {
  if (sortKey.value !== key) {
    return "↕";
  }
  return sortDirection.value === "asc" ? "↑" : "↓";
}

const PAGE_SIZE = 50;

const pageQuery = computed(() => ({
  types: selectedTypes.value,
  severities: selectedSeverity.value ? [selectedSeverity.value] : [],
  ...(selectedCategory.value ? { categories: [selectedCategory.value] } : {}),
  ...(selectedStack.value ? { stackNames: [selectedStack.value] } : {}),
  ...(selectedService.value ? { serviceNames: [selectedService.value] } : {}),
  limit: PAGE_SIZE,
  sort: sortKey.value,
  order: sortDirection.value,
}));

async function loadEvents(filters: typeof pageQuery.value, cursor?: string) {
  expandedKey.value = "";
  const response = await fetchEvents(cursor ? { ...filters, cursor } : filters);
  return { items: Array.isArray(response.events) ? response.events : [], nextCursor: response.nextCursor };
}

async function loadFilterOptions() {
  try {
    const response = await fetchEventFilters();
    filterOptions.value = {
      stacks: Array.isArray(response.stacks) ? response.stacks : [],
      services: Array.isArray(response.services) ? response.services : [],
    };
  } catch {
    filterOptions.value = { stacks: [], services: [] };
  }
}

async function persistFilters(): Promise<void> {
  const currentValues = [
    normalizeSeverityQuery(route.query.severity),
    normalizeCategoryQuery(route.query.category),
    normalizeTextQuery(route.query.stack_name),
    normalizeTextQuery(route.query.service_name),
  ];
  const selectedValues = [
    selectedSeverity.value, selectedCategory.value, selectedStack.value, selectedService.value,
  ];
  if (sameStrings(normalizeTypesQuery(route.query.types), selectedTypes.value) &&
    currentValues.every((value, index) => value === selectedValues[index])) {
    return;
  }

  const nextQuery = { ...route.query };
  if (selectedTypes.value.length > 0) {
    nextQuery.types = [...selectedTypes.value];
  } else {
    delete nextQuery.types;
  }
  const values: [string, string][] = [
    ["severity", selectedSeverity.value],
    ["category", selectedCategory.value],
    ["stack_name", selectedStack.value],
    ["service_name", selectedService.value],
  ];
  for (const [key, value] of values) {
    if (value) {
      nextQuery[key] = value;
    } else {
      delete nextQuery[key];
    }
  }
  await router.replace({ query: nextQuery });
}

watch([sortKey, sortDirection], async ([key, direction]) => {
  if (normalizeSortQuery(route.query.sort) === key && normalizeSortDirectionQuery(route.query.order) === direction) {
    return;
  }

  await router.replace({
    query: {
      ...route.query,
      sort: key,
      order: direction,
    },
  });
});

watch(
  () => [route.query.severity, route.query.category, route.query.stack_name, route.query.service_name],
  ([severity, category, stackName, serviceName]) => {
    selectedSeverity.value = normalizeSeverityQuery(severity);
    selectedCategory.value = normalizeCategoryQuery(category);
    selectedStack.value = normalizeTextQuery(stackName);
    selectedService.value = normalizeTextQuery(serviceName);
  },
);

watch(
  () => route.query.types,
  (types) => {
    const normalizedTypes = normalizeTypesQuery(types);
    if (!sameStrings(selectedTypes.value, normalizedTypes)) {
      selectedTypes.value = normalizedTypes;
    }
  },
);

watch(
  () => [route.query.sort, route.query.order],
  ([sort, order]) => {
    const nextSortKey = normalizeSortQuery(sort);
    const nextSortDirection = normalizeSortDirectionQuery(order);
    if (sortKey.value !== nextSortKey) {
      sortKey.value = nextSortKey;
    }
    if (sortDirection.value !== nextSortDirection) {
      sortDirection.value = nextSortDirection;
    }
  },
);

watch([selectedTypes, selectedSeverity, selectedCategory, selectedStack, selectedService], () => {
  void persistFilters();
});

onMounted(() => {
  void loadFilterOptions();
});
</script>

<template>
  <section class="services-page events-page">
    <AppFilterPanel panel-id="events-filter-panel" :active-count="activeFilterCount" :columns="5">
      <AppSelect
        multiple
        class="events-type-filter"
        :model-value="selectedTypes"
        :options="typeFilterOptions"
        :label="t('events.filterType')"
        :placeholder="t('activity.allTypes')"
        :heading="t('events.types')"
        :selection-label="selectedTypeSummary"
        descriptive
        @update:model-value="selectTypes"
      >
        <template #icon>
          <svg class="events-filter-icon" viewBox="0 0 20 20" aria-hidden="true">
            <path d="M3.5 5h13l-5 5.7v4.1l-3 1.5v-5.6L3.5 5Z" />
          </svg>
        </template>
      </AppSelect>

      <AppSelect
        v-model="selectedSeverity"
        :options="severitySelectOptions"
        :reset-option="{ value: '', label: t('activity.allSeverities') }"
        :label="t('events.filterSeverity')"
      >
        <template #icon><SeverityIcon class="events-filter-icon" /></template>
      </AppSelect>

      <AppSelect
        v-model="selectedCategory"
        :options="categorySelectOptions"
        :reset-option="{ value: '', label: t('activity.allCategories') }"
        :label="t('events.filterCategory')"
      >
        <template #icon>
          <svg class="events-filter-icon" viewBox="0 0 20 20" aria-hidden="true">
            <path d="M3.5 4.5v5.3l6.7 6.7 6.3-6.3-6.7-6.7H4.5a1 1 0 0 0-1 1Z" />
            <circle cx="7" cy="7" r="1" />
          </svg>
        </template>
      </AppSelect>

      <AppSelect
        ref="stackFilter"
        :model-value="selectedStack"
        :options="stackFilterOptions"
        :reset-option="{ value: '', label: t('deployments.allStacks') }"
        :label="t('events.filterStack')"
        searchable
        :search-label="t('common.searchStacks')"
        :search-placeholder="t('common.searchStacks')"
        :empty-message="t('common.noMatchingStacks')"
        @update:model-value="selectStack"
      >
        <template #icon>
          <SidebarIcon class="events-filter-icon" name="services" />
        </template>
      </AppSelect>

      <AppSelect
        :model-value="selectedServiceKey"
        :options="serviceSelectOptions"
        :reset-option="{ value: '', label: t('deployments.allServices') }"
        :selection-label="selectedServiceLabel"
        :label="t('events.filterService')"
        searchable
        :search-label="t('common.searchServices')"
        :search-placeholder="t('common.searchServices')"
        :empty-message="t('common.noMatchingServices')"
        @update:model-value="selectService"
      >
        <template #icon>
          <svg class="events-filter-icon" viewBox="0 0 20 20" aria-hidden="true">
            <path d="m10 2.8 6.5 3.6v7.2L10 17.2l-6.5-3.6V6.4L10 2.8Z" />
            <path d="m3.8 6.5 6.2 3.4 6.2-3.4M10 9.9v7" />
          </svg>
        </template>
      </AppSelect>
    </AppFilterPanel>

    <AppPagination
      class="events-pagination"
      :label="t('events.pages')"
      :query="pageQuery"
      :load-page="loadEvents"
      hide-when-empty
      v-slot="{ items: events, loading, error: loadingError }"
    >
      <AppTableEmpty v-if="loading && events.length === 0" :message="t('events.loading')" />

      <AppTableEmpty v-else-if="loadingError && events.length === 0" role="alert">{{ t('events.loadError', { error: loadingError }) }}</AppTableEmpty>

      <AppTableEmpty v-else-if="events.length === 0" :message="t('events.empty')" />

      <AppTable v-else fixed table-class="events-table" wrap-class="events-table-wrap">
        <template #head>
          <tr>
            <th>{{ t('common.type') }}</th>
            <th :aria-sort="ariaSort('severity')">
              <button class="app-table-sort" type="button" @click="changeSort('severity')">
                {{ t('activity.severity') }} <span class="app-table-sort-indicator" aria-hidden="true">{{ sortIndicator("severity") }}</span>
              </button>
            </th>
            <th>{{ t('common.message') }}</th>
            <th :aria-sort="ariaSort('time')">
              <button class="app-table-sort" type="button" @click="changeSort('time')">
                {{ t('common.time') }} <span class="app-table-sort-indicator" aria-hidden="true">{{ sortIndicator("time") }}</span>
              </button>
            </th>
          </tr>
        </template>
        <template v-for="group in eventGroups(events)" :key="group.key">
          <tr class="events-date-row"><th colspan="4" scope="rowgroup">{{ group.label }}</th></tr>
          <template v-for="event in group.events" :key="event.id">
            <tr
              class="app-table-row--clickable events-row"
              tabindex="0"
              role="button"
              :aria-expanded="expandedKey === event.id"
              :aria-label="t('events.toggleDetails', { type: event.type || t('events.event') })"
              @click="toggleDetails(event)"
              @keydown.enter="toggleDetails(event)"
              @keydown.space.prevent="toggleDetails(event)"
            >
              <td class="events-type">
                <span class="events-type-content">
                  <SidebarIcon class="events-row-chevron" :class="{ 'events-row-chevron--open': expandedKey === event.id }" name="chevron" />
                  <span class="events-type-label" :title="event.type || t('common.unknown')">{{ event.type || t('common.unknown') }}</span>
                </span>
              </td>
              <td class="events-severity">
                <span class="event-severity" :class="`event-severity-${normalizedSeverity(event)}`">
                  {{ eventSeverityLabel(event.severity) }}
                </span>
              </td>
              <td class="events-message">{{ eventMessage(event) }}</td>
              <td class="events-time">
                <time class="events-time-full" :datetime="event.created_at">{{ formatDate(event.created_at) }}</time>
                <time class="events-time-compact" :datetime="event.created_at">{{ formatEventTime(event.created_at) }}</time>
              </td>
            </tr>
            <tr v-if="expandedKey === event.id" class="events-details-row">
              <td colspan="4">
                <ul v-if="sortedEventDetails(event).length > 0" class="event-details">
                  <li v-for="[key, value] in sortedEventDetails(event)" :key="key" class="event-detail">
                    <span class="event-detail-key">{{ eventDetailLabel(key) }}</span>
                    <span class="event-detail-content">
                      <code class="event-detail-value">{{ value }}</code>
                      <button
                        v-if="canFilterDetail(key)"
                        class="event-detail-filter"
                        type="button"
                        :aria-label="t('events.filterDetail', { field: eventDetailLabel(key), value })"
                        @click.stop="applyDetailFilter(key, value, event)"
                      >
                        {{ t('activity.filter') }}
                      </button>
                    </span>
                  </li>
                </ul>
                <p v-else class="meta">{{ t('events.noDetails') }}</p>
              </td>
            </tr>
          </template>
        </template>
      </AppTable>
      <p v-if="loadingError && events.length > 0" class="meta" role="alert">{{ t('events.loadError', { error: loadingError }) }}</p>
    </AppPagination>
  </section>
</template>

<style scoped>
.events-page { min-width: 0; }
:deep(.events-table) { min-width: 720px; }
:deep(.events-table th:nth-child(1)),
:deep(.events-table td:nth-child(1)) { width: 21%; }
:deep(.events-table th:nth-child(2)),
:deep(.events-table td:nth-child(2)) { width: 14%; }
:deep(.events-table th:nth-child(3)),
:deep(.events-table td:nth-child(3)) { width: 43%; }
:deep(.events-table th:nth-child(4)),
:deep(.events-table td:nth-child(4)) { width: 22%; }

.events-date-row,
.events-time-compact { display: none; }
.events-type { overflow: hidden; }
.events-type-content {
  width: 100%;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 7px;
  overflow: hidden;
  font-weight: 600;
}
.events-type-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.events-row-chevron {
  width: 14px;
  height: 14px;
  flex: 0 0 14px;
  color: var(--muted);
  transform: rotate(-90deg);
  transition: transform 120ms ease;
}
.events-row-chevron--open { transform: rotate(0deg); }
.events-severity,
.events-time { white-space: nowrap; }
.events-message { overflow-wrap: anywhere; }
.events-details-row .event-details {
  margin: 0;
  gap: 0;
}
.events-details-row .event-detail {
  grid-template-columns: minmax(130px, 18%) minmax(0, 1fr);
  gap: 12px;
  padding: 7px 10px;
  border-bottom: 1px solid var(--border-subtle);
}
.events-details-row .event-detail:last-child { border-bottom: 0; }
.events-details-row .event-detail-key {
  border-radius: 0;
  background: transparent;
  padding: 0;
  color: var(--muted);
}
.events-details-row .event-detail-value {
  border: 0;
  border-radius: 0;
  background: transparent;
  padding: 0;
}

@media (max-width: 720px) {
  :deep(.events-table-wrap) { overflow-x: visible; }
  :deep(.events-table) { min-width: 0; table-layout: auto; }
  :deep(.events-table thead) { display: none; }
  :deep(.events-table tbody) { display: block; }
  :deep(.events-date-row) { display: block; }
  :deep(.events-table .events-row > td) { width: auto; }
  :deep(.events-table .events-date-row > th:first-child) {
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
  :deep(.events-row) {
    display: grid;
    grid-template-columns: 64px minmax(0, 1fr) auto;
    grid-template-rows: auto auto;
    gap: 4px 12px;
    align-items: start;
    padding: 12px 10px;
    border-bottom: 1px solid var(--border-subtle);
  }
  :deep(.events-table .events-row > td) {
    border-bottom: 0;
    padding: 0;
  }
  .events-type {
    grid-column: 2;
    grid-row: 1;
    min-width: 0;
    overflow: visible;
  }
  .events-type-content {
    overflow: visible;
    line-height: 1.25;
  }
  .events-type-label {
    overflow: visible;
    text-overflow: clip;
    white-space: normal;
    overflow-wrap: anywhere;
  }
  .events-row-chevron { display: none; }
  .events-severity {
    grid-column: 3;
    grid-row: 1 / span 2;
  }
  .events-message {
    grid-column: 2;
    grid-row: 2;
    min-width: 0;
    color: var(--muted);
    font-size: 0.82rem;
    line-height: 1.3;
  }
  .events-time {
    grid-column: 1;
    grid-row: 1 / span 2;
    padding-top: 1px;
    color: var(--text);
    font-size: 0.84rem;
    font-weight: 500;
  }
  .events-time-full { display: none; }
  .events-time-compact { display: inline; }
  :deep(.events-details-row) {
    display: block;
    border-bottom: 1px solid var(--border-subtle);
  }
  :deep(.events-table .events-details-row > td) {
    display: block;
    width: 100%;
    box-sizing: border-box;
    padding: 0 10px 10px 86px;
    border-bottom: 0;
  }
  .events-details-row .event-detail {
    grid-template-columns: minmax(90px, 34%) minmax(0, 1fr);
    gap: 8px;
    padding: 6px 0;
  }
  .events-details-row .event-detail-content { align-items: center; }
}
</style>
