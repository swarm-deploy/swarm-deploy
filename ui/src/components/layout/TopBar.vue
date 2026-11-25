<script setup lang="ts">
import { t } from "../../i18n";
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import { fetchSearch } from "../../api/search";
import type { SearchResult } from "../../api/types";
import { useOverviewStore } from "../../stores/overview";
import { useSecretDetailsStore } from "../../stores/secretDetails";
import AppButton from "../common/AppButton.vue";
import SidebarIcon from "./SidebarIcon.vue";

defineProps<{ assistantEnabled: boolean }>();

const emit = defineEmits<{
  toggleAssistant: [];
}>();

const MIN_QUERY_LENGTH = 2;
const SEARCH_DEBOUNCE_MS = 300;

const overviewStore = useOverviewStore();
const secretDetailsStore = useSecretDetailsStore();
const route = useRoute();
const router = useRouter();

const searchRootRef = ref<HTMLElement | null>(null);
const searchInputRef = ref<HTMLInputElement | null>(null);
const searchQuery = ref("");
const searchOpen = ref(false);
const mobileSearchExpanded = ref(false);
const searchLoading = ref(false);
const searchError = ref("");
const searchResults = ref<SearchResult[]>([]);

let debounceTimer: ReturnType<typeof setTimeout> | undefined;
let searchRequestID = 0;

const normalizedQuery = computed(() => searchQuery.value.trim());
const serviceResults = computed(() => searchResults.value.filter((item) => item.kind === "service"));
const secretResults = computed(() => searchResults.value.filter((item) => item.kind === "secret"));
const stackResults = computed(() => searchResults.value.filter((item) => item.kind === "stack"));
const showNoResults = computed(
  () =>
    searchOpen.value &&
    !searchLoading.value &&
    !searchError.value &&
    serviceResults.value.length === 0 &&
    secretResults.value.length === 0 &&
    stackResults.value.length === 0,
);
const mobileTitle = computed(() => {
  switch (route.name) {
    case "overview":
      return t("common.overview");
    case "realtime":
      return t("common.realtime");
    case "events":
      return t("navigation.eventsAlerts");
    case "graph":
      return t("navigation.graph");
    case "cluster":
      return t("navigation.cluster");
    case "networks":
      return t("common.networks");
    case "secrets":
      return t("common.secrets");
    case "deployments":
      return t("deployments.title");
    case "recommendations":
      return t("recommendations.title");
    case "stack-details":
    case "service-details":
      return null;
    case "service-config":
      return String(route.params.config ?? "Swarm Deploy");
    default:
      return "Swarm Deploy";
  }
});
function resetSearchState() {
  searchResults.value = [];
  searchLoading.value = false;
  searchError.value = "";
}

function closeSearch() {
  searchOpen.value = false;
  mobileSearchExpanded.value = false;
}

async function toggleMobileSearch() {
  if (mobileSearchExpanded.value) {
    closeSearch();
    return;
  }

  mobileSearchExpanded.value = true;
  await nextTick();
  searchInputRef.value?.focus();
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
    searchError.value = error instanceof Error ? error.message : t("navigation.searchFailed");
  } finally {
    if (requestID === searchRequestID) {
      searchLoading.value = false;
    }
  }
}

async function selectStack(item: SearchResult) {
  const stackName = item.stack?.trim() ?? item.label.trim();
  if (!stackName) {
    return;
  }

  closeSearch();
  await router.push({ name: "stack-details", params: { stack: stackName } });
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
  <header class="topbar-shell" :class="{ 'mobile-search-expanded': mobileSearchExpanded }">
    <h1 v-if="mobileTitle" class="topbar-mobile-title">{{ mobileTitle }}</h1>
    <div ref="searchRootRef" class="topbar-search" :class="{ 'mobile-open': mobileSearchExpanded }">
      <input
        ref="searchInputRef"
        v-model="searchQuery"
        type="search"
        :placeholder="t('navigation.searchPlaceholder')"
        :aria-label="t('navigation.searchPlaceholder')"
        @focus="scheduleSearch"
      />
      <button
        type="button"
        class="topbar-search-toggle"
        :aria-label="mobileSearchExpanded ? t('assistant.closeSearch') : t('navigation.searchPlaceholder')"
        :aria-expanded="mobileSearchExpanded"
        @click="toggleMobileSearch"
      >
        <SidebarIcon :name="mobileSearchExpanded ? 'close' : 'search'" />
      </button>
      <div v-if="searchOpen" class="topbar-search-dropdown">
        <p v-if="searchLoading" class="topbar-search-state">{{ t('navigation.searching') }}</p>
        <p v-else-if="searchError" class="topbar-search-state">{{ t('navigation.searchError', { error: searchError }) }}</p>
        <template v-else>
          <div v-if="stackResults.length > 0" class="topbar-search-group">
            <p class="topbar-search-group-title">{{ t('common.stacks') }}</p>
            <button
              v-for="item in stackResults"
              :key="`stack-${item.stack}-${item.match}`"
              type="button"
              class="topbar-search-item"
              @click="selectStack(item)"
            >
              <span>{{ item.label }}</span>
            </button>
          </div>
          <div v-if="serviceResults.length > 0" class="topbar-search-group">
            <p class="topbar-search-group-title">{{ t('common.services') }}</p>
            <button
              v-for="item in serviceResults"
              :key="`service-${item.stack}-${item.service}-${item.match}`"
              type="button"
              class="topbar-search-item"
              @click="selectService(item)"
            >
              <span>{{ item.label }}</span>
              <small v-if="item.stack">{{ t('navigation.searchStack', { stack: item.stack }) }}</small>
            </button>
          </div>
          <div v-if="secretResults.length > 0" class="topbar-search-group">
            <p class="topbar-search-group-title">{{ t('common.secrets') }}</p>
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
          <p v-if="showNoResults" class="topbar-search-state">{{ t('navigation.noMatches') }}</p>
        </template>
      </div>
    </div>

    <div class="topbar-actions">
      <AppButton
        variant="secondary"
        class="topbar-assistant-button"
        :aria-label="t('assistant.name')"
        :disabled="!assistantEnabled"
        @click="emit('toggleAssistant')"
      >
        <span class="topbar-assistant-label">{{ t('assistant.name') }}</span>
        <span class="topbar-assistant-icon" :title="t('assistant.name')" aria-hidden="true">
          <svg
            width="26"
            height="26"
            viewBox="0 0 24 24"
            fill="none"
            stroke="url(#topbar-assistant-gradient)"
            stroke-width="1.75"
            stroke-linecap="round"
          >
            <defs>
              <linearGradient id="topbar-assistant-gradient" x1="0%" y1="100%" x2="100%" y2="0%">
                <stop offset="0%" stop-color="#667FA6" />
                <stop offset="100%" stop-color="#9079A5" />
              </linearGradient>
            </defs>
            <rect x="4" y="7" width="16" height="13" rx="3" />
            <path d="M12 7V3M9.5 15Q12 18 14.5 15" />
            <circle cx="9" cy="12" r="1" fill="url(#topbar-assistant-gradient)" stroke="none" />
            <circle cx="15" cy="12" r="1" fill="url(#topbar-assistant-gradient)" stroke="none" />
          </svg>
        </span>
      </AppButton>
    </div>
  </header>
</template>
