<script setup lang="ts">
import { formatDate } from "../utils/format";
import { formatNumber, locale, t } from "../i18n";
import { computed, onMounted, ref, watch } from "vue";

import { fetchSecretManagers, fetchSecrets, syncSecretManager } from "../api/secrets";
import type { SecretInfo, SecretManagerInfo, SecretManagerSyncResponse } from "../api/types";
import AppButton from "../components/common/AppButton.vue";
import AppSelect from "../components/common/AppSelect.vue";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import CreateSecretModal from "../components/secrets/CreateSecretModal.vue";
import SecretManagerCard from "../components/secrets/SecretManagerCard.vue";
import { useSecretDetailsStore } from "../stores/secretDetails";

const props = withDefaults(defineProps<{ initialSearch?: string }>(), { initialSearch: "" });

const loading = ref(false);
const loadingError = ref("");
const secrets = ref<SecretInfo[]>([]);
const secretManagers = ref<SecretManagerInfo[]>([]);
const syncingManager = ref("");
const managerFeedback = ref<Record<string, { kind: "success" | "error"; message: string }>>({});
const searchQuery = ref(props.initialSearch);
const createModalOpen = ref(false);
const createFeedback = ref("");
const secretDetailsStore = useSecretDetailsStore();

type SortKey = "name" | "description" | "createdAt" | "externalPath" | "externalVersionID";
type SortDirection = "ascending" | "descending";
type MobileSortValue = `${SortKey}:${SortDirection}`;

const sortKey = ref<SortKey>("name");
const sortDirection = ref<SortDirection>("ascending");
const collator = new Intl.Collator(locale(), { numeric: true, sensitivity: "base" });

watch(() => props.initialSearch, (value) => {
  searchQuery.value = value;
});

const mobileSortValue = computed<MobileSortValue>({
  get: (): MobileSortValue => `${sortKey.value}:${sortDirection.value}` as MobileSortValue,
  set: (value: MobileSortValue) => {
    const [key, direction] = value.split(":") as [SortKey, SortDirection];
    sortKey.value = key;
    sortDirection.value = direction;
  },
});

const mobileSortOptions = computed<Array<{ value: MobileSortValue; label: string }>>(() => [
  { value: "name:ascending", label: `${t("common.name")} ↑` },
  { value: "name:descending", label: `${t("common.name")} ↓` },
  { value: "description:ascending", label: `${t("common.description")} ↑` },
  { value: "description:descending", label: `${t("common.description")} ↓` },
  { value: "createdAt:ascending", label: `${t("secrets.dateAdded")} ↑` },
  { value: "createdAt:descending", label: `${t("secrets.dateAdded")} ↓` },
  { value: "externalPath:ascending", label: `${t("secrets.externalPath")} ↑` },
  { value: "externalPath:descending", label: `${t("secrets.externalPath")} ↓` },
  { value: "externalVersionID:ascending", label: `${t("secrets.externalVersionId")} ↑` },
  { value: "externalVersionID:descending", label: `${t("secrets.externalVersionId")} ↓` },
]);

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

function formatMobileDate(raw: string): string {
  return formatDate(raw, { day: "numeric", month: "short" });
}

async function loadSecrets() {
  loading.value = true;
  loadingError.value = "";

  try {
    const response = await fetchSecrets();
    secrets.value = Array.isArray(response.secrets) ? response.secrets : [];
  } catch (error) {
    loadingError.value = error instanceof Error ? error.message : t("secrets.loadFailed");
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
  return t("secrets.syncComplete", {
    created: formatNumber(result.created),
    updated: formatNumber(result.updated),
    removed: formatNumber(result.removed),
    unchanged: formatNumber(result.unchanged),
  });
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
      message: error instanceof Error ? error.message : t("secrets.syncFailed"),
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

</script>

<template>
  <section class="services-page">
    <header class="services-header">
      <h2 class="mobile-title-duplicate">{{ t('common.secrets') }}</h2>
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
        :placeholder="t('secrets.searchPlaceholder')"
        :aria-label="t('secrets.search')"
      />
      <div class="secrets-mobile-sort">
        <span>{{ t('secrets.sort') }}</span>
        <AppSelect v-model="mobileSortValue" :options="mobileSortOptions" :label="t('secrets.sort')" />
      </div>
      <AppButton compact class="secrets-create-button" @click="createModalOpen = true">
        <span class="secrets-create-label-desktop">{{ t('secrets.create') }}</span>
        <span class="secrets-create-label-mobile">{{ t('secrets.createShort') }}</span>
      </AppButton>
    </div>

    <AppTableEmpty v-if="loading && secrets.length === 0" :message="t('common.loading')" />

    <AppTableEmpty v-else-if="loadingError">{{ t('secrets.loadError', { error: loadingError }) }}</AppTableEmpty>

    <AppTableEmpty v-else-if="secrets.length === 0" :message="t('secrets.empty')" />

    <AppTableEmpty v-else-if="filteredSecrets.length === 0" :message="t('secrets.noSearchMatches')" />

    <AppTable v-else class="secrets-table" fixed :min-width="showDescriptionColumn ? '900px' : '720px'">
      <template #head>
          <tr>
            <th class="secrets-desktop-cell" :aria-sort="ariaSort('name')">
              <button class="app-table-sort" type="button" @click="changeSort('name')">
                {{ t('common.name') }} <span class="app-table-sort-indicator" aria-hidden="true">{{ sortIndicator('name') }}</span>
              </button>
            </th>
            <th v-if="showDescriptionColumn" class="secrets-desktop-cell" :aria-sort="ariaSort('description')">
              <button class="app-table-sort" type="button" @click="changeSort('description')">
                {{ t('common.description') }} <span class="app-table-sort-indicator" aria-hidden="true">{{ sortIndicator('description') }}</span>
              </button>
            </th>
            <th class="secrets-desktop-cell" :aria-sort="ariaSort('createdAt')">
              <button class="app-table-sort" type="button" @click="changeSort('createdAt')">
                {{ t('secrets.dateAdded') }} <span class="app-table-sort-indicator" aria-hidden="true">{{ sortIndicator('createdAt') }}</span>
              </button>
            </th>
            <th class="secrets-desktop-cell" :aria-sort="ariaSort('externalPath')">
              <button class="app-table-sort" type="button" @click="changeSort('externalPath')">
                {{ t('secrets.externalPath') }} <span class="app-table-sort-indicator" aria-hidden="true">{{ sortIndicator('externalPath') }}</span>
              </button>
            </th>
            <th class="secrets-desktop-cell" :aria-sort="ariaSort('externalVersionID')">
              <button class="app-table-sort" type="button" @click="changeSort('externalVersionID')">
                {{ t('secrets.externalVersionId') }} <span class="app-table-sort-indicator" aria-hidden="true">{{ sortIndicator('externalVersionID') }}</span>
              </button>
            </th>
            <th class="secrets-mobile-cell">{{ t('common.secret') }}</th>
            <th class="secrets-mobile-cell secrets-mobile-date-heading">{{ t('secrets.dateAddedShort') }}</th>
          </tr>
      </template>
          <tr
            v-for="secret in sortedSecrets"
            :key="secret.id"
            class="app-table-row--clickable"
            tabindex="0"
            role="button"
            :aria-label="t('secrets.openDetails', { secret: secret.name })"
            @click="openSecretDetails(secret.name)"
            @keydown.enter="openSecretDetails(secret.name)"
            @keydown.space.prevent="openSecretDetails(secret.name)"
          >
            <td class="secrets-desktop-cell">{{ secret.name || t('common.notApplicable') }}</td>
            <td v-if="showDescriptionColumn" class="secrets-desktop-cell">{{ secret.description || t('common.notApplicable') }}</td>
            <td class="secrets-desktop-cell">{{ formatDate(secret.created_at) }}</td>
            <td class="secrets-desktop-cell">{{ secret.external?.path || t('common.notApplicable') }}</td>
            <td class="secrets-desktop-cell">{{ secret.external?.version_id || t('common.notApplicable') }}</td>
            <td class="secrets-mobile-cell secrets-mobile-summary-cell">
              <span class="secrets-mobile-summary">
                <strong>{{ secret.name || t('common.notApplicable') }}</strong>
                <span v-if="secret.description" class="secrets-mobile-description">{{ secret.description }}</span>
                <span v-if="secret.external?.path || secret.external?.version_id" class="secrets-mobile-external">
                  <code v-if="secret.external?.path">{{ secret.external.path }}</code>
                  <span v-if="secret.external?.version_id" class="secrets-mobile-version">v{{ secret.external.version_id }}</span>
                </span>
                <span v-else class="secrets-mobile-source">{{ t('secrets.swarmSecret') }}</span>
              </span>
            </td>
            <td class="secrets-mobile-cell secrets-mobile-date">
              <time :datetime="secret.created_at">{{ formatMobileDate(secret.created_at) }}</time>
            </td>
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
.secrets-create-button { flex: 0 0 auto; }
.secrets-mobile-sort, .secrets-create-label-mobile, .secrets-mobile-cell { display: none; }
.secrets-create-feedback { margin: 0; border: 1px solid var(--success-border); border-radius: 8px; background: var(--success-muted); color: var(--success); padding: 8px 10px; font-size: .82rem; }
.secrets-table :deep(td) { overflow-wrap: anywhere; }

@media (max-width: 650px) {
  .secrets-table { overflow-x: hidden; }

  .secrets-toolbar-actions {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: end;
  }

  .secrets-toolbar-actions .secrets-search-input {
    grid-column: 1 / -1;
    max-width: none;
    font-size: 16px;
  }

  .secrets-mobile-sort {
    min-width: 0;
    display: grid;
    gap: 4px;
    color: var(--muted);
    font-size: .7rem;
  }

  .secrets-mobile-sort :deep(.app-select) {
    min-width: 0;
    width: 100%;
    --app-select-height: 34px;
  }

  .secrets-create-label-desktop { display: none; }
  .secrets-create-label-mobile { display: inline; }

  .secrets-table :deep(.app-table) {
    min-width: 0 !important;
    table-layout: fixed;
  }

  .secrets-table :deep(.secrets-desktop-cell) { display: none; }
  .secrets-table :deep(.secrets-mobile-cell) { display: table-cell; }

  .secrets-table :deep(.secrets-mobile-date-heading),
  .secrets-table :deep(.secrets-mobile-date) {
    width: 72px;
    text-align: right;
  }

  .secrets-table :deep(.secrets-mobile-date) {
    white-space: nowrap;
  }

  .secrets-table :deep(th) { padding-top: 8px; padding-bottom: 8px; }
  .secrets-table :deep(td) { vertical-align: top; padding-top: 12px; padding-bottom: 12px; }

  .secrets-mobile-summary {
    min-width: 0;
    display: grid;
    gap: 4px;
  }

  .secrets-mobile-summary strong {
    color: var(--text);
    font-size: .84rem;
    font-weight: 600;
    line-height: 1.3;
    overflow-wrap: anywhere;
  }

  .secrets-mobile-description {
    color: var(--muted);
    font-size: .74rem;
    line-height: 1.35;
    overflow-wrap: anywhere;
  }

  .secrets-mobile-external {
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 6px;
    margin-top: 2px;
  }

  .secrets-mobile-external code {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--muted);
    font-size: .68rem;
  }

  .secrets-mobile-version {
    flex: 0 0 auto;
    border-radius: 999px;
    background: var(--accent-muted);
    color: var(--accent-strong);
    padding: 1px 5px;
    font-size: .66rem;
    line-height: 1.35;
  }

  .secrets-mobile-source,
  .secrets-mobile-date {
    color: var(--muted);
    font-size: .7rem;
  }
}

@media (max-width: 360px) {
  .secrets-toolbar-actions { grid-template-columns: minmax(0, 1fr); }
  .secrets-toolbar-actions .secrets-search-input { grid-column: 1; }
  .secrets-mobile-sort { width: 100%; }
  .secrets-create-button { width: 100%; }
}
</style>
