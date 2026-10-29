<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import { fetchEventFilters, fetchEvents } from "../api/overview";
import type { EventCategory, EventFilterOptions, EventFilterService, EventHistoryItem, EventSeverity } from "../api/types";
import AppMultiSelect from "../components/common/AppMultiSelect.vue";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import AppPagination from "../components/common/AppPagination.vue";
import SidebarIcon from "../components/layout/SidebarIcon.vue";
import { eventTypeDescriptions, eventTypeMetadata } from "../utils/eventMetadata";
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
const cursors = ref<string[]>([""]);
const page = ref(0);
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

const typeFilterOptions = computed(() => {
  const options = eventTypeMetadata.map((metadata) => ({ ...metadata }));
  for (const type of selectedTypes.value) {
    if (!options.some((option) => option.type === type)) {
      options.push({ type, description: eventTypeDescriptions.get(type) ?? "Custom event type" });
    }
  }
  return options.map((option) => ({ value: option.type, label: option.type, description: option.description }));
});

const selectedTypeSummary = computed(() => {
  if (selectedTypes.value.length === 0) return "All types";
  if (selectedTypes.value.length <= 2) return selectedTypes.value.join(", ");
  return `${selectedTypes.value.length} types selected`;
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

function selectTypes(types: string[]): void {
  selectedTypes.value = types;
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
  ++requestID;
  page.value = 0;
  cursors.value = [""];
  events.value = [];
  nextCursor.value = "";
  await loadPage(0, "");
}

async function loadPage(index: number, cursor: string) {
  const currentRequestID = ++requestID;
  loading.value = true;
  loadingError.value = "";
  expandedKey.value = "";

  try {
    const response = await fetchEvents(eventQuery(cursor));
    if (currentRequestID !== requestID) {
      return;
    }
    events.value = Array.isArray(response.events) ? response.events : [];
    nextCursor.value = response.nextCursor ?? "";
    page.value = index;
    cursors.value[index] = cursor;
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

function nextPage(): void {
  if (!loading.value && nextCursor.value) void loadPage(page.value + 1, nextCursor.value);
}

function previousPage(): void {
  if (!loading.value && page.value > 0) void loadPage(page.value - 1, cursors.value[page.value - 1] ?? "");
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
          <AppMultiSelect
            class="events-type-filter"
            :model-value="selectedTypes"
            :options="typeFilterOptions"
            label="Filter events by type"
            placeholder="All types"
            heading="Event types"
            :selection-label="selectedTypeSummary"
            descriptive
            @update:model-value="selectTypes"
          >
            <template #icon>
              <svg class="events-filter-icon" viewBox="0 0 20 20" aria-hidden="true">
                <path d="M3.5 5h13l-5 5.7v4.1l-3 1.5v-5.6L3.5 5Z" />
              </svg>
            </template>
          </AppMultiSelect>
        </div>

        <label class="events-filter-field events-filter-field--select">
          <svg class="events-filter-icon events-severity-filter-icon" viewBox="0 0 20 20" aria-hidden="true">
            <path d="M5 12.5v2.5M10 9v6M15 5v10" />
          </svg>
          <select v-model="selectedSeverity" aria-label="Filter events by severity">
            <option value="">All severities</option>
            <option v-for="severity in severityOptions" :key="severity" :value="severity">{{ severityLabels[severity] }}</option>
          </select>
        </label>

        <label class="events-filter-field events-filter-field--select">
          <svg class="events-filter-icon" viewBox="0 0 20 20" aria-hidden="true">
            <path d="M3.5 4.5v5.3l6.7 6.7 6.3-6.3-6.7-6.7H4.5a1 1 0 0 0-1 1Z" />
            <circle cx="7" cy="7" r="1" />
          </svg>
          <select v-model="selectedCategory" aria-label="Filter events by category">
            <option value="">All categories</option>
            <option v-for="category in categoryOptions" :key="category" :value="category">
              {{ category.charAt(0).toUpperCase() + category.slice(1) }}
            </option>
          </select>
        </label>

        <div class="events-filter-field">
          <details ref="stackFilter" class="events-search-filter events-filter-dropdown">
            <summary aria-label="Filter events by stack">
              <SidebarIcon class="events-filter-icon" name="services" />
              <span class="events-filter-selection" :class="{ 'events-filter-placeholder': !selectedStack }">{{ selectedStack || "All stacks" }}</span>
              <span class="events-filter-chevron" aria-hidden="true">⌄</span>
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
          <details ref="serviceFilter" class="events-search-filter events-filter-dropdown">
            <summary aria-label="Filter events by service">
              <svg class="events-filter-icon" viewBox="0 0 20 20" aria-hidden="true">
                <path d="m10 2.8 6.5 3.6v7.2L10 17.2l-6.5-3.6V6.4L10 2.8Z" />
                <path d="m3.8 6.5 6.2 3.4 6.2-3.4M10 9.9v7" />
              </svg>
              <span class="events-filter-selection" :class="{ 'events-filter-placeholder': !selectedService }">{{ selectedServiceLabel }}</span>
              <span class="events-filter-chevron" aria-hidden="true">⌄</span>
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
    <p v-if="loadingError && events.length > 0" class="meta" role="alert">Failed to load events: {{ loadingError }}</p>
    <AppPagination v-if="events.length > 0 || page > 0" class="events-pagination" label="Event pages" :page="page" :loading="loading" :has-next="Boolean(nextCursor)" @previous="previousPage" @next="nextPage" />
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
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 10px;
  align-items: center;
  box-sizing: border-box;
  width: 100%;
  min-width: 0;
  border: 1px solid var(--border-subtle);
  border-radius: 10px;
  background: var(--surface-card-soft);
  padding: 12px;
}
.events-filter-field {
  position: relative;
  min-width: 0;
}
.events-filter-field select,
.events-filter-dropdown > summary {
  box-sizing: border-box;
  width: 100%;
  height: 38px;
  border: 1px solid var(--border-default);
  border-radius: 7px;
  background: var(--surface-input);
  color: var(--text);
  font: inherit;
  font-size: 0.83rem;
}
.events-filter-field select {
  padding: 7px 10px 7px 36px;
}
.events-filter-icon {
  width: 17px;
  height: 17px;
  flex: 0 0 17px;
  color: var(--muted);
  fill: none;
  stroke: currentColor;
  stroke-width: 1.6;
  stroke-linecap: round;
  stroke-linejoin: round;
  pointer-events: none;
}
.events-filter-field--select > .events-filter-icon {
  position: absolute;
  z-index: 1;
  top: 50%;
  left: 11px;
  transform: translateY(-50%);
}
.events-severity-filter-icon {
  stroke-width: 1.35;
}
.events-filter-dropdown {
  position: relative;
  min-width: 0;
}
.events-filter-dropdown > summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 7px 10px;
  cursor: pointer;
  list-style: none;
}
.events-filter-dropdown > summary::-webkit-details-marker {
  display: none;
}
.events-filter-selection {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  color: var(--text);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.events-filter-placeholder {
  color: var(--text);
}
.events-filter-chevron {
  flex: 0 0 auto;
  color: var(--muted);
}
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
.events-search-filter-menu button {
  display: flex;
  align-items: center;
}
.events-search-filter-menu button:hover,
.events-search-filter-menu button.selected {
  background: var(--surface-hover-soft);
}
.events-search-filter-menu small {
  color: var(--muted);
  font-size: 0.7rem;
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
.events-filter-field select:focus-visible,
.events-filter-dropdown > summary:focus-visible {
  outline: 2px solid var(--accent-focus);
  outline-offset: 2px;
}
@media (max-width: 980px) {
  .events-filter-bar {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
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
    padding: 10px;
  }
  .events-filter-bar--open {
    display: grid;
  }
  .events-search-filter-menu {
    width: 100%;
    max-width: 100%;
  }
  .events-filter-field select {
    font-size: 16px;
  }
}
</style>
