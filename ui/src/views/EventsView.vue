<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import { fetchEventFilters, fetchEvents } from "../api/overview";
import type { EventCategory, EventFilterOptions, EventFilterService, EventHistoryItem, EventSeverity } from "../api/types";
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

const categoryOptions: EventCategory[] = ["sync", "security", "swarm"];

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
const sortKey = ref<SortKey>(normalizeSortQuery(route.query.sort));
const sortDirection = ref<SortDirection>(normalizeSortDirectionQuery(route.query.order));
const expandedKey = ref("");
const filterPanel = ref<HTMLElement | null>(null);
const stackFilter = ref<HTMLDetailsElement | null>(null);
const serviceFilter = ref<HTMLDetailsElement | null>(null);
const filterOptions = ref<EventFilterOptions>({ stacks: [], services: [] });
const stackSearch = ref("");
const serviceSearch = ref("");
const mobileFiltersOpen = ref(false);

let requestID = 0;

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
  const search = stackSearch.value.trim().toLocaleLowerCase();
  return options.sort((left, right) => left.localeCompare(right))
    .filter((stack) => !search || stack.toLocaleLowerCase().includes(search));
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
  const search = serviceSearch.value.trim().toLocaleLowerCase();
  return options.sort((left, right) =>
    left.stack_name.localeCompare(right.stack_name) || left.service_name.localeCompare(right.service_name),
  ).filter((service) => !search || `${service.stack_name} ${service.service_name}`.toLocaleLowerCase().includes(search));
});

const selectedServiceLabel = computed(() => {
  if (!selectedService.value) return "All services";
  if (selectedStack.value) return selectedService.value;
  const matches = filterOptions.value.services.filter((service) => service.service_name === selectedService.value);
  return matches.length === 1 ? `${matches[0].service_name} — ${matches[0].stack_name}` : selectedService.value;
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

function applyDetailFilter(key: string, value: string, event: EventHistoryItem): void {
  if (key === "stack_name") {
    selectStack(value);
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
  stackSearch.value = "";
  if (stackFilter.value) stackFilter.value.open = false;
}

function selectService(service?: EventFilterService): void {
  if (!service) {
    selectedService.value = "";
  } else {
    if (!selectedStack.value && service.stack_name) selectedStack.value = service.stack_name;
    selectedService.value = service.service_name;
  }
  serviceSearch.value = "";
  if (serviceFilter.value) serviceFilter.value.open = false;
}

function handleDocumentClick(event: MouseEvent): void {
  const target = event.target;
  if (!(target instanceof Node) || filterPanel.value?.contains(target)) {
    return;
  }
  for (const filter of filterPanel.value?.querySelectorAll<HTMLDetailsElement>(".events-filter-dropdown[open]") ?? []) {
    filter.open = false;
  }
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
  void loadFilterOptions();
  void loadEvents();
});

onBeforeUnmount(() => {
  document.removeEventListener("click", handleDocumentClick);
});
</script>

<template>
  <section class="services-page events-page">
    <header class="services-header events-header">
      <button
        class="events-mobile-filter-toggle"
        type="button"
        :aria-expanded="mobileFiltersOpen"
        aria-controls="events-filter-panel"
        @click="mobileFiltersOpen = !mobileFiltersOpen"
      >
        Filters ({{ activeFilterCount }})
      </button>
      <div
        id="events-filter-panel"
        ref="filterPanel"
        class="events-filter-bar"
        :class="{ 'events-filter-bar--open': mobileFiltersOpen }"
      >
        <div class="events-filter-field">
          <span>Type</span>
          <details class="events-type-filter events-filter-dropdown">
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
                  <span class="events-type-filter-option-name" :title="metadata.type">{{ metadata.type }}</span>
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

        <div class="events-filter-field">
          <span>Stack</span>
          <details ref="stackFilter" class="events-search-filter events-filter-dropdown">
            <summary aria-label="Filter events by stack">
              <span :class="{ 'events-filter-placeholder': !selectedStack }">{{ selectedStack || "All stacks" }}</span>
              <span aria-hidden="true">⌄</span>
            </summary>
            <div class="events-search-filter-menu">
              <input v-model="stackSearch" type="search" placeholder="Search stacks" aria-label="Search stacks" />
              <button type="button" :class="{ selected: !selectedStack }" @click="selectStack('')">All stacks</button>
              <button
                v-for="stack in stackFilterOptions"
                :key="stack"
                type="button"
                :class="{ selected: selectedStack === stack }"
                @click="selectStack(stack)"
              >
                {{ stack }}
              </button>
              <p v-if="stackFilterOptions.length === 0" class="events-filter-empty">No matching stacks</p>
            </div>
          </details>
        </div>

        <div class="events-filter-field">
          <span>Service</span>
          <details ref="serviceFilter" class="events-search-filter events-filter-dropdown">
            <summary aria-label="Filter events by service">
              <span :class="{ 'events-filter-placeholder': !selectedService }">{{ selectedServiceLabel }}</span>
              <span aria-hidden="true">⌄</span>
            </summary>
            <div class="events-search-filter-menu">
              <input v-model="serviceSearch" type="search" placeholder="Search services" aria-label="Search services" />
              <button type="button" :class="{ selected: !selectedService }" @click="selectService()">All services</button>
              <button
                v-for="service in serviceFilterOptions"
                :key="`${service.stack_name}/${service.service_name}`"
                type="button"
                :class="{ selected: selectedService === service.service_name && (!selectedStack || selectedStack === service.stack_name) }"
                @click="selectService(service)"
              >
                <span>{{ service.service_name }}</span>
                <small v-if="!selectedStack && service.stack_name">{{ service.stack_name }}</small>
              </button>
              <p v-if="serviceFilterOptions.length === 0" class="events-filter-empty">No matching services</p>
            </div>
          </details>
        </div>
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
                        @click.stop="applyDetailFilter(key, value, event)"
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
.events-header {
  display: block;
}
.events-mobile-filter-toggle {
  display: none;
}
.events-filter-bar {
  display: grid;
  grid-template-columns: minmax(190px, 1.35fr) repeat(4, minmax(130px, 1fr));
  gap: 0.65rem;
  width: 100%;
}
.events-filter-field {
  min-width: 0;
}
.events-filter-field > span {
  display: block;
  margin-bottom: 0.3rem;
  color: var(--muted);
  font-size: 0.75rem;
  font-weight: 600;
}
.events-filter-field select,
.events-filter-dropdown > summary {
  box-sizing: border-box;
  width: 100%;
  height: 2.35rem;
  border: 1px solid var(--border-default);
  border-radius: 0.45rem;
  background: var(--surface-elevated);
  color: var(--text);
  font: inherit;
  font-size: 0.84rem;
}
.events-filter-field select {
  padding: 0 0.65rem;
}
.events-filter-dropdown {
  position: relative;
  min-width: 0;
}
.events-filter-dropdown > summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  padding: 0 0.7rem;
  cursor: pointer;
  list-style: none;
}
.events-filter-dropdown > summary::-webkit-details-marker {
  display: none;
}
.events-filter-placeholder {
  color: var(--muted);
}
.events-type-filter-selection {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.events-type-chip + .events-type-chip::before {
  content: ", ";
}
.events-type-filter-menu,
.events-search-filter-menu {
  position: absolute;
  z-index: 30;
  top: calc(100% + 0.35rem);
  left: 0;
  box-sizing: border-box;
  width: max(100%, 260px);
  max-width: calc(100vw - 2rem);
  max-height: min(320px, calc(100dvh - 8rem));
  overflow-x: hidden;
  overflow-y: auto;
  border: 1px solid var(--border-default);
  border-radius: 0.5rem;
  background: var(--surface-elevated);
  box-shadow: var(--shadow-popover);
  padding: 0.45rem;
}
.events-type-filter-menu-header,
.events-type-filter-option,
.events-search-filter-menu button {
  display: flex;
  align-items: center;
}
.events-type-filter-menu-header {
  justify-content: space-between;
  padding: 0.35rem 0.4rem 0.55rem;
}
.events-type-filter-clear {
  border: 0;
  background: transparent;
  color: var(--accent-strong);
}
.events-type-filter-option {
  align-items: flex-start;
  gap: 0.55rem;
  padding: 0.4rem;
  border-radius: 0.35rem;
}
.events-type-filter-menu {
  width: max(100%, 380px);
}
.events-type-filter-option input {
  flex: 0 0 1rem;
  width: 1rem;
  min-width: 1rem;
  height: 1rem;
  margin: 0.15rem 0 0;
  padding: 0;
}
.events-type-filter-option:hover,
.events-search-filter-menu button:hover,
.events-search-filter-menu button.selected {
  background: var(--surface-hover-soft);
}
.events-type-filter-option-text {
  display: grid;
  gap: 0.15rem;
  min-width: 0;
}
.events-type-filter-option-name {
  overflow: hidden;
  font-size: 0.82rem;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.events-type-filter-option-description,
.events-search-filter-menu small {
  color: var(--muted);
  font-size: 0.7rem;
}
.events-type-filter-option-description {
  line-height: 1.3;
  overflow-wrap: anywhere;
}
.events-search-filter-menu input {
  box-sizing: border-box;
  width: 100%;
  margin-bottom: 0.35rem;
  border: 1px solid var(--border-default);
  border-radius: 0.35rem;
  background: var(--surface);
  color: var(--text);
  padding: 0.5rem 0.6rem;
  font: inherit;
  font-size: 0.82rem;
}
.events-search-filter-menu button {
  justify-content: space-between;
  gap: 0.75rem;
  width: 100%;
  border: 0;
  border-radius: 0.35rem;
  background: transparent;
  color: var(--text);
  padding: 0.5rem 0.55rem;
  text-align: left;
  font: inherit;
  font-size: 0.82rem;
}
.events-filter-empty {
  margin: 0;
  padding: 0.65rem 0.55rem;
  color: var(--muted);
  font-size: 0.78rem;
}
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
@media (max-width: 720px) {
  .events-mobile-filter-toggle {
    display: inline-flex;
    align-items: center;
    width: max-content;
    min-height: 2.3rem;
    border: 1px solid var(--border-default);
    border-radius: 0.45rem;
    background: var(--surface-elevated);
    color: var(--text);
    padding: 0.45rem 0.75rem;
    font: inherit;
    font-size: 0.84rem;
  }
  .events-filter-bar {
    display: none;
    grid-template-columns: 1fr;
  }
  .events-filter-bar--open {
    display: grid;
  }
  .events-type-filter-menu,
  .events-search-filter-menu {
    width: 100%;
    max-width: 100%;
  }
}
</style>
