<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import { fetchEvents } from "../api/overview";
import type { EventHistoryItem, EventSeverity } from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import { formatDate } from "../utils/format";

const eventTypeOptions = [
  "deploySuccess",
  "deployFailed",
  "sendNotificationFailed",
  "syncManualStarted",
  "nodeConnected",
  "nodeDisconnected",
  "serviceMissed",
  "serviceReplicasIncreased",
  "serviceReplicasDecreased",
  "serviceRestarted",
  "servicePruned",
  "networkCreated",
  "userAuthenticated",
  "assistantPromptInjectionDetected",
] as const;
const severityOptions: EventSeverity[] = ["info", "warn", "error", "alert"];

type SortKey = "time" | "severity";
type SortDirection = "asc" | "desc";

const severityRank: Record<EventSeverity, number> = {
  info: 0,
  warn: 1,
  error: 2,
  alert: 3,
};

const route = useRoute();
const router = useRouter();
const loading = ref(false);
const loadingError = ref("");
const events = ref<EventHistoryItem[]>([]);
const selectedTypes = ref<string[]>(normalizeTypesQuery(route.query.types));
const selectedSeverity = ref(normalizeSeverityQuery(route.query.severity));
const sortKey = ref<SortKey>(normalizeSortQuery(route.query.sort));
const sortDirection = ref<SortDirection>(normalizeSortDirectionQuery(route.query.order));
const expandedKey = ref("");
const typeFilter = ref<HTMLDetailsElement | null>(null);

let requestID = 0;

const typeFilterOptions = computed(() => {
  const options = [...eventTypeOptions] as string[];
  for (const type of selectedTypes.value) {
    if (!options.includes(type)) {
      options.push(type);
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

const visibleEvents = computed(() => [...events.value].sort(compareEvents));

function firstQueryValue(value: unknown): unknown {
  return Array.isArray(value) ? value[0] : value;
}

function normalizeSeverityQuery(value: unknown): EventSeverity | "" {
  const severity = firstQueryValue(value);
  return severityOptions.includes(severity as EventSeverity) ? (severity as EventSeverity) : "";
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

function eventKey(event: EventHistoryItem): string {
  return `${event.type}-${event.created_at}-${event.message}`;
}

function eventTimestamp(event: EventHistoryItem): number {
  const timestamp = Date.parse(event.created_at);
  return Number.isNaN(timestamp) ? 0 : timestamp;
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

function compareEvents(left: EventHistoryItem, right: EventHistoryItem): number {
  if (sortKey.value === "severity") {
    const severityComparison = severityRank[normalizedSeverity(left)] - severityRank[normalizedSeverity(right)];
    if (severityComparison !== 0) {
      return sortDirection.value === "asc" ? severityComparison : -severityComparison;
    }

    const timeComparison = eventTimestamp(right) - eventTimestamp(left);
    if (timeComparison !== 0) {
      return timeComparison;
    }
  } else {
    const timeComparison = eventTimestamp(left) - eventTimestamp(right);
    if (timeComparison !== 0) {
      return sortDirection.value === "asc" ? timeComparison : -timeComparison;
    }
  }

  return eventKey(left).localeCompare(eventKey(right));
}

function sortedEventDetails(item: EventHistoryItem): [string, string][] {
  return Object.entries(item.details ?? {}).sort(([leftKey], [rightKey]) => leftKey.localeCompare(rightKey));
}

function toggleDetails(event: EventHistoryItem): void {
  const key = eventKey(event);
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

async function loadEvents() {
  loading.value = true;
  loadingError.value = "";
  const currentRequestID = ++requestID;

  try {
    const response = await fetchEvents({
      types: selectedTypes.value,
      severities: selectedSeverity.value ? [selectedSeverity.value] : [],
    });
    if (currentRequestID !== requestID) {
      return;
    }

    events.value = Array.isArray(response.events) ? response.events : [];
  } catch (error) {
    if (currentRequestID !== requestID) {
      return;
    }

    events.value = [];
    loadingError.value = error instanceof Error ? error.message : "Failed to load events";
  } finally {
    if (currentRequestID === requestID) {
      loading.value = false;
    }
  }
}

watch(selectedTypes, async (types) => {
  const currentTypes = normalizeTypesQuery(route.query.types);
  if (sameStrings(currentTypes, types)) {
    return;
  }

  const nextQuery = { ...route.query };
  if (types.length > 0) {
    nextQuery.types = [...types];
  } else {
    delete nextQuery.types;
  }
  await router.replace({ query: nextQuery });
});

watch(selectedSeverity, async (severity) => {
  if (normalizeSeverityQuery(route.query.severity) === severity) {
    return;
  }

  const nextQuery = { ...route.query };
  if (severity) {
    nextQuery.severity = severity;
  } else {
    delete nextQuery.severity;
  }
  await router.replace({ query: nextQuery });
});

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
  () => route.query.severity,
  (severity) => {
    selectedSeverity.value = normalizeSeverityQuery(severity);
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

watch([selectedTypes, selectedSeverity], () => {
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
      <h2>Events</h2>
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
              <label v-for="type in typeFilterOptions" :key="type" class="events-type-filter-option">
                <input
                  type="checkbox"
                  :checked="selectedTypes.includes(type)"
                  @change="toggleType(type)"
                />
                <span>{{ type }}</span>
              </label>
            </div>
          </details>
        </div>

        <label class="events-filter-field">
          <span>Severity</span>
          <select v-model="selectedSeverity">
            <option value="">All severities</option>
            <option v-for="severity in severityOptions" :key="severity" :value="severity">{{ severity }}</option>
          </select>
        </label>
      </div>
    </header>

    <AppTableEmpty v-if="loading && events.length === 0" message="Loading events..." />

    <AppTableEmpty v-else-if="loadingError">Failed to load events: {{ loadingError }}</AppTableEmpty>

    <AppTableEmpty v-else-if="visibleEvents.length === 0" message="No events match the selected filters." />

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
          <template v-for="event in visibleEvents" :key="eventKey(event)">
            <tr
              class="app-table-row--clickable"
              tabindex="0"
              role="button"
              :aria-expanded="expandedKey === eventKey(event)"
              :aria-label="`Toggle details for ${event.type || 'event'}`"
              @click="toggleDetails(event)"
              @keydown.enter="toggleDetails(event)"
              @keydown.space.prevent="toggleDetails(event)"
            >
              <td>{{ formatDate(event.created_at) }}</td>
              <td>
                <span class="event-severity" :class="`event-severity-${normalizedSeverity(event)}`">
                  {{ normalizedSeverity(event) }}
                </span>
              </td>
              <td>{{ event.type || "unknown" }}</td>
              <td>{{ event.message || "No message" }}</td>
            </tr>
            <tr v-if="expandedKey === eventKey(event)" class="events-details-row">
              <td colspan="4">
                <ul v-if="sortedEventDetails(event).length > 0" class="event-details">
                  <li v-for="[key, value] in sortedEventDetails(event)" :key="key" class="event-detail">
                    <span class="event-detail-key">{{ key }}</span>
                    <code class="event-detail-value">{{ value }}</code>
                  </li>
                </ul>
                <p v-else class="meta">details: n/a</p>
              </td>
            </tr>
          </template>
    </AppTable>
  </section>
</template>
