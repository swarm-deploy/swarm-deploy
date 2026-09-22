<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
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

const route = useRoute();
const router = useRouter();
const loading = ref(false);
const loadingError = ref("");
const events = ref<EventHistoryItem[]>([]);
const selectedTypes = ref<string[]>(normalizeTypesQuery(route.query.types));
const selectedType = ref(selectedTypes.value.length === 1 ? selectedTypes.value[0] : "");
const selectedSeverity = ref(normalizeSeverityQuery(route.query.severity));
const expandedKey = ref("");

let requestID = 0;

const visibleEvents = computed(() => events.value.slice().reverse());

function normalizeSeverityQuery(value: unknown): EventSeverity | "" {
  const severity = Array.isArray(value) ? value[0] : value;
  return severityOptions.includes(severity as EventSeverity) ? (severity as EventSeverity) : "";
}

function normalizeTypesQuery(value: unknown): string[] {
  const rawTypes = Array.isArray(value) ? value : value ? [value] : [];
  return rawTypes.filter((type): type is string => eventTypeOptions.includes(type as (typeof eventTypeOptions)[number]));
}

function eventKey(event: EventHistoryItem): string {
  return `${event.type}-${event.created_at}-${event.message}`;
}

function normalizedSeverity(item: EventHistoryItem): "info" | "warn" | "error" | "alert" {
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
  const key = eventKey(event);
  expandedKey.value = expandedKey.value === key ? "" : key;
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

watch(selectedType, async (type) => {
  const nextQuery = { ...route.query };
  if (type) {
    nextQuery.types = type;
  } else {
    delete nextQuery.types;
  }

  const currentTypes = normalizeTypesQuery(route.query.types);
  if (!type && currentTypes.length > 1) {
    return;
  }

  if (currentTypes.length !== (type ? 1 : 0) || currentTypes[0] !== type) {
    await router.replace({ query: nextQuery });
  }
});

watch(selectedSeverity, async (severity) => {
  const nextQuery = { ...route.query };
  if (severity) {
    nextQuery.severity = severity;
  } else {
    delete nextQuery.severity;
  }

  if (normalizeSeverityQuery(route.query.severity) !== severity) {
    await router.replace({ query: nextQuery });
  }
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
    selectedTypes.value = normalizeTypesQuery(types);
    selectedType.value = selectedTypes.value.length === 1 ? selectedTypes.value[0] : "";
  },
);

watch([selectedTypes, selectedSeverity], () => {
  expandedKey.value = "";
  void loadEvents();
});

onMounted(() => {
  void loadEvents();
});
</script>

<template>
  <section class="services-page events-page">
    <header class="services-header events-header">
      <h2>Events</h2>
      <div class="events-filter-bar">
        <label class="events-filter-field">
          <span>Type</span>
          <select v-model="selectedType">
            <option value="">All types</option>
            <option v-for="type in eventTypeOptions" :key="type" :value="type">{{ type }}</option>
          </select>
        </label>
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
            <th>Time</th>
            <th>Severity</th>
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
