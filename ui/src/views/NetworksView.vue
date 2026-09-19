<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import { fetchNetworks } from "../api/cluster";
import type { NetworkInfo } from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";

const loading = ref(false);
const loadingError = ref("");
const networks = ref<NetworkInfo[]>([]);
const searchQuery = ref("");

const normalizedQuery = computed(() => searchQuery.value.trim().toLowerCase());

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

async function loadNetworks() {
  loading.value = true;
  loadingError.value = "";

  try {
    const response = await fetchNetworks();
    const nextNetworks = Array.isArray(response.networks) ? response.networks : [];
    networks.value = [...nextNetworks].sort((left, right) => left.name.localeCompare(right.name));
  } catch (error) {
    loadingError.value = error instanceof Error ? error.message : "Failed to load networks";
    networks.value = [];
  } finally {
    loading.value = false;
  }
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

onMounted(() => {
  void loadNetworks();
});
</script>

<template>
  <section class="services-page">
    <header class="services-header">
      <h2>Networks</h2>
      <div class="services-header-actions">
        <input
          v-model="searchQuery"
          type="search"
          class="secrets-search-input"
          placeholder="Search by name, stack, scope, driver, labels..."
          aria-label="Search networks"
        />
      </div>
    </header>

    <AppTableEmpty v-if="loading && networks.length === 0" message="Loading..." />

    <AppTableEmpty v-else-if="loadingError">Failed to load networks: {{ loadingError }}</AppTableEmpty>

    <AppTableEmpty v-else-if="networks.length === 0" message="No networks found." />

    <AppTableEmpty v-else-if="filteredNetworks.length === 0" message="No networks match your search." />

    <AppTable v-else min-width="960px">
      <template #head>
          <tr>
            <th>Name</th>
            <th>Stack</th>
            <th>Driver</th>
            <th>Attachable</th>
            <th>Internal</th>
            <th>Ingress</th>
            <th>Managed</th>
            <th>Labels</th>
            <th>Options</th>
          </tr>
      </template>
          <tr v-for="network in filteredNetworks" :key="network.id">
            <td>{{ network.name || "n/a" }}</td>
            <td>{{ network.stack_name || "n/a" }}</td>
            <td>{{ network.driver || "n/a" }}</td>
            <td>{{ boolText(network.attachable) }}</td>
            <td>{{ boolText(network.internal) }}</td>
            <td>{{ boolText(network.ingress) }}</td>
            <td>{{ boolText(network.managed) }}</td>
            <td>{{ mapText(network.labels) }}</td>
            <td>{{ mapText(network.options) }}</td>
          </tr>
    </AppTable>
  </section>
</template>
