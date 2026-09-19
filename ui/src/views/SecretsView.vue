<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import { fetchSecrets } from "../api/secrets";
import type { SecretInfo } from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
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
      <div class="services-header-actions">
        <input
          v-model="searchQuery"
          type="search"
          class="secrets-search-input"
          placeholder="Search by name, version, external path..."
          aria-label="Search secrets"
        />
      </div>
    </header>

    <AppTableEmpty v-if="loading && secrets.length === 0" message="Loading..." />

    <AppTableEmpty v-else-if="loadingError">Failed to load secrets: {{ loadingError }}</AppTableEmpty>

    <AppTableEmpty v-else-if="secrets.length === 0" message="No secrets found." />

    <AppTableEmpty v-else-if="filteredSecrets.length === 0" message="No secrets match your search." />

    <AppTable v-else fixed min-width="720px">
      <template #head>
          <tr>
            <th>Name</th>
            <th>Date Added</th>
            <th>External Path</th>
            <th>External Version ID</th>
          </tr>
      </template>
          <tr
            v-for="secret in filteredSecrets"
            :key="secret.id"
            class="app-table-row--clickable"
            tabindex="0"
            role="button"
            :aria-label="`Open details for ${secret.name}`"
            @click="openSecretDetails(secret.name)"
            @keydown.enter="openSecretDetails(secret.name)"
            @keydown.space.prevent="openSecretDetails(secret.name)"
          >
            <td>{{ secret.name || "n/a" }}</td>
            <td>{{ formatDate(secret.created_at) }}</td>
            <td>{{ secret.external?.path || "n/a" }}</td>
            <td>{{ secret.external?.version_id || "n/a" }}</td>
          </tr>
    </AppTable>
  </section>
</template>
