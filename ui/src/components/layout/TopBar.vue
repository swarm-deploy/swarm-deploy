<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { RouterLink } from "vue-router";

import { fetchSearch } from "../../api/search";
import type { SearchResult } from "../../api/types";
import { useCurrentUserStore } from "../../stores/currentUser";
import { useOverviewStore } from "../../stores/overview";
import { useSecretDetailsStore } from "../../stores/secretDetails";

defineProps<{
  syncDisabled: boolean;
  syncPending: boolean;
  assistantEnabled: boolean;
  assistantOpen: boolean;
  notificationsDisabled: boolean;
}>();

const emit = defineEmits<{
  syncNow: [];
  openNotifications: [];
  toggleAssistant: [];
}>();

const MIN_QUERY_LENGTH = 2;
const SEARCH_DEBOUNCE_MS = 300;

const overviewStore = useOverviewStore();
const currentUserStore = useCurrentUserStore();
const secretDetailsStore = useSecretDetailsStore();

const searchRootRef = ref<HTMLElement | null>(null);
const searchQuery = ref("");
const searchOpen = ref(false);
const searchLoading = ref(false);
const searchError = ref("");
const searchResults = ref<SearchResult[]>([]);

let debounceTimer: ReturnType<typeof setTimeout> | undefined;
let searchRequestID = 0;

const normalizedQuery = computed(() => searchQuery.value.trim());
const visibleResults = computed(() => {
  if (!searchOpen.value) {
    return [];
  }
  return searchResults.value.filter((item) => item.kind === "service" || item.kind === "secret");
});
const serviceResults = computed(() => visibleResults.value.filter((item) => item.kind === "service"));
const secretResults = computed(() => visibleResults.value.filter((item) => item.kind === "secret"));
const showNoResults = computed(
  () => searchOpen.value && !searchLoading.value && !searchError.value && visibleResults.value.length === 0,
);
const currentUserLabel = computed(() => currentUserStore.displayName.trim() || "User");

function resetSearchState() {
  searchResults.value = [];
  searchLoading.value = false;
  searchError.value = "";
}

function closeSearch() {
  searchOpen.value = false;
}

function scheduleSearch() {
  if (debounceTimer) {
    clearTimeout(debounceTimer);
  }

  const query = normalizedQuery.value;
  if (query.length < MIN_QUERY_LENGTH) {
    resetSearchState();
    searchOpen.value = false;
    return;
  }

  searchOpen.value = true;
  debounceTimer = setTimeout(() => {
    void performSearch(query);
  }, SEARCH_DEBOUNCE_MS);
}

async function performSearch(query: string) {
  searchLoading.value = true;
  searchError.value = "";
  const requestID = ++searchRequestID;

  try {
    const response = await fetchSearch(query);
    if (requestID !== searchRequestID) {
      return;
    }

    searchResults.value = Array.isArray(response.results) ? response.results : [];
  } catch (error) {
    if (requestID !== searchRequestID) {
      return;
    }
    searchResults.value = [];
    searchError.value = error instanceof Error ? error.message : "Search failed";
  } finally {
    if (requestID === searchRequestID) {
      searchLoading.value = false;
    }
  }
}

async function selectService(item: SearchResult) {
  const stackName = item.stack?.trim() ?? "";
  const serviceName = item.service?.trim() ?? item.label;
  if (!stackName || !serviceName) {
    return;
  }

  closeSearch();
  await overviewStore.openServiceStatusModal(stackName, serviceName);
}

async function selectSecret(item: SearchResult) {
  const secretName = item.secret_name?.trim() ?? item.label;
  if (!secretName) {
    return;
  }

  closeSearch();
  await secretDetailsStore.openSecretDetails(secretName);
}

function handleDocumentClick(event: MouseEvent) {
  const root = searchRootRef.value;
  if (!root) {
    return;
  }
  if (event.target instanceof Node && root.contains(event.target)) {
    return;
  }

  closeSearch();
}

function handleEscape(event: KeyboardEvent) {
  if (event.key === "Escape") {
    closeSearch();
  }
}

watch(searchQuery, scheduleSearch);

onMounted(() => {
  document.addEventListener("mousedown", handleDocumentClick);
  document.addEventListener("keydown", handleEscape);
});

onUnmounted(() => {
  if (debounceTimer) {
    clearTimeout(debounceTimer);
  }
  document.removeEventListener("mousedown", handleDocumentClick);
  document.removeEventListener("keydown", handleEscape);
});
</script>

<template>
  <header class="topbar-shell">
    <div class="topbar-brand">
      <p class="eyebrow">GitOps for Docker Swarm</p>
      <RouterLink to="/overview" class="brand-link">Swarm Deploy</RouterLink>
    </div>

    <div ref="searchRootRef" class="topbar-search">
      <input
        v-model="searchQuery"
        type="search"
        placeholder="Search services and secrets"
        aria-label="Search services and secrets"
        @focus="scheduleSearch"
      />
      <div v-if="searchOpen" class="topbar-search-dropdown">
        <p v-if="searchLoading" class="topbar-search-state">Searching...</p>
        <p v-else-if="searchError" class="topbar-search-state">Search failed: {{ searchError }}</p>
        <template v-else>
          <div v-if="serviceResults.length > 0" class="topbar-search-group">
            <p class="topbar-search-group-title">Services</p>
            <button
              v-for="item in serviceResults"
              :key="`service-${item.stack}-${item.service}-${item.match}`"
              type="button"
              class="topbar-search-item"
              @click="selectService(item)"
            >
              <span>{{ item.label }}</span>
              <small v-if="item.stack">stack: {{ item.stack }}</small>
            </button>
          </div>
          <div v-if="secretResults.length > 0" class="topbar-search-group">
            <p class="topbar-search-group-title">Secrets</p>
            <button
              v-for="item in secretResults"
              :key="`secret-${item.secret_name}-${item.match}`"
              type="button"
              class="topbar-search-item"
              @click="selectSecret(item)"
            >
              <span>{{ item.label }}</span>
            </button>
          </div>
          <p v-if="showNoResults" class="topbar-search-state">No matches found.</p>
        </template>
      </div>
    </div>

    <div class="topbar-actions">
      <button type="button" :disabled="syncDisabled || syncPending" @click="emit('syncNow')">
        {{ syncPending ? "Syncing..." : "Sync now" }}
      </button>
      <button type="button" :disabled="notificationsDisabled" @click="emit('openNotifications')">Events</button>
      <button type="button" :disabled="!assistantEnabled" @click="emit('toggleAssistant')">
        {{ assistantOpen ? "Assistant Open" : "Assistant" }}
      </button>
      <button type="button" class="button-ghost">{{ currentUserLabel }}</button>
    </div>
  </header>
</template>
