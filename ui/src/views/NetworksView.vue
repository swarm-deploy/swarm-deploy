<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import { fetchNetworks } from "../api/cluster";
import type { NetworkInfo } from "../api/types";
import AppMultiSelect from "../components/common/AppMultiSelect.vue";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import SidebarIcon from "../components/layout/SidebarIcon.vue";

const VXLAN_ID_OPTION = "com.docker.network.driver.overlay.vxlanid_list";

const loading = ref(false);
const loadingError = ref("");
const networks = ref<NetworkInfo[]>([]);
const searchQuery = ref("");
const selectedStacks = ref<string[]>([]);
const availableStacks = ref<string[]>([]);
let request = 0;

const normalizedQuery = computed(() => searchQuery.value.trim().toLowerCase());
const stackOptions = computed(() => {
  const stacks = new Set([...availableStacks.value, ...selectedStacks.value]);
  return [...stacks]
    .sort((left, right) => left.localeCompare(right))
    .map((stack) => ({ value: stack, label: stack }));
});
const selectedStackSummary = computed(() => {
  if (selectedStacks.value.length === 0) return "All stacks";
  if (selectedStacks.value.length <= 2) return selectedStacks.value.join(", ");
  return `${selectedStacks.value.length} stacks selected`;
});

const filteredNetworks = computed(() => {
  const query = normalizedQuery.value;
  if (!query) {
    return networks.value;
  }

  return networks.value.filter((network) => {
    const labels = Object.entries(network.labels ?? {})
      .map(([key, value]) => `${key}=${value}`)
      .join(" ");
    const options = Object.entries(network.options ?? {})
      .map(([key, value]) => `${key}=${value}`)
      .join(" ");
    const managed = boolText(network.managed);

    return `${network.name} ${network.stack_name ?? ""} ${network.scope} ${network.driver} ${network.id} ${managed} ${labels} ${options}`
      .toLowerCase()
      .includes(query);
  });
});

const hasOptionsColumn = computed(() =>
  networks.value.some((network) => Object.keys(network.options ?? {}).some((key) => key !== VXLAN_ID_OPTION)),
);

async function loadNetworks() {
  const current = ++request;
  loading.value = true;
  loadingError.value = "";

  try {
    const response = await fetchNetworks({ stacks: selectedStacks.value });
    if (current !== request) return;
    const nextNetworks = Array.isArray(response.networks) ? response.networks : [];
    networks.value = [...nextNetworks].sort((left, right) => left.name.localeCompare(right.name));
    if (selectedStacks.value.length === 0) {
      availableStacks.value = [...new Set(nextNetworks
        .map((network) => network.stack_name?.trim() ?? "")
        .filter(Boolean))];
    }
  } catch (error) {
    if (current === request) {
      loadingError.value = error instanceof Error ? error.message : "Failed to load networks";
      networks.value = [];
    }
  } finally {
    if (current === request) loading.value = false;
  }
}

function selectStacks(stacks: string[]): void {
  selectedStacks.value = stacks;
  void loadNetworks();
}

function boolText(value: boolean): string {
  return value ? "true" : "false";
}

function mapText(values?: Record<string, string>): string {
  const entries = Object.entries(values ?? {});
  if (entries.length === 0) {
    return "n/a";
  }

  return entries
    .sort(([leftKey], [rightKey]) => leftKey.localeCompare(rightKey))
    .map(([key, value]) => `${key}=${value}`)
    .join(", ");
}

function vxlanIDText(options?: Record<string, string>): string {
  return options?.[VXLAN_ID_OPTION] || "n/a";
}

function optionsText(options?: Record<string, string>): string {
  const entries = Object.entries(options ?? {}).filter(([key]) => key !== VXLAN_ID_OPTION);
  if (entries.length === 0) {
    return "n/a";
  }

  return entries
    .sort(([leftKey], [rightKey]) => leftKey.localeCompare(rightKey))
    .map(([key, value]) => `${key}=${value}`)
    .join(", ");
}

onMounted(() => {
  void loadNetworks();
});
</script>

<template>
  <section class="services-page networks-page">
    <header class="services-header">
      <h2>Networks</h2>
    </header>

    <div class="networks-filters" aria-label="Network filters">
      <label class="networks-search">
        <svg class="networks-filter-icon" viewBox="0 0 20 20" aria-hidden="true">
          <circle cx="8.5" cy="8.5" r="5.5" />
          <path d="m12.5 12.5 4 4" />
        </svg>
        <span class="sr-only">Search networks</span>
        <input
          v-model="searchQuery"
          type="search"
          placeholder="Search by name, stack, scope, driver, labels..."
        />
      </label>
      <div class="networks-filter-field">
        <AppMultiSelect
          class="networks-stack-filter"
          :model-value="selectedStacks"
          :options="stackOptions"
          label="Filter networks by stack"
          placeholder="All stacks"
          heading="Stacks"
          :selection-label="selectedStackSummary"
          searchable
          search-label="Search stacks"
          search-placeholder="Search stacks"
          empty-message="No matching stacks"
          @update:model-value="selectStacks"
        >
          <template #icon>
            <SidebarIcon class="networks-filter-icon" name="services" />
          </template>
        </AppMultiSelect>
      </div>
    </div>

    <AppTableEmpty v-if="loading && networks.length === 0" message="Loading..." />

    <AppTableEmpty v-else-if="loadingError">Failed to load networks: {{ loadingError }}</AppTableEmpty>

    <AppTableEmpty v-else-if="networks.length === 0" message="No networks found." />

    <AppTableEmpty v-else-if="filteredNetworks.length === 0" message="No networks match the current filters." />

    <AppTable v-else min-width="960px">
      <template #head>
          <tr>
            <th>Name</th>
            <th>Stack</th>
            <th>Driver</th>
            <th>VXLAN ID</th>
            <th>Attachable</th>
            <th>Internal</th>
            <th>Ingress</th>
            <th>Managed</th>
            <th>Labels</th>
            <th v-if="hasOptionsColumn">Options</th>
          </tr>
      </template>
          <tr v-for="network in filteredNetworks" :key="network.id">
            <td>{{ network.name || "n/a" }}</td>
            <td>{{ network.stack_name || "n/a" }}</td>
            <td>{{ network.driver || "n/a" }}</td>
            <td>{{ vxlanIDText(network.options) }}</td>
            <td>{{ boolText(network.attachable) }}</td>
            <td>{{ boolText(network.internal) }}</td>
            <td>{{ boolText(network.ingress) }}</td>
            <td>{{ boolText(network.managed) }}</td>
            <td>{{ mapText(network.labels) }}</td>
            <td v-if="hasOptionsColumn">{{ optionsText(network.options) }}</td>
          </tr>
    </AppTable>
  </section>
</template>

<style scoped>
.networks-page {
  gap: 16px;
  min-width: 0;
}

.networks-filters {
  min-width: 0;
  display: grid;
  grid-template-columns: minmax(240px, 1fr) 260px;
  gap: 10px;
  align-items: center;
  padding: 12px;
  border: 1px solid var(--border-subtle);
  border-radius: 10px;
  background: var(--surface-card-soft);
}

.networks-search {
  position: relative;
  min-width: 0;
}

.networks-filter-field {
  min-width: 0;
  color: var(--muted);
}

.networks-filter-icon {
  width: 17px;
  height: 17px;
  flex: 0 0 17px;
  color: var(--muted);
  fill: none;
  stroke: currentColor;
  stroke-width: 1.6;
  pointer-events: none;
}

.networks-search .networks-filter-icon {
  position: absolute;
  z-index: 1;
  top: 50%;
  left: 11px;
  transform: translateY(-50%);
}

.networks-search input {
  box-sizing: border-box;
  width: 100%;
  height: 38px;
  border: 1px solid var(--border-default);
  border-radius: 7px;
  background: var(--surface-input);
  color: var(--text);
  padding: 7px 10px 7px 36px;
  font: inherit;
  font-size: .83rem;
}

.networks-search input:focus-visible {
  outline: 2px solid var(--accent-focus);
  outline-offset: 2px;
}

@media (max-width: 700px) {
  .networks-page {
    gap: 12px;
  }

  .networks-filters {
    grid-template-columns: minmax(0, 1fr);
    padding: 10px;
  }
}

@media (max-width: 460px) {
  .networks-search input {
    font-size: 16px;
  }
}
</style>
