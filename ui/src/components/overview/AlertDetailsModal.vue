<script setup lang="ts">
import { computed } from "vue";

import AppModal from "../common/AppModal.vue";
import AppTable from "../common/AppTable.vue";
import { useOverviewStore } from "../../stores/overview";
import { formatDate, shortCommitHash } from "../../utils/format";

const overviewStore = useOverviewStore();

const alert = computed(() => overviewStore.alertDetailsAlert);
const serviceTarget = computed(() => {
  if (alert.value?.resourceType !== "service") return null;
  const separator = alert.value.resourceId.indexOf("/");
  if (separator <= 0 || separator === alert.value.resourceId.length - 1) return null;
  return {
    name: "service-details",
    params: {
      stack: alert.value.resourceId.slice(0, separator),
      service: alert.value.resourceId.slice(separator + 1),
    },
  };
});

function closeAlertDetailsModal() {
  overviewStore.closeAlertDetailsModal();
}

function openDeployment() {
  const deploymentID = alert.value?.deploymentId;
  if (!deploymentID) return;
  closeAlertDetailsModal();
  void overviewStore.openDeploymentDetailsModal(deploymentID);
}

function openCommit() {
  const commit = alert.value?.commit;
  if (!commit) return;
  closeAlertDetailsModal();
  void overviewStore.openCommitDetailsModal(commit);
}

function stackTarget(stack: string) {
  return { path: "/overview", hash: `#stack-${encodeURIComponent(stack)}` };
}
</script>

<template>
  <AppModal
    :open="overviewStore.alertDetailsModalOpen"
    labelledby="alert-details-title"
    keep-mounted
    @close="closeAlertDetailsModal"
  >
    <template #title>
      <h2 id="alert-details-title">Alert details</h2>
    </template>

    <div class="modal-body">
      <div v-if="alert" class="alert-details-summary">
        <div class="alert-details-heading">
          <div><span class="alert-details-kind">{{ alert.kind.replaceAll("_", " ") }}</span><h3>{{ alert.title || "Alert" }}</h3></div>
          <strong class="alert-details-status" :class="`alert-details-status--${alert.status}`">{{ alert.status }}</strong>
        </div>
        <div class="alert-details-diagnostic">
          <strong>{{ alert.kind === "sync_failed" ? "Failure reason" : "Details" }}</strong>
          <p>{{ alert.message || "No detailed diagnostic was recorded." }}</p>
        </div>
      </div>
      <AppTable summary aria-label="Alert metadata">
          <tr>
            <th scope="row">Resource</th>
            <td>{{ alert?.resourceType || "resource" }} / {{ alert?.resourceId || "unknown" }}</td>
          </tr>
          <tr v-if="alert?.failureStage"><th scope="row">Failure stage</th><td>{{ alert.failureStage }}</td></tr>
          <tr><th scope="row">Occurrences</th><td>{{ alert?.occurrences ?? 0 }}</td></tr>
          <tr><th scope="row">Opened</th><td>{{ formatDate(alert?.openedAt) }}</td></tr>
          <tr><th scope="row">Updated</th><td>{{ formatDate(alert?.updatedAt) }}</td></tr>
          <tr v-if="alert?.resolvedAt"><th scope="row">Resolved</th><td>{{ formatDate(alert.resolvedAt) }}</td></tr>
          <tr v-if="alert?.resolution"><th scope="row">Resolution</th><td>{{ alert.resolution.reason }} · {{ alert.resolution.message }}</td></tr>
          <tr v-if="alert?.errorCode"><th scope="row">Technical code</th><td><code>{{ alert.errorCode }}</code></td></tr>
      </AppTable>
      <nav v-if="alert" class="alert-details-actions" aria-label="Related resources">
        <button v-if="alert.deploymentId" type="button" @click="openDeployment">View deployment</button>
        <RouterLink v-if="alert.resourceType === 'stack' && alert.resourceId" :to="stackTarget(alert.resourceId)" @click="closeAlertDetailsModal">View stack</RouterLink>
        <RouterLink v-if="serviceTarget" :to="serviceTarget" @click="closeAlertDetailsModal">View service</RouterLink>
        <button v-if="alert.commit" type="button" class="button-ghost" @click="openCommit">Commit {{ shortCommitHash(alert.commit) }}</button>
      </nav>
    </div>
  </AppModal>
</template>

<style scoped>
.alert-details-summary { margin-bottom: 8px; }
.alert-details-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
.alert-details-heading h3 { margin: 2px 0 0; }
.alert-details-kind { color: var(--muted); font-size: .72rem; letter-spacing: .08em; text-transform: uppercase; }
.alert-details-status { border-radius: 999px; padding: 4px 9px; text-transform: capitalize; }
.alert-details-status--open { color: var(--error); background: var(--error-muted); }
.alert-details-status--resolved { color: var(--success); background: var(--success-muted); }
.alert-details-diagnostic { margin-top: 10px; border-left: 3px solid var(--error); border-radius: 5px; background: var(--error-muted); padding: 8px 10px; }
.alert-details-diagnostic p { margin: 5px 0 0; white-space: pre-wrap; overflow-wrap: anywhere; }
.alert-details-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 8px; }
.alert-details-actions a { display: inline-flex; align-items: center; border-radius: 6px; background: var(--accent); color: var(--text-on-accent); padding: 7px 10px; text-decoration: none; }
</style>
