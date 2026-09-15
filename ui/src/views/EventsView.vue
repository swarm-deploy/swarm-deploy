<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";

import { fetchEvents } from "../api/overview";
import type { EventHistoryItem, EventSeverity } from "../api/types";
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
  "userAuthenticated",
  "assistantPromptInjectionDetected",
];
const severityOptions: EventSeverity[] = ["info", "warn", "error", "alert"];

const loading = ref(false);
const loadingError = ref("");
const events = ref<EventHistoryItem[]>([]);
const selectedType = ref("");
const selectedSeverity = ref("");
const expandedKey = ref("");

let requestID = 0;

const visibleEvents = computed(() => events.value.slice().reverse());

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
      types: selectedType.value ? [selectedType.value] : [],
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

watch([selectedType, selectedSeverity], () => {
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

    <div v-if="loading && events.length === 0" class="services-empty">
      <p class="meta">Loading events...</p>
    </div>

    <div v-else-if="loadingError" class="services-empty">
      <p class="meta">Failed to load events: {{ loadingError }}</p>
    </div>

    <div v-else-if="visibleEvents.length === 0" class="services-empty">
      <p class="meta">No events match the selected filters.</p>
    </div>

    <div v-else class="secrets-table-wrap events-table-wrap">
      <table class="container-status-table events-table">
        <thead>
          <tr>
            <th>Time</th>
            <th>Severity</th>
            <th>Type</th>
            <th>Message</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="event in visibleEvents" :key="eventKey(event)">
            <tr class="events-table-row" @click="toggleDetails(event)">
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
        </tbody>
      </table>
    </div>
  </section>
</template>
