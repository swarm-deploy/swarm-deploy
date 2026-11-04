<script setup lang="ts">
import { computed } from "vue";

import type { EventHistoryItem } from "../../api/types";
import { formatDate } from "../../utils/format";
import AppModal from "../common/AppModal.vue";
import AppTable from "../common/AppTable.vue";

const props = defineProps<{
  open: boolean;
  event: EventHistoryItem | null;
  loading: boolean;
  error: string;
}>();

const emit = defineEmits<{ close: [] }>();
const sortedDetails = computed(() =>
  Object.entries(props.event?.details ?? {}).sort(([left], [right]) => left.localeCompare(right)),
);
</script>

<template>
  <AppModal :open="open" labelledby="event-details-title" keep-mounted @close="emit('close')">
    <template #title>
      <h2 id="event-details-title">Event details</h2>
    </template>

    <div class="modal-body">
      <p v-if="loading" class="meta">Loading event details...</p>
      <p v-else-if="error" class="meta" role="alert">{{ error }}</p>
      <template v-else-if="event">
        <AppTable summary aria-label="Event metadata">
          <tr><th scope="row">Time</th><td>{{ formatDate(event.created_at) }}</td></tr>
          <tr><th scope="row">Type</th><td>{{ event.type }}</td></tr>
          <tr><th scope="row">Severity</th><td>{{ event.severity }}</td></tr>
          <tr><th scope="row">Category</th><td>{{ event.category }}</td></tr>
          <tr><th scope="row">Message</th><td>{{ event.message }}</td></tr>
          <tr><th scope="row">Event ID</th><td><code>{{ event.id }}</code></td></tr>
        </AppTable>
        <ul v-if="sortedDetails.length > 0" class="event-details overview-event-details">
          <li v-for="[key, value] in sortedDetails" :key="key" class="event-detail">
            <span class="event-detail-key">{{ key }}</span>
            <code class="event-detail-value">{{ value }}</code>
          </li>
        </ul>
        <p v-else class="meta">details: n/a</p>
      </template>
    </div>
  </AppModal>
</template>

<style scoped>
.overview-event-details { margin-top: 14px; }
</style>
