<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { useRoute } from "vue-router";

import { fetchAlerts, fetchDeployments, fetchRecommendations, fetchStackResources, fetchStacks } from "../api/overview";
import { fetchServices } from "../api/services";
import type { Alert, DeploymentSummary, NetworkInfo, ServiceInfo, StackResourceInfo, StackView } from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import SidebarIcon from "../components/layout/SidebarIcon.vue";
import DeploymentIcon from "../components/overview/DeploymentIcon.vue";
import ServicesTable from "../components/overview/ServicesTable.vue";
import SummaryRow from "../components/overview/SummaryRow.vue";
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
const recommendationCount = ref<number | null>(null);
const recommendationsError = ref("");
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

function deploymentStatusClass(status: string): string {
  switch (status) {
    case "succeeded": return "success";
    case "failed": return "failed";
    case "running": return "deployment-status-running";
    default: return "unknown";
  }
}

function deploymentStatusLabel(status: string): string {
  return status ? status.charAt(0).toUpperCase() + status.slice(1) : "Unknown";
}

function openDeploymentFromKeyboard(event: KeyboardEvent, id: string) {
  if (event.target !== event.currentTarget || (event.key !== "Enter" && event.key !== " ")) return;
  event.preventDefault();
  void store.openDeploymentDetailsModal(id);
}

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
  recommendationCount.value = null;
  recommendationsError.value = "";
  error.value = "";
  deploymentsError.value = "";
  alertsError.value = "";
  resourcesError.value = "";
  if (!name.trim()) { loading.value = false; return; }
  loading.value = true;
  const [stackResult, serviceResult, deploymentResult, alertResult, resourceResult, recommendationResult] = await Promise.allSettled([
    fetchStacks(), fetchServices(), fetchDeployments({ stack: name, limit: 3 }),
    fetchAlerts({ status: "open", stack: name }), fetchStackResources(name), fetchRecommendations({ stack: name }),
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
      .sort((left, right) => Date.parse(right.started_at) - Date.parse(left.started_at)).slice(0, 3);
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
  if (recommendationResult.status === "fulfilled") {
    recommendationCount.value = (recommendationResult.value.recommendations ?? []).filter((item) => item.subject.stack === name).length;
  } else recommendationsError.value = errorMessage(recommendationResult.reason);
  loading.value = false;
}

watch(stackName, () => { void load(); }, { immediate: true });
onUnmounted(() => { request++; });
</script>

<template>
  <section class="services-page stack-details-page">
    <AppTableEmpty v-if="loading" role="status" message="Loading stack..." />
    <AppTableEmpty v-else-if="error" role="alert">
      Failed to load stack: {{ error }} <button type="button" class="button-ghost" @click="load">Retry</button>
    </AppTableEmpty>
    <AppTableEmpty v-else-if="!stack" message="Stack not found." />
    <template v-else>
      <header class="stack-details-header">
        <div class="stack-details-heading">
          <h2>{{ stack.name }}</h2>
          <span class="overview-stack-status-badge" :class="`overview-stack-status-badge--${syncStatus.kind}`">{{ syncStatus.label }}</span>
        </div>
        <button type="button" class="button-ghost" @click="store.openStackManifestModal(stack.name)">Compare state</button>
        <dl class="stack-details-metadata meta">
          <div><dt>Last deploy</dt><dd>{{ formatDate(stack.last_deploy_at) }}</dd></div>
          <div><dt>Commit</dt><dd>
            <button v-if="stack.last_commit" type="button" class="overview-commit-badge" :aria-label="`Open commit ${shortCommitHash(stack.last_commit)}`" @click="store.openCommitDetailsModal(stack.last_commit)">{{ shortCommitHash(stack.last_commit) }}</button>
            <span v-else>n/a</span>
          </dd></div>
        </dl>
      </header>
      <p v-if="stack.last_error" class="meta" role="alert">{{ stack.last_error }}</p>
      <dl class="stack-details-summary" aria-label="Stack summary">
        <div data-summary="services"><dt><DeploymentIcon name="service" class="summary-icon summary-icon--muted" />Services</dt><dd>{{ services.length }}</dd></div>
        <div data-summary="synced"><dt><DeploymentIcon name="check" class="summary-icon summary-icon--success" />Synced</dt><dd>{{ stack.status?.synced ?? 0 }}</dd></div>
        <div data-summary="out-of-sync"><dt><DeploymentIcon name="warning" class="summary-icon" :class="stack.status?.out_of_synced > 0 ? 'summary-icon--warning' : 'summary-icon--muted'" />OutOfSync</dt><dd>{{ stack.status?.out_of_synced ?? 0 }}</dd></div>
        <div data-summary="alerts"><dt><RouterLink class="stack-summary-link" :to="{ name: 'events', query: { tab: 'alerts', stack_name: stack.name } }" :aria-label="`View active alerts for stack ${stack.name}`"><SidebarIcon name="alerts" class="summary-icon" :class="!alertsError && alerts.length > 0 ? 'summary-icon--error' : 'summary-icon--muted'" />Active alerts</RouterLink></dt><dd>{{ alertsError ? "n/a" : alerts.length }}</dd></div>
        <div data-summary="recommendations"><dt><RouterLink class="stack-summary-link" :to="{ name: 'recommendations', query: { stack_name: stack.name } }" :aria-label="`View recommendations for stack ${stack.name}`"><SidebarIcon name="recommendations" class="summary-icon" :class="recommendationCount !== null && recommendationCount > 0 ? 'summary-icon--warning' : 'summary-icon--muted'" />Recommendations</RouterLink></dt><dd>{{ recommendationCount ?? "n/a" }}</dd></div>
      </dl>
      <p v-if="recommendationsError" class="meta" role="alert">Failed to load recommendations: {{ recommendationsError }}</p>
      <div class="stack-details-main">
        <section aria-labelledby="stack-services-title">
          <h3 id="stack-services-title">Services</h3>
          <ServicesTable :services="services" min-width="520px" />
        </section>
        <section aria-labelledby="stack-deployments-title">
          <h3 id="stack-deployments-title">Recent Deployments</h3>
          <p v-if="deploymentsError" class="meta" role="alert">Failed to load deployments: {{ deploymentsError }}</p>
          <p v-else-if="!deployments.length" class="meta">No deployments yet.</p>
          <AppTable v-else min-width="360px" table-class="stack-deployments-table" aria-label="Recent deployments">
            <template #head><tr><th>Status</th><th>Commit</th><th>Timestamp</th></tr></template>
            <tr v-for="deployment in deployments" :key="deployment.id" class="app-table-row--clickable" tabindex="0"
              :aria-label="`Open ${deployment.status} deployment details`"
              @click="store.openDeploymentDetailsModal(deployment.id)"
              @keydown="openDeploymentFromKeyboard($event, deployment.id)">
              <td><span class="status" :class="deploymentStatusClass(deployment.status)">{{ deploymentStatusLabel(deployment.status) }}</span></td>
              <td>
                <button v-if="deployment.commit" type="button" class="overview-commit-badge"
                  :aria-label="`Open deployment commit ${deployment.commit}`"
                  @click.stop="store.openCommitDetailsModal(deployment.commit)" @keydown.stop>{{ shortCommitHash(deployment.commit) }}</button>
                <span v-else class="meta">n/a</span>
              </td>
              <td><time :datetime="deployment.started_at">{{ formatDate(deployment.started_at) }}</time></td>
            </tr>
          </AppTable>
        </section>
      </div>
      <p v-if="alertsError" class="meta" role="alert">Failed to load alerts: {{ alertsError }}</p>
      <section v-else-if="alerts.length" aria-labelledby="stack-alerts-title">
        <h3 id="stack-alerts-title">Active Alerts</h3>
        <div class="stack-alert-list">
          <SummaryRow v-for="alert in alerts" :key="alert.id" interactive :aria-label="`Open alert: ${alert.title}`" @activate="store.openAlertDetailsModal(alert)">
            <span class="overview-alert-message">{{ alert.title }}</span>
            <time class="meta" :datetime="alert.updatedAt">{{ formatDate(alert.updatedAt) }}</time>
          </SummaryRow>
        </div>
      </section>
      <section aria-labelledby="stack-resources-title">
        <h3 id="stack-resources-title">Resources</h3>
        <div class="stack-resource-summary" :key="stack.name">
          <details class="stack-resource">
            <summary><DeploymentIcon name="network" />Networks <strong>{{ resourcesError ? "n/a" : networks.length }}</strong></summary>
            <p v-if="resourcesError" class="meta" role="alert">Resource information unavailable: {{ resourcesError }}</p>
            <ul v-else-if="networks.length" class="stack-resource-list">
              <li v-for="network in networks" :key="network.id"><span>{{ network.name }}</span><span class="meta">{{ network.driver }} · {{ network.scope }}</span></li>
            </ul>
            <p v-else class="meta">No stack-owned networks.</p>
          </details>
          <details v-for="group in [{ title: 'Secrets', icon: 'secret', items: secrets }, { title: 'Configs', icon: 'config', items: configs }]" :key="group.title" class="stack-resource">
            <summary><DeploymentIcon :name="group.icon === 'secret' ? 'secret' : 'config'" />{{ group.title }} <strong>{{ resourcesError ? "n/a" : group.items.length }}</strong></summary>
            <p v-if="resourcesError" class="meta">Resource information unavailable: {{ resourcesError }}</p>
            <ul v-else-if="group.items.length" class="stack-resource-list">
              <li v-for="resource in group.items" :key="resource.id">{{ resource.name }}</li>
            </ul>
            <p v-else class="meta">No stack-owned {{ group.title.toLowerCase() }}.</p>
          </details>
        </div>
      </section>
    </template>
  </section>
</template>

<style scoped>
.stack-details-page { display: grid; gap: 12px; min-width: 0; }
.stack-details-page > section { min-width: 0; }
.stack-details-heading { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; min-width: 0; overflow-wrap: anywhere; }
.stack-details-header { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 6px 12px; }
.stack-details-heading h2 { margin: 0; overflow-wrap: anywhere; }
.stack-details-metadata { flex-basis: 100%; display: flex; flex-wrap: wrap; gap: 24px; margin: 0; }
.stack-details-metadata > div { display: flex; align-items: center; gap: 8px; }
.stack-details-metadata dd { margin: 0; }
.stack-details-summary { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 0; margin: 0; padding: 8px 0; border-block: 1px solid var(--border-subtle); }
.stack-details-summary > div { position: relative; display: flex; align-items: center; gap: 6px; padding: 0 14px; border-left: 1px solid var(--border-subtle); }
.stack-details-summary > div:first-child { padding-left: 0; border-left: 0; }
.stack-details-summary dt { display: flex; align-items: center; gap: 5px; color: var(--muted); font-size: .8rem; }
.stack-details-summary dd { margin: 0; font-weight: 600; font-size: .85rem; }
.stack-summary-link { display: inline-flex; align-items: center; gap: 5px; color: inherit; text-decoration: none; }
.stack-summary-link::after { content: ""; position: absolute; inset: 0; }
.stack-summary-link:hover { color: var(--accent-strong); }
.stack-details-summary > div:has(.stack-summary-link:focus-visible) { outline: 2px solid var(--accent-focus); outline-offset: 2px; }
.summary-icon { width: 15px; height: 15px; stroke: currentColor; stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; flex-shrink: 0; }
.summary-icon--muted { color: var(--muted); }
.summary-icon--success { color: var(--success); }
.summary-icon--warning { color: var(--warning); }
.summary-icon--error { color: var(--error); }
.stack-details-main { display: grid; grid-template-columns: minmax(0, 3fr) minmax(0, 2fr); align-items: start; gap: 18px; }
.stack-details-main > section { min-width: 0; }
.stack-details-main > section + section { border-left: 1px solid var(--border-subtle); padding-left: 18px; }
h3 { margin: 0; }
.stack-history-link { font-size: .8rem; }
h3 { margin: 0 0 8px; font-size: .95rem; }
p { margin: 6px 0; }
:deep(.app-table th), :deep(.app-table td) { padding-top: 7px; padding-bottom: 7px; }
:deep(.stack-deployments-table th:last-child), :deep(.stack-deployments-table td:last-child) { text-align: right; white-space: nowrap; }
.deployment-status-running { color: var(--info); border-color: var(--accent-border); background: var(--info-muted); }
.stack-alert-list { border-top: 1px solid var(--border-subtle); }
.stack-alert-list :deep(.overview-summary-row) { grid-template-columns: minmax(0, 1fr) auto; min-height: 0; padding: 7px 0; gap: 12px; }
.stack-alert-list time { margin-left: auto; white-space: nowrap; font-size: .75rem; }
.stack-resource-summary { display: flex; flex-wrap: wrap; align-items: flex-start; gap: 8px 24px; border-top: 1px solid var(--border-subtle); padding-top: 8px; }
.stack-resource { min-width: 130px; max-width: 100%; font-size: .85rem; }
.stack-resource summary { cursor: pointer; padding: 3px 0; }
.stack-resource summary :deep(.deployment-icon) { display: inline-block; width: 14px; height: 14px; vertical-align: -2px; margin-right: 5px; color: var(--muted); }
.stack-resource summary > strong, .stack-resource summary > span { margin-left: 8px; }
.stack-resource > p { max-width: 280px; }
.stack-resource-list { list-style: none; margin: 6px 0 0; padding: 0; }
.stack-resource-list li { display: flex; flex-wrap: wrap; gap: 4px 12px; padding: 4px 0; overflow-wrap: anywhere; }
@media (max-width: 1100px) {
  .stack-details-main { grid-template-columns: minmax(0, 1fr); gap: 14px; }
  .stack-details-main > section + section { border-left: 0; padding-left: 0; }
}
@media (max-width: 600px) {
  .stack-details-summary > div { padding: 0 8px; }
  .stack-alert-list :deep(.overview-summary-row) { grid-template-columns: minmax(0, 1fr); gap: 3px; }
  .stack-alert-list time { margin-left: 0; }
}
</style>
