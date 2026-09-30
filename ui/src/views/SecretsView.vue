<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import { fetchSecretManagers, fetchSecrets, syncSecretManager } from "../api/secrets";
import type { SecretInfo, SecretManagerInfo, SecretManagerSyncResponse } from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import SecretManagerCard from "../components/secrets/SecretManagerCard.vue";
import { useSecretDetailsStore } from "../stores/secretDetails";

const loading = ref(false);
const loadingError = ref("");
const secrets = ref<SecretInfo[]>([]);
const secretManagers = ref<SecretManagerInfo[]>([]);
const syncingManager = ref("");
const managerFeedback = ref<Record<string, { kind: "success" | "error"; message: string }>>({});
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

const cloudSecretManagers = computed(() =>
  secretManagers.value.filter((manager) => manager.kind === "cloud-secrets"),
);

const managedSecretCount = computed(() => secrets.value.filter((secret) => (
  Boolean(secret.external?.path || secret.external?.version_id)
)).length);

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

async function loadSecretManagers() {
  try {
    const response = await fetchSecretManagers();
    secretManagers.value = Array.isArray(response.secret_managers) ? response.secret_managers : [];
  } catch {
    // Secret Manager availability must not prevent the local secrets snapshot from rendering.
    secretManagers.value = [];
  }
}

onMounted(() => {
  void loadSecrets();
  void loadSecretManagers();
});

function managerKey(manager: SecretManagerInfo): string {
  return `${manager.stack}/${manager.service}`;
}

function syncSummary(result: SecretManagerSyncResponse): string {
  return `Sync complete: ${result.created} created, ${result.updated} updated, ${result.removed} removed, ${result.unchanged} unchanged`;
}

async function triggerManagerSync(manager: SecretManagerInfo) {
  const key = managerKey(manager);
  syncingManager.value = key;
  delete managerFeedback.value[key];

  try {
    const result = await syncSecretManager(manager.stack, manager.service);
    managerFeedback.value[key] = { kind: "success", message: syncSummary(result) };
    await loadSecretManagers();
    window.setTimeout(() => void loadSecrets(), 500);
  } catch (error) {
    managerFeedback.value[key] = {
      kind: "error",
      message: error instanceof Error ? error.message : "Failed to synchronize secrets",
    };
  } finally {
    syncingManager.value = "";
  }
}

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

    <div v-if="cloudSecretManagers.length > 0" class="secret-manager-list">
      <SecretManagerCard
        v-for="manager in cloudSecretManagers"
        :key="managerKey(manager)"
        :manager="manager"
        :managed-count="managedSecretCount"
        :syncing="syncingManager === managerKey(manager)"
        :sync-error="managerFeedback[managerKey(manager)]?.kind === 'error' ? managerFeedback[managerKey(manager)]?.message : ''"
        :feedback="managerFeedback[managerKey(manager)]?.kind === 'success' ? managerFeedback[managerKey(manager)]?.message : ''"
        @sync="triggerManagerSync(manager)"
      />
    </div>

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
