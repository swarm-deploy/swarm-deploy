<script setup lang="ts">
import { enumLabel, t } from "../i18n";
import { computed, onUnmounted, ref, watch } from "vue";
import { useRoute } from "vue-router";

import { fetchNetwork } from "../api/cluster";
import { fetchSwarmServices } from "../api/services";
import type { NetworkInfo, SwarmService } from "../api/types";
import AppCopyButton from "../components/common/AppCopyButton.vue";
import AppPanel from "../components/common/AppPanel.vue";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import DeploymentIcon from "../components/overview/DeploymentIcon.vue";
import ServiceReplicasBadge from "../components/services/ServiceReplicasBadge.vue";
import { formatDate } from "../utils/format";

const VXLAN_ID_OPTION = "com.docker.network.driver.overlay.vxlanid_list";
const MTU_OPTION = "com.docker.network.driver.mtu";
const ENCRYPTED_OPTION = "encrypted";
const DIAGNOSTIC_OPTIONS = new Set([VXLAN_ID_OPTION, MTU_OPTION, ENCRYPTED_OPTION]);

const route = useRoute();
const networkID = computed(() => String(route.params.network ?? ""));
const network = ref<NetworkInfo | null>(null);
const services = ref<SwarmService[] | null>(null);
const loading = ref(false);
const loadError = ref("");
const servicesError = ref("");
let request = 0;

const connectedServices = computed(() => {
  const current = network.value;
  if (!current || !services.value) return [];

  return services.value.filter((service) => service.network_targets?.some((target) =>
    target === current.id || target === current.name,
  )).sort((left, right) => left.full_name.localeCompare(right.full_name));
});
const networkServices = computed(() => new Map(
  (network.value?.services ?? []).map((service) => [service.name, service]),
));
const isEncrypted = computed(() => Object.hasOwn(network.value?.options ?? {}, ENCRYPTED_OPTION));
const visibleOptions = computed(() => Object.entries(network.value?.options ?? {})
  .filter(([key]) => !DIAGNOSTIC_OPTIONS.has(key))
  .sort(([left], [right]) => left.localeCompare(right)));
const labels = computed(() => Object.entries(network.value?.labels ?? {})
  .sort(([left], [right]) => left.localeCompare(right)));
const auxiliaryAddresses = computed(() => (network.value?.ipam ?? []).flatMap((pool) =>
  Object.entries(pool.aux_addresses ?? {}).map(([name, address]) => ({ name, address })),
));
const hasDiagnostics = computed(() => Boolean(
  network.value?.options?.[VXLAN_ID_OPTION] ||
  network.value?.options?.[MTU_OPTION] ||
  network.value?.config_only ||
  network.value?.config_from ||
  labels.value.length ||
  visibleOptions.value.length ||
  auxiliaryAddresses.value.length ||
  network.value?.peers?.length,
));

function boolText(value: boolean | undefined): string {
  if (value === undefined) return t("common.notApplicable");
  return value ? t("common.yes") : t("common.no");
}

function serviceVIP(service: SwarmService): string {
  return networkServices.value.get(service.full_name)?.vip ??
    networkServices.value.get(service.name)?.vip ??
    t("common.notApplicable");
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

async function load(): Promise<void> {
  const current = ++request;
  const id = networkID.value;
  network.value = null;
  services.value = null;
  loadError.value = "";
  servicesError.value = "";
  if (!id) return;

  loading.value = true;
  const [networkResult, servicesResult] = await Promise.allSettled([
    fetchNetwork(id),
    fetchSwarmServices(),
  ]);
  if (current !== request) return;

  if (networkResult.status === "fulfilled") network.value = networkResult.value;
  else loadError.value = errorMessage(networkResult.reason, t("networks.loadDetailsFailed"));
  if (servicesResult.status === "fulfilled") services.value = servicesResult.value.services;
  else servicesError.value = errorMessage(servicesResult.reason, t("networks.loadServicesFailed"));
  loading.value = false;
}

watch(networkID, () => { void load(); }, { immediate: true });
onUnmounted(() => { request++; });
</script>

<template>
  <section class="services-page service-details-page network-details-page">
    <header v-if="network" class="network-heading">
      <DeploymentIcon name="network" class="network-heading-icon" />
      <div class="network-heading-content">
        <RouterLink :to="{ name: 'networks' }" class="service-inline-link">{{ t('common.networks') }}</RouterLink>
        <div class="network-heading-summary">
          <h2>{{ network.name }}</h2>
          <span
            v-if="network.managed"
            class="network-managed app-tooltip"
            tabindex="0"
            :aria-label="`${t('networks.managed')}: ${t('networks.managedHint')}`"
            :data-tooltip="t('networks.managedHint')"
          >{{ t('networks.managed') }} <span aria-hidden="true">ⓘ</span></span>
        </div>
        <div class="network-heading-metadata">
          <span>{{ network.driver || t('common.notApplicable') }}</span>
          <span>{{ enumLabel('networkScopes', network.scope, network.scope || t('common.notApplicable')) }}</span>
          <RouterLink
            v-if="network.stack_name"
            class="service-inline-link"
            :to="{ name: 'stack-details', params: { stack: network.stack_name } }"
          >{{ network.stack_name }}</RouterLink>
        </div>
      </div>
    </header>

    <AppTableEmpty v-if="loading" role="status" :message="t('networks.loadingDetails')" />
    <AppTableEmpty v-else-if="loadError" role="alert">
      {{ t('networks.loadDetailsError', { error: loadError }) }}
    </AppTableEmpty>
    <AppTableEmpty v-else-if="!network" :message="t('networks.noDetails')" />

    <template v-else>
      <div class="network-dossier">
        <AppPanel tag="section" class="network-section network-metadata" aria-labelledby="network-metadata-title">
          <template #header><h3 id="network-metadata-title">{{ t('common.network') }}</h3></template>
          <AppTable summary fixed :aria-label="t('networks.metadata')">
            <template #colgroup><colgroup><col class="network-metadata-label" /><col /></colgroup></template>
            <tr>
              <th scope="row">ID</th>
              <td><span class="network-copy-value"><code>{{ network.id }}</code><AppCopyButton :value="network.id" color="var(--text)" :label="t('networks.copyId')" /></span></td>
            </tr>
            <tr><th scope="row">{{ t('common.stack') }}</th><td><RouterLink v-if="network.stack_name" class="service-inline-link" :to="{ name: 'stack-details', params: { stack: network.stack_name } }">{{ network.stack_name }}</RouterLink><span v-else>{{ t('common.notApplicable') }}</span></td></tr>
            <tr><th scope="row">{{ t('common.driver') }}</th><td>{{ network.driver || t('common.notApplicable') }}</td></tr>
            <tr><th scope="row">{{ t('networks.scope') }}</th><td>{{ enumLabel('networkScopes', network.scope, network.scope || t('common.notApplicable')) }}</td></tr>
            <tr><th scope="row">{{ t('networks.encryption') }}</th><td>{{ isEncrypted ? t('networks.enabled') : t('networks.disabled') }}</td></tr>
            <tr><th scope="row">{{ t('networks.attachable') }}</th><td>{{ boolText(network.attachable) }}</td></tr>
            <tr><th scope="row">{{ t('networks.internal') }}</th><td>{{ boolText(network.internal) }}</td></tr>
            <tr><th scope="row">{{ t('networks.ingress') }}</th><td>{{ boolText(network.ingress) }}</td></tr>
            <tr><th scope="row">IPv4</th><td>{{ boolText(network.enable_ipv4) }}</td></tr>
            <tr><th scope="row">IPv6</th><td>{{ boolText(network.enable_ipv6) }}</td></tr>
            <tr v-if="network.created_at"><th scope="row">{{ t('common.created') }}</th><td><time :datetime="network.created_at">{{ formatDate(network.created_at) }}</time></td></tr>
          </AppTable>
        </AppPanel>

        <AppPanel tag="section" class="network-section network-addressing" header-class="network-addressing-header" aria-labelledby="network-addressing-title">
          <template #header>
            <h3 id="network-addressing-title">{{ t('networks.addressing') }}</h3>
            <span v-if="network.ipam_driver" class="meta">{{ network.ipam_driver }}</span>
          </template>
          <AppTableEmpty v-if="!network.ipam?.length" :message="t('networks.noAddressPools')" />
          <AppTable v-else fixed :aria-label="t('networks.addressing')">
            <template #head><tr><th scope="col">{{ t('networks.subnet') }}</th><th scope="col">{{ t('networks.gateway') }}</th><th scope="col">{{ t('networks.ipRange') }}</th></tr></template>
            <tr v-for="(pool, index) in network.ipam" :key="`${pool.subnet}-${index}`">
              <td><code>{{ pool.subnet || t('common.notApplicable') }}</code></td>
              <td><code>{{ pool.gateway || t('common.notApplicable') }}</code></td>
              <td><code>{{ pool.ip_range || t('common.notApplicable') }}</code></td>
            </tr>
          </AppTable>
        </AppPanel>
      </div>

      <AppPanel tag="section" class="network-section network-services" aria-labelledby="network-services-title">
        <template #header><h3 id="network-services-title">{{ t('networks.connectedServices') }}</h3></template>
        <AppTableEmpty v-if="servicesError" role="alert">{{ t('networks.loadServicesError', { error: servicesError }) }}</AppTableEmpty>
        <AppTableEmpty v-else-if="!connectedServices.length" :message="t('networks.noConnectedServices')" />
        <AppTable v-else class="network-services-table" min-width="700px" :aria-label="t('networks.connectedServices')">
          <template #head><tr><th scope="col">{{ t('common.service') }}</th><th scope="col">{{ t('common.stack') }}</th><th scope="col">{{ t('networks.discovery') }}</th><th scope="col">{{ t('networks.address') }}</th><th scope="col">{{ t('common.replicas') }}</th></tr></template>
          <tr v-for="service in connectedServices" :key="service.id">
            <td class="network-service-name"><RouterLink v-if="service.stack" class="service-inline-link" :to="{ name: 'service-details', params: { stack: service.stack, service: service.name } }">{{ service.name }}</RouterLink><strong v-else>{{ service.name }}</strong></td>
            <td class="network-service-stack"><RouterLink v-if="service.stack" class="service-inline-link" :to="{ name: 'stack-details', params: { stack: service.stack } }">{{ service.stack }}</RouterLink><span v-else>{{ t('common.notApplicable') }}</span></td>
            <td class="network-service-discovery">{{ service.endpoint_mode?.toUpperCase() || t('common.notApplicable') }}</td>
            <td class="network-service-address"><code>{{ serviceVIP(service) }}</code></td>
            <td class="network-service-replicas"><ServiceReplicasBadge :replicas="service.replicas" compact /></td>
          </tr>
        </AppTable>
      </AppPanel>

      <details class="network-diagnostics">
        <summary><span>{{ t('networks.configurationDiagnostics') }}</span><span aria-hidden="true">›</span></summary>
        <AppTableEmpty v-if="!hasDiagnostics" :message="t('networks.noDiagnostics')" />
        <div v-else class="network-diagnostics-grid">
          <section class="network-diagnostic-group" aria-labelledby="network-diagnostic-runtime-title">
            <h4 id="network-diagnostic-runtime-title">{{ t('networks.runtime') }}</h4>
            <dl class="network-diagnostic-list">
              <div v-if="network.options?.[VXLAN_ID_OPTION]"><dt>VXLAN ID</dt><dd><code>{{ network.options[VXLAN_ID_OPTION] }}</code></dd></div>
              <div v-if="network.options?.[MTU_OPTION]"><dt>MTU</dt><dd>{{ network.options[MTU_OPTION] }}</dd></div>
              <div><dt>{{ t('networks.configOnly') }}</dt><dd>{{ boolText(network.config_only) }}</dd></div>
              <div v-if="network.config_from"><dt>{{ t('networks.configFrom') }}</dt><dd>{{ network.config_from }}</dd></div>
              <div v-for="item in auxiliaryAddresses" :key="`${item.name}-${item.address}`"><dt>{{ t('networks.auxAddress', { name: item.name }) }}</dt><dd><code>{{ item.address }}</code></dd></div>
            </dl>
          </section>
          <section v-if="labels.length" class="network-diagnostic-group" aria-labelledby="network-diagnostic-labels-title">
            <h4 id="network-diagnostic-labels-title">{{ t('common.labels') }}</h4>
            <dl class="network-diagnostic-list"><div v-for="[key, value] in labels" :key="key"><dt>{{ key }}</dt><dd>{{ value }}</dd></div></dl>
          </section>
          <section v-if="visibleOptions.length" class="network-diagnostic-group" aria-labelledby="network-diagnostic-options-title">
            <h4 id="network-diagnostic-options-title">{{ t('networks.options') }}</h4>
            <dl class="network-diagnostic-list"><div v-for="[key, value] in visibleOptions" :key="key"><dt>{{ key }}</dt><dd>{{ value }}</dd></div></dl>
          </section>
          <section v-if="network.peers?.length" class="network-diagnostic-group" aria-labelledby="network-diagnostic-peers-title">
            <h4 id="network-diagnostic-peers-title">{{ t('networks.peers') }}</h4>
            <dl class="network-diagnostic-list"><div v-for="peer in network.peers" :key="`${peer.name}-${peer.ip}`"><dt>{{ peer.name }}</dt><dd><code>{{ peer.ip }}</code></dd></div></dl>
          </section>
        </div>
      </details>
    </template>
  </section>
</template>

<style scoped>
.network-details-page { container: service-details-page / inline-size; gap: 20px; }
.network-heading { display: grid; grid-template-columns: auto minmax(0, 1fr); align-items: center; gap: 12px; min-width: 0; }
.network-heading-icon { box-sizing: content-box; width: 24px; height: 24px; padding: 10px; border-radius: 8px; color: var(--muted); background: var(--surface-muted); }
.network-heading-content { min-width: 0; display: grid; gap: 2px; font-size: .84rem; }
.network-heading-summary { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 10px; }
.network-heading-summary h2 { margin: 0; font-size: clamp(1.3rem, 3vw, 1.85rem); overflow-wrap: anywhere; }
.network-heading-metadata { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 0; color: var(--muted); font-size: .75rem; }
.network-heading-metadata > * + *::before { content: ""; display: inline-block; width: 1px; height: 12px; margin: 0 10px; vertical-align: -2px; background: var(--border-subtle); }
.network-managed { display: inline-flex; align-items: center; gap: 3px; border-radius: 4px; color: var(--muted); cursor: help; font-size: .75rem; font-weight: 600; }
.network-managed::after { width: max-content; max-width: min(300px, calc(100vw - 32px)); white-space: normal; text-align: left; }
.network-managed:focus { outline: 2px solid var(--interactive-primary); outline-offset: 2px; }
.network-managed:focus::after { opacity: 1; transform: translate(-50%, 0); }
.network-dossier { display: grid; grid-template-columns: minmax(0, 3fr) minmax(0, 2fr); gap: 24px; }
.network-addressing { border-inline-start: 1px solid var(--border-subtle); padding-inline-start: 24px; }
.network-section { min-width: 0; border: 0; background: transparent; box-shadow: none; }
.network-section h3 { margin: 0 0 8px; color: var(--text); font-size: 1rem; }
.network-addressing :deep(.network-addressing-header) { display: flex; align-items: baseline; justify-content: space-between; gap: 8px; }
.network-section :deep(.app-table-wrap) { border-radius: 0; border: 0; border-block-start: 1px solid var(--border-subtle); background: transparent; }
.network-section :deep(.app-table th), .network-section :deep(.app-table td) { padding: 8px 6px; font-size: .8rem; overflow-wrap: anywhere; }
.network-section :deep(.app-table th:first-child), .network-section :deep(.app-table td:first-child) { padding-inline-start: 0; }
.network-section :deep(.app-table th:last-child), .network-section :deep(.app-table td:last-child) { padding-inline-end: 0; }
.network-section :deep(.app-table--summary tbody th), .network-section :deep(.app-table--summary tbody td) { padding-block: 8px; vertical-align: middle; font-size: .84rem; }
.network-section :deep(.app-table tr:last-child > *) { border-bottom: 1px solid var(--border-subtle); }
.network-metadata-label { width: 145px; }
.network-copy-value { display: flex; align-items: center; gap: 6px; min-width: 0; }
.network-copy-value code { min-width: 0; overflow-wrap: anywhere; }
.network-copy-value :deep(.app-copy-button) { margin-inline-start: auto; flex: none; }
.network-services { min-width: 0; }
.network-services-table :deep(.app-table th:nth-child(1)) { width: 25%; }
.network-services-table :deep(.app-table th:nth-child(2)) { width: 20%; }
.network-services-table :deep(.app-table th:nth-child(3)) { width: 15%; }
.network-services-table :deep(.app-table th:nth-child(4)) { width: 22%; }
.network-service-replicas { text-align: right; }
.network-service-replicas :deep(.app-status-badge) { font-size: .7rem; }
.network-diagnostics { min-width: 0; border-block: 1px solid var(--border-subtle); }
.network-diagnostics > summary { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 9px 0; color: var(--text); cursor: pointer; list-style: none; font-size: 1rem; font-weight: 650; }
.network-diagnostics > summary::-webkit-details-marker { display: none; }
.network-diagnostics > summary > span:last-child { color: var(--muted); font-size: 1.15rem; transition: transform .15s ease; }
.network-diagnostics[open] > summary > span:last-child { transform: rotate(90deg); }
.network-diagnostics-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); column-gap: 24px; padding: 2px 0 12px; }
.network-diagnostic-group { min-width: 0; }
.network-diagnostic-group h4 { margin: 10px 0 4px; color: var(--muted); font-size: .7rem; letter-spacing: .055em; text-transform: uppercase; }
.network-diagnostic-list { margin: 0; border-top: 1px solid var(--border-subtle); }
.network-diagnostic-list > div { display: grid; grid-template-columns: minmax(110px, 34%) minmax(0, 1fr); gap: 8px; padding: 7px 0; border-bottom: 1px solid var(--border-subtle); font-size: .76rem; overflow-wrap: anywhere; }
.network-diagnostic-list dt { color: var(--muted); }
.network-diagnostic-list dd { min-width: 0; margin: 0; }

@container service-details-page (max-width: 680px) {
  .network-details-page { gap: 16px; }
  .network-heading { grid-template-columns: minmax(0, 1fr); }
  .network-heading-icon { display: none; }
  .network-dossier { grid-template-columns: minmax(0, 1fr); gap: 18px; }
  .network-addressing { border-inline-start: 0; padding-inline-start: 0; }
  .network-metadata-label { width: clamp(94px, 35%, 126px); }
  .network-services-table :deep(.app-table) { display: block; width: 100%; min-width: 0 !important; table-layout: auto; }
  .network-services-table :deep(thead) { display: none; }
  .network-services-table :deep(tbody) { display: block; width: 100%; }
  .network-services-table :deep(tr) { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 4px 12px; padding: 10px 0; }
  .network-services-table :deep(td) { max-width: none; padding: 0; border: 0; overflow: visible; }
  .network-services-table :deep(tr + tr) { border-top: 1px solid var(--border-subtle); }
  .network-service-name { grid-column: 1; grid-row: 1; }
  .network-service-replicas { grid-column: 2; grid-row: 1; }
  .network-service-stack { grid-column: 1; grid-row: 2; color: var(--muted); font-size: .74rem; }
  .network-service-discovery { grid-column: 1; grid-row: 3; color: var(--muted); font-size: .74rem; }
  .network-service-address { grid-column: 2; grid-row: 2 / span 2; align-self: end; color: var(--muted); font-size: .74rem; text-align: right; }
  .network-diagnostics-grid { grid-template-columns: minmax(0, 1fr); }
}

@media (prefers-reduced-motion: reduce) {
  .network-diagnostics > summary > span:last-child { transition-duration: 0s; }
}
</style>
