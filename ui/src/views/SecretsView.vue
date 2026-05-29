<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import { fetchSecrets } from "../api/secrets";
import type { SecretInfo } from "../api/types";
import { useSecretDetailsStore } from "../stores/secretDetails";

const loading = ref(false);
const loadingError = ref("");
const secrets = ref<SecretInfo[]>([]);
const searchQuery = ref("");
const secretDetailsStore = useSecretDetailsStore();

const normalizedQuery = computed(() => searchQuery.value.trim().toLowerCase());

const filteredSecrets = computed(() => {
  const query = normalizedQuery.value;
  if (!query) {
    return secrets.value;
  }

  return secrets.value.filter((secret) => {
    const name = secret.name ?? "";
    const versionID = `${secret.version_id ?? ""}`;
    const createdAt = secret.created_at ?? "";
    const externalPath = secret.external?.path ?? "";
    const externalVersionID = secret.external?.version_id ?? "";

    return `${name} ${versionID} ${createdAt} ${externalPath} ${externalVersionID}`.toLowerCase().includes(query);
  });
});

function sortSecrets(items: SecretInfo[]): SecretInfo[] {
  return [...items].sort((left, right) => {
    const byName = left.name.localeCompare(right.name);
    if (byName !== 0) {
      return byName;
    }

    return left.version_id - right.version_id;
  });
}

async function loadSecrets() {
  loading.value = true;
  loadingError.value = "";

  try {
    const response = await fetchSecrets();
    const nextSecrets = Array.isArray(response.secrets) ? response.secrets : [];
    secrets.value = sortSecrets(nextSecrets);
  } catch (error) {
    loadingError.value = error instanceof Error ? error.message : "Failed to load secrets";
    secrets.value = [];
  } finally {
    loading.value = false;
  }
}

onMounted(() => {
  void loadSecrets();
});

async function openSecretDetails(secretName: string) {
  await secretDetailsStore.openSecretDetails(secretName);
}

function formatDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value || "n/a";
  }

  return date.toLocaleString();
}
</script>

<template>
  <section class="services-page">
    <header class="services-header">
      <h2>Secrets</h2>
    </header>

    <section class="secrets-toolbar">
      <input
        v-model="searchQuery"
        type="search"
        class="secrets-search-input"
        placeholder="Search by name, version, external path..."
        aria-label="Search secrets"
      />
    </section>

    <div v-if="loading && secrets.length === 0" class="services-empty">
      <p class="meta">Loading...</p>
    </div>

    <div v-else-if="loadingError" class="services-empty">
      <p class="meta">Failed to load secrets: {{ loadingError }}</p>
    </div>

    <div v-else-if="secrets.length === 0" class="services-empty">
      <p class="meta">No secrets found.</p>
    </div>

    <div v-else-if="filteredSecrets.length === 0" class="services-empty">
      <p class="meta">No secrets match your search.</p>
    </div>

    <div v-else class="secrets-table-wrap">
      <table class="container-status-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Version ID</th>
            <th>Date Added</th>
            <th>External Path</th>
            <th>External Version ID</th>
            <th />
          </tr>
        </thead>
        <tbody>
          <tr v-for="secret in filteredSecrets" :key="secret.id">
            <td>{{ secret.name || "n/a" }}</td>
            <td>
              <code>{{ secret.version_id }}</code>
            </td>
            <td>{{ formatDate(secret.created_at) }}</td>
            <td>{{ secret.external?.path || "n/a" }}</td>
            <td>{{ secret.external?.version_id || "n/a" }}</td>
            <td>
              <button
                type="button"
                class="service-status-btn"
                :disabled="!secret.name"
                @click="secret.name && openSecretDetails(secret.name)"
              >
                Details
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
