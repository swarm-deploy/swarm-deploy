<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import { fetchEvents } from "../api/overview";
import type { EventCategory, EventHistoryItem, EventSeverity } from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import { eventTypeDescriptions, eventTypeMetadata, type EventTypeMetadata } from "../utils/eventMetadata";
import { formatDate } from "../utils/format";

const severityOptions: EventSeverity[] = ["info", "warn", "error"];
const severityLabels: Record<EventSeverity, string> = {
  info: "Info",
  warn: "Warning",
  error: "Error",
  alert: "Alert",
};

type SortKey = "time" | "severity";
type SortDirection = "asc" | "desc";
type EventPeriod = "" | "1h" | "24h" | "7d" | "30d";

const categoryOptions: EventCategory[] = ["sync", "security", "swarm"];
const periodOptions: { value: Exclude<EventPeriod, "">; label: string; duration: number }[] = [
  { value: "1h", label: "Last 1h", duration: 60 * 60 * 1000 },
  { value: "24h", label: "Last 24h", duration: 24 * 60 * 60 * 1000 },
  { value: "7d", label: "Last 7d", duration: 7 * 24 * 60 * 60 * 1000 },
  { value: "30d", label: "Last 30d", duration: 30 * 24 * 60 * 60 * 1000 },
];

const route = useRoute();
const router = useRouter();
const loading = ref(false);
const loadingError = ref("");
const events = ref<EventHistoryItem[]>([]);
const nextCursor = ref("");
const loadingMore = ref(false);
const selectedTypes = ref<string[]>(normalizeTypesQuery(route.query.types));
const selectedSeverity = ref(normalizeSeverityQuery(route.query.severity));
const selectedCategory = ref(normalizeCategoryQuery(route.query.category));
const selectedStack = ref(normalizeTextQuery(route.query.stack_name));
const selectedService = ref(normalizeTextQuery(route.query.service_name));
const selectedPeriod = ref(normalizePeriodQuery(route.query.period));
const sortKey = ref<SortKey>(normalizeSortQuery(route.query.sort));
const sortDirection = ref<SortDirection>(normalizeSortDirectionQuery(route.query.order));
const expandedKey = ref("");
const typeFilter = ref<HTMLDetailsElement | null>(null);

let requestID = 0;
let resolvedSince = resolvePeriodSince(selectedPeriod.value);

const typeFilterOptions = computed<EventTypeMetadata[]>(() => {
  const options = eventTypeMetadata.map((metadata) => ({ ...metadata }));
  for (const type of selectedTypes.value) {
    if (!options.some((option) => option.type === type)) {
      options.push({ type, description: eventTypeDescriptions.get(type) ?? "Custom event type" });
    }
  }
  return options;
});

const selectedTypeSummary = computed(() => {
  if (selectedTypes.value.length === 0) {
    return ["All types"];
  }
  if (selectedTypes.value.length <= 2) {
    return selectedTypes.value;
  }
  return [`${selectedTypes.value.length} types selected`];
});


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

function normalizePeriodQuery(value: unknown): EventPeriod {
  const period = firstQueryValue(value);
  return periodOptions.some((option) => option.value === period) ? (period as EventPeriod) : "";
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

function sortedEventDetails(item: EventHistoryItem): [string, string][] {
  return Object.entries(item.details ?? {}).sort(([leftKey], [rightKey]) => leftKey.localeCompare(rightKey));
}

function toggleDetails(event: EventHistoryItem): void {
  const key = event.id;
  expandedKey.value = expandedKey.value === key ? "" : key;
}

function toggleType(type: string): void {
  selectedTypes.value = selectedTypes.value.includes(type)
    ? selectedTypes.value.filter((selectedType) => selectedType !== type)
    : [...selectedTypes.value, type];
}

function clearTypes(): void {
  selectedTypes.value = [];
}

const hasActiveFilters = computed(() =>
  selectedTypes.value.length > 0 || Boolean(selectedSeverity.value) || Boolean(selectedCategory.value) ||
  Boolean(selectedStack.value) || Boolean(selectedService.value) || Boolean(selectedPeriod.value),
);

function clearFilters(): void {
  selectedTypes.value = [];
  selectedSeverity.value = "";
  selectedCategory.value = "";
  selectedStack.value = "";
  selectedService.value = "";
  selectedPeriod.value = "";
}

function applyDetailFilter(key: string, value: string): void {
  if (key === "stack_name") {
    selectedStack.value = value;
  } else if (key === "service_name") {
    selectedService.value = value;
  }
}

function canFilterDetail(key: string): boolean {
  return key === "stack_name" || key === "service_name";
}

function resolvePeriodSince(period: EventPeriod): string {
  const option = periodOptions.find((candidate) => candidate.value === period);
  return option ? new Date(Date.now() - option.duration).toISOString() : "";
}

function handleDocumentClick(event: MouseEvent): void {
  const filter = typeFilter.value;
  const target = event.target;
  if (!filter?.open || !(target instanceof Node) || filter.contains(target)) {
    return;
  }

  filter.open = false;
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

function eventQuery(cursor?: string) {
  return {
    types: selectedTypes.value,
    severities: selectedSeverity.value ? [selectedSeverity.value] : [],
    ...(selectedCategory.value ? { categories: [selectedCategory.value] } : {}),
    ...(selectedStack.value ? { stackNames: [selectedStack.value] } : {}),
    ...(selectedService.value ? { serviceNames: [selectedService.value] } : {}),
    ...(resolvedSince ? { since: resolvedSince } : {}),
    limit: PAGE_SIZE,
    sort: sortKey.value,
    order: sortDirection.value,
    ...(cursor ? { cursor } : {}),
  };
}

async function loadEvents() {
  const currentRequestID = ++requestID;
  loading.value = true;
  loadingMore.value = false;
  loadingError.value = "";
  expandedKey.value = "";
  nextCursor.value = "";
  events.value = [];

  try {
    const response = await fetchEvents(eventQuery());
    if (currentRequestID !== requestID) {
      return;
    }
    events.value = Array.isArray(response.events) ? response.events : [];
    nextCursor.value = response.nextCursor ?? "";
  } catch (error) {
    if (currentRequestID !== requestID) {
      return;
    }
    loadingError.value = error instanceof Error ? error.message : "Failed to load events";
  } finally {
    if (currentRequestID === requestID) {
      loading.value = false;
    }
  }
}

async function loadMoreEvents() {
  if (!nextCursor.value || loadingMore.value || loading.value) {
    return;
  }

  const currentRequestID = ++requestID;
  const cursor = nextCursor.value;
  loadingMore.value = true;
  loadingError.value = "";
  try {
    const response = await fetchEvents(eventQuery(cursor));
    if (currentRequestID !== requestID) {
      return;
    }
    const existingIDs = new Set(events.value.map((event) => event.id));
    const newEvents = (response.events ?? []).filter((event) => !existingIDs.has(event.id));
    events.value = [...events.value, ...newEvents];
    nextCursor.value = response.nextCursor ?? "";
  } catch (error) {
    if (currentRequestID !== requestID) {
      return;
    }
    loadingError.value = error instanceof Error ? error.message : "Failed to load more events";
  } finally {
    if (currentRequestID === requestID) {
      loadingMore.value = false;
    }
  }
}

async function persistFilters(): Promise<void> {
  const currentValues = [
    normalizeSeverityQuery(route.query.severity),
    normalizeCategoryQuery(route.query.category),
    normalizeTextQuery(route.query.stack_name),
    normalizeTextQuery(route.query.service_name),
    normalizePeriodQuery(route.query.period),
  ];
  const selectedValues = [
    selectedSeverity.value, selectedCategory.value, selectedStack.value, selectedService.value, selectedPeriod.value,
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
    ["period", selectedPeriod.value],
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
  () => [route.query.severity, route.query.category, route.query.stack_name, route.query.service_name, route.query.period],
  ([severity, category, stackName, serviceName, period]) => {
    selectedSeverity.value = normalizeSeverityQuery(severity);
    selectedCategory.value = normalizeCategoryQuery(category);
    selectedStack.value = normalizeTextQuery(stackName);
    selectedService.value = normalizeTextQuery(serviceName);
    selectedPeriod.value = normalizePeriodQuery(period);
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

watch([selectedTypes, selectedSeverity, selectedCategory, selectedStack, selectedService, selectedPeriod], () => {
  resolvedSince = resolvePeriodSince(selectedPeriod.value);
  expandedKey.value = "";
  void persistFilters();
  void loadEvents();
});

watch([sortKey, sortDirection], () => {
  expandedKey.value = "";
  void loadEvents();
});

onMounted(() => {
  document.addEventListener("click", handleDocumentClick);
  void loadEvents();
});

onBeforeUnmount(() => {
  document.removeEventListener("click", handleDocumentClick);
});
</script>

<template>
  <section class="services-page events-page">
    <header class="services-header events-header">
      <div class="events-filter-bar">
        <div class="events-filter-field">
          <span>Type</span>
          <details ref="typeFilter" class="events-type-filter">
            <summary aria-label="Filter events by type">
              <span class="events-type-filter-selection">
                <span
                  v-for="label in selectedTypeSummary"
                  :key="label"
                  class="events-type-chip"
                  :class="{ 'events-type-chip--placeholder': selectedTypes.length === 0 }"
                >
                  {{ label }}
                </span>
              </span>
              <span class="events-type-filter-chevron" aria-hidden="true">⌄</span>
            </summary>
            <div class="events-type-filter-menu">
              <div class="events-type-filter-menu-header">
                <strong>Event types</strong>
                <button type="button" class="events-type-filter-clear" :disabled="selectedTypes.length === 0" @click="clearTypes">
                  Clear
                </button>
              </div>
              <label v-for="metadata in typeFilterOptions" :key="metadata.type" class="events-type-filter-option">
                <input
                  type="checkbox"
                  :value="metadata.type"
                  :checked="selectedTypes.includes(metadata.type)"
                  @change="toggleType(metadata.type)"
                />
                <span class="events-type-filter-option-text">
                  <span class="events-type-filter-option-name">{{ metadata.type }}</span>
                  <span class="events-type-filter-option-description">{{ metadata.description }}</span>
                </span>
              </label>
            </div>
          </details>
        </div>

        <label class="events-filter-field">
          <span>Severity</span>
          <select v-model="selectedSeverity" aria-label="Filter events by severity">
            <option value="">All severities</option>
            <option v-for="severity in severityOptions" :key="severity" :value="severity">{{ severityLabels[severity] }}</option>
          </select>
        </label>

        <label class="events-filter-field">
          <span>Category</span>
          <select v-model="selectedCategory" aria-label="Filter events by category">
            <option value="">All categories</option>
            <option v-for="category in categoryOptions" :key="category" :value="category">
              {{ category.charAt(0).toUpperCase() + category.slice(1) }}
            </option>
          </select>
        </label>

        <label class="events-filter-field events-filter-field--text">
          <span>Stack</span>
          <input v-model.trim.lazy="selectedStack" type="text" placeholder="Exact stack name" aria-label="Filter events by stack" />
        </label>

        <label class="events-filter-field events-filter-field--text">
          <span>Service</span>
          <input v-model.trim.lazy="selectedService" type="text" placeholder="Exact service name" aria-label="Filter events by service" />
        </label>

        <label class="events-filter-field">
          <span>Period</span>
          <select v-model="selectedPeriod" aria-label="Filter events by period">
            <option value="">All time</option>
            <option v-for="period in periodOptions" :key="period.value" :value="period.value">{{ period.label }}</option>
          </select>
        </label>

        <button class="events-clear-filters" type="button" :disabled="!hasActiveFilters" @click="clearFilters">
          Clear filters
        </button>
      </div>
    </header>

    <AppTableEmpty v-if="loading && events.length === 0" message="Loading events..." />

    <AppTableEmpty v-else-if="loadingError && events.length === 0">Failed to load events: {{ loadingError }}</AppTableEmpty>

    <AppTableEmpty v-else-if="events.length === 0" message="No events match the selected filters." />

    <AppTable v-else fixed min-width="720px" table-class="events-table">
      <template #head>
          <tr>
            <th :aria-sort="ariaSort('time')">
              <button class="app-table-sort" type="button" @click="changeSort('time')">
                Time <span class="app-table-sort-indicator" aria-hidden="true">{{ sortIndicator("time") }}</span>
              </button>
            </th>
            <th :aria-sort="ariaSort('severity')">
              <button class="app-table-sort" type="button" @click="changeSort('severity')">
                Severity <span class="app-table-sort-indicator" aria-hidden="true">{{ sortIndicator("severity") }}</span>
              </button>
            </th>
            <th>Type</th>
            <th>Message</th>
          </tr>
      </template>
          <template v-for="event in events" :key="event.id">
            <tr
              class="app-table-row--clickable"
              tabindex="0"
              role="button"
              :aria-expanded="expandedKey === event.id"
              :aria-label="`Toggle details for ${event.type || 'event'}`"
              @click="toggleDetails(event)"
              @keydown.enter="toggleDetails(event)"
              @keydown.space.prevent="toggleDetails(event)"
            >
              <td>{{ formatDate(event.created_at) }}</td>
              <td>
                <span class="event-severity" :class="`event-severity-${normalizedSeverity(event)}`">
                  {{ severityLabels[normalizedSeverity(event)] }}
                </span>
              </td>
              <td>{{ event.type || "unknown" }}</td>
              <td>{{ event.message || "No message" }}</td>
            </tr>
            <tr v-if="expandedKey === event.id" class="events-details-row">
              <td colspan="4">
                <ul v-if="sortedEventDetails(event).length > 0" class="event-details">
                  <li v-for="[key, value] in sortedEventDetails(event)" :key="key" class="event-detail">
                    <span class="event-detail-key">{{ key }}</span>
                    <span class="event-detail-content">
                      <code class="event-detail-value">{{ value }}</code>
                      <button
                        v-if="canFilterDetail(key)"
                        class="event-detail-filter"
                        type="button"
                        :aria-label="`Filter by ${key} ${value}`"
                        @click.stop="applyDetailFilter(key, value)"
                      >
                        Filter
                      </button>
                    </span>
                  </li>
                </ul>
                <p v-else class="meta">details: n/a</p>
              </td>
            </tr>
          </template>
    </AppTable>
    <div v-if="events.length > 0 && (nextCursor || loadingMore || loadingError)" class="events-pagination">
      <p v-if="loadingError" class="meta" role="alert">Failed to load more events: {{ loadingError }}</p>
      <button v-if="nextCursor" class="events-load-more" type="button" :disabled="loadingMore" @click="loadMoreEvents">
        {{ loadingMore ? "Loading..." : "Load more" }}
      </button>
    </div>
  </section>
</template>

<style scoped>
.events-pagination {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 1rem;
  padding: 1.25rem;
}
.events-load-more {
  cursor: pointer;
  padding: 0.55rem 1.25rem;
  border: 1px solid var(--border-color, #43495b);
  border-radius: 0.5rem;
  background: var(--surface, transparent);
  color: inherit;
}
.events-load-more:disabled {
  cursor: wait;
  opacity: 0.6;
}
</style>
