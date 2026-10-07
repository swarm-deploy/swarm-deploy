<script setup lang="ts">
import { computed, onMounted, onUnmounted } from "vue";

import AppTable from "../common/AppTable.vue";
import { useOverviewStore } from "../../stores/overview";
import { formatDate } from "../../utils/format";

const overviewStore = useOverviewStore();

const alert = computed(() => overviewStore.alertDetailsAlert);

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
        <AppTable summary aria-label="Alert details">
            <tr>
              <th scope="row">Title</th>
              <td>{{ alert?.title || "Alert" }}</td>
            </tr>
            <tr>
              <th scope="row">Status</th>
              <td>{{ alert?.status || "n/a" }}</td>
            </tr>
            <tr>
              <th scope="row">Resource</th>
              <td>{{ alert?.resourceType || "resource" }} / {{ alert?.resourceId || "unknown" }}</td>
            </tr>
            <tr>
              <th scope="row">Message</th>
              <td>{{ alert?.message || "No message" }}</td>
            </tr>
            <tr><th scope="row">Occurrences</th><td>{{ alert?.occurrences ?? 0 }}</td></tr>
            <tr><th scope="row">Opened</th><td>{{ formatDate(alert?.openedAt) }}</td></tr>
            <tr><th scope="row">Updated</th><td>{{ formatDate(alert?.updatedAt) }}</td></tr>
            <tr v-if="alert?.resolvedAt"><th scope="row">Resolved</th><td>{{ formatDate(alert.resolvedAt) }}</td></tr>
            <tr v-if="alert?.resolution"><th scope="row">Resolution</th><td>{{ alert.resolution.reason }} · {{ alert.resolution.message }}</td></tr>
        </AppTable>
      </div>
    </div>
  </div>
</template>
