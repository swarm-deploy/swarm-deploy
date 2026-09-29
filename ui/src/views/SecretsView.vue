<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import { fetchSecretManagers, fetchSecrets, syncSecretManager } from "../api/secrets";
import type { SecretInfo, SecretManagerInfo, SecretManagerSyncResponse } from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import { useSecretDetailsStore } from "../stores/secretDetails";

const loading = ref(false);
const loadingError = ref("");
const secrets = ref<SecretInfo[]>([]);
const secretManagers = ref<SecretManagerInfo[]>([]);
const syncingManager = ref("");
const managerFeedback = ref<Record<string, string>>({});
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

function safeProviderLink(raw: string | undefined): string {
  if (!raw) {
    return "";
  }

  try {
    const parsed = new URL(raw);
    return parsed.protocol === "http:" || parsed.protocol === "https:" ? parsed.toString() : "";
  } catch {
    return "";
  }
}

function formatRelativeTime(raw: string | undefined): string {
  if (!raw) {
    return "Not synced since the last restart";
  }

  const timestamp = new Date(raw).valueOf();
  if (Number.isNaN(timestamp)) {
    return raw;
  }

  const elapsedMinutes = Math.max(0, Math.floor((Date.now() - timestamp) / 60000));
  if (elapsedMinutes < 1) {
    return "Last synced just now";
  }
  if (elapsedMinutes < 60) {
    return `Last synced ${elapsedMinutes} min ago`;
  }

  const elapsedHours = Math.floor(elapsedMinutes / 60);
  return `Last synced ${elapsedHours === 1 ? "1 hour" : `${elapsedHours} hours`} ago`;
}

function syncSummary(result: SecretManagerSyncResponse): string {
  return `Sync complete: ${result.created} created, ${result.updated} updated, ${result.removed} removed, ${result.unchanged} unchanged`;
}

async function triggerManagerSync(manager: SecretManagerInfo) {
  const key = managerKey(manager);
  syncingManager.value = key;
  managerFeedback.value[key] = "";

  try {
    const result = await syncSecretManager(manager.stack, manager.service);
    managerFeedback.value[key] = syncSummary(result);
    await loadSecretManagers();
    window.setTimeout(() => void loadSecrets(), 500);
  } catch (error) {
    managerFeedback.value[key] = error instanceof Error ? error.message : "Failed to synchronize secrets";
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
      <article
        v-for="manager in cloudSecretManagers"
        :key="managerKey(manager)"
        class="secret-manager-card"
        :class="{ 'secret-manager-card--unavailable': !manager.available }"
      >
        <div class="secret-manager-copy">
          <div class="secret-manager-heading">
            <span class="secret-manager-status" :class="{ available: manager.available }" aria-hidden="true"></span>
            <strong v-if="manager.available && manager.provider?.name">
              Some secrets are managed by
              <a
                v-if="safeProviderLink(manager.provider.link)"
                :href="safeProviderLink(manager.provider.link)"
                target="_blank"
                rel="noopener noreferrer"
              >{{ manager.provider.name }}</a>
              <span v-else>{{ manager.provider.name }}</span>
            </strong>
            <strong v-else-if="manager.available">cloud-secrets Secret Manager</strong>
            <strong v-else>cloud-secrets Secret Manager is unavailable</strong>
            <span v-if="manager.version" class="secret-manager-version">{{ manager.version }}</span>
          </div>
          <span class="secret-manager-meta">
            {{ manager.available ? formatRelativeTime(manager.last_sync_at) : (manager.error || "Controller is unavailable") }}
          </span>
          <span v-if="managerFeedback[managerKey(manager)]" class="secret-manager-feedback">
            {{ managerFeedback[managerKey(manager)] }}
          </span>
        </div>
        <button
          type="button"
          class="secret-manager-sync"
          :disabled="!manager.controllable || !manager.available || syncingManager === managerKey(manager)"
          @click="triggerManagerSync(manager)"
        >
          {{ syncingManager === managerKey(manager) ? "Syncing..." : "Sync" }}
        </button>
      </article>
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
