<script setup lang="ts">
import { enumLabel, t } from "../i18n";
import { computed, onUnmounted, ref, watch } from "vue";
import { useRoute } from "vue-router";

import { fetchNetwork } from "../api/cluster";
import { fetchServices } from "../api/services";
import type { NetworkInfo, ServiceInfo } from "../api/types";
import AppCopyButton from "../components/common/AppCopyButton.vue";
import AppPanel from "../components/common/AppPanel.vue";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import DeploymentIcon from "../components/overview/DeploymentIcon.vue";
import { usePageStore } from "../stores/page";
import { formatDate } from "../utils/format";

const VXLAN_ID_OPTION = "com.docker.network.driver.overlay.vxlanid_list";
const ENCRYPTED_OPTION = "encrypted";

const route = useRoute();
const pageStore = usePageStore();
const networkID = computed(() => String(route.params.network ?? ""));
const network = ref<NetworkInfo | null>(null);
const services = ref<ServiceInfo[] | null>(null);
const loading = ref(false);
const loadError = ref("");
const servicesError = ref("");
let request = 0;

const connectedServices = computed(() => {
  const current = network.value;
  if (!current) return [];

  const catalogServices = services.value ?? [];
  const serviceByFullName = new Map(catalogServices.map((service) => [
    service.stack ? `${service.stack}_${service.name}` : service.name,
    service,
  ]));
  const rows: Array<{
    key: string;
    name: string;
    vip?: string;
    service?: ServiceInfo;
  }> = [];

  for (const endpoint of current.services ?? []) {
    const service = serviceByFullName.get(endpoint.name);
    rows.push({
      key: endpoint.name,
      name: service?.name ?? endpoint.name,
      vip: endpoint.vip,
      service,
    });
  }

  return rows.sort((left, right) => left.key.localeCompare(right.key));
});
const isEncrypted = computed(() => Object.hasOwn(network.value?.options ?? {}, ENCRYPTED_OPTION));

function boolText(value: boolean | undefined): string {
  if (value === undefined) return t("common.notApplicable");
  return value ? t("common.yes") : t("common.no");
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

async function load(): Promise<void> {
  const current = ++request;
  const id = networkID.value;
  network.value = null;
  pageStore.setTitle("");
  services.value = null;
  loadError.value = "";
  servicesError.value = "";
  if (!id) return;

  loading.value = true;
  const [networkResult, servicesResult] = await Promise.allSettled([
    fetchNetwork(id),
    fetchServices(),
  ]);
  if (current !== request) return;

  if (networkResult.status === "fulfilled") {
    network.value = networkResult.value;
    pageStore.setTitle(networkResult.value.name);
  }
  else loadError.value = errorMessage(networkResult.reason, t("networks.loadDetailsFailed"));
  if (servicesResult.status === "fulfilled") services.value = servicesResult.value.services;
  else servicesError.value = errorMessage(servicesResult.reason, t("networks.loadCatalogFailed"));
  loading.value = false;
}

watch(networkID, () => { void load(); }, { immediate: true });
onUnmounted(() => {
  request++;
  pageStore.setTitle("");
});
</script>

<template>
  <section class="services-page service-details-page network-details-page">
    <header v-if="network" class="network-heading">
      <DeploymentIcon name="network" class="network-heading-icon" />
      <div class="network-heading-content">
        <div class="network-heading-summary">
          <h2>{{ network.name }}</h2>
        </div>
        <div class="network-heading-metadata">
          <span>{{ network.driver || t('common.notApplicable') }}</span>
          <span>{{ enumLabel('networkScopes', network.scope, network.scope || t('common.notApplicable')) }}</span>
          <span
            v-if="network.managed"
            class="network-managed app-tooltip network-managed-tooltip"
            tabindex="0"
            :aria-label="`${t('networks.managed')}: ${t('networks.managedHint')}`"
            :data-tooltip="t('networks.managedHint')"
          >{{ t('networks.managed') }} <span aria-hidden="true">ⓘ</span></span>
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
            <tr v-if="network.options?.[VXLAN_ID_OPTION]"><th scope="row">VXLAN ID</th><td><code>{{ network.options[VXLAN_ID_OPTION] }}</code></td></tr>
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
            <span v-if="network.ipam_driver && network.ipam_driver !== 'default'" class="meta">{{ network.ipam_driver }}</span>
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
        <p v-if="servicesError" class="meta" role="alert">{{ t('networks.loadCatalogError', { error: servicesError }) }}</p>
        <AppTableEmpty v-if="!connectedServices.length" :message="t('networks.noConnectedServices')" />
        <AppTable v-else class="network-services-table" min-width="520px" :aria-label="t('networks.connectedServices')">
          <template #head><tr><th scope="col">{{ t('common.service') }}</th><th scope="col">{{ t('common.stack') }}</th><th scope="col">{{ t('networks.address') }}</th></tr></template>
          <tr v-for="item in connectedServices" :key="item.key">
            <td class="network-service-name"><RouterLink v-if="item.service?.stack" class="service-inline-link" :to="{ name: 'service-details', params: { stack: item.service.stack, service: item.service.name } }">{{ item.name }}</RouterLink><span v-else>{{ item.name }}</span></td>
            <td class="network-service-stack"><RouterLink v-if="item.service?.stack" class="service-inline-link" :to="{ name: 'stack-details', params: { stack: item.service.stack } }">{{ item.service.stack }}</RouterLink><span v-else>{{ t('common.notApplicable') }}</span></td>
            <td class="network-service-address"><code>{{ item.vip || t('common.notApplicable') }}</code></td>
          </tr>
        </AppTable>
      </AppPanel>
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
.network-services-table :deep(.app-table th:nth-child(1)) { width: 40%; }
.network-services-table :deep(.app-table th:nth-child(2)) { width: 25%; }

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
  .network-services-table :deep(.app-table td) { max-width: none; padding: 0; border: 0; overflow: visible; }
  .network-services-table :deep(tr + tr) { border-top: 1px solid var(--border-subtle); }
  .network-services-table :deep(.app-table tr:last-child) { border-bottom: 1px solid var(--border-subtle); }
  .network-services-table :deep(.app-table tr:last-child > *) { border-bottom: 0; }
  .network-service-name { grid-column: 1; grid-row: 1; }
  .network-service-stack { grid-column: 1; grid-row: 2; color: var(--muted); font-size: .74rem; }
  .network-service-address { grid-column: 2; grid-row: 1 / span 2; align-self: end; color: var(--muted); font-size: .74rem; text-align: right; }
}
</style>
