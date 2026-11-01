<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { useRoute } from "vue-router";

import { fetchAlerts, fetchDeployments, fetchStackResources, fetchStacks } from "../api/overview";
import { fetchServices } from "../api/services";
import type { Alert, DeploymentSummary, NetworkInfo, ServiceInfo, StackResourceInfo, StackView } from "../api/types";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import DeploymentIcon from "../components/overview/DeploymentIcon.vue";
import RecentDeploymentsPanel from "../components/overview/RecentDeploymentsPanel.vue";
import ServicesTable from "../components/overview/ServicesTable.vue";
import DeploymentActivityChart from "../components/stack/DeploymentActivityChart.vue";
import { useOverviewStore } from "../stores/overview";
import { formatDate, shortCommitHash } from "../utils/format";
import { stackSyncStatus } from "../utils/stackStatus";

const route = useRoute();
const store = useOverviewStore();
const stackName = computed(() => typeof route.params.stack === "string" ? route.params.stack : "");
const stack = ref<StackView | null>(null);
const services = ref<ServiceInfo[]>([]);
const deployments = ref<DeploymentSummary[]>([]);
const alerts = ref<Alert[]>([]);
const networks = ref<NetworkInfo[]>([]);
const secrets = ref<StackResourceInfo[]>([]);
const configs = ref<StackResourceInfo[]>([]);
const loading = ref(false);
const error = ref("");
const deploymentsError = ref("");
const alertsError = ref("");
const resourcesError = ref("");
const syncStatus = computed(() => stackSyncStatus(stack.value?.status));
let request = 0;

function errorMessage(reason: unknown): string {
  return reason instanceof Error ? reason.message : "Request failed";
}

function deploymentSummary(deployment: DeploymentSummary): string {
  const changed = deployment.summary.added + deployment.summary.changed + deployment.summary.removed;
  if (deployment.status === "failed" && deployment.reason) return deployment.reason;
  if (changed > 0) return `${changed} resource${changed === 1 ? "" : "s"} changed`;
  if (deployment.resources.services > 0) {
    const count = deployment.resources.services;
    return `${count} service${count === 1 ? "" : "s"} processed`;
  }
  return deployment.phase === "completed" ? "Stack state reconciled" : `Phase: ${deployment.phase}`;
}

const recentDeployments = computed(() => deployments.value.map((deployment) => ({
  id: deployment.id,
  status: deployment.status,
  startedAt: deployment.started_at,
  commit: deployment.commit,
  phase: deployment.phase,
  summary: deploymentSummary(deployment),
})));

async function load() {
  const current = ++request;
  const name = stackName.value;
  stack.value = null;
  services.value = [];
  deployments.value = [];
  alerts.value = [];
  networks.value = [];
  secrets.value = [];
  configs.value = [];
  error.value = "";
  deploymentsError.value = "";
  alertsError.value = "";
  resourcesError.value = "";
  if (!name.trim()) { loading.value = false; return; }
  loading.value = true;
  const [stackResult, serviceResult, deploymentResult, alertResult, resourceResult] = await Promise.allSettled([
    fetchStacks(), fetchServices(), fetchDeployments({ stack: name, limit: 4 }),
    fetchAlerts({ status: "open", stack: name }), fetchStackResources(name),
  ]);
  if (current !== request) return;
  if (stackResult.status === "fulfilled") {
    stack.value = stackResult.value.stacks?.find((item) => item.name === name) ?? null;
  } else error.value = errorMessage(stackResult.reason);
  if (serviceResult.status === "fulfilled") {
    services.value = (serviceResult.value.services ?? []).filter((item) => item.stack === name)
      .sort((left, right) => left.name.localeCompare(right.name));
  } else error.value = [error.value, errorMessage(serviceResult.reason)].filter(Boolean).join("; ");
  if (deploymentResult.status === "fulfilled") {
    deployments.value = (deploymentResult.value.deployments ?? []).filter((item) => item.stack === name)
      .sort((left, right) => Date.parse(right.started_at) - Date.parse(left.started_at)).slice(0, 4);
  } else deploymentsError.value = errorMessage(deploymentResult.reason);
  if (alertResult.status === "fulfilled") {
    alerts.value = (alertResult.value.alerts ?? []).filter((item) =>
      item.status === "open" && item.resourceType === "stack" && item.resourceId === name);
  } else alertsError.value = errorMessage(alertResult.reason);
  if (resourceResult.status === "fulfilled") {
    networks.value = (resourceResult.value.networks ?? []).filter((item) => item.stack_name === name)
      .sort((left, right) => left.name.localeCompare(right.name));
    secrets.value = resourceResult.value.secrets ?? [];
    configs.value = resourceResult.value.configs ?? [];
  } else resourcesError.value = errorMessage(resourceResult.reason);
  loading.value = false;
}

watch(stackName, () => { void load(); }, { immediate: true });
onUnmounted(() => { request++; });
</script>

<template>
  <section class="stack-details-page">
    <AppTableEmpty v-if="loading" role="status" message="Loading stack..." />
    <AppTableEmpty v-else-if="error" role="alert">
      Failed to load stack: {{ error }} <button type="button" class="button-ghost" @click="load">Retry</button>
    </AppTableEmpty>
    <AppTableEmpty v-else-if="!stack" message="Stack not found." />
    <template v-else>
      <header class="stack-hero">
        <div class="stack-hero-icon"><DeploymentIcon name="stack" /></div>
        <div class="stack-hero-copy">
          <div class="stack-hero-title">
            <h2>{{ stack.name }}</h2>
            <span class="stack-state" :class="`stack-state--${syncStatus.kind}`">
              <span class="stack-state-dot" />{{ syncStatus.label }}
            </span>
          </div>
          <dl class="stack-hero-metadata">
            <div><dt>Compose:</dt><dd>{{ stack.compose_file || "n/a" }}</dd></div>
            <div><dt>Updated:</dt><dd>{{ formatDate(stack.last_deploy_at) }}</dd></div>
            <div><dt>Commit:</dt><dd>
              <button v-if="stack.last_commit" type="button" class="stack-commit-button"
                :aria-label="`Open commit ${shortCommitHash(stack.last_commit)}`"
                @click="store.openCommitDetailsModal(stack.last_commit)">
                {{ shortCommitHash(stack.last_commit) }}
              </button>
              <span v-else>n/a</span>
            </dd></div>
          </dl>
        </div>
        <button type="button" class="stack-compare-button" @click="store.openStackManifestModal(stack.name)">Compare state</button>
      </header>

      <p v-if="stack.last_error" class="stack-error" role="alert">{{ stack.last_error }}</p>

      <dl class="stack-summary" aria-label="Stack summary">
        <div class="stack-summary-health" data-summary="health">
          <DeploymentIcon name="check" class="metric-icon--success" />
          <div>
            <dt>Service health</dt>
            <dd><strong>{{ stack.status?.synced ?? 0 }} / {{ services.length }}</strong><span>synced</span></dd>
          </div>
        </div>
        <div class="stack-summary-drift" :class="{ 'stack-summary-drift--warning': (stack.status?.out_of_synced ?? 0) > 0 }" data-summary="out-of-sync">
          <DeploymentIcon name="warning" />
          <div>
            <dt>Out of sync</dt>
            <dd>{{ stack.status?.out_of_synced ?? 0 }}</dd>
          </div>
        </div>
        <div class="stack-summary-resources">
          <dt>Resources</dt>
          <dd>
            <span data-summary="networks"><DeploymentIcon name="network" /><strong>{{ resourcesError ? "n/a" : networks.length }}</strong> networks</span>
            <span data-summary="secrets"><DeploymentIcon name="secret" /><strong>{{ resourcesError ? "n/a" : secrets.length }}</strong> secrets</span>
            <span data-summary="configs"><DeploymentIcon name="config" /><strong>{{ resourcesError ? "n/a" : configs.length }}</strong> configs</span>
          </dd>
        </div>
      </dl>

      <p v-if="alertsError" class="stack-inline-error" role="alert">Failed to load alerts: {{ alertsError }}</p>
      <section v-else-if="alerts.length" class="stack-panel stack-alert-panel" aria-labelledby="stack-alerts-title">
        <header class="overview-summary-panel-header">
          <div class="service-panel-heading">
            <span class="overview-summary-panel-icon" aria-hidden="true">
              <DeploymentIcon name="warning" />
            </span>
            <h3 id="stack-alerts-title" class="overview-panel-title">Active Alerts</h3>
          </div>
        </header>
        <button v-for="alert in alerts" :key="alert.id" type="button" class="stack-alert-row"
          :aria-label="`Open alert: ${alert.title}`" @click="store.openAlertDetailsModal(alert)">
          <DeploymentIcon name="warning" /><span>{{ alert.title }}</span><time :datetime="alert.updatedAt">{{ formatDate(alert.updatedAt) }}</time>
        </button>
      </section>

      <div class="stack-content-grid">
        <section class="stack-panel stack-services-panel" aria-labelledby="stack-services-title">
          <header class="overview-summary-panel-header">
            <div class="service-panel-heading">
              <span class="overview-summary-panel-icon" aria-hidden="true">
                <DeploymentIcon name="service" />
              </span>
              <h3 id="stack-services-title" class="overview-panel-title">Services</h3>
            </div>
          </header>
          <ServicesTable :services="services" min-width="620px" variant="stack-detail" />
        </section>

        <RecentDeploymentsPanel
          id="stack-deployments-title"
          class="stack-deployments-panel"
          title="Recent Deployments"
          :link="{ name: 'deployments', query: { stack: stack.name } }"
          :deployments="recentDeployments"
          :error="deploymentsError"
        />
      </div>

      <DeploymentActivityChart :stack="stack.name" />

    </template>
  </section>
</template>

<style scoped>
/* Hallmark · full redesign · genre: modern-minimal · shape: app screen · screen: services and deployments first · built: the project · v2.0.0 */
/* Hallmark · pre-emit critique: P4 H4 E4 S5 R4 V4 · weakest: E, responsive table and focus states receive explicit treatment */
/* Hallmark · answered F5 · status icon coins anchor the real deployment timeline and distinguish its live states */
.stack-details-page { display: grid; gap: 16px; min-width: 0; }
.stack-hero { display: grid; grid-template-columns: 48px minmax(0, 1fr) auto; align-items: center; gap: 16px; min-width: 0; }
.stack-hero-icon { width: 48px; height: 48px; display: grid; place-items: center; border: 1px solid var(--border-default); border-radius: 10px; color: var(--muted); background: var(--surface-muted); }
.stack-hero-icon :deep(svg) { width: 28px; height: 28px; }
.stack-hero-copy { min-width: 0; display: grid; gap: 8px; }
.stack-hero-title { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; }
.stack-hero-title h2 { min-width: 0; margin: 0; font-size: 1.45rem; line-height: 1.15; overflow-wrap: break-word; text-wrap: balance; }
.stack-state { display: inline-flex; align-items: center; gap: 8px; min-height: 28px; padding: 4px 8px; border: 0; border-radius: 999px; color: var(--muted); background: var(--surface-muted); font-size: .78rem; font-weight: 500; font-variant-numeric: tabular-nums; }
.stack-state-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--muted); }
.stack-state--synced .stack-state-dot { background: var(--success); }
.stack-state--out-of-sync .stack-state-dot { background: var(--warning); }
.stack-hero-metadata { display: flex; flex-wrap: wrap; gap: 8px 0; margin: 0; color: var(--muted); font-size: .8rem; }
.stack-hero-metadata > div { display: flex; align-items: center; gap: 4px; }
.stack-hero-metadata > div + div::before { content: ""; width: 1px; height: 14px; margin: 0 16px; background: var(--border-subtle); }
.stack-hero-metadata dt, .stack-hero-metadata dd { margin: 0; }
.stack-hero-metadata dd { color: var(--text); }
.stack-compare-button { border: 1px solid var(--border-default); background: var(--surface-control); color: var(--text); box-shadow: none; }
.stack-compare-button:hover { border-color: var(--accent-border); background: var(--surface-hover-strong); filter: none; }
.stack-compare-button:focus-visible, .stack-commit-button:focus-visible, .stack-alert-row:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: 2px; }
.stack-compare-button:active, .stack-commit-button:active { transform: translateY(1px); }
.stack-compare-button:disabled, .stack-commit-button:disabled, .stack-alert-row:disabled { color: var(--muted); background: var(--surface-muted); opacity: .65; cursor: not-allowed; }
.stack-commit-button { border: 0; background: transparent; color: var(--control-primary); padding: 0; border-radius: 3px; font: inherit; font-weight: 650; box-shadow: none; }
.stack-commit-button:hover { color: var(--control-primary-hover); transform: none; filter: none; }
.stack-commit-button { white-space: nowrap; }
.stack-error, .stack-inline-error { margin: 0; padding: 12px 16px; border: 1px solid var(--error-border); border-radius: 8px; color: var(--error); background: var(--error-muted); font-size: .84rem; }
.stack-summary { display: grid; grid-template-columns: minmax(220px, 1.2fr) minmax(160px, .8fr) minmax(0, 2fr); margin: 0; border: 1px solid var(--border-subtle); border-radius: 10px; background: var(--surface-card-soft); overflow: hidden; }
.stack-summary > div { min-width: 0; min-height: 72px; padding: 12px 16px; }
.stack-summary > div + div { border-left: 1px solid var(--border-subtle); }
.stack-summary-health, .stack-summary-drift { display: flex; align-items: center; gap: 12px; }
.stack-summary :deep(.deployment-icon) { width: 24px; height: 24px; flex: 0 0 auto; color: var(--muted); }
.stack-summary .metric-icon--success { color: var(--success); }
.stack-summary-drift--warning { color: var(--warning); background: var(--warning-muted); }
.stack-summary-drift--warning :deep(.deployment-icon) { color: var(--warning); }
.stack-summary dt { color: var(--muted); font-size: .76rem; }
.stack-summary dd { margin: 0; color: var(--text); font-variant-numeric: tabular-nums; }
.stack-summary-health dd { display: flex; align-items: baseline; gap: 8px; }
.stack-summary-health dd strong { font-size: 1.1rem; }
.stack-summary-health dd span { color: var(--muted); font-size: .78rem; }
.stack-summary-drift dd { font-size: 1.1rem; font-weight: 700; }
.stack-summary-resources { display: grid; align-content: center; gap: 8px; }
.stack-summary-resources dd { display: flex; flex-wrap: wrap; gap: 8px 20px; }
.stack-summary-resources dd > span { display: inline-flex; align-items: center; gap: 8px; color: var(--muted); font-size: .78rem; white-space: nowrap; }
.stack-summary-resources dd :deep(.deployment-icon) { width: 17px; height: 17px; }
.stack-summary-resources strong { color: var(--text); font-size: .9rem; }
.stack-content-grid { display: grid; grid-template-columns: minmax(0, 2fr) minmax(330px, 1fr); align-items: stretch; gap: 16px; min-width: 0; }
.stack-panel { min-width: 0; border: 1px solid var(--border-subtle); border-radius: 10px; background: var(--surface-card-soft); overflow: hidden; }
.stack-services-panel :deep(.app-table th) { height: 44px; background: transparent; }
.stack-services-panel :deep(.app-table td) { height: 52px; }
.stack-services-panel :deep(.app-table th), .stack-services-panel :deep(.app-table td) { padding-inline: 16px; }
.stack-services-panel :deep(.services-stack-table--detail .services-col-name) { width: 25%; }
.stack-services-panel :deep(.services-stack-table--detail .services-col-sync-status) { width: 25%; }
.stack-services-panel :deep(.services-stack-table--detail .services-col-type) { width: 20%; }
.stack-services-panel :deep(.services-stack-table--detail .services-col-version) { width: 30%; }
.stack-services-panel :deep(.services-cell-image) { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.stack-services-panel :deep(.stack-service-name-wrap) { display: flex; align-items: center; gap: 8px; min-width: 0; }
.stack-alert-panel { display: grid; }
.stack-alert-row { width: 100%; display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: 12px; border: 0; border-bottom: 1px solid var(--border-subtle); border-radius: 0; background: transparent; color: var(--text); padding: 12px 16px; text-align: left; box-shadow: none; }
.stack-alert-row:last-child { border-bottom: 0; }
.stack-alert-row:hover { transform: none; background: var(--surface-hover); filter: none; }
.stack-alert-row:active { background: var(--surface-hover-strong); }
.stack-alert-row :deep(svg) { color: var(--error); }
.stack-alert-row time { color: var(--muted); font-size: .74rem; white-space: nowrap; }
@media (max-width: 1180px) {
  .stack-summary { grid-template-columns: minmax(200px, 1fr) minmax(150px, .75fr) minmax(0, 1.5fr); }
  .stack-content-grid { grid-template-columns: minmax(0, 1fr); }
}
@media (max-width: 720px) {
  .stack-details-page { gap: 12px; }
  .stack-hero { grid-template-columns: 28px minmax(0, 1fr) auto; align-items: center; column-gap: 8px; row-gap: 8px; }
  .stack-hero-icon { grid-column: 1; grid-row: 1; width: 28px; height: 28px; border: 0; border-radius: 0; background: transparent; }
  .stack-hero-icon :deep(svg) { width: 28px; height: 28px; }
  .stack-hero-copy, .stack-hero-title { display: contents; }
  .stack-hero-title h2 { grid-column: 2; grid-row: 1; min-width: 0; }
  .stack-state { grid-column: 3; grid-row: 1; min-height: 24px; justify-self: end; padding: 2px 8px; font-size: .75rem; }
  .stack-state-dot { width: 6px; height: 6px; }
  .stack-hero-metadata { grid-column: 1 / -1; grid-row: 2; display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 4px 16px; min-width: 0; padding-top: 2px; }
  .stack-hero-metadata > div { min-width: 0; }
  .stack-hero-metadata > div:first-child { grid-column: 1 / -1; }
  .stack-hero-metadata > div:nth-child(2) { grid-column: 1; }
  .stack-hero-metadata > div:nth-child(3) { grid-column: 2; justify-self: end; }
  .stack-hero-metadata > div + div::before { display: none; }
  .stack-hero-metadata dd { min-width: 0; overflow-wrap: break-word; }
  .stack-compare-button { grid-column: 1 / -1; grid-row: 3; justify-self: end; }
  .stack-summary { grid-template-columns: minmax(0, 1fr); }
  .stack-summary > div { min-height: 64px; }
  .stack-summary > div + div { border-top: 1px solid var(--border-subtle); border-left: 0; }
  .stack-summary-resources dd { gap: 8px 16px; }
  .stack-services-panel :deep(.services-stack-table--detail) { display: block; width: 100%; min-width: 0 !important; table-layout: auto; }
  .stack-services-panel :deep(.services-stack-table--detail colgroup) { display: none; }
  .stack-services-panel :deep(.services-stack-table--detail thead) { display: none; }
  .stack-services-panel :deep(.services-stack-table--detail tbody) { display: block; width: 100%; }
  .stack-services-panel :deep(.services-stack-table--detail tr) { width: 100%; display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 4px 12px; padding: 12px 16px; }
  .stack-services-panel :deep(.services-stack-table--detail tr + tr) { border-top: 1px solid var(--border-subtle); }
  .stack-services-panel :deep(.services-stack-table--detail td) { max-width: none; height: auto; padding: 0; border: 0; overflow: visible; text-overflow: clip; }
  .stack-services-panel :deep(.services-cell-name) { grid-column: 1; grid-row: 1; }
  .stack-services-panel :deep(.services-cell-sync-status) { grid-column: 2; grid-row: 1; text-align: right; }
  .stack-services-panel :deep(.services-cell-type) { grid-column: 1; grid-row: 2; color: var(--muted); font-size: .78rem; }
  .stack-services-panel :deep(.services-cell-image) { grid-column: 2; grid-row: 2; color: var(--muted); font-size: .78rem; text-align: right; }
  .stack-services-panel :deep(.app-table-empty-cell) { grid-column: 1 / -1; text-align: left; }
}
@media (prefers-reduced-motion: reduce) {
  .stack-details-page button { transition-duration: 0s; }
}
</style>
