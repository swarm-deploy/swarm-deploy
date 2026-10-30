<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import { fetchSecretManagers, fetchSecrets, syncSecretManager } from "../api/secrets";
import type { SecretInfo, SecretManagerInfo, SecretManagerSyncResponse } from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import CreateSecretModal from "../components/secrets/CreateSecretModal.vue";
import SecretManagerCard from "../components/secrets/SecretManagerCard.vue";
import { useSecretDetailsStore } from "../stores/secretDetails";

const loading = ref(false);
const loadingError = ref("");
const secrets = ref<SecretInfo[]>([]);
const secretManagers = ref<SecretManagerInfo[]>([]);
const syncingManager = ref("");
const managerFeedback = ref<Record<string, { kind: "success" | "error"; message: string }>>({});
const searchQuery = ref("");
const createModalOpen = ref(false);
const createFeedback = ref("");
const secretDetailsStore = useSecretDetailsStore();

type SortKey = "name" | "description" | "createdAt" | "externalPath" | "externalVersionID";
type SortDirection = "ascending" | "descending";

const sortKey = ref<SortKey>("name");
const sortDirection = ref<SortDirection>("ascending");
const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });

const normalizedQuery = computed(() => searchQuery.value.trim().toLowerCase());

const showDescriptionColumn = computed(() =>
  secrets.value.some((secret) => Boolean(secret.description?.trim())),
);

const filteredSecrets = computed(() => {
  const query = normalizedQuery.value;
  if (!query) {
    return secrets.value;
  }

  return secrets.value.filter((secret) => {
    const name = secret.name ?? "";
    const description = secret.description ?? "";
    const versionID = `${secret.version_id ?? ""}`;
    const createdAt = secret.created_at ?? "";
    const externalPath = secret.external?.path ?? "";
    const externalVersionID = secret.external?.version_id ?? "";
    const usedBy = (secret.used_by ?? [])
      .map((usage) => `${usage.stack} ${usage.service} ${usage.target ?? ""}`)
      .join(" ");

    return `${name} ${description} ${versionID} ${createdAt} ${externalPath} ${externalVersionID} ${usedBy}`
      .toLowerCase()
      .includes(query);
  });
});

const sortedSecrets = computed(() => [...filteredSecrets.value].sort(compareSecrets));

const cloudSecretManagers = computed(() =>
  secretManagers.value.filter((manager) => manager.kind === "cloud-secrets"),
);

const managedSecretCount = computed(() => secrets.value.filter((secret) => (
  Boolean(secret.external?.path || secret.external?.version_id)
)).length);

function sortableValue(secret: SecretInfo, key: SortKey): string | number | null {
  switch (key) {
    case "name":
      return secret.name.trim() || null;
    case "description":
      return secret.description?.trim() || null;
    case "createdAt": {
      const timestamp = Date.parse(secret.created_at);
      return Number.isNaN(timestamp) ? null : timestamp;
    }
    case "externalPath":
      return secret.external?.path?.trim() || null;
    case "externalVersionID":
      return secret.external?.version_id?.trim() || null;
  }
}

function compareNullable(left: string | number | null, right: string | number | null): number {
  if (left === null) return right === null ? 0 : 1;
  if (right === null) return -1;
  if (typeof left === "number" && typeof right === "number") return left - right;
  return collator.compare(String(left), String(right));
}

function compareSecrets(left: SecretInfo, right: SecretInfo): number {
  const leftValue = sortableValue(left, sortKey.value);
  const rightValue = sortableValue(right, sortKey.value);
  const comparison = compareNullable(leftValue, rightValue);
  if (comparison !== 0) {
    if (leftValue === null || rightValue === null) return comparison;
    return sortDirection.value === "ascending" ? comparison : -comparison;
  }

  return collator.compare(left.name, right.name) || left.version_id - right.version_id || collator.compare(left.id, right.id);
}

function changeSort(key: SortKey): void {
  if (sortKey.value === key) {
    sortDirection.value = sortDirection.value === "ascending" ? "descending" : "ascending";
    return;
  }

  sortKey.value = key;
  sortDirection.value = "ascending";
}

function ariaSort(key: SortKey): SortDirection | "none" {
  return sortKey.value === key ? sortDirection.value : "none";
}

function sortIndicator(key: SortKey): string {
  if (sortKey.value !== key) return "↕";
  return sortDirection.value === "ascending" ? "↑" : "↓";
}

async function loadSecrets() {
  loading.value = true;
  loadingError.value = "";

  try {
    const response = await fetchSecrets();
    secrets.value = Array.isArray(response.secrets) ? response.secrets : [];
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

async function handleSecretCreated(message?: string) {
  createModalOpen.value = false;
  createFeedback.value = message || "";
  await Promise.all([loadSecrets(), loadSecretManagers()]);
  if (message) {
    window.setTimeout(() => {
      createFeedback.value = "";
    }, 5000);
  }
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

    <p v-if="createFeedback" class="secrets-create-feedback" role="status">{{ createFeedback }}</p>

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

    <div class="services-header-actions secrets-toolbar-actions">
      <input
        v-model="searchQuery"
        type="search"
        class="secrets-search-input"
        placeholder="Search by name, service, description, version, external path..."
        aria-label="Search secrets"
      />
      <button type="button" class="secrets-create-button" @click="createModalOpen = true">Create Secret</button>
    </div>

    <AppTableEmpty v-if="loading && secrets.length === 0" message="Loading..." />

    <AppTableEmpty v-else-if="loadingError">Failed to load secrets: {{ loadingError }}</AppTableEmpty>

    <AppTableEmpty v-else-if="secrets.length === 0" message="No secrets found." />

    <AppTableEmpty v-else-if="filteredSecrets.length === 0" message="No secrets match your search." />

    <AppTable v-else class="secrets-table" fixed :min-width="showDescriptionColumn ? '900px' : '720px'">
      <template #head>
          <tr>
            <th :aria-sort="ariaSort('name')">
              <button class="app-table-sort" type="button" @click="changeSort('name')">
                Name <span class="app-table-sort-indicator" aria-hidden="true">{{ sortIndicator('name') }}</span>
              </button>
            </th>
            <th v-if="showDescriptionColumn" :aria-sort="ariaSort('description')">
              <button class="app-table-sort" type="button" @click="changeSort('description')">
                Description <span class="app-table-sort-indicator" aria-hidden="true">{{ sortIndicator('description') }}</span>
              </button>
            </th>
            <th :aria-sort="ariaSort('createdAt')">
              <button class="app-table-sort" type="button" @click="changeSort('createdAt')">
                Date Added <span class="app-table-sort-indicator" aria-hidden="true">{{ sortIndicator('createdAt') }}</span>
              </button>
            </th>
            <th :aria-sort="ariaSort('externalPath')">
              <button class="app-table-sort" type="button" @click="changeSort('externalPath')">
                External Path <span class="app-table-sort-indicator" aria-hidden="true">{{ sortIndicator('externalPath') }}</span>
              </button>
            </th>
            <th :aria-sort="ariaSort('externalVersionID')">
              <button class="app-table-sort" type="button" @click="changeSort('externalVersionID')">
                External Version ID <span class="app-table-sort-indicator" aria-hidden="true">{{ sortIndicator('externalVersionID') }}</span>
              </button>
            </th>
          </tr>
      </template>
          <tr
            v-for="secret in sortedSecrets"
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
            <td v-if="showDescriptionColumn">{{ secret.description || "n/a" }}</td>
            <td>{{ formatDate(secret.created_at) }}</td>
            <td>{{ secret.external?.path || "n/a" }}</td>
            <td>{{ secret.external?.version_id || "n/a" }}</td>
          </tr>
    </AppTable>

    <CreateSecretModal
      :open="createModalOpen"
      :managers="secretManagers"
      @close="createModalOpen = false"
      @created="handleSecretCreated"
    />
  </section>
</template>

<style scoped>
.secrets-toolbar-actions { width: 100%; max-width: none; align-items: center; justify-content: space-between; gap: 10px; }
.secrets-toolbar-actions .secrets-search-input { flex: 1 1 360px; max-width: 520px; min-width: 0; }
.secrets-create-button { flex: 0 0 auto; border-color: var(--accent-border); border-radius: 6px; background: var(--control-primary); padding: 8px 13px; font-weight: 600; box-shadow: none; }
.secrets-create-button:hover { background: var(--control-primary-hover); filter: none; }
.secrets-create-button:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: 2px; box-shadow: none; }
.secrets-create-feedback { margin: 0; border: 1px solid var(--success-border); border-radius: 8px; background: var(--success-muted); color: var(--success); padding: 8px 10px; font-size: .82rem; }
.secrets-table :deep(td) { overflow-wrap: anywhere; }
</style>
