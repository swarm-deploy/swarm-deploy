<script setup lang="ts">
import { computed } from "vue";

import AppButton from "../common/AppButton.vue";
import AppModal from "../common/AppModal.vue";
import AppStatusBadge from "../common/AppStatusBadge.vue";
import DeploymentIcon from "./DeploymentIcon.vue";
import { useOverviewStore } from "../../stores/overview";
import { formatDate, shortCommitHash } from "../../utils/format";

const overviewStore = useOverviewStore();

const alert = computed(() => overviewStore.alertDetailsAlert);
const isResolved = computed(() => alert.value?.status === "resolved");
const resolutionTitle = computed(() => {
  switch (alert.value?.resolution?.reason) {
    case "recovered": return "Recovered successfully";
    case "resource_removed": return "Resource removed";
    default: return "Resolved";
  }
});
const stackTarget = computed(() => {
  if (alert.value?.resourceType !== "stack" || !alert.value.resourceId) return null;
  return { path: "/overview", hash: `#stack-${encodeURIComponent(alert.value.resourceId)}` };
});
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
const resourceTarget = computed(() => stackTarget.value ?? serviceTarget.value);

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
</script>

<template>
  <AppModal
    :open="overviewStore.alertDetailsModalOpen"
    labelledby="alert-details-title"
    card-class="alert-details-card"
    header-class="alert-details-header"
    keep-mounted
    @close="closeAlertDetailsModal"
  >
    <template #title>
      <h2 id="alert-details-title">Alert details</h2>
    </template>

    <template v-if="alert && overviewStore.alertDetailsModalOpen">
      <div class="modal-body alert-details-body">
        <div>
          <div class="alert-details-heading">
            <div>
              <span class="alert-details-kind">{{ alert.kind.replaceAll("_", " ") }}</span>
              <h3>{{ alert.title || "Alert" }}</h3>
            </div>
            <AppStatusBadge :variant="isResolved ? 'success' : 'error'" filled>
              <DeploymentIcon v-if="isResolved" name="check" />
              {{ isResolved ? "Resolved" : "Active" }}
            </AppStatusBadge>
          </div>
          <div class="alert-details-resource">
            <DeploymentIcon v-if="alert.resourceType === 'stack' || alert.resourceType === 'service'" :name="alert.resourceType" />
            <span>
              {{ alert.resourceType }} /
              <RouterLink v-if="resourceTarget" :to="resourceTarget" class="alert-details-link" @click="closeAlertDetailsModal">
                {{ alert.resourceId }}
              </RouterLink>
              <strong v-else>{{ alert.resourceId || "unknown" }}</strong>
            </span>
            <span class="alert-details-occurrences">{{ alert.occurrences }} {{ alert.occurrences === 1 ? "occurrence" : "occurrences" }}</span>
          </div>
        </div>

        <section v-if="isResolved" class="alert-details-resolution" aria-label="Resolution">
          <DeploymentIcon v-if="alert.resolution?.reason === 'recovered'" name="check" class="alert-details-recovery-icon" />
          <div class="alert-details-resolution-message">
            <h4>{{ resolutionTitle }}</h4>
            <p v-if="alert.resolution?.message">{{ alert.resolution.message }}</p>
          </div>
          <time v-if="alert.resolvedAt" :datetime="alert.resolvedAt" :title="formatDate(alert.resolvedAt)">
            {{ new Date(alert.resolvedAt).toLocaleTimeString() }}
          </time>
        </section>

        <section>
          <h4>What happened</h4>
          <p class="alert-details-diagnostic" :class="{ 'alert-details-diagnostic--open': !isResolved }">
            {{ alert.message || "No detailed diagnostic was recorded." }}
          </p>
        </section>

        <details v-if="alert.findings?.length" :key="alert.id" class="alert-details-findings">
          <summary>
            Findings <span class="alert-details-count">{{ alert.findings.length }}</span>
            <svg class="alert-details-chevron" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
              <path d="m4 6 4 4 4-4" />
            </svg>
          </summary>
          <ul>
            <li v-for="finding in alert.findings" :key="finding.code">
              <div class="alert-details-finding">
                <strong>{{ finding.summary }}</strong>
                <div class="alert-details-finding-code">Code <code>{{ finding.code }}</code></div>
              </div>
            </li>
          </ul>
        </details>

        <section class="alert-details-metadata">
          <h4>Details</h4>
          <dl>
            <div>
              <dt>Opened</dt>
              <dd><time :datetime="alert.openedAt">{{ formatDate(alert.openedAt) }}</time></dd>
            </div>
            <div v-if="isResolved && alert.resolvedAt">
              <dt>Resolved</dt>
              <dd><time :datetime="alert.resolvedAt">{{ formatDate(alert.resolvedAt) }}</time></dd>
            </div>
            <div v-if="alert.errorCode">
              <dt>Technical code</dt>
              <dd><code>{{ alert.errorCode }}</code></dd>
            </div>
            <div v-if="alert.commit">
              <dt>Commit</dt>
              <dd>
                <button type="button" class="alert-details-link" :title="alert.commit" @click="openCommit">
                  {{ shortCommitHash(alert.commit) }}
                </button>
              </dd>
            </div>
          </dl>
        </section>
      </div>

      <footer class="alert-details-footer">
        <span>Updated <time :datetime="alert.updatedAt">{{ formatDate(alert.updatedAt) }}</time></span>
        <nav v-if="stackTarget || alert.deploymentId" class="alert-details-actions" aria-label="Related resources">
          <AppButton v-if="stackTarget" :to="stackTarget" variant="secondary" compact @click="closeAlertDetailsModal">View stack</AppButton>
          <AppButton v-if="alert.deploymentId" compact @click="openDeployment">
            View deployment <span aria-hidden="true">↗</span>
          </AppButton>
        </nav>
      </footer>
    </template>
  </AppModal>
</template>

<style scoped>
:deep(.alert-details-card) {
  display: flex;
  flex-direction: column;
  max-width: 830px;
  overflow: hidden;
  border-color: var(--border-default);
  border-radius: 14px;
}

:global(:root:not([data-theme="light"]) .alert-details-card) { background: var(--workspace-bg); }

:deep(.alert-details-header) {
  flex-shrink: 0;
  min-height: 64px;
  padding-inline-start: 27px;
}

:deep(.alert-details-header .modal-close) { top: 14px; right: 17px; }

.alert-details-body {
  align-content: start;
  gap: 24px;
  min-height: 0;
  overflow: auto;
  padding: 24px 27px;
  overflow-wrap: anywhere;
}

.alert-details-heading,
.alert-details-resolution,
.alert-details-footer {
  display: flex;
  justify-content: space-between;
  gap: 12px;
}

.alert-details-heading,
.alert-details-resolution { align-items: flex-start; }
.alert-details-heading h3 { margin: 6px 0 0; font-size: 1.35rem; }
.alert-details-heading .app-status-badge {
  flex-shrink: 0;
  border: 0;
  padding: 6px 10px;
  font-weight: 700;
}
.alert-details-heading .app-status-badge--success { background: color-mix(in srgb, var(--success) 14%, transparent); }
.alert-details-kind { color: var(--muted); font-size: .75rem; font-weight: 700; letter-spacing: .08em; text-transform: uppercase; }
.alert-details-body h4 { margin: 0; font-size: .95rem; }
.alert-details-resource { display: flex; align-items: center; flex-wrap: wrap; gap: 4px 9px; margin-top: 16px; color: var(--muted); }
.alert-details-resource .deployment-icon { width: 15px; height: 15px; }
.alert-details-resource strong,
.alert-details-resource a { color: var(--text); font-weight: 600; }
.alert-details-occurrences::before { content: "·"; margin-inline-end: 9px; }

.alert-details-link {
  border: 0;
  padding: 0;
  background: transparent;
  color: var(--accent-strong);
  font: inherit;
  text-decoration: underline;
  text-decoration-color: var(--border-strong);
  text-underline-offset: .18em;
}
.alert-details-link:hover { transform: none; filter: none; text-decoration-color: currentColor; }
.alert-details-link:focus-visible,
.alert-details-findings summary:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: 2px; }

.alert-details-resolution {
  flex-wrap: wrap;
  justify-content: flex-start;
  border: 1px solid color-mix(in srgb, var(--success) 24%, var(--line));
  border-radius: 8px;
  padding: 14px 16px;
  background: var(--success-muted);
}
.alert-details-resolution-message { flex: 1 1 12rem; }
.alert-details-recovery-icon {
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: color-mix(in srgb, var(--success) 24%, transparent);
  color: var(--success);
  stroke-width: 2.6;
}
.alert-details-resolution h4 { color: color-mix(in srgb, var(--success), var(--text)); }
.alert-details-resolution p { margin: 5px 0 0; color: color-mix(in srgb, var(--text) 80%, var(--muted)); }
.alert-details-resolution time { margin-inline-start: auto; color: var(--muted); font-size: .8rem; }

.alert-details-diagnostic {
  margin: 12px 0 0;
  border-inline-start: 2px solid color-mix(in srgb, var(--error) 35%, var(--muted));
  padding: 3px 0 3px 13px;
  color: color-mix(in srgb, var(--text) 65%, var(--muted));
  white-space: pre-wrap;
  line-height: 1.5;
}
.alert-details-diagnostic--open { border-inline-start-color: var(--error-border); }

.alert-details-findings,
.alert-details-metadata { border-top: 1px solid var(--line); }
.alert-details-findings summary {
  display: flex;
  align-items: center;
  gap: 8px;
  padding-top: 12px;
  cursor: pointer;
  font-weight: 700;
  list-style: none;
}
.alert-details-findings summary::-webkit-details-marker { display: none; }
.alert-details-count {
  border-radius: 999px;
  padding: 2px 7px;
  background: var(--surface-muted-strong);
  color: var(--muted);
  font-size: .75rem;
}
.alert-details-chevron { width: 18px; height: 18px; margin-inline-start: auto; color: var(--muted); }
.alert-details-findings[open] .alert-details-chevron { transform: rotate(180deg); }
.alert-details-findings ul { list-style: none; margin: 12px 0 0; padding: 0; }
.alert-details-findings li { padding: 8px 0; }
.alert-details-findings li:first-child { padding-top: 0; }
.alert-details-findings li:last-child { padding-bottom: 0; }
.alert-details-findings li + li { border-top: 1px solid var(--line); }
.alert-details-finding { border-inline-start: 2px solid color-mix(in srgb, var(--error) 35%, var(--muted)); padding: 2px 0 2px 13px; }
.alert-details-finding-code {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 4px 8px;
  margin-top: 8px;
  color: var(--muted);
  font-size: .8rem;
}
.alert-details-finding-code code {
  min-width: 0;
  border: 1px solid var(--line);
  border-radius: 5px;
  padding: 2px 7px;
  background: var(--surface-muted);
}

.alert-details-metadata { padding-top: 16px; }
.alert-details-metadata dl { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 14px 20px; margin: 12px 0 0; }
.alert-details-metadata dt { margin-bottom: 3px; color: var(--muted); font-size: .8rem; }
.alert-details-metadata dd { margin: 0; }
.alert-details-footer { align-items: center; flex-shrink: 0; flex-wrap: wrap; border-top: 1px solid var(--line); padding: 14px 27px; }
.alert-details-footer > span { color: var(--muted); font-size: .8rem; }
.alert-details-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 8px; margin-inline-start: auto; }

@media (max-width: 600px) {
  :deep(.alert-details-card) { max-width: calc(100vw - 16px); }
  :deep(.alert-details-header) { min-height: 48px; padding-inline-start: 12px; }
  :deep(.alert-details-header .modal-close) { top: 6px; right: 8px; }
  .alert-details-body { gap: 16px; padding: 16px 12px; }
  .alert-details-heading h3 { font-size: 1.15rem; }
  .alert-details-resolution { column-gap: 8px; row-gap: 4px; padding: 10px; }
  .alert-details-resolution-message { flex-basis: calc(100% - 32px); }
  .alert-details-footer { padding-inline: 12px; }
}
</style>
