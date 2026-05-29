<script setup lang="ts">
import { computed, onMounted, onUnmounted } from "vue";

import type { EventHistoryItem } from "../../api/types";
import { useOverviewStore } from "../../stores/overview";
import { formatDate } from "../../utils/format";

const overviewStore = useOverviewStore();
const eventDetailsPriority = ["stack", "commit", "destination", "channel", "event_type", "error"];

const reversedEvents = computed(() => overviewStore.events.slice().reverse());

function sortedEventDetails(item: EventHistoryItem): [string, string][] {
  const details = item.details && typeof item.details === "object" ? item.details : {};
  const detailPairs = Object.entries(details);
  const order = Object.fromEntries(eventDetailsPriority.map((key, index) => [key, index]));
  return detailPairs.sort(([leftKey], [rightKey]) => {
    const leftOrder = order[leftKey] ?? Number.MAX_SAFE_INTEGER;
    const rightOrder = order[rightKey] ?? Number.MAX_SAFE_INTEGER;
    if (leftOrder !== rightOrder) {
      return leftOrder - rightOrder;
    }
    return leftKey.localeCompare(rightKey);
  });
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

function closeEventHistoryModal() {
  overviewStore.closeEventsModal();
}

function handleEscape(event: KeyboardEvent) {
  if (event.key === "Escape" && overviewStore.eventsModalOpen) {
    closeEventHistoryModal();
  }
}

onMounted(() => {
  document.addEventListener("keydown", handleEscape);
});

onUnmounted(() => {
  document.removeEventListener("keydown", handleEscape);
});
</script>

<template>
  <div class="modal" :class="{ hidden: !overviewStore.eventsModalOpen }" aria-hidden="true">
    <div class="modal-overlay" @click="closeEventHistoryModal" />
    <div class="modal-card" role="dialog" aria-modal="true" aria-labelledby="event-history-title">
      <div class="modal-header">
        <h2 id="event-history-title">Event history</h2>
        <button class="modal-close" type="button" aria-label="Close modal" @click="closeEventHistoryModal">x</button>
      </div>

      <div class="modal-body">
        <p v-if="overviewStore.eventsLoading" class="meta">Loading event history...</p>
        <p v-else-if="overviewStore.eventsError" class="meta">Failed to load event history: {{ overviewStore.eventsError }}</p>
        <p v-else-if="reversedEvents.length === 0" class="meta">No events yet.</p>
        <div v-else class="event-list">
          <article v-for="event in reversedEvents" :key="`${event.type}-${event.created_at}-${event.message}`" class="event-item">
            <p class="event-item-header">
              <span class="event-severity" :class="`event-severity-${normalizedSeverity(event)}`">
                {{ normalizedSeverity(event) }}
              </span>
              <span><strong>{{ event.type || "unknown" }}</strong> - {{ formatDate(event.created_at) }}</span>
            </p>
            <p class="meta">{{ event.message || "No details" }}</p>
            <ul v-if="sortedEventDetails(event).length > 0" class="event-details">
              <li
                v-for="[key, value] in sortedEventDetails(event)"
                :key="key"
                class="event-detail"
                :class="{ 'event-detail-error': key === 'error' }"
              >
                <span class="event-detail-key">{{ key }}</span>
                <code class="event-detail-value">{{ value }}</code>
              </li>
            </ul>
            <p v-else class="meta">details: n/a</p>
          </article>
        </div>
      </div>
    </div>
  </div>
</template>
