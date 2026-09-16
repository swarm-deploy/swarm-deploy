<script setup lang="ts">
import { computed, onMounted, onUnmounted } from "vue";

import { useOverviewStore } from "../../stores/overview";
import { formatDate } from "../../utils/format";

const overviewStore = useOverviewStore();

const alertEvent = computed(() => overviewStore.alertDetailsEvent);
const detailEntries = computed<[string, string][]>(() => {
  return Object.entries(alertEvent.value?.details ?? {})
    .map(([key, value]) => [key, String(value)] as [string, string])
    .sort(([leftKey], [rightKey]) => leftKey.localeCompare(rightKey));
});

function closeAlertDetailsModal() {
  overviewStore.closeAlertDetailsModal();
}

function handleEscape(event: KeyboardEvent) {
  if (event.key === "Escape" && overviewStore.alertDetailsModalOpen) {
    closeAlertDetailsModal();
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
  <div class="modal" :class="{ hidden: !overviewStore.alertDetailsModalOpen }" aria-hidden="true">
    <div class="modal-overlay" @click="closeAlertDetailsModal" />
    <div class="modal-card" role="dialog" aria-modal="true" aria-labelledby="alert-details-title">
      <div class="modal-header">
        <h2 id="alert-details-title">Alert details</h2>
        <button class="modal-close" type="button" aria-label="Close modal" @click="closeAlertDetailsModal">x</button>
      </div>

      <div class="modal-body">
        <table class="service-status-summary-table" aria-label="Alert details">
          <tbody>
            <tr>
              <th scope="row">Message</th>
              <td>{{ alertEvent?.message || "No message" }}</td>
            </tr>
            <tr>
              <th scope="row">Severity</th>
              <td>{{ alertEvent?.severity || "n/a" }}</td>
            </tr>
            <tr>
              <th scope="row">Type</th>
              <td>{{ alertEvent?.type || "unknown" }}</td>
            </tr>
            <tr>
              <th scope="row">Timestamp</th>
              <td>{{ formatDate(alertEvent?.created_at) }}</td>
            </tr>
          </tbody>
        </table>

        <ul v-if="detailEntries.length > 0" class="event-details alert-details-list">
          <li v-for="[key, value] in detailEntries" :key="key" class="event-detail">
            <span class="event-detail-key">{{ key }}</span>
            <code class="event-detail-value">{{ value }}</code>
          </li>
        </ul>
        <p v-else class="meta">details: n/a</p>
      </div>
    </div>
  </div>
</template>
