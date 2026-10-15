<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { useRoute } from "vue-router";

import { fetchNetworks } from "../api/cluster";
import { fetchAlerts, fetchDeployments, fetchStacks } from "../api/overview";
import { fetchServices } from "../api/services";
import type { Alert, DeploymentSummary, NetworkInfo, ServiceInfo, StackView } from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import ServicesTable from "../components/overview/ServicesTable.vue";
import SummaryPanel from "../components/overview/SummaryPanel.vue";
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
const loading = ref(false);
const error = ref("");
const deploymentsError = ref("");
const alertsError = ref("");
const networksError = ref("");
const syncStatus = computed(() => stackSyncStatus(stack.value?.status));
const deploymentsLink = computed(() => ({ name: "deployments", query: { stack_name: stackName.value } }));
let request = 0;

function errorMessage(reason: unknown): string {
  return reason instanceof Error ? reason.message : "Request failed";
}

async function load() {
  const current = ++request;
  const name = stackName.value;
  stack.value = null;
  services.value = [];
  deployments.value = [];
  alerts.value = [];
  networks.value = [];
  error.value = "";
  deploymentsError.value = "";
  alertsError.value = "";
  networksError.value = "";
  if (!name.trim()) { loading.value = false; return; }
  loading.value = true;
  const [stackResult, serviceResult, deploymentResult, alertResult, networkResult] = await Promise.allSettled([
    fetchStacks(), fetchServices(), fetchDeployments({ stack: name, limit: 5 }),
    // Without a limit the existing API returns all open alerts, so filtering cannot hide a stack alert.
    fetchAlerts({ status: "open" }), fetchNetworks(),
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
      .sort((left, right) => Date.parse(right.started_at) - Date.parse(left.started_at));
  } else deploymentsError.value = errorMessage(deploymentResult.reason);
  if (alertResult.status === "fulfilled") {
    alerts.value = (alertResult.value.alerts ?? []).filter((item) =>
      item.status === "open" && item.resourceType === "stack" && item.resourceId === name);
  } else alertsError.value = errorMessage(alertResult.reason);
  if (networkResult.status === "fulfilled") {
    networks.value = (networkResult.value.networks ?? []).filter((item) => item.stack_name === name)
      .sort((left, right) => left.name.localeCompare(right.name));
  } else networksError.value = errorMessage(networkResult.reason);
  loading.value = false;
}

watch(stackName, () => { void load(); }, { immediate: true });
onUnmounted(() => { request++; });
</script>

<template>
  <section class="services-page stack-details-page">
    <nav class="stack-breadcrumbs meta" aria-label="Breadcrumb">
      <RouterLink to="/overview">Overview</RouterLink><span aria-hidden="true">/</span><span aria-current="page">{{ stackName || "Unknown stack" }}</span>
    </nav>
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
        <div><dt>Services</dt><dd>{{ services.length }}</dd></div>
        <div><dt>Synced</dt><dd>{{ stack.status?.synced ?? 0 }}</dd></div>
        <div><dt>OutOfSync</dt><dd>{{ stack.status?.out_of_synced ?? 0 }}</dd></div>
        <div><dt>Active alerts</dt><dd>{{ alertsError ? "n/a" : alerts.length }}</dd></div>
      </dl>
      <section aria-labelledby="stack-services-title">
        <h3 id="stack-services-title">Services</h3>
        <ServicesTable :services="services" />
      </section>
      <div class="stack-details-panels">
        <SummaryPanel title="Recent Deployments" icon="deployments" :to="deploymentsLink">
          <p v-if="deploymentsError" class="meta" role="alert">Failed to load deployments: {{ deploymentsError }}</p>
          <p v-else-if="!deployments.length" class="meta">No deployments yet.</p>
          <SummaryRow v-for="deployment in deployments" v-else :key="deployment.id" interactive :aria-label="`Open ${deployment.status} deployment details`" @activate="store.openDeploymentDetailsModal(deployment.id)">
            <span class="overview-summary-row-copy">
              <strong>{{ deployment.status }}</strong>
              <time class="overview-summary-secondary" :datetime="deployment.started_at">{{ formatDate(deployment.started_at) }}</time>
            </span>
            <span class="overview-summary-sha-badge">{{ shortCommitHash(deployment.commit) || "n/a" }}</span>
          </SummaryRow>
          <RouterLink :to="deploymentsLink" class="stack-history-link">View all deployments</RouterLink>
        </SummaryPanel>
        <SummaryPanel title="Active Alerts" icon="alerts" to="/events?tab=alerts">
          <p v-if="alertsError" class="meta" role="alert">Failed to load alerts: {{ alertsError }}</p>
          <p v-else-if="!alerts.length" class="meta">No active alerts.</p>
          <SummaryRow v-for="alert in alerts" v-else :key="alert.id" interactive :aria-label="`Open alert: ${alert.title}`" @activate="store.openAlertDetailsModal(alert)">
            <span class="overview-summary-severity overview-summary-severity--alert" aria-hidden="true" />
            <span class="overview-summary-row-copy"><span class="overview-alert-message">{{ alert.title }}</span><time class="overview-summary-secondary" :datetime="alert.updatedAt">{{ formatDate(alert.updatedAt) }}</time></span>
          </SummaryRow>
        </SummaryPanel>
      </div>
      <section v-if="networks.length || networksError" aria-labelledby="stack-resources-title">
        <h3 id="stack-resources-title">Resources</h3>
        <p v-if="networksError" class="meta" role="alert">Network information unavailable: {{ networksError }}</p>
        <template v-else>
          <h4>Networks</h4>
          <AppTable min-width="360px">
            <template #head><tr><th>Name</th><th>Driver</th><th>Scope</th></tr></template>
            <tr v-for="network in networks" :key="network.id"><td>{{ network.name }}</td><td>{{ network.driver }}</td><td>{{ network.scope }}</td></tr>
          </AppTable>
        </template>
      </section>
    </template>
  </section>
</template>

<style scoped>
.stack-details-page { display: grid; gap: 20px; }
.stack-breadcrumbs, .stack-details-heading { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; min-width: 0; overflow-wrap: anywhere; }
.stack-breadcrumbs a, .stack-history-link { color: var(--accent-strong); }
.stack-details-header { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 12px; }
.stack-details-heading h2 { margin: 0; overflow-wrap: anywhere; }
.stack-details-metadata { flex-basis: 100%; display: flex; flex-wrap: wrap; gap: 24px; margin: 0; }
.stack-details-metadata > div { display: flex; align-items: center; gap: 8px; }
.stack-details-metadata dd { margin: 0; }
.stack-details-summary { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; margin: 0; padding: 14px; border: 1px solid var(--border-subtle); border-radius: 10px; background: var(--surface-card-soft); }
.stack-details-summary dt { color: var(--muted); font-size: .8rem; }
.stack-details-summary dd { margin: 6px 0 0; font-weight: 600; font-size: 1.2rem; }
.stack-details-panels { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
.stack-history-link { display: inline-block; margin-top: 12px; font-size: .85rem; }
h3 { margin: 0 0 12px; }
@media (max-width: 760px) {
  .stack-details-panels { grid-template-columns: minmax(0, 1fr); }
  .stack-details-summary { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
</style>
