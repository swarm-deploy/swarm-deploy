<script setup lang="ts">
import { computed } from "vue";
import { t } from "../../i18n";

import type { EventHistoryItem } from "../../api/types";
import { eventCategoryLabel, eventDetailLabel, eventMessage, eventSeverityLabel } from "../../utils/eventPresentation";
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
      <h2 id="event-details-title">{{ t('events.details') }}</h2>
    </template>

    <div class="modal-body">
      <p v-if="loading" class="meta">{{ t('events.loadingDetails') }}</p>
      <p v-else-if="error" class="meta" role="alert">{{ error }}</p>
      <template v-else-if="event">
        <AppTable summary :aria-label="t('events.metadata')">
          <tr><th scope="row">{{ t('common.time') }}</th><td>{{ formatDate(event.created_at) }}</td></tr>
          <tr><th scope="row">{{ t('common.type') }}</th><td>{{ event.type }}</td></tr>
          <tr><th scope="row">{{ t('activity.severity') }}</th><td>{{ eventSeverityLabel(event.severity) }}</td></tr>
          <tr><th scope="row">{{ t('activity.category') }}</th><td>{{ eventCategoryLabel(event.category) }}</td></tr>
          <tr><th scope="row">{{ t('common.message') }}</th><td>{{ eventMessage(event) }}</td></tr>
          <tr><th scope="row">{{ t('events.eventId') }}</th><td><code>{{ event.id }}</code></td></tr>
        </AppTable>
        <ul v-if="sortedDetails.length > 0" class="event-details overview-event-details">
          <li v-for="[key, value] in sortedDetails" :key="key" class="event-detail">
            <span class="event-detail-key">{{ eventDetailLabel(key) }}</span>
            <code class="event-detail-value">{{ value }}</code>
          </li>
        </ul>
        <p v-else class="meta">{{ t('events.noDetails') }}</p>
      </template>
    </div>
  </AppModal>
</template>

<style scoped>
.overview-event-details { margin-top: 14px; }
</style>
