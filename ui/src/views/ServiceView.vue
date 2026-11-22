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

const repositoryIcons = new Map([
  ["github.com", "M12 2a10 10 0 0 0-3.16 19.49c.5.09.68-.22.68-.48v-1.88c-2.78.6-3.37-1.18-3.37-1.18-.45-1.16-1.11-1.47-1.11-1.47-.91-.62.07-.61.07-.61 1 .07 1.53 1.03 1.53 1.03.9 1.53 2.35 1.09 2.92.83.09-.65.35-1.09.64-1.34-2.22-.25-4.55-1.11-4.55-4.94 0-1.09.39-1.98 1.03-2.68-.1-.25-.45-1.27.1-2.64 0 0 .84-.27 2.75 1.02A9.58 9.58 0 0 1 12 6.52c.85 0 1.69.11 2.48.33 1.91-1.29 2.75-1.02 2.75-1.02.55 1.37.2 2.39.1 2.64.64.7 1.03 1.59 1.03 2.68 0 3.84-2.34 4.68-4.57 4.93.36.31.68.92.68 1.85v3.08c0 .27.18.58.69.48A10 10 0 0 0 12 2Z"],
  // GitLab and Bitbucket icon paths from Simple Icons.
  ["gitlab.com", "m23.6004 9.5927-.0337-.0862L20.3.9814a.851.851 0 0 0-.3362-.405.8748.8748 0 0 0-.9997.0539.8748.8748 0 0 0-.29.4399l-2.2055 6.748H7.5375l-2.2057-6.748a.8573.8573 0 0 0-.29-.4412.8748.8748 0 0 0-.9997-.0537.8585.8585 0 0 0-.3362.4049L.4332 9.5015l-.0325.0862a6.0657 6.0657 0 0 0 2.0119 7.0105l.0113.0087.03.0213 4.976 3.7264 2.462 1.8633 1.4995 1.1321a1.0085 1.0085 0 0 0 1.2197 0l1.4995-1.1321 2.4619-1.8633 5.006-3.7489.0125-.01a6.0682 6.0682 0 0 0 2.0094-7.003z"],
  ["bitbucket.org", "M.778 1.213a.768.768 0 00-.768.892l3.263 19.81c.084.5.515.868 1.022.873H19.95a.772.772 0 00.77-.646l3.27-20.03a.768.768 0 00-.768-.891zM14.52 15.53H9.522L8.17 8.466h7.561z"],
]);

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
const replicaCounts = computed(() => liveService.value?.replicas);
const replicaBadgeText = computed(() => {
  if (!replicaCounts.value) return swarmServices.value ? "Unavailable" : "Unknown";
  const { running, desired } = replicaCounts.value;
  return `${running} / ${desired} replicas`;
});
const replicaBadgeVariant = computed(() => {
  if (!replicaCounts.value) return swarmServices.value ? "error" : "neutral";
  const { running, desired } = replicaCounts.value;
  if (running === desired) return "success";
  if (desired > 0 && running === 0) return "error";
  return "warning";
});
const image = computed(() => serviceInfo.value?.image || spec.value?.image);
const repository = computed(() => {
  const href = serviceInfo.value?.repository_url;
  const value = href?.trim();
  if (!href || !value) return;

  try {
    const url = new URL(value.startsWith("//") ? `https:${value}` : value);
    return {
      href,
      label: url.pathname.replace(/^\/+/, "") || "/",
      iconPath: repositoryIcons.get(url.hostname.replace(/^www\./, "")),
    };
  } catch {
    return { href, label: value };
  }
});
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
    Custom: new Map<string, string>(),
    Docker: new Map(Object.entries(spec.value?.labels?.docker ?? {})),
    "Swarm Deploy": new Map(Object.entries(spec.value?.labels?.swarm_deploy ?? {})),
    Other: new Map<string, string>(),
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
const labelPreview = computed(() => {
  const group = labelGroups.value.find(({ name }) => name === "Custom") ?? labelGroups.value.at(-1);
  return group?.labels.at(-1);
});
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
        <RouterLink :to="stackRoute" class="service-inline-link service-stack-link">{{ stackName }}</RouterLink>
        <div class="service-heading-summary">
          <h2>{{ serviceName }}</h2>
          <AppStatusBadge
            v-if="!loading || replicaCounts"
            class="service-replica-badge"
            :class="{ 'service-replica-badge--desktop': !loading && serviceStatus }"
            :variant="replicaBadgeVariant"
            filled
          >
            {{ replicaBadgeText }}
          </AppStatusBadge>
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
          </div>
        </details>
      </div>
    </header>

    <AppTableEmpty v-if="loading" message="Loading service details..." />
    <AppTableEmpty v-else-if="!serviceStatus" role="alert">Failed to load service details: {{ loadingError }}</AppTableEmpty>

    <template v-else>
      <p v-if="loadingError || runtimeError" class="meta" role="status">{{ loadingError || runtimeError }}</p>
      <div class="service-dossier">
        <div class="service-primary">
          <AppPanel tag="section" class="service-section service-metadata" header-class="service-metadata-header" aria-labelledby="service-metadata-title">
            <template #header>
              <h3 id="service-metadata-title">Service</h3>
              <AppStatusBadge class="service-replica-badge service-replica-badge--mobile" :variant="replicaBadgeVariant" filled>
                {{ replicaBadgeText }}
              </AppStatusBadge>
            </template>
            <AppTable summary fixed aria-label="Service">
              <template #colgroup><colgroup><col class="service-metadata-label" /><col /></colgroup></template>
              <tr><th scope="row">Type</th><td>{{ serviceInfo?.type_title || serviceInfo?.type || "n/a" }}</td></tr>
              <tr><th scope="row">Description</th><td>{{ serviceInfo?.description || "n/a" }}</td></tr>
              <tr><th scope="row">Mode</th><td class="service-mode">{{ liveService?.mode || spec?.mode || "n/a" }}</td></tr>
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
              <tr v-if="repository">
                <th scope="row">Repository</th>
                <td>
                  <a :href="repository.href" :title="repository.href" class="service-inline-link service-repository-link" target="_blank" rel="noopener noreferrer">
                    <svg v-if="repository.iconPath" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                      <path :d="repository.iconPath" />
                    </svg>
                    <span>{{ repository.label }}</span>
                  </a>
                </td>
              </tr>
              <tr v-if="links.length">
                <th scope="row">Links</th>
                <td>
                  <div class="service-links">
                    <a
                      v-for="link in links"
                      :key="`${link.type}-${link.url}`"
                      :href="link.url"
                      :title="link.url"
                      class="service-link-badge"
                      target="_blank"
                      rel="noopener noreferrer"
                    >
                      <AppStatusBadge filled>{{ link.type }}</AppStatusBadge>
                    </a>
                  </div>
                </td>
              </tr>
              <tr v-if="webRoutes.length">
                <th scope="row">Web Routes</th>
                <td>
                  <ul class="service-web-routes">
                    <li v-for="item in webRoutes" :key="`${item.domain}-${item.address}-${item.port}`">
                      {{ item.domain }} ({{ item.address }}:{{ item.port }})
                    </li>
                  </ul>
                </td>
              </tr>
              <tr><th scope="row">Created</th><td><time :datetime="liveService?.created_at">{{ formatDate(liveService?.created_at) }}</time></td></tr>
              <tr>
                <th scope="row">Labels</th>
                <td>
                  <div v-if="labelPreview" class="service-labels-preview">
                    <code class="service-label-token" :title="labelPreview.join('=')">{{ previewKey(labelPreview[0]) }}<span>={{ labelPreview[1] }}</span></code>
                    <AppButton variant="secondary" compact class="service-labels-show" aria-haspopup="dialog" @click="labelsModalOpen = true">Show all</AppButton>
                  </div>
                  <span v-else class="meta">No labels.</span>
                </td>
              </tr>
            </AppTable>
          </AppPanel>

          <AppPanel v-if="spec" tag="section" class="service-section" aria-labelledby="service-resources-title">
            <template #header><h3 id="service-resources-title">Resources</h3></template>
            <AppTable summary fixed aria-label="Service resources">
              <tr><th scope="row">Requested / limited RAM</th><td>{{ spec.requested_ram_bytes ? formatBytes(spec.requested_ram_bytes) : 0 }} / {{ spec.limit_ram_bytes ? formatBytes(spec.limit_ram_bytes) : "∞" }}</td></tr>
              <tr><th scope="row">Requested / limited CPU</th><td>{{ spec.requested_cpu_nano ? formatNanoCPU(spec.requested_cpu_nano) : 0 }} / {{ spec.limit_cpu_nano ? formatNanoCPU(spec.limit_cpu_nano) : "∞" }}</td></tr>
            </AppTable>
          </AppPanel>
        </div>

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
    </template>

    <AppModal
      :open="labelsModalOpen"
      labelledby="service-labels-title"
      keep-mounted
      card-class="service-labels-modal-card"
      @close="labelsModalOpen = false"
    >
      <template #title>
        <h2 id="service-labels-title">Service labels</h2>
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

.service-replica-badge {
  white-space: nowrap;
}

.service-replica-badge--mobile {
  display: none;
}

.service-links {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.service-link-badge {
  display: inline-flex;
  max-width: 100%;
  border-radius: 999px;
  text-decoration: none;
}

.service-link-badge :deep(.app-status-badge) {
  min-width: 0;
  white-space: normal;
  overflow-wrap: anywhere;
}

.service-link-badge:hover :deep(.app-status-badge) {
  color: var(--accent-strong);
  border-color: var(--accent-border);
}

.service-link-badge:focus-visible {
  outline: 2px solid var(--accent-focus);
  outline-offset: 2px;
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
  min-width: 150px;
  padding: 4px;
  border: 1px solid var(--border-default);
  border-radius: 8px;
  background: var(--shell-chrome-bg);
}

.service-actions-items > button {
  display: flex;
  align-items: center;
  min-height: 32px;
  padding: 6px 8px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  box-shadow: none;
  color: var(--text);
  font: inherit;
  font-size: .8rem;
  text-align: left;
}

.service-actions-items > button:hover {
  background: var(--surface-hover);
  transform: none;
  filter: none;
}

.service-actions-items > button:focus-visible {
  outline: 2px solid var(--accent-focus);
  outline-offset: -2px;
}

.service-dossier {
  display: grid;
  grid-template-columns: minmax(0, 3fr) minmax(0, 2fr);
  gap: 24px;
}

.service-primary,
.service-related {
  min-width: 0;
  display: grid;
  align-content: start;
  gap: 18px;
}

.service-related {
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

.service-stack-link {
  color: var(--control-primary);
}

.service-inline-link:hover {
  text-decoration: underline;
}

.service-repository-link {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  max-width: 100%;
}

.service-repository-link svg {
  flex: none;
  width: .9rem;
  height: .9rem;
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

.service-image-copy-button {
  margin-inline-start: auto;
}

.service-labels-preview {
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-width: 0;
  gap: 4px;
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
.service-web-routes {
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
button.service-entity {
  border: 0;
  border-radius: 0;
  background: transparent;
  color: var(--text);
  box-shadow: none;
}

.service-entity:hover,
.service-deployment-open:hover {
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

.service-labels-modal-body {
  gap: 14px;
}

.service-labels-group-header {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  min-height: 25px;
  padding: 0 2px 5px;
  border-bottom: 1px solid var(--border-subtle);
  color: var(--muted);
  font-size: .6875rem;
  font-weight: 700;
  letter-spacing: .055em;
  text-transform: uppercase;
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

@media (max-width: 650px) {
  .service-replica-badge--desktop {
    display: none;
  }

  .service-replica-badge--mobile {
    display: inline-flex;
  }

  .service-metadata :deep(.service-metadata-header) {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    margin-block-end: 8px;
  }

  .service-metadata h3 {
    margin: 0;
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
