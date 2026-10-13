<script setup lang="ts">
import { computed, onMounted, onUnmounted } from "vue";

import AppTable from "../common/AppTable.vue";
import { useOverviewStore } from "../../stores/overview";
import { formatDate, shortCommitHash } from "../../utils/format";

const overviewStore = useOverviewStore();

const alert = computed(() => overviewStore.alertDetailsAlert);

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
        <button class="modal-close" type="button" aria-label="Close modal" @click="closeAlertDetailsModal">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" aria-hidden="true"><path d="m7 7 10 10M17 7 7 17" /></svg>
        </button>
      </div>

      <div class="modal-body">
        <div v-if="alert" class="alert-details-summary">
          <div class="alert-details-heading">
            <div><span class="alert-details-kind">{{ alert.kind.replaceAll("_", " ") }}</span><h3>{{ alert.title || "Alert" }}</h3></div>
            <strong class="alert-details-status" :class="`alert-details-status--${alert.status}`">{{ alert.status }}</strong>
          </div>
          <div class="alert-details-diagnostic">
            <strong>{{ alert.kind === "deploy_failed" ? "Failure reason" : "Details" }}</strong>
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
          <button v-if="alert.commit" type="button" class="button-ghost" @click="openCommit">Commit {{ shortCommitHash(alert.commit) }}</button>
        </nav>
      </div>
    </div>
  </div>
</template>

<style scoped>
.alert-details-summary { margin-bottom: 14px; }
.alert-details-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.alert-details-heading h3 { margin: 2px 0 0; }
.alert-details-kind { color: var(--muted); font-size: .72rem; letter-spacing: .08em; text-transform: uppercase; }
.alert-details-status { border-radius: 999px; padding: 4px 9px; text-transform: capitalize; }
.alert-details-status--open { color: var(--error); background: var(--error-muted); }
.alert-details-status--resolved { color: var(--success); background: var(--success-muted); }
.alert-details-diagnostic { margin-top: 14px; border-left: 3px solid var(--error); border-radius: 5px; background: var(--error-muted); padding: 10px 12px; }
.alert-details-diagnostic p { margin: 5px 0 0; white-space: pre-wrap; overflow-wrap: anywhere; }
.alert-details-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 14px; }
.alert-details-actions a { display: inline-flex; align-items: center; border-radius: 6px; background: var(--accent); color: var(--text-on-accent); padding: 8px 12px; text-decoration: none; }
</style>
