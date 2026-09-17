<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";

import { fetchRecommendations } from "../api/overview";
import type { Recommendation, RecommendationSeverity } from "../api/types";

const severityOptions: RecommendationSeverity[] = ["high", "medium", "low"];
const severityRank: Record<RecommendationSeverity, number> = {
  high: 0,
  medium: 1,
  low: 2,
};

const loading = ref(false);
const loadingError = ref("");
const recommendations = ref<Recommendation[]>([]);
const selectedStack = ref("");
const selectedSeverity = ref<RecommendationSeverity | "">("");

let requestID = 0;

const stackOptions = computed(() =>
  Array.from(new Set(recommendations.value.map((recommendation) => recommendation.subject.stack).filter(Boolean))).sort(),
);

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
  return `${recommendation.severity}-${recommendation.subject.stack}-${recommendation.subject.service}-${recommendation.recommendation}`;
}

function severityClass(severity: RecommendationSeverity): string {
  return `recommendation-severity-${severity}`;
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

watch(stackOptions, (options) => {
  if (selectedStack.value && !options.includes(selectedStack.value)) {
    selectedStack.value = "";
  }
});

onMounted(() => {
  void loadRecommendations();
});
</script>

<template>
  <section class="services-page recommendations-page">
    <header class="services-header events-header">
      <h2>Recommendations</h2>
      <div class="events-filter-bar">
        <label class="events-filter-field">
          <span>Stack</span>
          <select v-model="selectedStack">
            <option value="">All stacks</option>
            <option v-for="stack in stackOptions" :key="stack" :value="stack">{{ stack }}</option>
          </select>
        </label>
        <label class="events-filter-field">
          <span>Severity</span>
          <select v-model="selectedSeverity">
            <option value="">All severities</option>
            <option v-for="severity in severityOptions" :key="severity" :value="severity">{{ severity }}</option>
          </select>
        </label>
      </div>
    </header>

    <div v-if="loading && recommendations.length === 0" class="services-empty">
      <p class="meta">Loading recommendations...</p>
    </div>

    <div v-else-if="loadingError" class="services-empty">
      <p class="meta">Failed to load recommendations: {{ loadingError }}</p>
    </div>

    <div v-else-if="visibleRecommendations.length === 0" class="services-empty recommendations-empty">
      <p class="meta">No recommendations match the selected filters.</p>
    </div>

    <div v-else class="secrets-table-wrap recommendations-table-wrap">
      <table class="container-status-table recommendations-table">
        <thead>
          <tr>
            <th>Severity</th>
            <th>Recommendation</th>
            <th>Service</th>
            <th>Stack</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="recommendation in visibleRecommendations" :key="recommendationKey(recommendation)">
            <td>
              <span class="recommendation-severity" :class="severityClass(recommendation.severity)">
                {{ recommendation.severity }}
              </span>
            </td>
            <td>{{ recommendation.recommendation || "No recommendation text" }}</td>
            <td>{{ recommendation.subject.service || "unknown service" }}</td>
            <td>{{ recommendation.subject.stack || "unknown stack" }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
