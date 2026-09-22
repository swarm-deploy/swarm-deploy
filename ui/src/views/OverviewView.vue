<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";

import { fetchAlerts, fetchEvents, fetchRecommendations } from "../api/overview";
import type { Alert, EventHistoryItem, Recommendation, RecommendationSeverity } from "../api/types";
import StackCard from "../components/overview/StackCard.vue";
import SummaryPanel from "../components/overview/SummaryPanel.vue";
import SummaryRow from "../components/overview/SummaryRow.vue";
import { useOverviewStore } from "../stores/overview";
import { formatDate, shortCommitHash } from "../utils/format";

const overviewStore = useOverviewStore();
const deploymentEvents = ref<EventHistoryItem[]>([]);
const alerts = ref<Alert[]>([]);
const recommendations = ref<Recommendation[]>([]);
const overviewEventsError = ref("");
const overviewAlertsError = ref("");
const overviewRecommendationsError = ref("");
const deploymentEventsLimit = 4;
const alertEventsLimit = 4;
const recommendationsLimit = 4;
const recommendationSeverityRank: Record<RecommendationSeverity, number> = {
  high: 0,
  medium: 1,
  low: 2,
};

let refreshTimer: ReturnType<typeof setInterval> | undefined;

const syncInfo = computed(() => overviewStore.syncInfo);
const syncRevision = computed(() => String(syncInfo.value?.git_revision ?? "").trim());
const sortedRecommendations = computed(() =>
  recommendations.value
    .slice()
    .sort(
      (left, right) =>
        recommendationSeverityRank[left.severity] - recommendationSeverityRank[right.severity] ||
        left.subject.stack.localeCompare(right.subject.stack) ||
        left.subject.service.localeCompare(right.subject.service),
    ),
);
const overviewRecommendations = computed(() => sortedRecommendations.value.slice(0, recommendationsLimit));
const serviceCountsByStack = computed(() => {
  const counts = new Map<string, number>();
  for (const service of overviewStore.services) {
    counts.set(service.stack, (counts.get(service.stack) ?? 0) + 1);
  }

  return counts;
});

function serviceCount(stackName: string): number {
  return serviceCountsByStack.value.get(stackName) ?? 0;
}

function deploymentResult(item: EventHistoryItem): string {
  if (item.type === "deploySuccess") {
    return "success";
  }
  if (item.type === "deployFailed") {
    return "failed";
  }

  return item.severity;
}

function deploymentResultClass(item: EventHistoryItem): string {
  return item.type === "deploySuccess" ? "success" : "failed";
}

function detailValue(item: EventHistoryItem, keys: string[]): string {
  const details = item.details ?? {};
  for (const key of keys) {
    const value = String(details[key] ?? "").trim();
    if (value) {
      return value;
    }
  }

  return "";
}

function formatTime(raw: string | undefined): string {
  if (!raw) {
    return "n/a";
  }

  const parsed = new Date(raw);
  if (Number.isNaN(parsed.valueOf())) {
    return raw;
  }

  const pad = (value: number) => String(value).padStart(2, "0");
  return `${pad(parsed.getHours())}:${pad(parsed.getMinutes())}`;
}

function formatRelativeTime(raw: string | undefined): string {
  if (!raw) {
    return "time unknown";
  }

  const timestamp = new Date(raw).valueOf();
  if (Number.isNaN(timestamp)) {
    return raw;
  }

  const elapsedMinutes = Math.max(0, Math.floor((Date.now() - timestamp) / 60000));
  if (elapsedMinutes < 1) {
    return "just now";
  }
  if (elapsedMinutes < 60) {
    return `${elapsedMinutes} min ago`;
  }

  const elapsedHours = Math.floor(elapsedMinutes / 60);
  return elapsedHours === 1 ? "1 hour ago" : `${elapsedHours} hours ago`;
}

function recommendationSubject(recommendation: Recommendation): string {
  return [recommendation.subject.stack, recommendation.subject.service].filter(Boolean).join(" / ") || "Swarm Deploy";
}

function recommendationSeverityClass(severity: RecommendationSeverity): string {
  return `overview-recommendation-severity-${severity}`;
}

async function openCommitDetails(commitHash: string | undefined) {
  const hash = String(commitHash || "").trim();
  if (!hash) {
    return;
  }

  await overviewStore.openCommitDetailsModal(hash);
}

async function openStackManifest(stackName: string) {
  const stack = String(stackName || "").trim();
  if (!stack) {
    return;
  }

  await overviewStore.openStackManifestModal(stack);
}

function openAlertDetails(alert: Alert) {
  overviewStore.openAlertDetailsModal(alert);
}

async function refreshOverview() {
  overviewEventsError.value = "";
  overviewAlertsError.value = "";
  overviewRecommendationsError.value = "";

  const [overviewResult, deploymentsResult, alertsResult, recommendationsResult] = await Promise.allSettled([
    overviewStore.loadOverview(),
    fetchEvents({ types: ["deploySuccess", "deployFailed"], limit: deploymentEventsLimit }),
    fetchAlerts({ status: "open", limit: alertEventsLimit }),
    fetchRecommendations({ limit: recommendationsLimit }),
  ]);

  if (overviewResult.status === "rejected") {
    overviewStore.loadingError = overviewResult.reason instanceof Error ? overviewResult.reason.message : "Failed to load state";
  }
  if (deploymentsResult.status === "fulfilled") {
    deploymentEvents.value = Array.isArray(deploymentsResult.value.events) ? deploymentsResult.value.events.reverse() : [];
  } else {
    deploymentEvents.value = [];
    overviewEventsError.value =
      deploymentsResult.reason instanceof Error ? deploymentsResult.reason.message : "Failed to load latest deployments";
  }
  if (alertsResult.status === "fulfilled") {
    alerts.value = Array.isArray(alertsResult.value.alerts) ? alertsResult.value.alerts : [];
  } else {
    alerts.value = [];
    overviewAlertsError.value = alertsResult.reason instanceof Error ? alertsResult.reason.message : "Failed to load alerts";
  }
  if (recommendationsResult.status === "fulfilled") {
    recommendations.value = Array.isArray(recommendationsResult.value.recommendations)
      ? recommendationsResult.value.recommendations
      : [];
  } else {
    recommendations.value = [];
    overviewRecommendationsError.value =
      recommendationsResult.reason instanceof Error
        ? recommendationsResult.reason.message
        : "Failed to load recommendations";
  }
}

onMounted(async () => {
  await refreshOverview();
  refreshTimer = setInterval(() => {
    void refreshOverview();
  }, 10000);
});

onUnmounted(() => {
  if (refreshTimer) {
    clearInterval(refreshTimer);
    refreshTimer = undefined;
  }
});
</script>

<template>
  <section class="status-panel">
    <p v-if="overviewStore.loadingError" class="meta">Failed to load state: {{ overviewStore.loadingError }}</p>
    <p v-else-if="!syncInfo" class="meta">Sync status is unavailable.</p>
    <p v-else class="meta">
      Last sync: {{ formatDate(syncInfo.last_sync_at) }} | reason: {{ syncInfo.last_sync_reason || "n/a" }} | result:
      {{ syncInfo.last_sync_result || "n/a" }} | revision:
      <button
        v-if="syncRevision"
        type="button"
        class="stack-commit-badge status unknown"
        @click="openCommitDetails(syncRevision)"
      >
        {{ shortCommitHash(syncRevision) }}
      </button>
      <span v-else> n/a</span>
      <template v-if="syncInfo.last_sync_error"> | error: {{ syncInfo.last_sync_error }}</template>
    </p>
  </section>

  <section class="overview-summary-grid" aria-label="Overview highlights">
    <SummaryPanel
      title="Latest Deployments"
      icon="deployments"
      :to="{ path: '/events', query: { types: ['deploySuccess', 'deployFailed'] } }"
    >
      <p v-if="overviewEventsError && deploymentEvents.length === 0" class="meta">
        Failed to load latest deployments: {{ overviewEventsError }}
      </p>
      <div v-else-if="deploymentEvents.length === 0" class="overview-summary-empty">
        <span class="overview-summary-empty-icon" aria-hidden="true">↓</span>
        <strong>No deployments yet</strong>
        <span>Recent deployments will appear here</span>
      </div>
      <div v-else class="overview-deployment-list">
        <SummaryRow
          v-for="event in deploymentEvents.slice(0, deploymentEventsLimit)"
          :key="`${event.type}-${event.created_at}-${event.message}`"
          :interactive="Boolean(detailValue(event, ['commit', 'revision']))"
          :aria-label="`Open deployment commit ${detailValue(event, ['commit', 'revision'])}`"
          @activate="openCommitDetails(detailValue(event, ['commit', 'revision']))"
        >
          <span class="overview-deployment-stack">{{ detailValue(event, ["stack", "stack_name"]) || "unknown stack" }}</span>
          <span class="overview-deployment-result" :class="deploymentResultClass(event)">
            <span class="overview-deployment-result-icon" aria-hidden="true">
              {{ event.type === "deploySuccess" ? "✓" : "!" }}
            </span>
            <span>
              {{ deploymentResult(event) }}
            </span>
          </span>
          <time class="overview-deployment-time" :datetime="event.created_at">{{ formatTime(event.created_at) }}</time>
          <span v-if="detailValue(event, ['commit', 'revision'])" class="overview-commit-badge overview-summary-sha-badge">
            {{ shortCommitHash(detailValue(event, ["commit", "revision"])) }}
          </span>
          <span v-else class="overview-summary-value-empty">n/a</span>
        </SummaryRow>
      </div>
    </SummaryPanel>

    <SummaryPanel title="Alerts" icon="alerts" to="/alerts">
      <p v-if="overviewAlertsError && alerts.length === 0" class="meta">Failed to load alerts: {{ overviewAlertsError }}</p>
      <div v-else-if="alerts.length === 0" class="overview-summary-empty">
        <span class="overview-summary-empty-icon overview-summary-empty-icon--healthy" aria-hidden="true">✓</span>
        <strong>No active alerts</strong>
        <span>Everything looks healthy</span>
      </div>
      <div v-else class="overview-alert-list">
        <SummaryRow
          v-for="alert in alerts"
          :key="alert.id"
          interactive
          :aria-label="`Open alert: ${alert.title}`"
          @activate="openAlertDetails(alert)"
        >
          <span class="overview-summary-severity overview-summary-severity--alert" aria-hidden="true"></span>
          <span class="overview-summary-row-copy">
            <span class="overview-alert-message">{{ alert.title }}</span>
            <span class="overview-summary-secondary">
              {{ alert.resourceId || "unknown stack" }} · {{ formatRelativeTime(alert.updatedAt) }}
            </span>
          </span>
        </SummaryRow>
      </div>
    </SummaryPanel>

    <SummaryPanel title="Recommendations" icon="recommendations" to="/recommendations">
      <p v-if="overviewRecommendationsError && overviewRecommendations.length === 0" class="meta">
        Failed to load recommendations: {{ overviewRecommendationsError }}
      </p>
      <div v-else-if="overviewRecommendations.length === 0" class="overview-summary-empty">
        <span class="overview-summary-empty-icon overview-summary-empty-icon--healthy" aria-hidden="true">✓</span>
        <strong>No recommendations</strong>
        <span>Your configuration looks good</span>
      </div>
      <div v-else class="overview-recommendation-list">
        <SummaryRow
          v-for="recommendation in overviewRecommendations"
          :key="`${recommendation.severity}-${recommendation.subject.stack}-${recommendation.subject.service}-${recommendation.title}`"
          to="/recommendations"
          :aria-label="`View recommendation: ${recommendation.title}`"
        >
          <span
            class="overview-summary-severity"
            :class="recommendationSeverityClass(recommendation.severity)"
            :title="recommendation.severity"
            aria-hidden="true"
          ></span>
          <span class="overview-summary-row-copy">
            <span class="overview-recommendation-text">{{ recommendation.title }}</span>
            <span class="overview-summary-secondary">{{ recommendationSubject(recommendation) }}</span>
          </span>
        </SummaryRow>
      </div>
    </SummaryPanel>
  </section>

  <section>
    <div v-if="overviewStore.loading && overviewStore.stacks.length === 0" class="stack-grid">
      <article class="stack-card">
        <p class="meta">Loading...</p>
      </article>
    </div>

    <div v-else-if="overviewStore.stacks.length === 0" class="stack-grid">
      <article class="stack-card">
        <p class="meta">No stacks configured.</p>
      </article>
    </div>

    <div v-else class="stack-grid">
      <StackCard
        v-for="stack in overviewStore.stacks"
        :key="stack.name"
        :stack="stack"
        :service-count="serviceCount(stack.name)"
        @compare="openStackManifest"
        @commit="openCommitDetails"
      />
    </div>
  </section>
</template>
