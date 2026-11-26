<script setup lang="ts">
import { computed } from "vue";
import { enumLabel, formatNumber, plural, t } from "../../i18n";

import AppButton from "../common/AppButton.vue";
import AppModal from "../common/AppModal.vue";
import AppStatusBadge from "../common/AppStatusBadge.vue";
import DeploymentIcon from "./DeploymentIcon.vue";
import { useOverviewStore } from "../../stores/overview";
import {
  alertFindingTitle, alertKindLabel, alertMessage, alertResolutionMessage, alertResourceLabel, alertStatusLabel, alertTitle,
} from "../../utils/alertPresentation";
import { formatDate, shortCommitHash } from "../../utils/format";

const props = defineProps<{ linked?: boolean }>();
const overviewStore = useOverviewStore();

const timestampOptions: Intl.DateTimeFormatOptions = {
  month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23",
};
const detailTimestamp: Intl.DateTimeFormatOptions = { ...timestampOptions, year: "numeric" };

const alert = computed(() => overviewStore.alertDetailsAlert);
const isResolved = computed(() => alert.value?.status === "resolved");
const resolutionTitle = computed(() => {
  const reason = alert.value?.resolution?.reason;
  return reason ? enumLabel("alertResolutions", reason) : alertStatusLabel("resolved");
});
const stackTarget = computed(() => {
  if (alert.value?.resourceType !== "stack" || !alert.value.resourceId) return null;
  return { name: "stack-details", params: { stack: alert.value.resourceId } };
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

function closeOnResourceNavigation() {
  if (!props.linked) closeAlertDetailsModal();
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
    class="alert-details-modal"
    keep-mounted
    @close="closeAlertDetailsModal"
  >
    <template #title>
      <h2 id="alert-details-title">{{ t('alerts.details') }}</h2>
    </template>

    <div v-if="overviewStore.alertDetailsLoading" class="modal-body alert-details-body">
      <p class="meta" role="status">{{ t('alerts.loadingDetails') }}</p>
    </div>
    <div v-else-if="overviewStore.alertDetailsError" class="modal-body alert-details-body">
      <p class="meta" role="alert">{{ overviewStore.alertDetailsError }}</p>
    </div>
    <template v-else-if="alert && overviewStore.alertDetailsModalOpen">
      <div class="modal-body alert-details-body">
        <div>
          <div class="alert-details-heading">
            <div>
              <span class="alert-details-kind">{{ alertKindLabel(alert.kind) }}</span>
              <h3>{{ alertTitle(alert) }}</h3>
            </div>
            <AppStatusBadge :variant="isResolved ? 'success' : 'error'" filled>
              <DeploymentIcon v-if="isResolved" name="check" />
              {{ alertStatusLabel(alert.status) }}
            </AppStatusBadge>
          </div>
          <div class="alert-details-resource">
            <svg v-if="alert.resourceType === 'stack'" class="alert-details-stack-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" aria-hidden="true">
              <path d="M1.5 1.5h13v13h-13zM1.5 4.75h13m-13 3.25h13m-13 3.25h13" />
            </svg>
            <DeploymentIcon v-else-if="alert.resourceType === 'service'" name="service" />
            <span>
              {{ alertResourceLabel(alert.resourceType) }} /
              <RouterLink v-if="resourceTarget" :to="resourceTarget" class="alert-details-link" @click="closeOnResourceNavigation">
                {{ alert.resourceId }}
              </RouterLink>
              <strong v-else>{{ alert.resourceId || t('alerts.unknownResource') }}</strong>
            </span>
            <span class="alert-details-occurrences">{{ plural('alerts.occurrenceCount', alert.occurrences) }}</span>
          </div>
        </div>

        <section v-if="isResolved" class="alert-details-resolution" :aria-label="t('alerts.resolution')">
          <DeploymentIcon v-if="alert.resolution?.reason === 'recovered'" name="check" class="alert-details-recovery-icon" />
          <div class="alert-details-resolution-message">
            <h4>{{ resolutionTitle }}</h4>
            <p v-if="alert.resolution?.message">{{ alertResolutionMessage(alert) }}</p>
          </div>
          <time v-if="alert.resolvedAt" :datetime="alert.resolvedAt" :title="formatDate(alert.resolvedAt)">
            {{ formatDate(alert.resolvedAt, { timeStyle: "medium" }) }}
          </time>
        </section>

        <section>
          <h4>{{ t('alerts.diagnostic') }}</h4>
          <p class="alert-details-diagnostic" :class="{ 'alert-details-diagnostic--open': !isResolved }">
            {{ alertMessage(alert) || t('alerts.noDiagnostic') }}
          </p>
        </section>

        <details v-if="alert.findings?.length" :key="alert.id" class="alert-details-findings">
          <summary>
            {{ t('alerts.findings') }} <span class="alert-details-count">{{ formatNumber(alert.findings.length) }}</span>
            <svg class="alert-details-chevron" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
              <path d="m4 6 4 4 4-4" />
            </svg>
          </summary>
          <ul>
            <li v-for="finding in alert.findings" :key="finding.code">
              <div class="alert-details-finding">
                <strong>{{ alertFindingTitle(finding) }}</strong>
                <div v-if="finding.summary && finding.summary !== alertFindingTitle(finding)" class="alert-details-finding-summary">
                  {{ finding.summary }}
                </div>
                <div class="alert-details-finding-code">{{ t('alerts.code') }} <code>{{ finding.code }}</code></div>
              </div>
            </li>
          </ul>
        </details>

        <section class="alert-details-metadata">
          <h4>{{ t('common.details') }}</h4>
          <dl>
            <div>
              <dt>{{ t('alerts.openedAt') }}</dt>
              <dd><time :datetime="alert.openedAt">{{ formatDate(alert.openedAt, detailTimestamp) }}</time></dd>
            </div>
            <div v-if="isResolved && alert.resolvedAt">
              <dt>{{ t('alerts.resolvedAt') }}</dt>
              <dd><time :datetime="alert.resolvedAt">{{ formatDate(alert.resolvedAt, detailTimestamp) }}</time></dd>
            </div>
            <div v-if="alert.errorCode">
              <dt>{{ t('alerts.technicalCode') }}</dt>
              <dd><code>{{ alert.errorCode }}</code></dd>
            </div>
            <div v-if="alert.commit">
              <dt>{{ t('common.commit') }}</dt>
              <dd>
                <button type="button" class="alert-details-link" :title="alert.commit"
                  :aria-label="t('common.openCommit', { commit: alert.commit })" @click="openCommit">
                  {{ shortCommitHash(alert.commit) }}
                </button>
              </dd>
            </div>
          </dl>
        </section>
      </div>

      <footer class="alert-details-footer">
        <span>{{ t('common.updated') }} <time :datetime="alert.updatedAt" :title="formatDate(alert.updatedAt)">{{ formatDate(alert.updatedAt, timestampOptions) }}</time></span>
        <nav v-if="stackTarget || alert.deploymentId" class="alert-details-actions" :aria-label="t('alerts.relatedResources')">
          <AppButton v-if="stackTarget" :to="stackTarget" variant="secondary" compact @click="closeOnResourceNavigation">{{ t('alerts.viewStack') }}</AppButton>
          <AppButton v-if="alert.deploymentId" compact @click="openDeployment">
            {{ t('alerts.viewDeployment') }} <span aria-hidden="true">↗</span>
          </AppButton>
        </nav>
      </footer>
    </template>
  </AppModal>
</template>

<style scoped>
:deep(.alert-details-card) {
  --alert-details-inset: 27px;
  --alert-details-divider: var(--border-default);
  --alert-details-recovery: var(--success);
  --alert-details-incident-line: color-mix(in srgb, var(--error) 30%, var(--muted));
  display: flex;
  flex-direction: column;
  max-width: 830px;
  max-height: calc(100dvh - 32px);
  margin-block: 16px;
  overflow: hidden;
  border-color: var(--border-strong);
  border-radius: 14px;
  font-size: 13px;
  line-height: 1.4;
}

:global(:root:not([data-theme="light"]) .alert-details-card) {
  --alert-details-recovery: color-mix(in srgb, var(--success) 85%, var(--info));
  background: color-mix(in srgb, var(--workspace-bg) 99%, var(--info));
  color: color-mix(in srgb, var(--text) 80%, var(--muted));
}

:deep(.alert-details-header) {
  flex-shrink: 0;
  min-height: 66px;
  padding-inline-start: var(--alert-details-inset);
  border-color: var(--alert-details-divider);
}

.alert-details-modal #alert-details-title { font-size: 17px; }
:deep(.alert-details-header .modal-close) { top: 15px; right: 17px; }
:deep(.alert-details-header .modal-close svg) { width: 24px; height: 24px; }

.alert-details-body {
  align-content: start;
  gap: 27px;
  min-height: 0;
  overflow: auto;
  padding: 27px var(--alert-details-inset) 26px;
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
.alert-details-heading h3 { margin: 8px 0 0; font-size: 21px; line-height: 1.22; }
.alert-details-heading .app-status-badge {
  flex-shrink: 0;
  min-height: 31px;
  border: 0;
  padding: 6px 12px;
  font-size: 12px;
  font-weight: 700;
}
.alert-details-heading .app-status-badge--success {
  background: color-mix(in srgb, var(--alert-details-recovery) 15%, transparent);
  color: var(--alert-details-recovery);
}
.alert-details-kind { color: var(--muted); font-size: 11px; font-weight: 700; letter-spacing: .10em; text-transform: uppercase; }
.alert-details-body h4 { margin: 0; font-size: 14px; }
.alert-details-resource { display: flex; align-items: center; flex-wrap: wrap; gap: 4px 9px; margin-top: 21px; color: var(--muted); }
.alert-details-resource svg { flex: 0 0 auto; width: 16px; height: 16px; }
.alert-details-resource strong,
.alert-details-resource a { color: color-mix(in srgb, var(--text) 70%, var(--muted)); font-weight: 700; }
.alert-details-occurrences::before { content: "·"; margin-inline-end: 9px; }

.alert-details-link {
  border: 0;
  padding: 0;
  background: transparent;
  color: var(--accent-strong);
  font: inherit;
  text-decoration: none;
  text-underline-offset: .18em;
}
.alert-details-link:hover { transform: none; filter: none; text-decoration: underline; }
.alert-details-link:focus-visible,
.alert-details-findings summary:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: 2px; }

.alert-details-resolution {
  flex-wrap: wrap;
  justify-content: flex-start;
  border: 1px solid color-mix(in srgb, var(--alert-details-recovery) 23%, var(--alert-details-divider));
  border-radius: 9px;
  padding: 16px 17px;
  background: color-mix(in srgb, var(--alert-details-recovery) 8%, transparent);
}
.alert-details-resolution-message { flex: 1 1 12rem; }
.alert-details-recovery-icon {
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: color-mix(in srgb, var(--alert-details-recovery) 30%, transparent);
  color: var(--alert-details-recovery);
  stroke-width: 2.6;
}
.alert-details-resolution h4 { color: color-mix(in srgb, var(--alert-details-recovery) 47%, var(--text)); }
.alert-details-resolution p { margin: 6px 0 0; color: color-mix(in srgb, var(--text) 60%, var(--muted)); }
.alert-details-resolution time { margin-inline-start: auto; color: var(--muted); font-size: 11.5px; }

.alert-details-diagnostic {
  margin: 15px 0 0;
  border-inline-start: 2px solid var(--alert-details-incident-line);
  padding: 4px 0 4px 14px;
  color: color-mix(in srgb, var(--text) 42%, var(--muted));
  white-space: pre-wrap;
  line-height: 1.65;
}
.alert-details-diagnostic--open { border-inline-start-color: var(--error-border); }

.alert-details-findings,
.alert-details-metadata { border-top: 1px solid var(--alert-details-divider); }
.alert-details-findings summary {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-top: 15px;
  cursor: pointer;
  font-size: 14px;
  font-weight: 700;
  list-style: none;
}
.alert-details-findings summary::-webkit-details-marker { display: none; }
.alert-details-count {
  border-radius: 999px;
  padding: 2px 7px;
  background: var(--surface-muted-strong);
  color: var(--muted);
  font-size: 11.5px;
}
.alert-details-chevron { width: 20px; height: 20px; margin-inline-start: auto; color: var(--muted); }
.alert-details-findings[open] .alert-details-chevron { transform: rotate(180deg); }
.alert-details-findings ul { list-style: none; margin: 15px 0 0; padding: 0; }
.alert-details-findings li { padding: 8px 0; }
.alert-details-findings li:first-child { padding-top: 0; }
.alert-details-findings li:last-child { padding-bottom: 0; }
.alert-details-findings li + li { border-top: 1px solid var(--alert-details-divider); }
.alert-details-finding { border-inline-start: 2px solid var(--alert-details-incident-line); padding: 3px 0 3px 13px; }
.alert-details-finding strong,
.alert-details-finding-summary { white-space: pre-wrap; }
.alert-details-finding-code {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 4px 8px;
  margin-top: 10px;
  color: var(--muted);
  font-size: 11.5px;
}
.alert-details-finding-code code {
  min-width: 0;
  border: 1px solid var(--alert-details-divider);
  border-radius: 5px;
  padding: 3px 8px;
  background: var(--surface-muted);
  color: color-mix(in srgb, var(--text) 45%, var(--muted));
}

.alert-details-metadata { padding-top: 23px; }
.alert-details-metadata dl { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 17px 27px; margin: 16px 0 0; }
.alert-details-metadata dt { margin-bottom: 5px; color: var(--muted); font-size: 11.5px; }
.alert-details-metadata dd { margin: 0; }
.alert-details-metadata .alert-details-link { color: color-mix(in srgb, var(--accent-strong) 62%, currentColor); }
.alert-details-footer { align-items: center; flex-shrink: 0; flex-wrap: wrap; border-top: 1px solid var(--alert-details-divider); padding: 15px var(--alert-details-inset); }
.alert-details-footer > span { color: var(--muted); font-size: 11.5px; }
.alert-details-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 8px; margin-inline-start: auto; }
.alert-details-actions .app-button {
  min-height: 36px;
  border-radius: 7px;
  padding-inline: 14px;
  font-size: 12.5px;
  font-weight: 700;
}

@media (max-height: 760px) {
  :deep(.alert-details-header) { min-height: 54px; }
  :deep(.alert-details-header .modal-close) { top: 9px; }
  .alert-details-body { gap: 17px; padding-block: 18px; }
  .alert-details-resource { margin-top: 15px; }
  .alert-details-resolution { padding-block: 12px; }
  .alert-details-metadata { padding-top: 15px; }
  .alert-details-footer { padding-block: 10px; }
}

@media (max-width: 600px) {
  :deep(.alert-details-card) {
    --alert-details-inset: 12px;
    max-width: calc(100vw - 16px);
    max-height: calc(100dvh - 16px);
    margin-block: 8px;
  }
  :deep(.alert-details-header) { min-height: 48px; }
  :deep(.alert-details-header .modal-close) { top: 6px; right: 8px; }
  .alert-details-body { gap: 16px; padding-block: 16px; }
  .alert-details-heading h3 { font-size: 18px; }
  .alert-details-resolution { column-gap: 8px; row-gap: 4px; padding: 10px; }
  .alert-details-resolution-message { flex-basis: calc(100% - 32px); }
}
</style>
