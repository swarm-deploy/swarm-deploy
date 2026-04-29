<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import { fetchNetworks } from "../api/cluster";
import type { NetworkInfo } from "../api/types";

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

    return `${network.name} ${network.scope} ${network.driver} ${network.id} ${labels} ${options}`
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
    </header>

    <section class="secrets-toolbar">
      <input
        v-model="searchQuery"
        type="search"
        class="secrets-search-input"
        placeholder="Search by name, scope, driver, labels..."
        aria-label="Search networks"
      />
    </section>

    <div v-if="loading && networks.length === 0" class="services-empty">
      <p class="meta">Loading...</p>
    </div>

    <div v-else-if="loadingError" class="services-empty">
      <p class="meta">Failed to load networks: {{ loadingError }}</p>
    </div>

    <div v-else-if="networks.length === 0" class="services-empty">
      <p class="meta">No networks found.</p>
    </div>

    <div v-else-if="filteredNetworks.length === 0" class="services-empty">
      <p class="meta">No networks match your search.</p>
    </div>

    <div v-else class="secrets-table-wrap">
      <table class="container-status-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Scope</th>
            <th>Driver</th>
            <th>Attachable</th>
            <th>Internal</th>
            <th>Ingress</th>
            <th>Labels</th>
            <th>Options</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="network in filteredNetworks" :key="network.id">
            <td>{{ network.name || "n/a" }}</td>
            <td>{{ network.scope || "n/a" }}</td>
            <td>{{ network.driver || "n/a" }}</td>
            <td>{{ boolText(network.attachable) }}</td>
            <td>{{ boolText(network.internal) }}</td>
            <td>{{ boolText(network.ingress) }}</td>
            <td>{{ mapText(network.labels) }}</td>
            <td>{{ mapText(network.options) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
