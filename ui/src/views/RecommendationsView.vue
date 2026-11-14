<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import { fetchRecommendations } from "../api/overview";
import type { Recommendation, RecommendationSeverity } from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import SidebarIcon from "../components/layout/SidebarIcon.vue";

const severityOptions: RecommendationSeverity[] = ["high", "medium", "low"];
const severityRank: Record<RecommendationSeverity, number> = {
  high: 0,
  medium: 1,
  low: 2,
};

const loading = ref(false);
const loadingError = ref("");
const recommendations = ref<Recommendation[]>([]);
const route = useRoute();
const router = useRouter();
const routeStack = computed(() => typeof route.query.stack_name === "string" ? route.query.stack_name : "");
const selectedStack = ref(routeStack.value);
const selectedSeverity = ref<RecommendationSeverity | "">("");
const filterPanel = ref<HTMLElement | null>(null);
const stackFilter = ref<HTMLDetailsElement | null>(null);
const stackSearch = ref("");
const mobileFiltersOpen = ref(false);

let requestID = 0;

const activeFilterCount = computed(() => [Boolean(selectedStack.value), Boolean(selectedSeverity.value)].filter(Boolean).length);

const stackOptions = computed(() => {
  const search = stackSearch.value.trim().toLocaleLowerCase();
  return Array.from(new Set([
    selectedStack.value,
    ...recommendations.value.map((recommendation) => recommendation.subject.stack),
  ].filter(Boolean)))
    .sort()
    .filter((stack) => !search || stack.toLocaleLowerCase().includes(search));
});

const visibleRecommendations = computed(() =>
  recommendations.value
    .filter((recommendation) => !selectedStack.value || recommendation.subject.stack === selectedStack.value)
    .filter((recommendation) => !selectedSeverity.value || recommendation.severity === selectedSeverity.value)
    .slice()
    .sort(
      (left, right) =>
        severityRank[left.severity] - severityRank[right.severity] ||
        left.subject.stack.localeCompare(right.subject.stack) ||
        left.subject.service.localeCompare(right.subject.service),
    ),
);

function recommendationKey(recommendation: Recommendation): string {
  return `${recommendation.severity}-${recommendation.subject.stack}-${recommendation.subject.service}-${recommendation.title}`;
}

function severityClass(severity: RecommendationSeverity): string {
  return `recommendation-severity-${severity}`;
}

function severityDotClass(severity: RecommendationSeverity): string {
  return `overview-recommendation-severity-${severity}`;
}

function selectStack(stack: string): void {
  selectedStack.value = stack;
  stackSearch.value = "";
  if (stackFilter.value) stackFilter.value.open = false;
}

function handleDocumentClick(event: MouseEvent): void {
  const target = event.target;
  if (!(target instanceof Node) || filterPanel.value?.contains(target)) return;
  if (stackFilter.value) stackFilter.value.open = false;
}

async function loadRecommendations() {
  loading.value = true;
  loadingError.value = "";
  const currentRequestID = ++requestID;

  try {
    const response = await fetchRecommendations();
    if (currentRequestID !== requestID) {
      return;
    }

    recommendations.value = Array.isArray(response.recommendations) ? response.recommendations : [];
  } catch (error) {
    if (currentRequestID !== requestID) {
      return;
    }

    recommendations.value = [];
    loadingError.value = error instanceof Error ? error.message : "Failed to load recommendations";
  } finally {
    if (currentRequestID === requestID) {
      loading.value = false;
    }
  }
}

watch(routeStack, (stack) => { selectedStack.value = stack; });
watch(selectedStack, (stack) => {
  if (stack === routeStack.value) return;
  const query = { ...route.query };
  if (stack) query.stack_name = stack;
  else delete query.stack_name;
  void router.replace({ query });
});

onMounted(() => {
  document.addEventListener("click", handleDocumentClick);
  void loadRecommendations();
});

onBeforeUnmount(() => {
  document.removeEventListener("click", handleDocumentClick);
});
</script>

<template>
  <section class="services-page recommendations-page">
    <header class="services-header events-header recommendations-header">
      <h2>Recommendations</h2>
      <button
        class="recommendations-mobile-filter-toggle"
        type="button"
        :aria-expanded="mobileFiltersOpen"
        aria-controls="recommendations-filter-panel"
        @click="mobileFiltersOpen = !mobileFiltersOpen"
      >
        Filters ({{ activeFilterCount }})
      </button>
      <div
        id="recommendations-filter-panel"
        ref="filterPanel"
        class="recommendations-filter-bar"
        :class="{ 'recommendations-filter-bar--open': mobileFiltersOpen }"
      >
        <div class="recommendations-filter-field">
          <details ref="stackFilter" class="recommendations-filter-dropdown">
            <summary aria-label="Filter recommendations by stack">
              <SidebarIcon class="recommendations-filter-icon" name="services" />
              <span class="recommendations-filter-selection">{{ selectedStack || "All stacks" }}</span>
              <span class="recommendations-filter-chevron" aria-hidden="true">⌄</span>
            </summary>
            <div class="recommendations-search-filter-menu">
              <input v-model="stackSearch" type="search" placeholder="Search stacks" aria-label="Search stacks" />
              <button type="button" :class="{ selected: !selectedStack }" @click="selectStack('')">All stacks</button>
              <button
                v-for="stack in stackOptions"
                :key="stack"
                type="button"
                :class="{ selected: selectedStack === stack }"
                @click="selectStack(stack)"
              >
                {{ stack }}
              </button>
              <p v-if="stackOptions.length === 0" class="recommendations-filter-empty">No matching stacks</p>
            </div>
          </details>
        </div>

        <label class="recommendations-filter-field recommendations-filter-field--select">
          <svg class="recommendations-filter-icon recommendations-severity-filter-icon" viewBox="0 0 20 20" aria-hidden="true">
            <path d="M5 12.5v2.5M10 9v6M15 5v10" />
          </svg>
          <select v-model="selectedSeverity" aria-label="Filter recommendations by severity">
            <option value="">All severities</option>
            <option v-for="severity in severityOptions" :key="severity" :value="severity">{{ severity }}</option>
          </select>
        </label>
      </div>
    </header>

    <AppTableEmpty v-if="loading && recommendations.length === 0" message="Loading recommendations..." />

    <AppTableEmpty v-else-if="loadingError">Failed to load recommendations: {{ loadingError }}</AppTableEmpty>

    <AppTableEmpty
      v-else-if="visibleRecommendations.length === 0"
      message="No recommendations match the selected filters."
    />

    <AppTable v-else fixed table-class="recommendations-table" wrap-class="recommendations-table-wrap">
      <template #head>
          <tr>
            <th>Severity</th>
            <th>Recommendation</th>
            <th>Service</th>
            <th>Stack</th>
          </tr>
      </template>
          <tr v-for="recommendation in visibleRecommendations" :key="recommendationKey(recommendation)">
            <td>
              <span class="recommendation-severity" :class="severityClass(recommendation.severity)">
                {{ recommendation.severity }}
              </span>
            </td>
            <td>
              <span class="recommendation-cell-content">
                <span
                  class="overview-recommendation-severity-dot recommendation-mobile-severity-dot"
                  :class="severityDotClass(recommendation.severity)"
                  :title="recommendation.severity"
                  aria-hidden="true"
                ></span>
                <span>{{ recommendation.recommendation || "No recommendation" }}</span>
              </span>
            </td>
            <td>{{ recommendation.subject.service || "unknown service" }}</td>
            <td>{{ recommendation.subject.stack || "unknown stack" }}</td>
          </tr>
    </AppTable>
  </section>
</template>

<style scoped>
.recommendations-page {
  width: 100%;
  min-width: 0;
}
.recommendations-header {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 14px;
  align-items: stretch;
  justify-content: stretch;
  width: 100%;
  min-width: 0;
}
.recommendations-mobile-filter-toggle {
  display: none;
}
.recommendations-filter-bar {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
  align-items: center;
  box-sizing: border-box;
  width: 100%;
  max-width: none;
  min-width: 0;
  border: 1px solid var(--border-subtle);
  border-radius: 10px;
  background: var(--surface-card-soft);
  padding: 12px;
}
.recommendations-filter-field {
  position: relative;
  min-width: 0;
}
.recommendations-filter-field select,
.recommendations-filter-dropdown > summary {
  box-sizing: border-box;
  width: 100%;
  height: 38px;
  border: 1px solid var(--border-default);
  border-radius: 7px;
  background: var(--surface-input);
  color: var(--text);
  font: inherit;
  font-size: 0.83rem;
}
.recommendations-filter-field select {
  padding: 7px 10px 7px 36px;
}
.recommendations-filter-icon {
  width: 17px;
  height: 17px;
  flex: 0 0 17px;
  color: var(--muted);
  fill: none;
  stroke: currentColor;
  stroke-width: 1.6;
  stroke-linecap: round;
  stroke-linejoin: round;
  pointer-events: none;
}
.recommendations-filter-field--select > .recommendations-filter-icon {
  position: absolute;
  z-index: 1;
  top: 50%;
  left: 11px;
  transform: translateY(-50%);
}
.recommendations-severity-filter-icon {
  stroke-width: 1.35;
}
.recommendations-filter-dropdown {
  position: relative;
  min-width: 0;
}
.recommendations-filter-dropdown > summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 7px 10px;
  cursor: pointer;
  list-style: none;
}
.recommendations-filter-dropdown > summary::-webkit-details-marker {
  display: none;
}
.recommendations-filter-selection {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  color: var(--text);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.recommendations-filter-chevron {
  flex: 0 0 auto;
  color: var(--muted);
}
.recommendations-search-filter-menu {
  position: absolute;
  z-index: 30;
  top: calc(100% + 0.35rem);
  left: 0;
  box-sizing: border-box;
  width: max(100%, 260px);
  max-width: calc(100vw - 2rem);
  max-height: min(320px, calc(100dvh - 8rem));
  overflow-x: hidden;
  overflow-y: auto;
  border: 1px solid var(--border-default);
  border-radius: 0.5rem;
  background: var(--surface-elevated);
  box-shadow: var(--shadow-popover);
  padding: 0.45rem;
}
.recommendations-search-filter-menu input {
  box-sizing: border-box;
  width: 100%;
  margin-bottom: 0.35rem;
  border: 1px solid var(--border-default);
  border-radius: 0.35rem;
  background: var(--surface);
  color: var(--text);
  padding: 0.5rem 0.6rem;
  font: inherit;
  font-size: 0.82rem;
}
.recommendations-search-filter-menu button {
  width: 100%;
  border: 0;
  border-radius: 0.35rem;
  background: transparent;
  color: var(--text);
  padding: 0.5rem 0.55rem;
  text-align: left;
  font: inherit;
  font-size: 0.82rem;
}
.recommendations-search-filter-menu button:hover,
.recommendations-search-filter-menu button.selected {
  background: var(--surface-hover-soft);
}
.recommendations-filter-empty {
  margin: 0;
  padding: 0.65rem 0.55rem;
  color: var(--muted);
  font-size: 0.78rem;
}
.recommendations-filter-field select:focus-visible,
.recommendations-filter-dropdown > summary:focus-visible {
  outline: 2px solid var(--accent-focus);
  outline-offset: 2px;
}
@media (max-width: 720px) {
  .recommendations-mobile-filter-toggle {
    display: inline-flex;
    align-items: center;
    width: max-content;
    min-height: 2.3rem;
    border: 1px solid var(--border-default);
    border-radius: 0.45rem;
    background: var(--surface-elevated);
    color: var(--text);
    padding: 0.45rem 0.75rem;
    font: inherit;
    font-size: 0.84rem;
  }
  .recommendations-filter-bar {
    display: none;
    grid-template-columns: 1fr;
    padding: 10px;
  }
  .recommendations-filter-bar--open {
    display: grid;
  }
  .recommendations-search-filter-menu {
    width: 100%;
    max-width: 100%;
  }
  .recommendations-filter-field select {
    font-size: 16px;
  }
}
</style>
