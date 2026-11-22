<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute } from "vue-router";

import { fetchNetworks } from "../api/cluster";
import { fetchServiceDeployments, fetchServiceStatus } from "../api/overview";
import { fetchServices, fetchSwarmServices } from "../api/services";
import type { NetworkInfo, ServiceDeploymentStatus, ServiceDeploymentResponse, ServiceInfo, ServiceStatusResponse, SwarmService } from "../api/types";
import AppButton from "../components/common/AppButton.vue";
import AppCopyButton from "../components/common/AppCopyButton.vue";
import AppModal from "../components/common/AppModal.vue";
import AppPanel from "../components/common/AppPanel.vue";
import AppStatusBadge from "../components/common/AppStatusBadge.vue";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import SidebarIcon from "../components/layout/SidebarIcon.vue";
import DeploymentIcon from "../components/overview/DeploymentIcon.vue";
import NetworkDetailsModal from "../components/services/NetworkDetailsModal.vue";
import ServiceSyncPauseModal from "../components/services/ServiceSyncPauseModal.vue";
import { useOverviewStore } from "../stores/overview";
import { useSecretDetailsStore } from "../stores/secretDetails";
import { formatBytes, formatDate, formatNanoCPU } from "../utils/format";
import { serviceRuntimeState } from "../utils/realtime";

const route = useRoute();
const overviewStore = useOverviewStore();
const secretDetailsStore = useSecretDetailsStore();

const stackName = computed(() => String(route.params.stack ?? ""));
const serviceName = computed(() => String(route.params.service ?? ""));
const stackRoute = computed(() => ({ name: "stack-details", params: { stack: stackName.value } }));
const realtimeRoute = computed(() => ({ name: "realtime", query: { stack: stackName.value, service: serviceName.value } }));

const loading = ref(true);
const loadingError = ref("");
const runtimeError = ref("");
const serviceInfo = ref<ServiceInfo | null>(null);
const serviceStatus = ref<ServiceStatusResponse | null>(null);
const swarmServices = ref<SwarmService[] | null>(null);
const networks = ref<NetworkInfo[]>([]);
const networksLoading = ref(false);
const networksError = ref("");
const deployments = ref<ServiceDeploymentResponse[]>([]);
const deploymentsLoading = ref(true);
const deploymentsError = ref("");
const menu = ref<HTMLDetailsElement | null>(null);
const labelsModalOpen = ref(false);
const syncPauseOpen = ref(false);
const selectedNetworkTarget = ref("");

const spec = computed(() => serviceStatus.value?.spec);
const liveService = computed(() => swarmServices.value?.find((service) =>
  service.stack === stackName.value && service.name === serviceName.value));
const available = computed(() => {
  if (!liveService.value) return swarmServices.value ? "Unavailable" : "Unknown";
  return {
    running: "Available",
    degraded: liveService.value.replicas.running > 0 ? "Degraded" : "Unavailable",
    "scaled-down": "Scaled down",
    unknown: "Unknown",
  }[serviceRuntimeState(liveService.value)];
});
const image = computed(() => serviceInfo.value?.image || spec.value?.image);
const links = computed(() => serviceStatus.value?.links ?? []);
const webRoutes = computed(() => serviceInfo.value?.web_routes ?? []);

const deploymentBadges = {
  succeeded: { label: "Succeeded", variant: "success" },
  failed: { label: "Failed", variant: "error" },
  running: { label: "Running", variant: "info" },
  interrupted: { label: "Interrupted", variant: "neutral" },
  superseded: { label: "Superseded", variant: "neutral" },
} as const;
function deploymentBadge(status: ServiceDeploymentStatus) {
  return deploymentBadges[status] ?? { label: status, variant: "neutral" as const };
}

const labelGroups = computed(() => {
  const groups = {
    Docker: new Map(Object.entries(spec.value?.labels?.docker ?? {})),
    "Swarm Deploy": new Map(Object.entries(spec.value?.labels?.swarm_deploy ?? {})),
    Other: new Map<string, string>(),
    Custom: new Map<string, string>(),
  };
  for (const [key, value] of Object.entries(spec.value?.labels?.custom ?? {})) {
    if (key.startsWith("com.docker.")) groups.Docker.set(key, value);
    else if (key.startsWith("org.swarm-deploy.") || key.startsWith("swarm-deploy.")) groups["Swarm Deploy"].set(key, value);
    else if (key === "prometheus.port") groups.Other.set(key, value);
    else groups.Custom.set(key, value);
  }
  return Object.entries(groups).map(([name, labels]) => ({
    name,
    labels: [...labels].sort(([left], [right]) => left.localeCompare(right)),
  })).filter((group) => group.labels.length);
});
const labelCount = computed(() => labelGroups.value.reduce((total, group) => total + group.labels.length, 0));
const labelPreview = computed(() => labelGroups.value.flatMap((group) => group.labels).slice(-3));
function previewKey(key: string): string {
  return key.replace(/^swarm-deploy\./, "");
}

const networkTargets = computed(() => [...new Set(
  liveService.value?.network_targets ?? spec.value?.network?.map(({ target }) => target) ?? [],
)].filter(Boolean));
const attachedNetworks = computed(() => networkTargets.value.map((target) => ({
  target,
  info: networks.value.find((network) => network.id === target || network.name === target),
})));
const selectedNetwork = computed(() => attachedNetworks.value.find(({ target }) => target === selectedNetworkTarget.value));
const configs = computed(() => spec.value?.configs ?? []);
const secrets = computed(() => spec.value?.secrets ?? []);

function openPause() {
  if (menu.value) menu.value.open = false;
  syncPauseOpen.value = true;
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

watch([stackName, serviceName], ([stack, service], _, onCleanup) => {
  let current = true;
  onCleanup(() => { current = false; });

  loading.value = true;
  loadingError.value = "";
  runtimeError.value = "";
  serviceStatus.value = null;
  serviceInfo.value = null;
  swarmServices.value = null;
  networks.value = [];
  networksError.value = "";
  networksLoading.value = true;
  deployments.value = [];
  deploymentsLoading.value = true;
  deploymentsError.value = "";
  labelsModalOpen.value = false;
  syncPauseOpen.value = false;
  selectedNetworkTarget.value = "";
  if (menu.value) menu.value.open = false;

  void fetchServiceStatus(stack, service).then((status) => {
    if (current) serviceStatus.value = status;
  }).catch((error) => {
    if (current) loadingError.value = errorMessage(error, "Failed to load service details");
  }).finally(() => {
    if (current) loading.value = false;
  });
  void fetchServices().then((response) => {
    if (current) serviceInfo.value = response.services.find((item) => item.stack === stack && item.name === service) ?? null;
  }).catch((error) => {
    if (current && !loadingError.value) loadingError.value = errorMessage(error, "Failed to load service metadata");
  });
  void fetchSwarmServices().then((response) => {
    if (current) swarmServices.value = response.services;
  }).catch((error) => {
    if (current) runtimeError.value = errorMessage(error, "Failed to load Swarm service state");
  });
  void fetchNetworks().then((response) => {
    if (current) networks.value = response.networks;
  }).catch((error) => {
    if (current) networksError.value = errorMessage(error, "Failed to load network details");
  }).finally(() => {
    if (current) networksLoading.value = false;
  });
  void fetchServiceDeployments(stack, service, 4).then((response) => {
    if (current) deployments.value = response.deployments;
  }).catch((error) => {
    if (current) deploymentsError.value = errorMessage(error, "Failed to load deployments");
  }).finally(() => {
    if (current) deploymentsLoading.value = false;
  });
}, { immediate: true });
</script>

<template>
  <section class="services-page service-details-page">
    <header class="service-heading">
      <DeploymentIcon name="service" class="service-heading-icon" />
      <div class="service-heading-content">
        <RouterLink :to="stackRoute" class="service-inline-link">{{ stackName }}</RouterLink>
        <div class="service-heading-summary">
          <h2>{{ serviceName }}</h2>
          <template v-if="!loading || liveService">
            <AppStatusBadge :variant="available === 'Available' ? 'success' : available === 'Unavailable' ? 'error' : available === 'Degraded' ? 'warning' : 'neutral'" dot filled>
              {{ available }}
            </AppStatusBadge>
            <AppStatusBadge v-if="liveService" filled>{{ liveService.replicas.running }} / {{ liveService.replicas.desired }} replicas</AppStatusBadge>
          </template>
        </div>
      </div>
      <div class="service-heading-actions">
        <AppButton :to="realtimeRoute" variant="secondary" compact class="service-realtime-link" :aria-label="`Open ${serviceName} in Realtime`" title="Realtime">
          <SidebarIcon name="realtime" /><span>Realtime</span>
        </AppButton>
        <details v-if="serviceStatus" ref="menu" class="service-actions" @keydown.esc="menu && (menu.open = false)">
          <summary aria-label="Service actions" title="Service actions">⋯</summary>
          <div class="service-actions-items">
            <button type="button" @click="openPause">Pause sync</button>
            <a v-if="serviceInfo?.repository_url" :href="serviceInfo.repository_url" target="_blank" rel="noopener noreferrer">Open repository</a>
          </div>
        </details>
      </div>
    </header>

    <AppTableEmpty v-if="loading" message="Loading service details..." />
    <AppTableEmpty v-else-if="!serviceStatus" role="alert">Failed to load service details: {{ loadingError }}</AppTableEmpty>

    <template v-else>
      <p v-if="loadingError || runtimeError" class="meta" role="status">{{ loadingError || runtimeError }}</p>
      <div class="service-dossier">
        <AppPanel tag="section" class="service-section service-metadata" aria-labelledby="service-metadata-title">
          <template #header><h3 id="service-metadata-title">Service metadata</h3></template>
          <AppTable summary fixed aria-label="Service metadata">
            <template #colgroup><colgroup><col class="service-metadata-label" /><col /></colgroup></template>
            <tr><th scope="row">Stack</th><td><RouterLink :to="stackRoute" class="service-inline-link">{{ stackName }}</RouterLink></td></tr>
            <tr><th scope="row">Type</th><td>{{ serviceInfo?.type_title || serviceInfo?.type || "n/a" }}</td></tr>
            <tr><th scope="row">Description</th><td>{{ serviceInfo?.description || "n/a" }}</td></tr>
            <tr><th scope="row">Mode</th><td class="service-mode">{{ liveService?.mode || spec?.mode || "n/a" }}</td></tr>
            <tr>
              <th scope="row">Service ID</th>
              <td>
                <span v-if="liveService?.id" class="service-copy-value">
                  <code>{{ liveService.id }}</code>
                  <AppCopyButton :value="liveService.id" color="var(--text)" label="Copy service ID" copied-label="Service ID copied" />
                </span>
                <span v-else>n/a</span>
              </td>
            </tr>
            <tr><th scope="row">Endpoint mode</th><td>{{ liveService?.endpoint_mode?.toUpperCase() || "n/a" }}</td></tr>
            <tr>
              <th scope="row">Published port</th>
              <td>{{ liveService?.published_ports?.map(({ published_port, target_port, protocol }) => `${published_port} → ${target_port}/${protocol}`).join(", ") || "n/a" }}</td>
            </tr>
            <tr><th scope="row">Update strategy</th><td>{{ liveService?.update_parallelism == null ? "n/a" : liveService.update_parallelism === 0 ? "Rolling / all at once" : `Rolling / parallelism ${liveService.update_parallelism}` }}</td></tr>
            <tr>
              <th scope="row">Image</th>
              <td>
                <span v-if="image" class="service-copy-value">
                  <span>{{ image }}</span>
                  <AppCopyButton class="service-image-copy-button" :value="image" color="var(--text)" label="Copy image reference" copied-label="Image reference copied" />
                </span>
                <span v-else>n/a</span>
              </td>
            </tr>
            <tr>
              <th scope="row">Repository</th>
              <td>
                <a v-if="serviceInfo?.repository_url" :href="serviceInfo.repository_url" class="service-inline-link" target="_blank" rel="noopener noreferrer">{{ serviceInfo.repository_url }}</a>
                <span v-else>n/a</span>
              </td>
            </tr>
            <tr><th scope="row">Created</th><td><time :datetime="liveService?.created_at">{{ formatDate(liveService?.created_at) }}</time></td></tr>
            <tr>
              <th scope="row">Labels</th>
              <td>
                <div v-if="labelPreview.length" class="service-labels-preview">
                  <div class="service-labels-tokens">
                    <code v-for="[key, value] in labelPreview" :key="key" class="service-label-token" :title="`${key}=${value}`">{{ previewKey(key) }}<span>={{ value }}</span></code>
                  </div>
                  <AppButton variant="secondary" compact class="service-labels-show" aria-haspopup="dialog" @click="labelsModalOpen = true">Show all</AppButton>
                </div>
                <span v-else class="meta">No labels.</span>
              </td>
            </tr>
          </AppTable>
        </AppPanel>

        <div class="service-related">
          <AppPanel tag="section" class="service-section" aria-labelledby="service-related-title">
            <template #header><h3 id="service-related-title">Related entities</h3></template>
            <ul v-if="attachedNetworks.length || secrets.length || configs.length" class="service-entities">
              <li v-for="{ target, info } in attachedNetworks" :key="`network-${target}`">
                <button class="service-entity" type="button" aria-haspopup="dialog" @click="selectedNetworkTarget = target">
                  <DeploymentIcon name="network" /><span>Network</span><strong>{{ info?.name || target }}</strong><span aria-hidden="true">›</span>
                </button>
              </li>
              <li v-for="secret in secrets" :key="`secret-${secret.secret_id || secret.secret_name}-${secret.target}`">
                <button class="service-entity" type="button" aria-haspopup="dialog" @click="secretDetailsStore.openSecretDetails(secret.secret?.name || secret.secret_name)">
                  <DeploymentIcon name="secret" /><span>Secret</span><strong>{{ secret.secret?.name || secret.secret_name }}</strong><span aria-hidden="true">›</span>
                </button>
              </li>
              <li v-for="config in configs" :key="`config-${config.config_id || config.config_name}-${config.target}`">
                <RouterLink class="service-entity" :to="{ name: 'service-config', params: { stack: stackName, service: serviceName, config: config.config_id || config.config_name }, query: config.target ? { target: config.target } : undefined }">
                  <DeploymentIcon name="config" /><span>Config</span><strong>{{ config.config_name }}</strong><span aria-hidden="true">›</span>
                </RouterLink>
              </li>
            </ul>
            <AppTableEmpty v-else message="No related entities." />
          </AppPanel>

          <AppPanel tag="section" class="service-section service-deployments" aria-labelledby="service-deployments-title">
            <template #header>
              <h3 id="service-deployments-title"><RouterLink :to="{ name: 'deployments', query: { stack_name: stackName, service_name: serviceName } }">Deployments</RouterLink></h3>
            </template>
            <AppTableEmpty v-if="deploymentsLoading" message="Loading deployments..." />
            <AppTableEmpty v-else-if="deploymentsError" role="alert">{{ deploymentsError }}</AppTableEmpty>
            <AppTableEmpty v-else-if="!deployments.length" message="No deployments yet." />
            <AppTable v-else fixed aria-label="Deployment history">
              <template #head><tr><th scope="col">Version</th><th scope="col">Status</th><th scope="col">Started</th><th scope="col"><span class="sr-only">Details</span></th></tr></template>
              <tr v-for="deployment in deployments" :key="deployment.id" class="app-table-row--clickable" @click="overviewStore.openDeploymentDetailsModal(deployment.id)">
                <td><strong :title="deployment.image">{{ deployment.image_version || "n/a" }}</strong></td>
                <td><AppStatusBadge :variant="deploymentBadge(deployment.status).variant" filled>{{ deploymentBadge(deployment.status).label }}</AppStatusBadge></td>
                <td><time :datetime="deployment.created_at">{{ formatDate(deployment.created_at) }}</time></td>
                <td><button class="service-deployment-open" type="button" :aria-label="`Open deployment ${deployment.image_version || deployment.id} details`" @click.stop="overviewStore.openDeploymentDetailsModal(deployment.id)"><span aria-hidden="true">›</span></button></td>
              </tr>
            </AppTable>
          </AppPanel>
        </div>
      </div>

      <AppPanel v-if="links.length || webRoutes.length || spec" tag="section" class="service-section service-supplementary" aria-labelledby="service-resources-title">
        <template #header><h3 id="service-resources-title">Resources</h3></template>
        <AppTable summary fixed aria-label="Service resources">
          <tr v-if="spec"><th scope="row">Requested / limited RAM</th><td>{{ spec.requested_ram_bytes ? formatBytes(spec.requested_ram_bytes) : 0 }} / {{ spec.limit_ram_bytes ? formatBytes(spec.limit_ram_bytes) : "∞" }}</td></tr>
          <tr v-if="spec"><th scope="row">Requested / limited CPU</th><td>{{ spec.requested_cpu_nano ? formatNanoCPU(spec.requested_cpu_nano) : 0 }} / {{ spec.limit_cpu_nano ? formatNanoCPU(spec.limit_cpu_nano) : "∞" }}</td></tr>
          <tr v-if="webRoutes.length">
            <th scope="row">Web Routes</th>
            <td><ul><li v-for="item in webRoutes" :key="`${item.domain}-${item.address}-${item.port}`">{{ item.domain }} ({{ item.address }}:{{ item.port }})</li></ul></td>
          </tr>
          <tr v-if="links.length">
            <th scope="row">Links</th>
            <td><ul><li v-for="link in links" :key="`${link.type}-${link.url}`"><a :href="link.url" class="service-inline-link" target="_blank" rel="noopener noreferrer">{{ link.type }}</a></li></ul></td>
          </tr>
        </AppTable>
      </AppPanel>
    </template>

    <AppModal
      :open="labelsModalOpen"
      class="service-labels-modal"
      labelledby="service-labels-title"
      card-class="service-labels-modal-card"
      close-label="Close labels"
      @close="labelsModalOpen = false"
    >
      <template #title>
        <div>
          <p class="service-labels-context">Service metadata</p>
          <div class="service-labels-title">
            <h2 id="service-labels-title">All labels</h2>
            <span class="service-labels-count" :aria-label="`${labelCount} labels`">{{ labelCount }}</span>
          </div>
        </div>
      </template>
      <div class="modal-body service-labels-modal-body">
        <section v-for="group in labelGroups" :key="group.name" class="service-labels-group">
          <header class="service-labels-group-header">
            <h3>{{ group.name }}</h3>
            <span :aria-label="`${group.labels.length} labels`">{{ group.labels.length }}</span>
          </header>
          <dl class="service-labels-list">
            <div v-for="[key, value] in group.labels" :key="key" class="service-labels-list-item">
              <dt>{{ key }}</dt>
              <dd>
                <span>{{ value }}</span>
                <AppCopyButton
                  :value="`${key}=${value}`"
                  :label="`Copy ${key} label`"
                  copied-label="Label copied"
                  color="var(--muted)"
                />
              </dd>
            </div>
          </dl>
        </section>
      </div>
    </AppModal>

    <NetworkDetailsModal v-if="selectedNetwork" :target="selectedNetwork.target" :network="selectedNetwork.info" :services="swarmServices" :loading="networksLoading" :error="networksError" @close="selectedNetworkTarget = ''" />
    <ServiceSyncPauseModal v-if="syncPauseOpen" :stack="stackName" :service="serviceName" @close="syncPauseOpen = false" />
  </section>
</template>

<style scoped>
.service-details-page {
  gap: 20px;
}

.service-heading {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.service-heading-icon {
  box-sizing: content-box;
  width: 24px;
  height: 24px;
  padding: 10px;
  border-radius: 8px;
  color: var(--muted);
  background: var(--surface-muted);
}

.service-heading-content {
  min-width: 0;
  font-size: .84rem;
}

.service-heading-summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 10px;
}

.service-heading-summary h2 {
  margin: 0;
  font-size: clamp(1.3rem, 3vw, 1.85rem);
  overflow-wrap: anywhere;
}

.service-heading-summary :deep(.app-status-badge) {
  white-space: nowrap;
}

.service-heading-actions {
  display: flex;
  gap: 6px;
  align-items: center;
  align-self: center;
}

.service-realtime-link :deep(svg) {
  width: 17px;
  height: 17px;
  stroke: currentColor;
  stroke-width: 1.7;
}

.service-actions {
  position: relative;
}

.service-actions > summary {
  display: grid;
  place-items: center;
  width: 34px;
  height: 34px;
  border: 1px solid var(--border-default);
  border-radius: 8px;
  background: var(--surface-control);
  cursor: pointer;
  list-style: none;
}

.service-actions > summary::-webkit-details-marker {
  display: none;
}

.service-actions-items {
  position: absolute;
  z-index: 5;
  top: calc(100% + 6px);
  right: 0;
  display: grid;
  width: max-content;
  padding: 4px;
  border: 1px solid var(--border-default);
  border-radius: 8px;
  background: var(--surface-card);
}

.service-actions-items > * {
  padding: 7px 10px;
  text-align: left;
}

.service-dossier {
  display: grid;
  grid-template-columns: minmax(0, 3fr) minmax(0, 2fr);
  align-items: start;
  gap: 24px;
}

.service-related {
  min-width: 0;
  display: grid;
  align-content: start;
  gap: 18px;
  border-inline-start: 1px solid var(--border-subtle);
  padding-inline-start: 24px;
}

.service-section {
  min-width: 0;
  border: 0;
  background: transparent;
  box-shadow: none;
}

.service-section h3 {
  margin: 0 0 8px;
  color: var(--text);
  font-size: 1rem;
}

.service-section h3 a {
  color: inherit;
  text-decoration: none;
}

.service-inline-link {
  color: var(--accent-strong);
  text-decoration: none;
}

.service-inline-link:hover {
  text-decoration: underline;
}

.service-section h3 a:hover {
  color: var(--accent-strong);
}

.service-section :deep(.app-table-wrap) {
  border-radius: 0;
  border: 0;
  border-block-start: 1px solid var(--border-subtle);
  background: transparent;
}

.service-section :deep(.app-table--summary tbody th),
.service-section :deep(.app-table--summary tbody td) {
  padding: 8px 0;
  vertical-align: middle;
  font-size: .84rem;
}

.service-section :deep(.app-table--summary tbody th) {
  padding-inline-end: 8px;
}

.service-section :deep(.app-table tr:last-child > *) {
  border-bottom: 1px solid var(--border-subtle);
}

.service-metadata-label {
  width: 145px;
}

.service-mode {
  text-transform: capitalize;
}

.service-copy-value {
  display: flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
}

.service-labels-preview,
.service-labels-tokens {
  display: flex;
  align-items: center;
  min-width: 0;
  gap: 4px;
}

.service-labels-preview {
  justify-content: space-between;
}

.service-labels-tokens {
  flex: 0 1 auto;
}

.service-label-token {
  min-width: 0;
  padding: 3px 5px;
  border-radius: 6px;
  background: var(--surface-muted);
  color: var(--text);
  font-size: .72rem;
  font-family: inherit;
  text-overflow: ellipsis;
  overflow: hidden;
  white-space: nowrap;
}

.service-labels-show {
  flex: none;
  padding: 3px 6px;
  font-size: .71rem;
  white-space: nowrap;
}

.service-entities,
.service-supplementary ul {
  padding: 0;
  margin: 0;
  list-style: none;
}

.service-entities {
  border-top: 1px solid var(--border-subtle);
}

.service-entities li {
  border-bottom: 1px solid var(--border-subtle);
}

.service-entity {
  display: grid;
  grid-template-columns: 18px 56px minmax(0, 1fr) auto;
  align-items: center;
  width: 100%;
  gap: 8px;
  padding: 8px 5px;
  font-size: .83rem;
  text-align: left;
  text-decoration: none;
}

a.service-entity {
  color: var(--text);
}

.service-entity strong {
  overflow-wrap: anywhere;
}

.service-entity > span {
  color: var(--muted);
}

.service-deployment-open,
.service-actions-items button,
button.service-entity {
  border: 0;
  border-radius: 0;
  background: transparent;
  color: var(--text);
  box-shadow: none;
}

.service-entity:hover,
.service-deployment-open:hover,
.service-actions-items > *:hover {
  color: var(--accent-strong);
  background: var(--surface-muted);
  transform: none;
  filter: none;
}

.service-deployments :deep(.app-table th),
.service-deployments :deep(.app-table td) {
  padding: 7px 4px;
  font-size: .76rem;
  overflow-wrap: anywhere;
}

.service-deployments :deep(.app-table th:nth-child(2)) {
  width: 30%;
}

.service-deployments :deep(.app-table th:nth-child(3)) {
  width: 34%;
}

.service-deployments :deep(.app-table th:last-child) {
  width: 22px;
}

.service-deployments :deep(.app-status-badge) {
  max-width: 100%;
  padding-inline: 5px;
  font-size: .7rem;
}

.service-deployments tbody tr {
  cursor: pointer;
}

.service-deployments td time {
  color: var(--muted);
}

.service-deployment-open {
  padding: 1px 5px;
  font-size: 1rem;
}

.service-supplementary {
  max-width: 720px;
}

.service-labels-modal {
  display: grid;
  place-items: center;
}

.service-labels-modal :deep(.modal-overlay) {
  backdrop-filter: blur(2px);
}

.service-labels-modal :deep(.service-labels-modal-card) {
  display: flex;
  flex-direction: column;
  width: min(720px, calc(100% - 40px));
  max-height: calc(100dvh - 48px);
  margin: 0;
  overflow: hidden;
  border-radius: 12px;
  box-shadow: 0 24px 80px var(--backdrop-soft);
}

.service-labels-modal :deep(.modal-header) {
  flex: none;
  min-height: 92px;
  padding: 20px 68px 17px 22px;
}

.service-labels-modal :deep(.modal-close) {
  top: 20px;
  right: 22px;
  width: 38px;
  height: 38px;
  border: 1px solid var(--border-default);
  color: var(--text);
}

.service-labels-context,
.service-labels-group-header {
  color: var(--muted);
  font-weight: 700;
  letter-spacing: .055em;
  text-transform: uppercase;
}

.service-labels-context {
  margin: 0 0 4px;
  font-size: .75rem;
}

.service-labels-title {
  display: flex;
  align-items: center;
  gap: 8px;
}

.service-labels-title h2 {
  font-size: 1.375rem;
}

.service-labels-count {
  display: inline-grid;
  place-items: center;
  min-width: 23px;
  min-height: 20px;
  padding-inline: 6px;
  border-radius: 999px;
  background: var(--surface-muted);
  color: var(--muted);
  font-size: .7rem;
  font-weight: 700;
  text-align: center;
}

.service-labels-modal-body {
  min-height: 0;
  gap: 14px;
  padding: 18px 22px 22px;
  overflow-y: auto;
  overscroll-behavior: contain;
}

.service-labels-group-header {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  min-height: 25px;
  padding: 0 2px 5px;
  border-bottom: 1px solid var(--border-subtle);
  font-size: .6875rem;
}

.service-labels-group-header h3 {
  margin: 0;
  font-size: inherit;
}

.service-labels-list {
  margin: 0;
}

.service-labels-list-item,
.service-labels-list-item dd {
  display: grid;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.service-labels-list-item {
  grid-template-columns: minmax(0, 34%) minmax(0, 1fr);
  min-height: 36px;
  padding-block: 3px;
  border-bottom: 1px solid var(--border-subtle);
  font-size: .75rem;
  overflow-wrap: anywhere;
}

.service-labels-list-item dt,
.service-labels-list-item dd > span {
  white-space: pre-wrap;
}

.service-labels-list-item dt {
  color: var(--muted);
}

.service-labels-list-item dd {
  grid-template-columns: minmax(0, 1fr) 28px;
  margin: 0;
}

@media (max-width: 820px) {
  .service-labels-modal {
    align-items: end;
  }

  .service-labels-modal :deep(.service-labels-modal-card) {
    width: 100%;
    max-width: none;
    max-height: 88dvh;
    border-width: 1px 0 0;
    border-radius: 16px 16px 0 0;
  }

  .service-labels-modal :deep(.modal-header) {
    min-height: 88px;
    padding: 18px 62px 14px 16px;
  }

  .service-labels-modal :deep(.modal-close) {
    top: 18px;
    right: 16px;
  }

  .service-labels-modal-body {
    padding: 16px 16px 22px;
  }

  .service-labels-list-item {
    position: relative;
    grid-template-columns: minmax(0, 1fr);
    align-content: center;
    gap: 3px;
    min-height: 53px;
    padding: 7px 36px 7px 0;
  }

  .service-labels-list-item dd {
    display: block;
  }

  .service-labels-list-item :deep(.app-copy-button) {
    position: absolute;
    inset: 50% 0 auto auto;
    translate: 0 -50%;
  }
}

@container service-details-page (max-width: 900px) {
  .service-realtime-link span {
    display: none;
  }

  .service-realtime-link {
    padding-inline: 8px;
  }

  .service-label-token span {
    display: none;
  }

  .service-dossier {
    gap: 16px;
  }

  .service-related {
    padding-inline-start: 16px;
  }
}

@container service-details-page (max-width: 680px) {
  .service-dossier {
    grid-template-columns: minmax(0, 1fr);
  }

  .service-related {
    border-inline-start: 0;
    padding-inline-start: 0;
  }
}

@container service-details-page (max-width: 490px) {
  .service-heading {
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: start;
  }

  .service-heading-icon {
    display: none;
  }

  .service-metadata-label {
    width: 94px;
  }

  .service-heading-actions {
    align-self: start;
  }

  .service-label-token {
    padding-inline: 3px;
  }
}
</style>
